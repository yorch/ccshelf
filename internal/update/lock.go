package update

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// LockName is the lock file in the cache directory.
const LockName = "update.lock"

// ErrLocked means another ccshelf process is updating right now.
var ErrLocked = errors.New("another ccshelf update is in progress")

// AcquireLock takes the update lock in dir: an exclusive operating-system
// lock (flock on Unix, LockFileEx on Windows) on the file LockName, which is
// created with mode 0600 and never deleted. The kernel drops the lock when the
// holder exits for any reason. Thus a crash leaves nothing to expire, two
// processes can never both take over a stale lock, and a release can never
// remove a newer holder's lock. AcquireLock refuses a symlink at the lock's
// name. The
// returned release function is idempotent.
func AcquireLock(dir string) (release func(), err error) {
	path := filepath.Join(dir, LockName)
	fi, serr := os.Lstat(path) //nolint:gosec // a fixed name in the private cache directory
	if serr == nil && !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("the update lock %s is not a regular file", path)
	} else if serr != nil && !errors.Is(serr, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspecting the update lock %s: %w", path, serr)
	}
	f, err := openLockFile(path)
	if err != nil {
		return nil, fmt.Errorf("opening the update lock %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		if errors.Is(err, errWouldBlock) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	// The PID is best-effort diagnostic metadata; the kernel lock is authoritative.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = unlockFile(f)
			_ = f.Close()
		})
	}, nil
}

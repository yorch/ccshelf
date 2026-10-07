package update

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// LockName is the lock file in the cache directory.
const LockName = "update.lock"

// StaleLockAge is how old a lock file must be before it is taken over (a
// crashed update leaves one behind).
const StaleLockAge = 10 * time.Minute

// ErrLocked means another ccshelf process is updating right now.
var ErrLocked = errors.New("another ccshelf update is in progress")

// AcquireLock takes the update lock in dir by creating LockName exclusively
// (mode 0600; an existing file or symlink is never opened). A lock older than
// StaleLockAge by now() is removed and taken over once. The returned release
// function removes the lock.
func AcquireLock(dir string, now func() time.Time) (release func(), err error) {
	path := filepath.Join(dir, LockName)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a fixed name in the private cache directory
		if err == nil {
			_, _ = f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("creating the update lock %s: %w", path, err)
		}
		fi, serr := os.Lstat(path)
		if serr != nil {
			continue // vanished between the two calls: try again
		}
		if now().Sub(fi.ModTime()) < StaleLockAge {
			return nil, ErrLocked
		}
		if rerr := os.Remove(path); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return nil, fmt.Errorf("removing the stale update lock %s: %w", path, rerr)
		}
	}
	return nil, ErrLocked
}

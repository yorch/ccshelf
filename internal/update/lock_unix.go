//go:build !windows

package update

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// errWouldBlock is lockFile's answer when another process holds the lock.
var errWouldBlock = errors.New("lock held")

// openLockFile opens (creating it if needed, mode 0600) the lock file without
// following a symlink.
func openLockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600) //nolint:gosec // a fixed name in the private cache directory
}

func lockFile(f *os.File) error {
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) //nolint:gosec // fd fits in int
		switch {
		case err == nil:
			return nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK):
			return errWouldBlock
		}
		return err
	}
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN) //nolint:gosec // fd fits in int
}

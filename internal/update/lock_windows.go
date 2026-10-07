//go:build windows

package update

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// This file is UNTESTED ON REAL WINDOWS by the author (it compiles and vets for
// windows/amd64 and windows/arm64; the unit tests of the lock run on the
// Windows CI runner).

// errWouldBlock is lockFile's answer when another process holds the lock.
var errWouldBlock = errors.New("lock held")

// openLockFile opens (creating it if needed) the lock file. Windows has no
// O_NOFOLLOW; AcquireLock has already refused a link at this name.
func openLockFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // a fixed name in the private cache directory
}

func lockFile(f *os.File) error {
	// Keep the OS lock off byte zero: AcquireLock stores a readable PID there
	// while this handle holds the lock. Windows enforces byte-range locks for
	// writes through the owning handle too.
	ol := &windows.Overlapped{Offset: 1}
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol) //nolint:gosec // a handle
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return errWouldBlock
	}
	return err
}

func unlockFile(f *os.File) error {
	ol := &windows.Overlapped{Offset: 1}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ol) //nolint:gosec // a handle
}

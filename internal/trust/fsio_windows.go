//go:build windows

package trust

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// openNoFollow opens path after an Lstat check that the final component is
// not a symlink or reparse point. Windows has no portable O_NOFOLLOW, so a
// small race remains between the check and the open.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink", path)
	}
	return os.OpenFile(path, flag, perm)
}

// checkDirOwner is a no-op on Windows: the configuration directory lives
// under %APPDATA%, whose inherited ACL already restricts it to the user.
func checkDirOwner(os.FileInfo) error { return nil }

// checkFileOwner is a no-op on Windows for the same reason as checkDirOwner.
func checkFileOwner(os.FileInfo) error { return nil }

// tryLock takes an exclusive LockFileEx lock without blocking.
func tryLock(f *os.File) error {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return errLocked
	}
	return err
}

func unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}

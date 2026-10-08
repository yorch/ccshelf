//go:build !windows

package trust

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// openNoFollow opens path and fails if the final component is a symlink.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}

// checkDirOwner fails when the directory is owned by another user or is
// writable by group or others.
func checkDirOwner(fi os.FileInfo) error {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("owned by uid %d, not the current user (%d)", st.Uid, os.Geteuid())
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("writable by group or others (mode %o). Run chmod 700 on it", fi.Mode().Perm())
	}
	return nil
}

// checkFileOwner fails when the file is owned by another user or is
// accessible to group or others (the state files are created 0600).
func checkFileOwner(fi os.FileInfo) error {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("owned by uid %d, not the current user (%d)", st.Uid, os.Geteuid())
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("accessible to group or others (mode %o). Run chmod 600 on it", fi.Mode().Perm())
	}
	return nil
}

// tryLock takes an exclusive flock without blocking.
func tryLock(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return errLocked
		}
		return err
	}
}

func unlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }

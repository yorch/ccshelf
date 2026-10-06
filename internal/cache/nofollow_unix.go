//go:build !windows

package cache

import (
	"fmt"
	"os"
	"syscall"
)

// openNoFollow opens path and fails if the final component is a symlink.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag|syscall.O_NOFOLLOW, perm)
}

// checkOwner fails when the file is owned by another user.
func checkOwner(fi os.FileInfo) error {
	return checkOwnerAs(fi, os.Geteuid())
}

func checkOwnerAs(fi os.FileInfo, euid int) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != euid {
		return fmt.Errorf("owned by uid %d, not the current user (%d)", st.Uid, euid)
	}
	return nil
}

// secureDir verifies that the directory dir (already Lstat-ed as fi, not a
// symlink) belongs to the current user and repairs a mode wider than 0700.
// It works on an O_NOFOLLOW|O_DIRECTORY handle so that the checked directory
// and the repaired one are the same object.
func secureDir(dir string, fi os.FileInfo) error {
	if err := checkOwner(fi); err != nil {
		return err
	}
	if fi.Mode().Perm() == 0o700 {
		return nil
	}
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		return fmt.Errorf("open to repair its mode: %w", err)
	}
	defer f.Close()
	hfi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat to repair its mode: %w", err)
	}
	if !os.SameFile(fi, hfi) {
		return fmt.Errorf("changed while it was being checked")
	}
	if err := f.Chmod(0o700); err != nil {
		return fmt.Errorf("mode %o is wider than 0700 and chmod failed: %w", fi.Mode().Perm(), err)
	}
	return nil
}

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
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("owned by uid %d, not the current user (%d)", st.Uid, os.Geteuid())
	}
	return nil
}

//go:build !windows

package trust

import (
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
		return fmt.Errorf("writable by group or others (mode %o); run chmod 700 on it", fi.Mode().Perm())
	}
	return nil
}

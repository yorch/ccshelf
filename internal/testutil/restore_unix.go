//go:build !windows

package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// chmodNoFollow changes the mode through an O_NOFOLLOW handle.
func chmodNoFollow(path string, dir bool, mode os.FileMode) {
	flag := os.O_RDONLY | syscall.O_NOFOLLOW
	if dir {
		flag |= syscall.O_DIRECTORY
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		if dir && errors.Is(err, os.ErrPermission) {
			// A directory of mode 0000 to 0200 cannot be opened: go through
			// the parent handle.
			if parent, perr := os.OpenFile(filepath.Dir(path), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0); perr == nil {
				defer parent.Close()
				name := filepath.Base(path)
				var st unix.Stat_t
				if unix.Fstatat(int(parent.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && st.Mode&unix.S_IFMT == unix.S_IFDIR {
					_ = unix.Fchmodat(int(parent.Fd()), name, uint32(mode.Perm()), 0)
				}
			}
		}
		return
	}
	defer f.Close()
	_ = f.Chmod(mode)
}

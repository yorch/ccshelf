//go:build !windows

package testutil

import (
	"os"
	"syscall"
)

// chmodNoFollow changes the mode through an O_NOFOLLOW handle.
func chmodNoFollow(path string, dir bool, mode os.FileMode) {
	flag := os.O_RDONLY | syscall.O_NOFOLLOW
	if dir {
		flag |= syscall.O_DIRECTORY
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return
	}
	defer f.Close()
	_ = f.Chmod(mode)
}

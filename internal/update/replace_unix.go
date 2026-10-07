//go:build !windows

package update

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func platformInstall(newPath, exe, old string) error { return installAtomic(newPath, exe, old) }

func platformSwap(exe, old string) error { return swapAtomic(exe, old) }

// isReadOnly reports a read-only file system.
func isReadOnly(err error) bool { return errors.Is(err, syscall.EROFS) }

// dirOwnedByUser fails unless dir belongs to the current user.
func dirOwnedByUser(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("owned by uid %d, not you (%d): %w", st.Uid, os.Geteuid(), fs.ErrPermission)
	}
	return nil
}

//go:build windows

package trust

import (
	"fmt"
	"os"
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

//go:build windows

package cache

import (
	"fmt"
	"os"
)

// openNoFollow opens path after an Lstat check that the final component is
// not a symlink or reparse point. Windows has no portable O_NOFOLLOW, so a
// small race remains between the check and the open; the later re-hash and
// regular-file checks cover reads.
func openNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink", path)
	}
	return os.OpenFile(path, flag, perm)
}

// checkOwner is a no-op on Windows: the cache lives under %LOCALAPPDATA%,
// whose inherited ACL already restricts it to the user.
func checkOwner(os.FileInfo) error { return nil }

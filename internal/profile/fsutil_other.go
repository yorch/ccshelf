//go:build !unix

package profile

import "os"

// openNoFollow opens path read-only. This platform has no O_NOFOLLOW, so the
// caller's Lstat check (symlinks and reparse points are refused) and the fstat
// comparison that follows are the protection.
func openNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}

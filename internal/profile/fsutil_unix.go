//go:build unix

package profile

import (
	"os"
	"syscall"
)

// openNoFollow opens path read-only and fails when its final component is a
// symlink.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

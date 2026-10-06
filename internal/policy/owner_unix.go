//go:build !windows

package policy

import (
	"io/fs"
	"syscall"
)

// nonblock is OR-ed into the open flags so that opening a FIFO or device
// that slipped past the regular-file check never blocks.
const nonblock = syscall.O_NONBLOCK

// fileOwner returns the owning uid of fi.
func fileOwner(fi fs.FileInfo) (uint32, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}

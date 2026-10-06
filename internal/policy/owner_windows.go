//go:build windows

package policy

import "io/fs"

// nonblock is unused on Windows.
const nonblock = 0

// fileOwner always reports unknown on Windows: file ownership is an ACL
// there, which is not evaluated, so a symbolic link that leaves the managed
// directory is never followed (documented limitation).
func fileOwner(fs.FileInfo) (uint32, bool) { return 0, false }

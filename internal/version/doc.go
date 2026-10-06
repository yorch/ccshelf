// Package version reports the build identity of the ccshelf binary.
//
// Release builds set Version, Commit and Date with the linker:
//
//	-ldflags "-X github.com/yorch/ccshelf/internal/version.Version=0.1.0 ..."
//
// Builds made without those flags (go build, go install) fall back to the
// module and VCS data embedded by the Go toolchain.
package version

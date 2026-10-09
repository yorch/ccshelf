//go:build windows

package testutil

import "os"

func chmodNoFollow(path string, dir bool, mode os.FileMode) { _ = os.Chmod(path, mode) }

//go:build windows

package update

import (
	"errors"

	"golang.org/x/sys/windows"
)

// This file is UNTESTED ON REAL WINDOWS by the author: it compiles and vets for
// windows/amd64 and windows/arm64, and the algorithm it selects (installAside,
// swapAside) is exercised on every OS by the unit tests; CI runs the end-to-end
// update test on a Windows runner.

func platformInstall(newPath, exe, old string) error { return installAside(newPath, exe, old) }

func platformSwap(exe, old string) error { return swapAside(exe, old) }

// isReadOnly reports a write-protected medium.
func isReadOnly(err error) bool { return errors.Is(err, windows.ERROR_WRITE_PROTECT) }

// dirOwnedByUser accepts any directory: on Windows the write check
// (CheckWritable) is what decides, and ownership is not meaningful for the
// per-user and Program Files locations a binary is installed to.
func dirOwnedByUser(string) error { return nil }

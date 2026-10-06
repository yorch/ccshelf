//go:build !windows

package ui

import "os"

// enableVirtualTerminal is a no-op outside Windows: Unix terminals interpret
// ANSI sequences natively.
func enableVirtualTerminal(*os.File) bool { return true }

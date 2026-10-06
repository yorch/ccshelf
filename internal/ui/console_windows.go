//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVirtualTerminal turns on ANSI (virtual terminal) processing for the
// console behind f. It reports false when the console does not support it
// (legacy conhost before Windows 10 1511), in which case DetectMode falls back
// to plain output without color. The original console mode is left changed for
// the life of the process; the launcher restores the terminal before it starts
// claude (rule 6) by never leaving a prompt open.
func enableVirtualTerminal(f *os.File) bool {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}

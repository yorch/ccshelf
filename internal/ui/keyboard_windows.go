//go:build windows

package ui

import (
	"fmt"
	"os"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pickerKernel32    = windows.NewLazySystemDLL("kernel32.dll")
	pickerEventCount  = pickerKernel32.NewProc("GetNumberOfConsoleInputEvents")
	pickerReadConsole = pickerKernel32.NewProc("ReadConsoleInputW")
)

func preparePickerOutput(f *os.File) (func() error, bool) {
	h := windows.Handle(f.Fd())
	var original uint32
	if windows.GetConsoleMode(h, &original) != nil {
		return nil, false
	}
	if windows.SetConsoleMode(h, original|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return nil, false
	}
	return func() error { return windows.SetConsoleMode(h, original) }, true
}

// consoleKeyRecord has the 20-byte INPUT_RECORD layout. The event union's
// other variants are discarded; only KEY_EVENT (1) with keyDown is decoded.
type consoleKeyRecord struct {
	eventType                          uint16
	padding                            uint16
	keyDown                            int32
	repeat, virtualKey, scanCode, char uint16
	control                            uint32
}

func readPickerByte(f *os.File) func(time.Duration) (byte, bool, error) {
	var queued []byte
	var high uint16
	return func(wait time.Duration) (byte, bool, error) {
		if len(queued) > 0 {
			b := queued[0]
			queued = queued[1:]
			return b, true, nil
		}
		var count uint32
		r, _, err := pickerEventCount.Call(f.Fd(), uintptr(unsafe.Pointer(&count))) //nolint:gosec // Win32 writes one DWORD to this live uint32
		if r == 0 {
			return 0, false, fmt.Errorf("counting console events: %w", err)
		}
		if count == 0 {
			time.Sleep(wait)
			return 0, false, nil
		}
		var event consoleKeyRecord
		r, _, err = pickerReadConsole.Call(f.Fd(), uintptr(unsafe.Pointer(&event)), 1, uintptr(unsafe.Pointer(&count))) //nolint:gosec // one fixed-size INPUT_RECORD and one DWORD; layout checked by TestConsoleKeyRecordLayout
		if r == 0 {
			return 0, false, fmt.Errorf("reading console event: %w", err)
		}
		if count == 0 || event.eventType != 1 || event.keyDown == 0 {
			return 0, false, nil
		}
		switch event.virtualKey {
		case 0x26:
			queued = []byte("\x1b[A")
		case 0x28:
			queued = []byte("\x1b[B")
		default:
			if event.char == 0 {
				return 0, false, nil
			}
			if event.char >= 0xd800 && event.char <= 0xdbff {
				high = event.char
				return 0, false, nil
			}
			ch := rune(event.char)
			if high != 0 {
				ch = utf16.DecodeRune(rune(high), ch)
				high = 0
			}
			queued = []byte(string(ch))
		}
		b := queued[0]
		queued = queued[1:]
		return b, true, nil
	}
}

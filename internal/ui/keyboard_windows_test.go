//go:build windows

package ui

import (
	"testing"
	"unsafe"
)

func TestConsoleKeyRecordLayout(t *testing.T) {
	var r consoleKeyRecord
	if unsafe.Sizeof(r) != 20 || unsafe.Offsetof(r.keyDown) != 4 || unsafe.Offsetof(r.char) != 14 || unsafe.Offsetof(r.control) != 16 {
		t.Fatalf("INPUT_RECORD layout: size=%d keyDown=%d char=%d control=%d", unsafe.Sizeof(r), unsafe.Offsetof(r.keyDown), unsafe.Offsetof(r.char), unsafe.Offsetof(r.control))
	}
}

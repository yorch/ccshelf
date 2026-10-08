//go:build !darwin && !linux && !windows

package ui

import (
	"io"
	"os"
	"time"
)

func preparePickerOutput(*os.File) (func() error, bool) { return nil, false }

func readPickerByte(*os.File) func(time.Duration) (byte, bool, error) {
	return func(time.Duration) (byte, bool, error) { return 0, false, io.EOF }
}

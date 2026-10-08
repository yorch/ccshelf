//go:build darwin || linux

package ui

import (
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func preparePickerOutput(*os.File) (func() error, bool) {
	return func() error { return nil }, true
}

func readPickerByte(f *os.File) func(time.Duration) (byte, bool, error) {
	return func(wait time.Duration) (byte, bool, error) {
		fds := []unix.PollFd{{Fd: int32(f.Fd()), Events: unix.POLLIN}} //nolint:gosec // terminal fd fits in int32
		n, err := unix.Poll(fds, int(wait.Milliseconds()))
		if errors.Is(err, unix.EINTR) {
			return 0, false, nil
		}
		if err != nil || n == 0 {
			return 0, false, err
		}
		var b [1]byte
		n, err = f.Read(b[:])
		if n == 1 {
			return b[0], true, nil
		}
		if err == nil {
			err = io.EOF
		}
		return 0, false, err
	}
}

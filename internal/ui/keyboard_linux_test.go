//go:build linux

package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func pickerPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	master := os.NewFile(uintptr(fd), "pty master")
	t.Cleanup(func() { master.Close() })
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 80}); err != nil {
		t.Fatal(err)
	}
	return master, slave
}

func TestPickerPTYRestoration(t *testing.T) {
	for _, kind := range []string{"select", "multi", "ctrl-c", "ctrl-d", "escape", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			master, slave := pickerPTY(t)
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			originalRaw := pickerMakeRaw
			t.Cleanup(func() { pickerMakeRaw = originalRaw })
			ready := make(chan struct{})
			pickerMakeRaw = func(fd int) (*term.State, error) { st, err := term.MakeRaw(fd); close(ready); return st, err }
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			written := make(chan error, 1)
			go func() {
				<-ready
				input := "\x1b[B\r"
				switch kind {
				case "multi":
					input = " \x1b[B \r"
				case "ctrl-c":
					input = "\x03"
				case "ctrl-d":
					input = "\x04"
				case "escape":
					input = "\x1b"
				case "cancel":
					cancel()
					written <- nil
					return
				}
				_, err := master.WriteString(input)
				written <- err
			}()
			p := NewTTY(Streams{In: slave, Out: slave, Err: slave}, Mode{Interactive: true})
			if kind == "multi" {
				indexes, e := p.MultiSelect(ctx, fruit)
				err = e
				if !reflect.DeepEqual(indexes, []int{0, 1}) {
					t.Fatalf("multi: %v", indexes)
				}
			} else {
				i, e := p.Select(ctx, fruit)
				err = e
				if kind == "select" && i != 1 {
					t.Fatalf("select: %d", i)
				}
			}
			if writeErr := <-written; writeErr != nil {
				t.Fatal(writeErr)
			}
			if kind == "select" || kind == "multi" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrAborted) && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("terminal state changed: %v", err)
			}
			// The next consumer gets all of the input: no picker reader survives.
			master.WriteString("next\n")
			var buf [5]byte
			n, err := slave.Read(buf[:])
			if err != nil || string(buf[:n]) != "next\n" {
				t.Fatalf("next read: %q %v", buf[:n], err)
			}
		})
	}
}

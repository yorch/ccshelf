package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type hintErr struct{}

func (hintErr) Error() string { return "boom" }
func (hintErr) Hint() string  { return "try again" }

func TestCodeOf(t *testing.T) {
	base := errors.New("x")
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"plain", base, ExitFailure},
		{"failure", Failure(base), ExitFailure},
		{"usage", Usage(base), ExitUsage},
		{"policy", Policy(base), ExitPolicy},
		{"trust", TrustRequired(base), ExitTrust},
		{"wrapped exit", fmt.Errorf("ctx: %w", Policy(base)), ExitPolicy},
		{"missing flag", &MissingFlagError{Flag: "--x"}, ExitUsage},
		{"wrapped missing", fmt.Errorf("w: %w", MissingFlags("h", "--a", "--b")), ExitUsage},
		{"aborted", ErrAborted, ExitInterrupted},
		{"canceled", fmt.Errorf("w: %w", context.Canceled), ExitInterrupted},
		{"deadline", context.DeadlineExceeded, ExitFailure},
		{"explicit code wins", &ExitError{Code: 7, Err: ErrAborted}, 7},
	}
	for _, tt := range tests {
		if got := CodeOf(tt.err); got != tt.want {
			t.Errorf("%s: CodeOf = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestExitError(t *testing.T) {
	base := errors.New("inner")
	e := Usage(base)
	if !errors.Is(e, base) || e.Error() != "inner" {
		t.Errorf("unwrap/Error broken: %v", e)
	}
	for _, f := range []func(error) error{Usage, Policy, TrustRequired, Failure} {
		if f(nil) != nil {
			t.Error("constructor of nil must be nil")
		}
	}
	if got := (&ExitError{Code: 9}).Error(); got != "exit status 9" {
		t.Errorf("got %q", got)
	}
}

func TestMissingFlagMessages(t *testing.T) {
	tests := []struct {
		e    MissingFlagError
		want string
	}{
		{MissingFlagError{Flag: "--from", Hint: "parent"}, "missing required flag --from (parent)"},
		{MissingFlagError{Flag: "--from"}, "missing required flag --from"},
		{MissingFlagError{Hint: "Pick one"}, "a required value is missing (Pick one) and prompting is off"},
		{MissingFlagError{}, "a required value is missing and prompting is off"},
	}
	for _, tt := range tests {
		if got := tt.e.Error(); got != tt.want {
			t.Errorf("got %q, want %q", got, tt.want)
		}
	}
	if got := MissingFlags("h", "--a", "--b").Flag; got != "--a, --b" {
		t.Errorf("MissingFlags joined %q", got)
	}
}

func TestReport(t *testing.T) {
	tests := []struct {
		name string
		err  error
		mode Mode
		want string
	}{
		{"nil", nil, Mode{}, ""},
		{"plain error", errors.New("bad thing"), Mode{}, "error: bad thing\n"},
		{"color", errors.New("bad"), Mode{Color: true}, "\x1b[31merror:\x1b[0m bad\n"},
		{"missing flag", &MissingFlagError{Flag: "--from"},
			Mode{}, "error: missing required flag --from\nhint: pass --from, or run in a terminal without --no-interactive to be asked\n"},
		{"missing unknown", &MissingFlagError{Hint: "x"},
			Mode{}, "error: a required value is missing (x) and prompting is off\nhint: pass the value as a flag, or run in a terminal without --no-interactive to be asked\n"},
		{"hinter", hintErr{}, Mode{}, "error: boom\nhint: try again\n"},
		{"escape injection", errors.New("\x1b[2Jgone"), Mode{}, "error: \uFFFD[2Jgone\n"},
	}
	for _, tt := range tests {
		var b bytes.Buffer
		Report(&b, tt.err, tt.mode)
		if b.String() != tt.want {
			t.Errorf("%s:\n got %q\nwant %q", tt.name, b.String(), tt.want)
		}
	}
	var b bytes.Buffer
	Report(&b, Usage(&MissingFlagError{Flag: "--z"}), Mode{})
	if !strings.Contains(b.String(), "--z") {
		t.Errorf("wrapped missing flag not reported: %q", b.String())
	}
}

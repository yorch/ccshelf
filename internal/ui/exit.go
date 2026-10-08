package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Exit codes (R6 rule 8). They are part of the CLI contract: scripts react to
// them, so they never change meaning.
const (
	// ExitOK means success.
	ExitOK = 0
	// ExitFailure means the command failed.
	ExitFailure = 1
	// ExitUsage means a usage error, including a value that was missing and
	// could not be prompted for.
	ExitUsage = 2
	// ExitPolicy means the action was blocked by managed policy.
	ExitPolicy = 3
	// ExitTrust means the profile needs trust before it can run.
	ExitTrust = 4
	// ExitInterrupted means the user interrupted the command (Ctrl+C, Ctrl+D
	// at a prompt), by the shell convention 128 + SIGINT.
	ExitInterrupted = 130
)

// ErrAborted is returned by a Prompter when the user ends input (Ctrl+D or
// end of file) instead of answering. CodeOf maps it to ExitInterrupted.
var ErrAborted = errors.New("aborted")

// ExitError carries an exit code together with the error to report.
type ExitError struct {
	// Code is the process exit code.
	Code int
	// Err is the underlying error. It may be nil.
	Err error
}

// Error implements error.
func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

// Unwrap returns the underlying error.
func (e *ExitError) Unwrap() error { return e.Err }

func withCode(code int, err error) error {
	if err == nil {
		return nil
	}
	return &ExitError{Code: code, Err: err}
}

// Usage marks err as a usage error (exit code 2). A nil err stays nil.
func Usage(err error) error { return withCode(ExitUsage, err) }

// Policy marks err as blocked by policy (exit code 3). A nil err stays nil.
func Policy(err error) error { return withCode(ExitPolicy, err) }

// TrustRequired marks err as needing trust (exit code 4). A nil err stays nil.
func TrustRequired(err error) error { return withCode(ExitTrust, err) }

// Failure marks err as a plain failure (exit code 1). A nil err stays nil.
func Failure(err error) error { return withCode(ExitFailure, err) }

// MissingFlagError reports a required value that was not given and could not
// be asked for because the run is not interactive. It maps to exit code 2.
type MissingFlagError struct {
	// Flag names the missing flag or flags exactly as the user must type them,
	// for example "--from" or "--plugin, --skill-off". It may be empty when
	// the prompting code did not know the flag.
	Flag string
	// Hint says what the value is for, for example the prompt text.
	Hint string
}

// MissingFlags builds a MissingFlagError naming every flag in flags.
func MissingFlags(hint string, flags ...string) *MissingFlagError {
	return &MissingFlagError{Flag: strings.Join(flags, ", "), Hint: hint}
}

// Error implements error.
func (e *MissingFlagError) Error() string {
	switch {
	case e.Flag != "" && e.Hint != "":
		return fmt.Sprintf("missing required flag %s (%s)", e.Flag, e.Hint)
	case e.Flag != "":
		return fmt.Sprintf("missing required flag %s", e.Flag)
	case e.Hint != "":
		return fmt.Sprintf("a required value is missing (%s) and prompting is off", e.Hint)
	default:
		return "a required value is missing and prompting is off"
	}
}

// CodeOf returns the exit code for err: 0 for nil, the code of an ExitError,
// 2 for a MissingFlagError, 130 for ErrAborted and context cancellation, and 1
// for anything else.
func CodeOf(err error) int {
	var ee *ExitError
	var mf *MissingFlagError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &ee):
		return ee.Code
	case errors.As(err, &mf):
		return ExitUsage
	case errors.Is(err, ErrAborted), errors.Is(err, context.Canceled):
		return ExitInterrupted
	default:
		return ExitFailure
	}
}

// hinter is implemented by errors that know how to fix themselves.
type hinter interface{ Hint() string }

// Report prints err to w as "error: ..." followed by hints. For a
// MissingFlagError the exact missing flags are named. The message is
// sanitized to one line so a hostile profile description can neither inject
// terminal escape sequences nor forge a "hint:" line with a newline. A nil err prints nothing.
func Report(w io.Writer, err error, mode Mode) {
	if err == nil {
		return
	}
	var b strings.Builder
	b.WriteString(Status(mode, LevelError, SanitizeLine(err.Error())))
	b.WriteString("\n")
	var mf *MissingFlagError
	if errors.As(err, &mf) {
		if mf.Flag != "" {
			fmt.Fprintf(&b, "hint: pass %s, or run in a terminal without --no-interactive to be asked\n", SanitizeLine(mf.Flag))
		} else {
			b.WriteString("hint: pass the value as a flag, or run in a terminal without --no-interactive to be asked\n")
		}
	}
	var h hinter
	if errors.As(err, &h) {
		if s := h.Hint(); s != "" {
			fmt.Fprintf(&b, "hint: %s\n", SanitizeLine(s))
		}
	}
	_, _ = io.WriteString(w, b.String())
}

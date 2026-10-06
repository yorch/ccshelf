package ui

import (
	"fmt"
	"io"
	"strings"
)

// Recorder accumulates the flags a user chose interactively so the flow can
// print the equivalent flag-based command line (R6 rule 4):
//
//	rec := ui.NewRecorder("new", name)
//	rec.Flag("--from", "sre")
//	rec.Flag("--plugin", "a@b")
//	rec.Print(streams.Err, runtime.GOOS)
//
// A Recorder is for flags only. Never record a secret: values of flags whose
// names look secret are shown as <redacted> by Print, but the right design is
// to keep secrets out of flags altogether.
type Recorder struct {
	args []string
}

// NewRecorder starts a command line with the subcommand and its positional
// arguments, for example NewRecorder("new", "sre-night").
func NewRecorder(command string, positional ...string) *Recorder {
	r := &Recorder{}
	r.args = append(r.args, command)
	r.args = append(r.args, positional...)
	return r
}

// Flag records "name value". A name without a leading "-" gets "--". A value
// that starts with "-" is recorded as "name=value" so it cannot be mistaken
// for another flag.
func (r *Recorder) Flag(name, value string) {
	if !strings.HasPrefix(name, "-") {
		name = "--" + name
	}
	if strings.HasPrefix(value, "-") {
		r.args = append(r.args, name+"="+value)
		return
	}
	r.args = append(r.args, name, value)
}

// Bool records a switch such as "--force".
func (r *Recorder) Bool(name string) { r.args = append(r.args, name) }

// Args returns the recorded arguments (after the program name).
func (r *Recorder) Args() []string { return append([]string(nil), r.args...) }

// Print writes the "Equivalent: ccshelf ..." line to w, quoted for goos, with
// secret-looking values redacted.
func (r *Recorder) Print(w io.Writer, goos string) error {
	if err := Equivalent(w, goos, RedactArgs(r.args)); err != nil {
		return fmt.Errorf("printing the equivalent command: %w", err)
	}
	return nil
}

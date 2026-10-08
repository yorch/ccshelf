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
// A Recorder is for flags only. Never record a secret: the value of a flag
// whose name looks secret (whole words such as token or password) or that was
// recorded with SecretFlag is shown as <redacted> by Print, but the right
// design is to keep secrets out of flags altogether. The Recorder knows which
// arguments are values, so unlike RedactArgs it never redacts by guessing.
type Recorder struct {
	command    string
	positional []string
	flags      []recorded
}

type recorded struct {
	text   string // the switch, or the flag name when it has a value
	value  string
	hasVal bool
	equals bool // render as name=value
	secret bool
}

// NewRecorder starts a command line with the subcommand and its positional
// arguments, for example NewRecorder("new", "sre-night").
func NewRecorder(command string, positional ...string) *Recorder {
	return &Recorder{command: command, positional: append([]string(nil), positional...)}
}

func dashed(name string) string {
	if strings.HasPrefix(name, "-") {
		return name
	}
	return "--" + name
}

// Flag records "name value". A name without a leading "-" gets "--". A value
// that starts with "-" is recorded as "name=value" so it cannot be mistaken
// for another flag. The value is redacted by Print when the name looks secret.
func (r *Recorder) Flag(name, value string) {
	name = dashed(name)
	r.flags = append(r.flags, recorded{
		text: name, value: value, hasVal: true,
		equals: strings.HasPrefix(value, "-"),
		secret: looksSecret(name),
	})
}

// SecretFlag records a flag with a value that Print always redacts, whatever
// the flag is called.
func (r *Recorder) SecretFlag(name, value string) {
	r.Flag(name, value)
	r.flags[len(r.flags)-1].secret = true
}

// Bool records a switch such as "--force". A name without a leading "-" gets
// "--". A switch has no value, so nothing after it is ever redacted.
func (r *Recorder) Bool(name string) {
	r.flags = append(r.flags, recorded{text: dashed(name)})
}

// Args returns the recorded arguments (after the program name), with values
// as given (not redacted). Positional arguments follow the command. When one
// starts with "-" the flags come first and a "--" precedes the positional
// arguments so they cannot be read as flags.
func (r *Recorder) Args() []string { return r.render(false) }

func (r *Recorder) render(redact bool) []string {
	out := []string{r.command}
	dashPos := false
	for _, p := range r.positional {
		if strings.HasPrefix(p, "-") {
			dashPos = true
		}
	}
	positionals := func() {
		for _, p := range r.positional {
			if redact {
				if name, _, ok := strings.Cut(p, "="); ok && looksSecret(name) {
					p = name + "=" + RedactedValue
				}
			}
			out = append(out, p)
		}
	}
	if !dashPos {
		positionals()
	}
	for _, f := range r.flags {
		v := f.value
		if redact && f.secret {
			v = RedactedValue
		}
		switch {
		case !f.hasVal:
			out = append(out, f.text)
		case f.equals && !(redact && f.secret):
			out = append(out, f.text+"="+v)
		default:
			out = append(out, f.text, v)
		}
	}
	if dashPos {
		out = append(out, "--")
		positionals()
	}
	return out
}

// Print writes the "Equivalent: ccshelf ..." line to w, quoted for goos, with
// secret values redacted.
func (r *Recorder) Print(w io.Writer, goos string) error {
	return r.PrintFor(w, ShellForGOOS(goos))
}

// PrintFor is Print for an explicit quoting style (ShellPOSIX, ShellFish,
// ShellPowerShell or ShellCmd).
func (r *Recorder) PrintFor(w io.Writer, shell string) error {
	if err := EquivalentFor(w, shell, r.render(true)); err != nil {
		return fmt.Errorf("printing the equivalent command: %w", err)
	}
	return nil
}

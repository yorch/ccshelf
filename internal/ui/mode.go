package ui

import (
	"io"
	"os"
	"strconv"

	"golang.org/x/term"
)

// Streams are the three standard streams a command uses. Prompts and
// diagnostics go to Err. Results go to Out so they can be piped.
type Streams struct {
	// In is the input (normally os.Stdin).
	In io.Reader
	// Out receives command results.
	Out io.Writer
	// Err receives prompts, warnings and errors.
	Err io.Writer
}

// StdStreams returns Streams bound to the process's standard streams.
func StdStreams() Streams {
	return Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
}

// Mode describes how the command may talk to the user (R6 rules 2 and 7).
type Mode struct {
	// Interactive is true when prompts may be shown.
	Interactive bool
	// Color is true when ANSI color may be used on output.
	Color bool
	// Plain is true when only line-oriented prompts and ASCII output are used.
	Plain bool
	// JSON is true when machine-readable output was requested.
	JSON bool
	// Width is the terminal width in columns, or 0 when unknown or not a
	// terminal (no truncation).
	Width int
}

// ModeFlags are the command-line and configuration inputs to DetectMode.
type ModeFlags struct {
	// NoInteractive is --no-interactive.
	NoInteractive bool
	// NoColor is --no-color.
	NoColor bool
	// Plain is --plain.
	Plain bool
	// JSON is --json.
	JSON bool
	// ConfigColor is the ui.color setting: "auto" (or empty), "always" or "never".
	ConfigColor string
	// ConfigInteractive is the ui.interactive setting: "auto" (or empty) or "never".
	ConfigInteractive string
}

// isTerminal reports whether fd is a terminal. It is a test hook; production
// code never reassigns it.
var isTerminal = func(fd uintptr) bool { return term.IsTerminal(int(fd)) } //nolint:gosec // fd fits in int

// enableVT turns on virtual-terminal processing for a console. It is a test
// hook; production code never reassigns it.
var enableVT = enableVirtualTerminal

// terminalWidth returns the width of the terminal on fd, or 0.
var terminalWidth = func(fd uintptr) int {
	w, _, err := term.GetSize(int(fd)) //nolint:gosec // fd fits in int
	if err != nil || w <= 0 {
		return 0
	}
	return w
}

// DetectMode implements rule 2 of the interaction model.
//
// Interactive is true only when stdin and stdout are both terminals,
// --no-interactive is not set, ui.interactive is not "never", CI is not set
// (any non-empty value counts as set, including "false" and "0") and TERM is not "dumb". Color
// is off when NO_COLOR is set to any non-empty value (the no-color.org rule), --no-color is
// given, TERM is "dumb" or stdout is not a terminal. ui.color = "always"
// turns it on for a non-terminal stdout but never overrides NO_COLOR,
// --no-color or a dumb terminal. --plain forces line-oriented prompts, and so
// does TERM=dumb. --json disables prompts and color.
//
// On Windows the console's virtual-terminal processing is enabled on a
// best-effort basis. If that fails, the mode falls back to plain output
// without color. env may be nil (os.Getenv). A nil file counts as not a
// terminal.
func DetectMode(env func(string) string, in, out *os.File, flags ModeFlags) Mode {
	if env == nil {
		env = os.Getenv
	}
	inTerm := in != nil && isTerminal(in.Fd())
	outTerm := out != nil && isTerminal(out.Fd())
	vtOK := true
	if outTerm {
		vtOK = enableVT(out)
	}
	m := computeMode(env, inTerm, outTerm, vtOK, flags)
	if outTerm && !m.JSON {
		m.Width = terminalWidth(out.Fd())
	}
	if m.Width == 0 {
		if c, err := strconv.Atoi(env("COLUMNS")); err == nil && c > 0 && outTerm {
			m.Width = c
		}
	}
	return m
}

// computeMode is the pure part of DetectMode.
func computeMode(env func(string) string, inTerm, outTerm, vtOK bool, flags ModeFlags) Mode {
	dumb := env("TERM") == "dumb"
	noColorEnv := env("NO_COLOR") != ""

	m := Mode{JSON: flags.JSON}
	m.Interactive = inTerm && outTerm && vtOK && !flags.NoInteractive &&
		flags.ConfigInteractive != "never" && !ciSet(env("CI")) && !dumb && !flags.JSON

	color := outTerm && vtOK
	if flags.ConfigColor == "never" {
		color = false
	}
	if flags.ConfigColor == "always" {
		color = true
	}
	if noColorEnv || flags.NoColor || dumb || flags.JSON || (!vtOK && outTerm) {
		color = false
	}
	m.Color = color
	m.Plain = flags.Plain || dumb || (outTerm && !vtOK)
	return m
}

// ciSet implements the CI rule of docs/design/cli.md literally: the CI
// environment variable is set when it has any non-empty value. "false" and
// "0" count as set, because many CI systems and wrappers export CI with
// arbitrary spellings, and a wrongly interactive run is worse than a wrongly
// non-interactive one.
func ciSet(v string) bool { return v != "" }

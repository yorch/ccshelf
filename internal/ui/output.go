package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
)

// minColumn is the narrowest a table column is squeezed to.
const minColumn = 8

// Table writes an aligned table. Cells are sanitized (no terminal escape
// injection) and flattened to one line. When mode.Width is set, the widest
// columns are truncated with an ellipsis so rows fit; widths count wide
// Unicode characters as two columns. If even the minimum column widths cannot
// fit, rows become wrapped, labeled records without truncation. In plain mode
// the header rule and the ellipsis are ASCII. Trailing spaces are not written.
func Table(w io.Writer, headers []string, rows [][]string, mode Mode) error {
	ncol := len(headers)
	for _, r := range rows {
		if len(r) > ncol {
			ncol = len(r)
		}
	}
	if ncol == 0 {
		return nil
	}
	clean := func(s string) string {
		return strings.Join(strings.Fields(strings.ReplaceAll(Sanitize(s), "�", "?")), " ")
	}
	grid := make([][]string, 0, len(rows)+1)
	if len(headers) > 0 {
		grid = append(grid, cleanRow(headers, ncol, clean))
	}
	for _, r := range rows {
		grid = append(grid, cleanRow(r, ncol, clean))
	}
	widths := make([]int, ncol)
	for _, r := range grid {
		for i, c := range r {
			widths[i] = max(widths[i], DisplayWidth(c))
		}
	}
	const gap = 2
	if mode.Width > 0 {
		avail := mode.Width - gap*(ncol-1)
		for sum(widths) > avail {
			wi := 0
			for i, x := range widths {
				if x > widths[wi] {
					wi = i
				}
			}
			if widths[wi] <= minColumn {
				break
			}
			widths[wi]--
		}
		if sum(widths) > avail {
			return stackedTable(w, headers, grid, mode)
		}
	}
	var b bytes.Buffer
	emit := func(r []string) {
		var line strings.Builder
		for i, c := range r {
			c = Truncate(c, widths[i], mode.Plain)
			if i < len(r)-1 {
				line.WriteString(pad(c, widths[i]))
				line.WriteString(strings.Repeat(" ", gap))
			} else {
				line.WriteString(c)
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	for i, r := range grid {
		emit(r)
		if i == 0 && len(headers) > 0 {
			rule := "─"
			if mode.Plain {
				rule = "-"
			}
			parts := make([]string, ncol)
			for j := range parts {
				parts[j] = strings.Repeat(rule, widths[j])
			}
			var line strings.Builder
			for j, p := range parts {
				line.WriteString(p)
				if j < ncol-1 {
					line.WriteString(strings.Repeat(" ", gap))
				}
			}
			b.WriteString(line.String())
			b.WriteByte('\n')
		}
	}
	_, err := w.Write(b.Bytes())
	return err
}

func cleanRow(r []string, ncol int, clean func(string) string) []string {
	out := make([]string, ncol)
	for i := range out {
		if i < len(r) {
			out[i] = clean(r[i])
		}
	}
	return out
}

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

// JSONVersion is the version of the JSON envelope written by WriteJSON.
//
// Schema rule: the envelope is {"version":1,"kind":<string>,"data":<value>}.
// Within a version, a kind's data may gain fields but never lose or rename
// one and never change a field's type; any breaking change bumps the version,
// and a command keeps writing the old version until the documented removal.
// Keys are in a stable order (struct field order, maps sorted by key).
const JSONVersion = 1

type envelope struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Data    any    `json:"data"`
}

// WriteJSON writes v wrapped in the versioned envelope, indented by two
// spaces, followed by a newline. HTML characters are not escaped.
func WriteJSON(w io.Writer, kind string, v any) error {
	if kind == "" {
		return fmt.Errorf("writing JSON: empty kind")
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(envelope{Version: JSONVersion, Kind: kind, Data: v}); err != nil {
		return fmt.Errorf("encoding %s as JSON: %w", kind, err)
	}
	_, err := w.Write(b.Bytes())
	return err
}

// Level is the severity of a status line.
type Level int

// Status levels.
const (
	// LevelOK marks success.
	LevelOK Level = iota
	// LevelWarn marks something that needs attention.
	LevelWarn
	// LevelError marks a failure.
	LevelError
)

// Status prefixes text with "ok:", "warn:" or "error:". The word is always
// present so color is never the only carrier of meaning; with mode.Color the
// word is also green, yellow or red.
func Status(mode Mode, level Level, text string) string {
	word, code := "ok", "32"
	switch level {
	case LevelWarn:
		word, code = "warn", "33"
	case LevelError:
		word, code = "error", "31"
	}
	return Colorize(mode, code, word+":") + " " + text
}

// Colorize wraps text in the ANSI SGR sequence code ("1" bold, "2" dim, "31"
// red, ...) when mode.Color is set and returns it unchanged otherwise.
func Colorize(mode Mode, code, text string) string {
	if !mode.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

// Bold returns text in bold when mode.Color is set.
func Bold(mode Mode, text string) string { return Colorize(mode, "1", text) }

// Dim returns text dimmed when mode.Color is set.
func Dim(mode Mode, text string) string { return Colorize(mode, "2", text) }

// RedactedValue replaces every redacted value.
const RedactedValue = "<redacted>"

// RedactEnv returns a copy of env that keeps the names and replaces every
// value with "<redacted>". Environment values may be secrets, and output of
// dry-run, show and doctor is pasted into issues (SR4).
func RedactEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	for k := range env {
		out[k] = RedactedValue
	}
	return out
}

// RedactEnvList renders "NAME=<redacted>" lines for env, sorted by name.
func RedactEnvList(env map[string]string) []string {
	names := make([]string, 0, len(env))
	for k := range env {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]string, len(names))
	for i, k := range names {
		out[i] = k + "=" + RedactedValue
	}
	return out
}

// secretSubstrings are long, unambiguous words: a name containing one is
// secret wherever it appears ("--accessToken", "MYSECRETX").
var secretSubstrings = []string{
	"token", "secret", "password", "passwd", "passphrase", "credential",
	"apikey", "bearer", "cookie", "private",
}

// secretWords are short, ambiguous words that only count as a whole word
// (names are split at '-', '_', '.', spaces and camelCase boundaries), so
// "--author" and "--monkey" stay visible while "--auth", "--deploy-key" and
// "--key" are redacted.
var secretWords = map[string]bool{"auth": true, "key": true, "keys": true}

// benignWords are removed from a name before the substring check, for
// ordinary words that happen to contain a secret word.
var benignWords = []string{"secretary", "tokenize", "tokenizer", "tokenization"}

// splitName splits a flag or variable name into lower-case words at '-', '_',
// '.', spaces and camelCase boundaries.
func splitName(name string) []string {
	var b strings.Builder
	rs := []rune(name)
	for i, r := range rs {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1])) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return strings.FieldsFunc(strings.ToLower(b.String()), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' '
	})
}

// looksSecret reports whether a flag or variable name suggests a secret. It
// errs toward yes: over-redacting a display line is the safe side.
func looksSecret(name string) bool {
	toks := splitName(name)
	for _, t := range toks {
		if secretWords[t] {
			return true
		}
	}
	joined := strings.Join(toks, "")
	for _, w := range benignWords {
		joined = strings.ReplaceAll(joined, w, "")
	}
	for _, w := range secretSubstrings {
		if strings.Contains(joined, w) {
			return true
		}
	}
	return false
}

// RedactArgs returns a copy of args with secret-looking values replaced by
// "<redacted>": the value of "--flag=value" and of NAME=value arguments whose
// name suggests a secret (so "--author" is not a secret, but "--apiKey" and
// "--accessToken" are), and the argument after a secret-looking flag that has
// no "=", whatever it looks like (a secret may start with "-"), unless it is
// exactly "--". Redaction errs toward too much. Everything after a "--"
// is positional: only NAME=value arguments are checked there. The result is
// for display only. A Recorder knows exactly which flags carry a value and
// does not guess; prefer it for the Equivalent line.
func RedactArgs(args []string) []string {
	out := make([]string, len(args))
	redactNext := false
	positional := false
	for i, a := range args {
		switch {
		case redactNext:
			out[i] = RedactedValue
			redactNext = false
		case !positional && a == "--":
			out[i] = a
			positional = true
		case !positional && strings.HasPrefix(a, "-"):
			name, _, hasValue := strings.Cut(a, "=")
			switch {
			case !looksSecret(name):
				out[i] = a
			case hasValue:
				out[i] = name + "=" + RedactedValue
			default:
				out[i] = a
				redactNext = i+1 < len(args) && args[i+1] != "--"
			}
		default:
			if name, _, ok := strings.Cut(a, "="); ok && looksSecret(name) {
				out[i] = name + "=" + RedactedValue
			} else {
				out[i] = a
			}
		}
	}
	return out
}

package ui

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// ErrUnquotable is returned (wrapped) when a string cannot be quoted safely
// for the requested shell: it holds a control character, invalid UTF-8, or a
// character the shell cannot carry safely.
var ErrUnquotable = errors.New("cannot be quoted safely")

func checkQuotable(s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: invalid UTF-8", ErrUnquotable)
	}
	for _, r := range s {
		if needsSanitizing(r) || r == '\n' || r == '\t' {
			return fmt.Errorf("%w: contains control character %U", ErrUnquotable, r)
		}
	}
	return nil
}

func allSafe(s, extra string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("_./-+:"+extra, r):
		default:
			return false
		}
	}
	return true
}

// QuotePOSIX quotes s as one word for sh, bash and zsh (use QuoteFish for
// fish, which escapes backslash and quote inside single quotes): bare when
// it only has safe characters, otherwise in single quotes, closing and
// reopening the quotes around an escaped quote for each embedded single quote.
// Control characters, including newline, are refused with ErrUnquotable.
func QuotePOSIX(s string) (string, error) {
	if err := checkQuotable(s); err != nil {
		return "", err
	}
	if allSafe(s, "@%=,") && s[0] != '~' && s[0] != '=' {
		return s, nil
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'", nil
}

// QuoteFish quotes s as one word for fish. Inside single quotes fish treats
// backslash and single quote as escapes (unlike POSIX shells), so both are
// escaped with a backslash. A word of safe characters stays bare. Control
// characters, including newline, are refused with ErrUnquotable.
func QuoteFish(s string) (string, error) {
	if err := checkQuotable(s); err != nil {
		return "", err
	}
	if allSafe(s, "@=,") {
		return s, nil
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'", nil
}

// QuotePowerShell quotes s as one word for PowerShell: bare when safe,
// otherwise in single quotes, doubling every single quote, including the
// typographic quotes U+2018 to U+201B that PowerShell also treats as quotes.
// Control characters are refused with ErrUnquotable.
func QuotePowerShell(s string) (string, error) {
	if err := checkQuotable(s); err != nil {
		return "", err
	}
	if allSafe(s, "=") {
		return s, nil
	}
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s {
		switch r {
		case '\'', '‘', '’', '‚', '‛':
			b.WriteRune(r)
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('\'')
	return b.String(), nil
}

// QuoteCmd quotes s as one word for cmd.exe. A word of safe characters stays
// bare; a word with spaces and no cmd metacharacter is wrapped in double
// quotes using the CommandLineToArgvW rules; any other word has every
// metacharacter escaped with ^ as well. A '%' cannot be escaped reliably
// outside a batch file, so a string containing one is refused, as are control
// characters (ErrUnquotable).
func QuoteCmd(s string) (string, error) {
	if err := checkQuotable(s); err != nil {
		return "", err
	}
	if strings.ContainsRune(s, '%') {
		return "", fmt.Errorf("%w: '%%' is expanded by cmd.exe", ErrUnquotable)
	}
	if allSafe(s, `\@`) {
		return s, nil
	}
	arg := argvQuote(s)
	const meta = "()%!^\"<>&|"
	if !strings.ContainsAny(s, meta) {
		return arg, nil
	}
	var b strings.Builder
	for _, r := range arg {
		if strings.ContainsRune(meta, r) {
			b.WriteByte('^')
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}

// argvQuote quotes s for CommandLineToArgvW: always in double quotes,
// doubling backslashes that precede a quote.
func argvQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	bs := 0
	for _, r := range s {
		switch r {
		case '\\':
			bs++
			b.WriteRune(r)
		case '"':
			b.WriteString(strings.Repeat(`\`, bs+1))
			b.WriteRune(r)
			bs = 0
		default:
			bs = 0
			b.WriteRune(r)
		}
	}
	b.WriteString(strings.Repeat(`\`, bs))
	b.WriteByte('"')
	return b.String()
}

// Shell names accepted by Quote.
const (
	// ShellPOSIX is sh, bash, zsh and fish quoting.
	ShellPOSIX = "posix"
	// ShellFish is fish quoting, which differs from POSIX inside single quotes.
	ShellFish = "fish"
	// ShellPowerShell is PowerShell quoting.
	ShellPowerShell = "powershell"
	// ShellCmd is cmd.exe quoting.
	ShellCmd = "cmd"
)

// ShellForGOOS returns the quoting style used for the Equivalent line on an
// operating system: PowerShell on Windows (the default shell of Windows
// Terminal), POSIX elsewhere.
func ShellForGOOS(goos string) string {
	if goos == "windows" {
		return ShellPowerShell
	}
	return ShellPOSIX
}

// ShellForName maps a shell name ("bash", "zsh", "sh", "posix", "fish",
// "pwsh", "powershell", "cmd") to its quoting style, and reports whether the
// name is known.
func ShellForName(name string) (string, bool) {
	switch name {
	case "bash", "zsh", "sh", ShellPOSIX:
		return ShellPOSIX, true
	case ShellFish:
		return ShellFish, true
	case "pwsh", ShellPowerShell:
		return ShellPowerShell, true
	case ShellCmd:
		return ShellCmd, true
	}
	return "", false
}

// Quote quotes s for the named shell style (ShellPOSIX, ShellFish,
// ShellPowerShell or ShellCmd).
func Quote(shell, s string) (string, error) {
	switch shell {
	case ShellPOSIX:
		return QuotePOSIX(s)
	case ShellFish:
		return QuoteFish(s)
	case ShellPowerShell:
		return QuotePowerShell(s)
	case ShellCmd:
		return QuoteCmd(s)
	}
	return "", fmt.Errorf("unknown shell %q", shell)
}

// Join quotes every word for shell and joins them with spaces.
func Join(shell string, words []string) (string, error) {
	parts := make([]string, len(words))
	for i, w := range words {
		q, err := Quote(shell, w)
		if err != nil {
			return "", fmt.Errorf("argument %d: %w", i+1, err)
		}
		parts[i] = q
	}
	return strings.Join(parts, " "), nil
}

// Equivalent prints "Equivalent: ccshelf <args>" (R6 rule 4) with each
// argument quoted for the shell of goos. Arguments that cannot be quoted
// safely (control characters) make it return an error and print nothing, so a
// copied line can never run something the user did not choose.
func Equivalent(w io.Writer, goos string, args []string) error {
	return EquivalentFor(w, ShellForGOOS(goos), args)
}

// EquivalentFor is Equivalent for an explicit quoting style (ShellPOSIX,
// ShellFish, ShellPowerShell or ShellCmd), for a flow that knows the user's
// shell.
func EquivalentFor(w io.Writer, shell string, args []string) error {
	line, err := Join(shell, args)
	if err != nil {
		return fmt.Errorf("building the equivalent command: %w", err)
	}
	if line != "" {
		line = " " + line
	}
	_, err = fmt.Fprintf(w, "Equivalent: ccshelf%s\n", line)
	return err
}

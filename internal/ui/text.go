package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Sanitize makes text safe to print on a terminal: control characters
// (including ESC, so ANSI escape sequences, and CR), invisible and
// formatting characters (zero-width space and joiners, the Arabic letter mark,
// BOM, word joiner, soft hyphen, bidirectional controls, the tag block
// U+E0000 to U+E007F), line and paragraph separators and invalid UTF-8 are
// replaced by U+FFFD, except newline and tab, which are kept. Text that
// comes from profiles, catalogs or other repositories must pass through
// Sanitize (or SanitizeLine) before it reaches the terminal.
//
// Sanitize keeps newlines, so it is only for text that may span lines. Text
// that is printed as one line of a prompt, a title, a label or a "hint:" line
// must use SanitizeLine, or a newline in it could forge a line of the prompt.
func Sanitize(s string) string { return sanitize(s, false) }

// SanitizeLine is Sanitize for text that must stay on one line: newline
// becomes U+FFFD (so the tampering stays visible) and tab becomes a space.
func SanitizeLine(s string) string { return sanitize(s, true) }

// HasControl reports whether s holds anything Sanitize would replace or
// SanitizeLine would change: control characters (including newline and tab),
// invisible or formatting characters, or invalid UTF-8. Code that must refuse
// such values instead of cleaning them (paths, executable names) uses it.
func HasControl(s string) bool {
	return !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool {
		return needsSanitizing(r) || r == '\n' || r == '\t'
	})
}

func sanitize(s string, oneLine bool) string {
	needs := needsSanitizing
	if oneLine {
		needs = func(r rune) bool { return needsSanitizing(r) || r == '\n' || r == '\t' }
	}
	if !strings.ContainsFunc(s, needs) && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToValidUTF8(s, "\ufffd") {
		switch {
		case oneLine && r == '\t':
			b.WriteByte(' ')
		case needs(r):
			b.WriteRune('\ufffd')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func needsSanitizing(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '\u2028' || r == '\u2029' ||
		(r >= 0xE0000 && r <= 0xE007F)
}

// runeWidth returns the number of terminal columns r occupies: 0 for
// combining and format characters, 2 for East Asian wide characters and
// emoji, 1 otherwise.
func runeWidth(r rune) int {
	switch {
	case r == 0:
		return 0
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		return 0
	case r < 0x1100:
		return 1
	}
	switch {
	case r <= 0x115F, // Hangul Jamo
		r == 0x2329, r == 0x232A,
		r >= 0x231A && r <= 0x231B,
		r >= 0x23E9 && r <= 0x23EC,
		r >= 0x25FD && r <= 0x25FE,
		r >= 0x2600 && r <= 0x27BF && isEmojiPresentation(r),
		r >= 0x2E80 && r <= 0x303E,
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0xA4CF,
		r >= 0xA960 && r <= 0xA97F,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F,
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x1F680 && r <= 0x1F6FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

// isEmojiPresentation covers the handful of dingbat-range characters that
// render as two-column emoji by default.
func isEmojiPresentation(r rune) bool {
	switch r {
	case 0x2614, 0x2615, 0x2648, 0x2649, 0x264A, 0x264B, 0x264C, 0x264D, 0x264E, 0x264F,
		0x2650, 0x2651, 0x2652, 0x2653, 0x267F, 0x2693, 0x26A1, 0x26AA, 0x26AB, 0x26BD,
		0x26BE, 0x26C4, 0x26C5, 0x26CE, 0x26D4, 0x26EA, 0x26F2, 0x26F3, 0x26F5, 0x26FA,
		0x26FD, 0x2705, 0x270A, 0x270B, 0x2728, 0x274C, 0x274E, 0x2753, 0x2754, 0x2755,
		0x2757, 0x2795, 0x2796, 0x2797, 0x27B0, 0x27BF:
		return true
	}
	return false
}

// DisplayWidth returns the number of terminal columns s occupies.
func DisplayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// Truncate shortens s to at most width columns, ending with an ellipsis ("…",
// or "..." when ascii is true) when it had to cut. It never splits a
// character and counts wide characters as two columns. A width that cannot
// hold the ellipsis returns the ellipsis cut to fit.
func Truncate(s string, width int, ascii bool) string {
	if width <= 0 {
		return ""
	}
	if DisplayWidth(s) <= width {
		return s
	}
	ell := "…"
	if ascii {
		ell = "..."
	}
	ew := DisplayWidth(ell)
	if width <= ew {
		return truncateRaw(ell, width)
	}
	return truncateRaw(s, width-ew) + ell
}

func truncateRaw(s string, width int) string {
	w := 0
	for i, r := range s {
		rw := runeWidth(r)
		if w+rw > width {
			return s[:i]
		}
		w += rw
	}
	return s
}

// pad right-pads s with spaces to width columns.
func pad(s string, width int) string {
	if n := width - DisplayWidth(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

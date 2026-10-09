package profile

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxInstructionsSize is the largest joined text of the instructions files
// accepted, not counting the header. Each
// single file is also limited to [MaxPromptSize].
const MaxInstructionsSize = 64 << 10

// InstructionFile is one effective instructions file after the merge along
// the extends chain.
type InstructionFile struct {
	// Profile is the name of the profile that declares the file.
	Profile string
	// Source is the portable id of the source that holds the file.
	Source string
	// Path is the prompt path, for example prompts/base.md.
	Path string
	// Bytes is the size of the normalized content.
	Bytes int
	// Digest is the hex SHA-256 of the normalized content.
	Digest string

	content []byte
}

// InstructionsHeader returns the fixed first lines of the generated CLAUDE.md
// for a profile. They make sure that the file never starts with front matter,
// and they tell the model where the text comes from. The profile name matches
// [ValidName], so it holds only lower-case letters, digits and "-".
func InstructionsHeader(profile string) string {
	return "# Instructions from the ccshelf profile " + profile + "\n\n"
}

// joinInstructions joins the normalized contents with a blank line between
// them. Trailing newlines of each part are dropped and the result ends with
// one newline. Parts that are empty after the trim are skipped. It returns nil
// when nothing is left.
func joinInstructions(parts [][]byte) []byte {
	var out []byte
	for _, p := range parts {
		t := strings.TrimRight(string(p), "\n")
		if strings.TrimSpace(t) == "" {
			continue
		}
		if len(out) > 0 {
			out = append(out, '\n')
		}
		out = append(out, t...)
		out = append(out, '\n')
	}
	return out
}

// charRefRe matches the character references of "@" that Markdown decodes.
var charRefRe = regexp.MustCompile(`(?i)^&(#0*64|#x0*40|commat);`)

// CheckInstructionsText checks the text of one instructions file, or the
// joined text, against what Claude Code does with a CLAUDE.md file. It returns
// the 1-based line and a message that says what to change, or line 0 when the
// text is acceptable.
//
// Claude Code expands "@path" tokens in memory files into the content of other
// files. The check looks at the raw characters and does not follow the Markdown
// structure. No code span, fenced block or other construct is exempt. A text
// is refused when it has:
//   - a leading byte order mark,
//   - a first line of "---" (front matter),
//   - a carriage return that is not part of a CRLF line end,
//   - a character reference of "@" ("&#64;", "&#x40;" or "&commat;"),
//   - an "@" followed by a character that is not white space or a backslash,
//     unless a letter or a digit precedes the "@" (an email address) or an odd
//     number of backslashes precede it.
func CheckInstructionsText(text string) (int, string) {
	if strings.HasPrefix(text, "\ufeff") {
		return 1, "starts with a byte order mark. Remove the mark"
	}
	first, _, _ := strings.Cut(text, "\n")
	if strings.TrimSpace(first) == "---" {
		return 1, "starts with a line of \"---\", which Claude Code reads as front matter. Start the file with another line"
	}
	line := 1
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\n':
			line++
		case '\r':
			if i+1 >= len(text) || text[i+1] != '\n' {
				return line, "contains a carriage return that is not part of a line end. Use LF or CRLF line ends"
			}
		case '&':
			if m := charRefRe.FindString(text[i:]); m != "" {
				return line, "contains the character reference " + quote(m) + ", which Claude Code reads as an \"@\". Remove it"
			}
		case '@':
			if isImportAt(text, i) {
				return line, "contains the import token " + quote(tokenAt(text, i)) + ". " + importHint
			}
		}
	}
	return 0, ""
}

const importHint = "Claude Code reads it as a file import, also inside code. Write \\@ to keep the character (inside code the backslash stays visible), or remove it"

func quote(s string) string {
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	return "\"" + strings.ReplaceAll(s, "\"", "'") + "\""
}

// tokenAt returns the "@" at text[i] and the characters after it up to white
// space or a backslash, or the end of the line.
func tokenAt(text string, i int) string {
	j := i + 1
	for j < len(text) {
		r, _ := utf8.DecodeRuneInString(text[j:])
		if jsSpace(r) || r == '\\' {
			break
		}
		j += utf8.RuneLen(r)
	}
	return text[i:j]
}

// isImportAt reports whether the "@" at text[i] must be refused.
func isImportAt(text string, i int) bool {
	if i+1 >= len(text) {
		return false
	}
	next, _ := utf8.DecodeRuneInString(text[i+1:])
	if jsSpace(next) || next == '\\' {
		return false
	}
	if oddBackslashes(text, i) {
		return false
	}
	if i > 0 {
		prev, _ := utf8.DecodeLastRuneInString(text[:i])
		if unicode.IsLetter(prev) || unicode.IsNumber(prev) {
			return false
		}
	}
	return true
}

// jsSpace reports whether r matches "\s" of a JavaScript regular expression.
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// oddBackslashes reports whether an odd number of backslashes precede text[i].
func oddBackslashes(text string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && text[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

package profile

import (
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

// CheckInstructionsText checks the text of one instructions file, or the
// joined text, against what Claude Code does with a CLAUDE.md file. It returns
// the 1-based line and a message that says what to change, or line 0 when the
// text is acceptable.
//
// Claude Code expands "@path" tokens in memory files into the content of other
// files. The check does not copy its parser. It is stricter, so that a
// difference between the two parsers cannot hide a token:
//   - A text with a leading byte order mark, a first line of "---" (front
//     matter), a carriage return that is not part of a CRLF line end, an HTML
//     comment, or a line that starts with an HTML tag is refused.
//   - An "@" is refused when a character that is not white space or a
//     backslash follows it, unless a letter or a digit precedes it (an email
//     address) or an odd number of backslashes precede it.
//   - The "@" checks skip fenced code blocks and single-line code spans. A
//     fence is skipped only when a closing line exists. An unterminated fence
//     is not skipped.
func CheckInstructionsText(text string) (int, string) {
	if strings.HasPrefix(text, "\ufeff") {
		return 1, "starts with a byte order mark. Remove the mark"
	}
	first, _, _ := strings.Cut(text, "\n")
	if strings.TrimSpace(first) == "---" {
		return 1, "starts with a line of \"---\", which Claude Code reads as front matter. Start the file with another line"
	}
	for i := 0; i < len(text); i++ {
		if text[i] == '\r' && (i+1 >= len(text) || text[i+1] != '\n') {
			return 1 + strings.Count(text[:i], "\n"), "contains a carriage return that is not part of a line end. Use LF or CRLF line ends"
		}
	}
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimSuffix(ln, "\r")
	}
	exempt := fencedLines(lines)
	for i, ln := range lines {
		if strings.Contains(ln, "<!--") {
			return i + 1, "contains an HTML comment (\"<!--\"). Remove the comment"
		}
		if startsHTML(ln) {
			return i + 1, "starts with an HTML tag. Begin the line with other text, or put the tag in a code span (backticks)"
		}
		if exempt[i] {
			continue
		}
		if tok := importToken(ln); tok != "" {
			if len(tok) > 40 {
				tok = tok[:40] + "..."
			}
			return i + 1, "contains the import token " + quote(tok) + ". Claude Code reads it as a file import. Put the token in a code span (backticks) or in a closed fenced code block, or write a backslash before the @"
		}
	}
	return 0, ""
}

func quote(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "'") + "\"" }

// startsHTML reports whether ln starts, after up to three spaces, with "<"
// and a letter, "/", "!" or "?".
func startsHTML(ln string) bool {
	t := strings.TrimLeft(ln, " ")
	if len(ln)-len(t) > 3 || len(t) < 2 || t[0] != '<' {
		return false
	}
	c := t[1]
	return c == '/' || c == '!' || c == '?' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// fencedLines marks the lines of every closed fenced code block, the opening
// and closing lines included.
func fencedLines(lines []string) []bool {
	out := make([]bool, len(lines))
	for i := 0; i < len(lines); i++ {
		ch, n := fenceOpen(lines[i])
		if ch == 0 {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if isFenceClose(lines[j], ch, n) {
				for k := i; k <= j; k++ {
					out[k] = true
				}
				i = j
				break
			}
		}
	}
	return out
}

// fenceOpen reports the fence character and length when ln opens a fenced
// code block: up to three spaces, then three or more backticks or tildes, then
// an optional info string. The info string of a backtick fence may not contain
// a backtick.
func fenceOpen(ln string) (byte, int) {
	s := strings.TrimLeft(ln, " ")
	if len(ln)-len(s) > 3 || len(s) < 3 || (s[0] != '`' && s[0] != '~') {
		return 0, 0
	}
	n := 0
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 || (s[0] == '`' && strings.Contains(s[n:], "`")) {
		return 0, 0
	}
	return s[0], n
}

// isFenceClose reports whether ln closes a fence of character ch and length
// n: up to three spaces, then only ch, at least n times, then only spaces and
// tabs.
func isFenceClose(ln string, ch byte, n int) bool {
	s := strings.TrimLeft(ln, " ")
	if len(ln)-len(s) > 3 {
		return false
	}
	k := 0
	for k < len(s) && s[k] == ch {
		k++
	}
	return k >= n && strings.Trim(s[k:], " \t") == ""
}

// importToken returns the first import token in ln outside code spans, or "".
func importToken(ln string) string {
	for i := 0; i < len(ln); {
		switch ln[i] {
		case '`':
			if oddBackslashes(ln, i) {
				i++
				continue
			}
			n := 0
			for i+n < len(ln) && ln[i+n] == '`' {
				n++
			}
			if end := closingRun(ln, i+n, n); end >= 0 {
				i = end
			} else {
				i += n
			}
		case '@':
			if isImportAt(ln, i) {
				j := i + 1
				for j < len(ln) && !isSpaceByte(ln, j) {
					j++
				}
				return ln[i:j]
			}
			i++
		default:
			i++
		}
	}
	return ""
}

// isImportAt reports whether the "@" at ln[i] must be refused.
func isImportAt(ln string, i int) bool {
	if i+1 >= len(ln) {
		return false
	}
	next, _ := utf8.DecodeRuneInString(ln[i+1:])
	if jsSpace(next) || next == '\\' {
		return false
	}
	if oddBackslashes(ln, i) {
		return false
	}
	if i > 0 {
		prev, _ := utf8.DecodeLastRuneInString(ln[:i])
		if unicode.IsLetter(prev) || unicode.IsNumber(prev) {
			return false
		}
	}
	return true
}

func isSpaceByte(ln string, j int) bool {
	r, _ := utf8.DecodeRuneInString(ln[j:])
	return jsSpace(r) || r == '\\'
}

// jsSpace reports whether r matches "\s" of a JavaScript regular expression.
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// oddBackslashes reports whether an odd number of backslashes precede ln[i].
func oddBackslashes(ln string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && ln[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

// closingRun returns the index just after the first run of exactly n
// backticks at or after from, or -1.
func closingRun(p string, from, n int) int {
	for i := from; i < len(p); {
		if p[i] != '`' {
			i++
			continue
		}
		k := 0
		for i+k < len(p) && p[i+k] == '`' {
			k++
		}
		if k == n {
			return i + k
		}
		i += k
	}
	return -1
}

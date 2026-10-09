package profile

import (
	"fmt"
	"strings"
)

// MaxInstructionsSize is the largest joined instructions text accepted. Each
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

// FindImport looks for a CLAUDE.md import token in text: an "@" at the start
// of a line or after white space, followed by a character that is not white
// space. Claude Code expands such a token as a file import. The scan skips
// fenced code blocks (``` or ~~~) and inline code spans, where Claude Code
// does not expand imports. An unterminated fence runs to the end of the text.
// It returns the 1-based line and the token, or line 0 when there is none.
// It accepts LF and CRLF line ends.
func FindImport(text string) (line int, token string) {
	lines := strings.Split(text, "\n")
	var para []string
	paraStart := 0
	flush := func() (int, string) {
		if len(para) == 0 {
			return 0, ""
		}
		l, t := scanParagraph(strings.Join(para, "\n"))
		defer func() { para = para[:0] }()
		if l == 0 {
			return 0, ""
		}
		return paraStart + l, t
	}
	var fenceCh byte
	fenceLen := 0
	for i, raw := range lines {
		ln := strings.TrimRight(raw, "\r")
		if fenceCh != 0 {
			if isFenceClose(ln, fenceCh, fenceLen) {
				fenceCh = 0
			}
			continue
		}
		if ch, n := fenceOpen(ln); ch != 0 {
			if l, t := flush(); l != 0 {
				return l, t
			}
			fenceCh, fenceLen = ch, n
			continue
		}
		if strings.TrimSpace(ln) == "" {
			if l, t := flush(); l != 0 {
				return l, t
			}
			continue
		}
		if len(para) == 0 {
			paraStart = i
		}
		para = append(para, ln)
	}
	return flush()
}

// fenceOpen reports the fence character and length when ln opens a fenced
// code block: up to three spaces, then three or more backticks or tildes. The
// info string of a backtick fence may not contain a backtick.
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
// n: up to three spaces, at least n of ch, then only white space.
func isFenceClose(ln string, ch byte, n int) bool {
	s := strings.TrimLeft(ln, " ")
	if len(ln)-len(s) > 3 {
		return false
	}
	k := 0
	for k < len(s) && s[k] == ch {
		k++
	}
	return k >= n && strings.TrimSpace(s[k:]) == ""
}

// scanParagraph scans text without fences. Inline code spans may cross line
// ends but not blank lines, and the caller passes one paragraph at a time.
func scanParagraph(p string) (int, string) {
	line := 1
	for i := 0; i < len(p); {
		switch c := p[i]; {
		case c == '\n':
			line++
			i++
		case c == '`':
			n := 0
			for i+n < len(p) && p[i+n] == '`' {
				n++
			}
			end := closingRun(p, i+n, n)
			if end < 0 {
				i += n // no closing run: literal backticks
				continue
			}
			line += strings.Count(p[i:end], "\n")
			i = end
		case c == '@' && (i == 0 || isSpace(p[i-1])) && i+1 < len(p) && !isSpace(p[i+1]):
			j := i + 1
			for j < len(p) && !isSpace(p[j]) {
				j++
			}
			return line, p[i:j]
		default:
			i++
		}
	}
	return 0, ""
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

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// importProblem formats the refusal text for an import token.
func importProblem(token string) string {
	if len(token) > 40 {
		token = token[:40] + "..."
	}
	return fmt.Sprintf("contains the import token %q outside a code span. Put the token in backticks", token)
}

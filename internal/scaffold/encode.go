package scaffold

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// tomlString encodes s as a TOML basic string. Values are validated before
// they get here; the escaping is the second line of defense, so that nothing a
// user types can end the string or start a new key.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		case r > 0xffff:
			fmt.Fprintf(&b, `\U%08X`, r)
		case r == 0x2028 || r == 0x2029 || (r >= 0x80 && r < 0xa0) || r == 0xfeff || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || (r >= 0x200b && r <= 0x200f):
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// tomlArray encodes a one-line array of strings.
func tomlArray(ss []string) string {
	parts := make([]string, len(ss))
	for i, s := range ss {
		parts[i] = tomlString(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// mdEscape escapes s for Markdown text: every ASCII punctuation character is
// preceded by a backslash, which CommonMark allows for all of them, so a name
// can never start a link, emphasis, a heading, HTML or a table.
func mdEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// mdCode renders s as an inline code span that cannot be closed from inside.
func mdCode(s string) string {
	fence := "`"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// jsonIndent encodes v as JSON with two-space indentation, no HTML escaping,
// UTF-8 and a single trailing newline: the format of the starter marketplace.
func jsonIndent(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

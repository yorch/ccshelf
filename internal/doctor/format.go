package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// WriteText renders the report for a terminal: one line per finding
// ("DOC001 warning overlap: ..."), hints indented below, then skipped checks
// and a summary line.
func (r *Report) WriteText(w io.Writer) error {
	var b bytes.Buffer
	for _, f := range r.Findings {
		check := f.Check
		if check == "" {
			check = "policy"
		}
		fmt.Fprintf(&b, "%-6s %-7s %s: %s\n", f.Code, f.Severity, check, oneLine(f.Message))
		if f.Hint != "" {
			lines := strings.Split(f.Hint, "\n")
			fmt.Fprintf(&b, "       hint: %s\n", oneLine(lines[0]))
			for _, l := range lines[1:] {
				fmt.Fprintf(&b, "             %s\n", oneLine(l))
			}
		}
	}
	for _, s := range r.Skipped {
		fmt.Fprintf(&b, "skipped %s %s: %s\n", s.Code, s.Check, oneLine(s.Reason))
	}
	c := r.Counts()
	if len(r.Findings) == 0 {
		b.WriteString("doctor: no findings\n")
	} else {
		fmt.Fprintf(&b, "doctor: %d error(s), %d warning(s), %d info\n", c.Errors, c.Warnings, c.Infos)
	}
	_, err := w.Write(b.Bytes())
	return err
}

// Text returns WriteText as a string.
func (r *Report) Text() string {
	var b strings.Builder
	_ = r.WriteText(&b)
	return b.String()
}

// oneLine makes a value safe for one terminal line. Control characters (C0,
// DEL, C1, including tab and newline) and the Unicode line and paragraph
// separators become a space, so a hostile string cannot move the cursor or
// recolor the terminal; invisible formatting characters (zero-width characters,
// bidirectional overrides and isolates, the byte order mark, the soft hyphen)
// are removed, so text cannot be reordered or hidden.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7f, r >= 0x80 && r < 0xa0, r == 0x2028, r == 0x2029:
			return ' '
		case r == 0xad, r >= 0x200b && r <= 0x200f, r >= 0x202a && r <= 0x202e,
			r >= 0x2060 && r <= 0x2064, r >= 0x2066 && r <= 0x2069, r == 0xfeff:
			return -1
		}
		return r
	}, s)
}

// JSON returns the report as the data payload of the doctor JSON envelope:
// {"summary":{...},"findings":[...],"skipped":[...]} with two-space indent.
// The command wraps it in the common {"version","kind":"doctor","data"}
// envelope itself.
func (r *Report) JSON() ([]byte, error) {
	b, err := json.MarshalIndent(r.Payload(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding the doctor report: %w", err)
	}
	return append(b, '\n'), nil
}

// Payload is the data of the doctor JSON output.
type Payload struct {
	Summary  Counts    `json:"summary"`
	Findings []Finding `json:"findings"`
	Skipped  []Skip    `json:"skipped"`
}

// Payload returns the report as a Payload with no nil slices.
func (r *Report) Payload() Payload {
	p := Payload{Summary: r.Counts(), Findings: r.Findings, Skipped: r.Skipped}
	if p.Findings == nil {
		p.Findings = []Finding{}
	}
	if p.Skipped == nil {
		p.Skipped = []Skip{}
	}
	return p
}

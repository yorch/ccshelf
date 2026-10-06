package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// JSONVersion is the version of the JSON report format.
const JSONVersion = 1

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

// oneLine removes control characters other than tab from output, so a hostile
// string cannot move the cursor or recolor the terminal.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return ' '
		}
		return r
	}, s)
}

type jsonReport struct {
	Version  int       `json:"version"`
	Kind     string    `json:"kind"`
	Summary  Counts    `json:"summary"`
	Findings []Finding `json:"findings"`
	Skipped  []Skip    `json:"skipped"`
}

// JSON returns the report as indented JSON:
// {"version":1,"kind":"doctor","summary":{...},"findings":[...],"skipped":[...]}.
func (r *Report) JSON() ([]byte, error) {
	out := jsonReport{Version: JSONVersion, Kind: "doctor", Summary: r.Counts(), Findings: r.Findings, Skipped: r.Skipped}
	if out.Findings == nil {
		out.Findings = []Finding{}
	}
	if out.Skipped == nil {
		out.Skipped = []Skip{}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding the doctor report: %w", err)
	}
	return append(b, '\n'), nil
}

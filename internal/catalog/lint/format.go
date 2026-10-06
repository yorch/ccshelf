package lint

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// JSONVersion is the version of the JSON report format.
const JSONVersion = 1

// cleanText makes untrusted text safe for a terminal: control characters,
// DEL, C1 controls and bidirectional overrides are shown as escapes.
func cleanText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteString(" ")
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f || r == 0xfeff:
			fmt.Fprintf(&b, "\\u%04x", r)
		case unicode.IsControl(r):
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FormatText renders the report for a terminal.
func FormatText(r *Report) string {
	var b strings.Builder
	for _, f := range r.Findings {
		loc := f.File
		if loc != "" && f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", loc, f.Line)
		}
		if loc != "" {
			loc += ": "
		}
		plugin := ""
		if f.Plugin != "" {
			plugin = " [" + f.Plugin + "]"
		}
		fmt.Fprintf(&b, "%s%s %s%s %s\n", cleanText(loc), f.Severity, f.Code, cleanText(plugin), cleanText(f.Message))
		if f.Hint != "" {
			fmt.Fprintf(&b, "    hint: %s\n", cleanText(f.Hint))
		}
	}
	c := r.Counts()
	if len(r.Findings) == 0 {
		b.WriteString("no findings\n")
	} else {
		fmt.Fprintf(&b, "%d error(s), %d warning(s), %d info\n", c.Errors, c.Warnings, c.Infos)
	}
	return b.String()
}

// FormatJSON renders {"version":1,"findings":[...]} with two-space indent.
func FormatJSON(r *Report) ([]byte, error) {
	doc := struct {
		Version  int       `json:"version"`
		Findings []Finding `json:"findings"`
	}{JSONVersion, r.Findings}
	if doc.Findings == nil {
		doc.Findings = []Finding{}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode lint report: %w", err)
	}
	return append(out, '\n'), nil
}

var (
	dataEscaper = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")
	propEscaper = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C")
)

// EscapeGitHubData escapes the message of a GitHub workflow command.
func EscapeGitHubData(s string) string { return dataEscaper.Replace(s) }

// EscapeGitHubProperty escapes a property value of a workflow command.
func EscapeGitHubProperty(s string) string { return propEscaper.Replace(s) }

// FormatGitHub renders GitHub Actions workflow commands, one per finding:
// "::error file=F,line=N,title=CODE::message". Error maps to error, Warning
// to warning and Info to notice. Because names and descriptions come from
// untrusted files, % and line breaks are escaped in the message, and also
// ":" and "," in the properties, so a finding cannot start another command.
func FormatGitHub(r *Report) string {
	var b strings.Builder
	for _, f := range r.Findings {
		level := "notice"
		switch f.Severity {
		case Error:
			level = "error"
		case Warning:
			level = "warning"
		}
		var props []string
		if f.File != "" {
			props = append(props, "file="+EscapeGitHubProperty(f.File))
			if f.Line > 0 {
				props = append(props, fmt.Sprintf("line=%d", f.Line))
			}
		}
		props = append(props, "title="+EscapeGitHubProperty(f.Code))
		msg := f.Message
		if f.Hint != "" {
			msg += " (" + f.Hint + ")"
		}
		fmt.Fprintf(&b, "::%s %s::%s\n", level, strings.Join(props, ","), EscapeGitHubData(cleanText(msg)))
	}
	return b.String()
}

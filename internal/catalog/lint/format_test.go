package lint

import (
	"encoding/json"
	"strings"
	"testing"
)

func sample() *Report {
	r := &Report{Findings: []Finding{
		{Severity: Error, Code: "CAT010", Message: "plugin has no sidecar", File: "catalog/plugins/x.toml", Line: 3, Plugin: "x", Hint: "create it"},
		{Severity: Warning, Code: "CAT043", Message: "no CODEOWNERS"},
		{Severity: Info, Code: "CAT031", Message: "external", File: ".claude-plugin/marketplace.json"},
	}}
	r.Sort()
	return r
}

func TestFormatText(t *testing.T) {
	got := FormatText(sample())
	want := `warning CAT043 no CODEOWNERS
.claude-plugin/marketplace.json: info CAT031 external
catalog/plugins/x.toml:3: error CAT010 [x] plugin has no sidecar
    hint: create it
1 error(s), 1 warning(s), 1 info
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if FormatText(&Report{}) != "no findings\n" {
		t.Error("empty report text")
	}
}

func TestFormatTextNeutralizesControlCharacters(t *testing.T) {
	r := &Report{Findings: []Finding{{Severity: Error, Code: "CAT003", Plugin: "evil\x1b[31m", Message: "name \"a\x1b]0;pwned\x07\u202eb\" bad\nsecond line", File: "f\x1b"}}}
	out := FormatText(r)
	for _, bad := range []string{"\x1b", "\x07", "\u202e"} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains %q", bad)
		}
	}
	if strings.Count(out, "\n") != 2 { // finding line and summary line
		t.Errorf("a newline in a message must not create a line: %q", out)
	}
}

func TestFormatJSON(t *testing.T) {
	b, err := FormatJSON(sample())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version  int       `json:"version"`
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 || len(doc.Findings) != 3 || doc.Findings[2].Hint != "create it" {
		t.Errorf("doc = %+v", doc)
	}
	empty, _ := FormatJSON(&Report{})
	if !strings.Contains(string(empty), `"findings": []`) {
		t.Errorf("empty report must encode an empty array: %s", empty)
	}
	if !strings.HasSuffix(string(b), "}\n") || !strings.Contains(string(b), "\n  \"findings\"") {
		t.Errorf("format: %s", b)
	}
}

func TestFormatGitHub(t *testing.T) {
	got := FormatGitHub(sample())
	want := "::warning title=CAT043::no CODEOWNERS\n" +
		"::notice file=.claude-plugin/marketplace.json,title=CAT031::external\n" +
		"::error file=catalog/plugins/x.toml,line=3,title=CAT010::plugin has no sidecar (create it)\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatGitHubEscapesUntrustedText(t *testing.T) {
	r := &Report{Findings: []Finding{{
		Severity: Error, Code: "CAT003",
		File:    "dir,with:odd%name\nx",
		Message: "plugin \"a%b\r\n::error::injected\" has a bad name, really: yes",
		Hint:    "line\nbreak",
	}}}
	out := FormatGitHub(r)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("injection created %d lines: %q", len(lines), out)
	}
	if !strings.HasPrefix(out, "::error file=dir%2Cwith%3Aodd%25name%0Ax,title=CAT003::") {
		t.Errorf("property escaping: %q", out)
	}
	if strings.Contains(out, "\r") || !strings.Contains(out, "%25") {
		t.Errorf("data escaping: %q", out)
	}
	if EscapeGitHubData("a:b,c%\r\n") != "a:b,c%25%0D%0A" {
		t.Errorf("EscapeGitHubData = %q", EscapeGitHubData("a:b,c%\r\n"))
	}
	if EscapeGitHubProperty("a:b,c%\r\n") != "a%3Ab%2Cc%25%0D%0A" {
		t.Errorf("EscapeGitHubProperty = %q", EscapeGitHubProperty("a:b,c%\r\n"))
	}
}

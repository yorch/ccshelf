package doctor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONShape(t *testing.T) {
	b, err := (&Report{}).JSON()
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if _, ok := v["version"]; ok {
		t.Errorf("the payload has no envelope fields: %v", v)
	}
	if _, ok := v["kind"]; ok {
		t.Errorf("the payload has no envelope fields: %v", v)
	}
	if _, ok := v["summary"].(map[string]any); !ok {
		t.Errorf("summary missing: %v", v)
	}
	if f, ok := v["findings"].([]any); !ok || len(f) != 0 {
		t.Errorf("findings must be an empty array: %v", v["findings"])
	}
	if s, ok := v["skipped"].([]any); !ok || len(s) != 0 {
		t.Errorf("skipped must be an empty array: %v", v["skipped"])
	}
}

func TestTextNoFindingsAndSanitizing(t *testing.T) {
	if got := (&Report{}).Text(); got != "doctor: no findings\n" {
		t.Errorf("empty = %q", got)
	}
	r := &Report{Findings: []Finding{{Code: "DOC001", Severity: Warning, Check: "overlap", Message: "a\x1b[2Jb\r\nc", Hint: "line1\nline2\x07"}}}
	out := r.Text()
	if strings.ContainsAny(out, "\x1b\r\x07") {
		t.Errorf("control characters survived: %q", out)
	}
	for _, want := range []string{"DOC001 warning overlap: a [2Jb  c", "hint: line1", "line2", "1 warning(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
}

func TestOneLine(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "hello world", "hello world"},
		{"unicode kept", "café — 日本", "café — 日本"},
		{"C0 to space", "a\x00b\x1bc\x07d\te\nf\rg", "a b c d e f g"},
		{"DEL to space", "a\x7fb", "a b"},
		{"C1 to space", "a\u0080b\u009bc\u009fd", "a b c d"},
		{"just past C1 kept", "a\u00a0b", "a\u00a0b"},
		{"line and paragraph separators", "a\u2028b\u2029c", "a b c"},
		{"bidi overrides removed", "a\u202eb\u202ac\u202dd\u202be\u202cf", "abcdef"},
		{"bidi isolates removed", "a\u2066b\u2067c\u2068d\u2069e", "abcde"},
		{"zero width removed", "a\u200bb\u200cc\u200dd\u200ee\u200ff\u2060g\u2064h\ufeffi\u00adj", "abcdefghij"},
		{"neighbors of the removed ranges kept", "a\u200ab\u2010c\u2029", "a\u200ab\u2010c "},
		{"invalid utf8", "a\xffb", "a\ufffdb"},
	}
	for _, tc := range tests {
		if got := oneLine(tc.in); got != tc.want {
			t.Errorf("%s: oneLine(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestTextRemovesBidiInFindings(t *testing.T) {
	r := &Report{Findings: []Finding{{Code: "DOC001", Severity: Warning, Check: "overlap", Message: "safe\u202etxt.exe", Hint: "h\u200bint"}}}
	out := r.Text()
	if strings.ContainsAny(out, "\u202e\u200b") || !strings.Contains(out, "safetxt.exe") || !strings.Contains(out, "hint: hint") {
		t.Errorf("out = %q", out)
	}
}

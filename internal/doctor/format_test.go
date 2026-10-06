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
	if v["version"] != float64(1) || v["kind"] != "doctor" {
		t.Errorf("header = %v", v)
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

package profile

import (
	"encoding/json"
	"strings"
	"testing"
)

func styleFiles(parent, child string) map[string]string {
	line := func(s string) string {
		if s == "" {
			return ""
		}
		return "[session]\noutput_style = \"" + s + "\"\n"
	}
	return map[string]string{
		"profiles/base.toml":  "name = \"base\"\n" + line(parent),
		"profiles/child.toml": "name = \"child\"\nextends = [\"base\"]\n" + line(child),
	}
}

func TestOutputStyleInheritance(t *testing.T) {
	for _, c := range []struct{ parent, child, want string }{
		{"Concise", "", "Concise"},
		{"Concise", "Learning", "Learning"},
		{"", "Learning", "Learning"},
		{"", "", ""},
	} {
		r := closureFor(t, styleFiles(c.parent, c.child), KindOrg, "child")
		if got := r.Merged.Session.OutputStyle; got != c.want {
			t.Errorf("parent %q child %q: got %q, want %q", c.parent, c.child, got, c.want)
		}
	}
}

func TestOutputStyleInClosure(t *testing.T) {
	a := closureFor(t, styleFiles("", "Concise"), KindOrg, "child").Closure.Hash
	b := closureFor(t, styleFiles("", "Learning"), KindOrg, "child").Closure.Hash
	c := closureFor(t, styleFiles("", ""), KindOrg, "child").Closure.Hash
	if a == b || a == c || b == c {
		t.Errorf("output_style must change the closure hash: %s %s %s", a, b, c)
	}
}

// TestOutputStyleUnsetKeepsDigest proves that a profile without output_style
// has the identity digest that it had before the key existed.
func TestOutputStyleUnsetKeepsDigest(t *testing.T) {
	m := &Manifest{Name: "n", Description: "d", Owner: "o", Status: "active", Session: Session{Model: "opus", Effort: "low"}}
	id, _, err := profileDigests(m)
	if err != nil {
		t.Fatal(err)
	}
	type legacy struct {
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		Owner        string   `json:"owner"`
		Status       string   `json:"status"`
		SupersededBy string   `json:"superseded_by"`
		WhenToUse    []string `json:"when_to_use"`
		AvoidWhen    []string `json:"avoid_when"`
		Model        string   `json:"session.model"`
		Effort       string   `json:"session.effort"`
	}
	b, err := json.Marshal(legacy{Name: "n", Description: "d", Owner: "o", Status: "active", WhenToUse: []string{}, AvoidWhen: []string{}, Model: "opus", Effort: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if want := digest(b); id != want {
		t.Errorf("identity digest changed for a profile without output_style: %s != %s", id, want)
	}
	m.Session.OutputStyle = "Concise"
	id2, _, _ := profileDigests(m)
	if id2 == id {
		t.Error("identity digest ignores output_style")
	}
}

func TestOutputStyleCaseWarning(t *testing.T) {
	for _, c := range []struct {
		style string
		warn  bool
	}{
		{"explanatory", true}, {"CONCISE", true}, {"Explanatory", false}, {"Default", false},
		{"default", false}, {"my-style", false}, {"Concise2", false},
	} {
		r := closureFor(t, styleFiles("", c.style), KindOrg, "child")
		got := false
		for _, w := range r.Warnings {
			if strings.Contains(w, "output_style") {
				got = true
			}
		}
		if got != c.warn {
			t.Errorf("%q: warning = %v, want %v (%v)", c.style, got, c.warn, r.Warnings)
		}
	}
}

func TestOutputStyleProjectProfile(t *testing.T) {
	// Like model, a project profile may set output_style (SR2 does not list it).
	r := closureFor(t, map[string]string{"profiles/p.toml": "name = \"p\"\n[session]\noutput_style = \"Concise\"\n"}, KindProject, "p")
	if r.Merged.Session.OutputStyle != "Concise" {
		t.Errorf("got %q", r.Merged.Session.OutputStyle)
	}
}

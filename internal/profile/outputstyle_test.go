package profile

import (
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

// TestOutputStyleUnsetKeepsDigest pins digests that origin/main (v0.8.0)
// computed for fixed manifests. A profile without output_style must keep them.
func TestOutputStyleUnsetKeepsDigest(t *testing.T) {
	m := &Manifest{Name: "n", Description: "d", Owner: "o", Status: "active", Session: Session{Model: "opus", Effort: "low"}}
	id, ctl, err := profileDigests(m)
	if err != nil {
		t.Fatal(err)
	}
	const wantID = "f136e2c4d075c9e9d2c5504eb4d1242ea7df322f12dfee31e230106c40a6fc04"
	const wantCtl = "e21d5f0860a6c16dfcf013ead58063425dda4fea66028699fd6aadd8a3f51b94"
	if id != wantID || ctl != wantCtl {
		t.Errorf("digests changed for a profile without output_style:\nidentity %s\ncontrols %s", id, ctl)
	}
	m.Session.OutputStyle = "Concise"
	id2, ctl2, _ := profileDigests(m)
	if id2 != id {
		t.Error("output_style must not change the identity item")
	}
	if ctl2 == ctl {
		t.Error("controls digest ignores output_style")
	}
	r := closureFor(t, map[string]string{"profiles/a.toml": "name = \"a\"\ndescription = \"d\"\n[session]\nmodel = \"opus\"\n"}, KindOrg, "a")
	if want := "40b411784dcef95c40483610e940a7cddb09383bc3d4ce137419059fdbcd58f4"; r.Closure.Hash != want {
		t.Errorf("closure hash = %s, want %s", r.Closure.Hash, want)
	}
}

func TestOutputStyleIsRisky(t *testing.T) {
	a := closureFor(t, styleFiles("", ""), KindOrg, "child").Closure
	b := closureFor(t, styleFiles("", "Concise"), KindOrg, "child").Closure
	for i := range a.Items {
		if a.Items[i].Digest != b.Items[i].Digest && !b.Items[i].Risky {
			t.Errorf("changed item %s %s is not risky", b.Items[i].Kind, b.Items[i].Name)
		}
	}
}

func TestOutputStyleCaseWarning(t *testing.T) {
	for _, c := range []struct {
		style string
		warn  bool
	}{
		{"explanatory", true},
		{"CONCISE", true},
		{"Explanatory", false},
		{"Default", false},
		{"default", false},
		{"DEFAULT", false},
		{"my-style", false},
		{"Concise2", false},
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

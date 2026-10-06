package profile

import (
	"strings"
	"testing"
)

func TestList(t *testing.T) {
	org := orgSrc(t)
	pers := DirSource(KindPersonal, fixture(t, "tree", "shadow", "profiles"))
	got, err := List([]Source{org, pers, org})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "base,frontend,old" {
		t.Fatalf("names = %v", names)
	}
	fe := got[1]
	if fe.Kind != KindPersonal || len(fe.Shadows) != 1 || fe.Shadows[0] != org.ID() || fe.Description != "My own frontend" || fe.Status != "active" {
		t.Errorf("frontend: %+v", fe)
	}
	if got[2].Status != "deprecated" || got[0].Owner != "@platform" || got[0].Kind != KindOrg {
		t.Errorf("rows: %+v", got)
	}
}

func TestListConflictsAndErrors(t *testing.T) {
	a := mk(t, map[string]string{"profiles/dup.toml": "name = \"dup\"\n", "profiles/broken.toml": "name = \"broken\"\nhooks = 1\n"})
	b := mk(t, map[string]string{"profiles/dup.toml": "name = \"dup\"\n"})
	got, err := List([]Source{src(KindOrg, a), src(KindOrg, b)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "broken" || got[0].Err == "" || len(got[1].Conflict) != 2 {
		t.Errorf("got %+v", got)
	}
	proj := mk(t, map[string]string{"profiles/dup.toml": "name = \"dup\"\n"})
	pers := mk(t, map[string]string{"profiles/dup.toml": "name = \"dup\"\n"})
	got, _ = List([]Source{src(KindPersonal, pers), src(KindProject, proj)})
	if len(got[0].Conflict) != 2 || len(got[0].Shadows) != 0 {
		t.Errorf("project must conflict: %+v", got[0])
	}
}

func TestDescribeOptionalSections(t *testing.T) {
	root := mk(t, map[string]string{"profiles/min.toml": "name = \"min\"\nstatus = \"deprecated\"\nsuperseded_by = \"other\"\naccount = \"w\"\n"})
	r, err := Resolve("min", []Source{src(KindPersonal, root)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d := Describe(r)
	for _, want := range []string{"superseded by other", "Account: w", "Warning: profile min is deprecated", "Closure: sha256:"} {
		if !strings.Contains(d, want) {
			t.Errorf("missing %q in\n%s", want, d)
		}
	}
	if strings.Contains(d, "Skills:") || strings.Contains(d, "Description:") {
		t.Errorf("empty sections printed:\n%s", d)
	}
}

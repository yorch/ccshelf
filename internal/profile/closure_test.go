package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func closureFor(t *testing.T, files map[string]string, kind Kind, name string) *Resolved {
	t.Helper()
	root := mk(t, files)
	r, err := Resolve(name, []Source{src(kind, root)}, ResolveOptions{AllowProject: true})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

var baseFiles = func() map[string]string {
	return map[string]string{
		"profiles/a.toml":   "name = \"a\"\n[plugins]\ninclude = [\"p@m\"]\n[mcp]\nservers = [\"s\"]\n[session]\nappend_system_prompt_file = \"prompts/x.md\"\n",
		"mcp/registry.toml": "[servers.s]\ncommand = \"run\"\nargs = [\"1\"]\n",
		"prompts/x.md":      "hello\n",
	}
}

func TestClosureDeterministicAcrossRoots(t *testing.T) {
	a := closureFor(t, baseFiles(), KindOrg, "a")
	b := closureFor(t, baseFiles(), KindOrg, "a") // different temp root
	if a.Closure.Hash != b.Closure.Hash || len(a.Closure.Hash) != 64 {
		t.Errorf("hashes differ across roots: %s %s", a.Closure.Hash, b.Closure.Hash)
	}
	for _, it := range a.Closure.Items {
		if strings.Contains(it.Name, string(os.PathSeparator)+"T") || strings.HasPrefix(it.Name, "dir:/") {
			t.Errorf("machine path in item %+v", it)
		}
	}
}

func TestClosureCRLFNormalized(t *testing.T) {
	lf := baseFiles()
	crlf := baseFiles()
	for k, v := range crlf {
		crlf[k] = strings.ReplaceAll(v, "\n", "\r\n")
	}
	a := closureFor(t, lf, KindOrg, "a")
	b := closureFor(t, crlf, KindOrg, "a")
	if a.Closure.Hash != b.Closure.Hash {
		t.Errorf("CRLF changes the closure")
	}
}

func TestClosureChangesAndRisk(t *testing.T) {
	base := closureFor(t, baseFiles(), KindOrg, "a").Closure
	mutate := map[string]func(map[string]string){
		"registry command": func(f map[string]string) {
			f["mcp/registry.toml"] = "[servers.s]\ncommand = \"evil\"\nargs = [\"1\"]\n"
		},
		"registry windows override": func(f map[string]string) {
			f["mcp/registry.toml"] += "[servers.s.windows]\ncommand = \"cmd\"\n"
		},
		"prompt": func(f map[string]string) { f["prompts/x.md"] = "hello!\n" },
		"plugin": func(f map[string]string) {
			f["profiles/a.toml"] = strings.Replace(f["profiles/a.toml"], "p@m", "q@m", 1)
		},
		"profile description": func(f map[string]string) {
			f["profiles/a.toml"] = strings.Replace(f["profiles/a.toml"], "name = \"a\"\n", "name = \"a\"\ndescription = \"changed\"\n", 1)
		},
	}
	for name, fn := range mutate {
		t.Run(name, func(t *testing.T) {
			f := baseFiles()
			fn(f)
			c := closureFor(t, f, KindOrg, "a").Closure
			if c.Hash == base.Hash {
				t.Error("hash did not change")
			}
		})
	}
	risky := map[string]bool{}
	for _, it := range base.Items {
		risky[it.Kind] = risky[it.Kind] || it.Risky
	}
	for _, k := range []string{ItemRegistry, ItemPrompt, ItemPlugin, ItemProfileControls} {
		if !risky[k] {
			t.Errorf("%s items must be risky", k)
		}
	}
	if risky[ItemProfile] || risky[ItemSource] {
		t.Errorf("profile identity and source items are not risky: %v", risky)
	}
}

func TestClosureItemsSortedAndKindsDiffer(t *testing.T) {
	a := closureFor(t, baseFiles(), KindOrg, "a").Closure
	for i := 1; i < len(a.Items); i++ {
		p, c := a.Items[i-1], a.Items[i]
		if p.Kind > c.Kind || (p.Kind == c.Kind && p.Name > c.Name) {
			t.Errorf("unsorted: %v %v", p, c)
		}
	}
	// the same files from a personal source have a different source item
	p := closureFor(t, baseFiles(), KindPersonal, "a").Closure
	if p.Hash == a.Hash {
		t.Error("source kind must be part of the closure")
	}
}

type commitSource struct {
	Source
	sha string
}

func (c commitSource) Commit() string { return c.sha }
func (c commitSource) ID() string     { return "git:https://example.com/org.git@v1" }
func (c commitSource) Open(name string) (*File, error) {
	f, err := c.Source.Open(name)
	if f != nil {
		f.Source = c
	}
	return f, err
}

func TestClosureIncludesCommit(t *testing.T) {
	root := mk(t, baseFiles())
	h := func(sha string) (string, []ClosureItem) {
		r, err := Resolve("a", []Source{commitSource{src(KindOrg, root), sha}}, ResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return r.Closure.Hash, r.Closure.Items
	}
	h1, items := h("1111")
	h2, _ := h("2222")
	if h1 == h2 {
		t.Error("commit must change the closure")
	}
	found := false
	for _, it := range items {
		if it.Kind == ItemSource && it.Name == "git:https://example.com/org.git@v1" {
			found = true
		}
	}
	if !found {
		t.Errorf("git id should be used verbatim: %+v", items)
	}
}

func TestPortableSourceID(t *testing.T) {
	d := DirSource(KindProject, filepath.Join(t.TempDir(), "profiles"))
	if PortableSourceID(d) != "dir:project" {
		t.Error(PortableSourceID(d))
	}
}

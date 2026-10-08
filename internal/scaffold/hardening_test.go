package scaffold

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/yorch/ccshelf/internal/orgconfig"
)

// hostile values: each tries to end its value and start something of its own
// in the format it lands in.
var hostileInputs = []string{
	"x\n[injected]\nkey = 1",
	"x\"\nkey = \"y",
	"x\r\n# y",
	"a`b`c",
	"``` \n<script>alert(1)</script>",
	"${{ secrets.TOKEN }}",
	"bidi\u202e\u2066text",
	"<script>alert(1)</script>",
	"[link](http://example.invalid)",
	"x\\",
	"../../etc/passwd",
	"*\n/.github/ @evil",
}

// auditFiles checks every file of the plan against the format it is written
// in: the output must parse, carry only the keys this package writes, and
// hold nothing that the hostile inputs could have injected.
func auditFiles(t *testing.T, label string, plan *Plan) {
	t.Helper()
	for _, e := range plan.Entries {
		if e.Action != ActionCreate && e.Action != ActionOverwrite {
			continue
		}
		body := string(e.Content)
		where := label + ": " + e.Path
		if strings.ContainsAny(e.Path, "\n\r\x00\\") || strings.Contains(e.Path, "..") {
			t.Errorf("%s: unsafe path", where)
		}
		switch {
		case e.Path == "ccshelf.toml":
			var v map[string]any
			if err := toml.Unmarshal(e.Content, &v); err != nil {
				t.Errorf("%s does not parse: %v\n%s", where, err, body)
				continue
			}
			for k := range v {
				if k != "lint" && k != "catalog" && k != "profiles" && k != "protect" {
					t.Errorf("%s: injected table %q", where, k)
				}
			}
			assertKeys(t, where, v["lint"], "require", "platform_owners", "require_when_deprecated", "max_review_age_days")
			assertKeys(t, where, v["catalog"], "title", "metadata_source", "marketplaces")
			assertKeys(t, where, v["profiles"], "dir", "mcp_registry")
			assertKeys(t, where, v["protect"])
		case strings.HasPrefix(e.Path, "catalog/plugins/"):
			var v map[string]any
			if err := toml.Unmarshal(e.Content, &v); err != nil {
				t.Errorf("%s does not parse: %v\n%s", where, err, body)
				continue
			}
			assertKeys(t, where, v, "owner", "status", "when_to_use", "avoid_when", "support")
		case e.Path == ".claude-plugin/marketplace.json":
			var v struct {
				Name, Description string
				Owner             map[string]any
				Plugins           []map[string]any
			}
			if err := json.Unmarshal(e.Content, &v); err != nil {
				t.Errorf("%s does not parse: %v", where, err)
				continue
			}
			var top map[string]any
			_ = json.Unmarshal(e.Content, &top)
			assertKeys(t, where, top, "name", "owner", "description", "plugins")
			for _, p := range v.Plugins {
				assertKeys(t, where, p, "name", "source", "description", "author")
			}
		case e.Path == "profiles/example.toml.sample":
			for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
				if l != "" && !strings.HasPrefix(l, "#") {
					t.Errorf("%s: a line that is not a comment: %q", where, l)
				}
			}
			for _, l := range strings.Split(body, "\n") {
				if rest, ok := strings.CutPrefix(l, "# include = "); ok {
					var v []string
					if err := toml.Unmarshal([]byte("x = "+rest), &struct{ X *[]string }{&v}); err != nil || len(v) != 1 {
						t.Errorf("%s: the include line is not one TOML string: %q (%v)", where, l, err)
					}
				}
			}
		case e.Path == ".github/CODEOWNERS":
			for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				f := strings.Fields(l)
				if len(f) < 2 || !regexp.MustCompile(`^/?[A-Za-z0-9._*/-]+$`).MatchString(f[0]) {
					t.Errorf("%s: a rule with an unexpected pattern: %q", where, l)
				}
				for _, o := range f[1:] {
					if !ValidOwner(o) {
						t.Errorf("%s: an invalid owner %q in %q", where, o, l)
					}
				}
			}
		case e.Path == "README.md":
			auditMarkdown(t, where, body)
		}
		if strings.HasSuffix(e.Path, ".yml") {
			if strings.Contains(body, "evil") || strings.Contains(body, "injected") || strings.Contains(body, "secrets.TOKEN") || strings.Contains(body, "<script") {
				t.Errorf("%s: carries an input value", where)
			}
		}
	}
}

func assertKeys(t *testing.T, where string, v any, allowed ...string) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	for k := range m {
		found := false
		for _, a := range allowed {
			found = found || a == k
		}
		if !found {
			t.Errorf("%s: injected key %q", where, k)
		}
	}
}

// stripFencedBlocks removes the fenced code blocks (lines from one fence line
// to the next).
func stripFencedBlocks(s string) string {
	var out []string
	in := false
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "```") {
			in = !in
			continue
		}
		if !in {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// stripCodeSpans removes the inline code spans of s the way CommonMark reads
// them: a run of n backticks opens a span that the next run of exactly n
// backticks closes. An unclosed run is plain text.
func stripCodeSpans(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", s[i+1]) >= 0 {
			i += 2 // a backslash escape outside a code span
			continue
		}
		if s[i] != '`' {
			out.WriteByte(s[i])
			i++
			continue
		}
		n := 0
		for i+n < len(s) && s[i+n] == '`' {
			n++
		}
		closed := -1
		for j := i + n; j < len(s); {
			if s[j] != '`' {
				j++
				continue
			}
			m := 0
			for j+m < len(s) && s[j+m] == '`' {
				m++
			}
			if m == n {
				closed = j + m
				break
			}
			j += m
		}
		if closed < 0 {
			out.WriteString(s[i : i+n])
			i += n
			continue
		}
		i = closed
	}
	return out.String()
}

// auditMarkdown fails on raw HTML or an injected heading, link or code block
// outside the code spans of the template.
func auditMarkdown(t *testing.T, where, body string) {
	t.Helper()
	s := stripCodeSpans(stripFencedBlocks(body))
	s = strings.ReplaceAll(s, "<https://github.com/yorch/ccshelf>", "")
	if strings.Contains(s, "<") {
		t.Errorf("%s: raw HTML or an autolink outside code:\n%s", where, s)
	}
	if strings.Contains(s, "](http://example.invalid") {
		t.Errorf("%s: an injected link", where)
	}
	if strings.ContainsAny(body, "\u202e\u2066") {
		t.Errorf("%s: a bidi character", where)
	}
	fences := 0
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "```") {
			fences++
		}
	}
	if fences%2 != 0 {
		t.Errorf("%s: an unbalanced code fence", where)
	}
	// Every heading is one the template wrote.
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "#") && !strings.HasPrefix(l, "# ") && !strings.HasPrefix(l, "## ") {
			t.Errorf("%s: unexpected heading %q", where, l)
		}
	}
}

// TestHostileFlagsAreRejected feeds every user value a hostile string: it must
// be refused (a *FieldError) or, where the value is free text (--org), land
// only in encoded form.
func TestHostileFlagsAreRejected(t *testing.T) {
	set := map[string]func(*Params, string){
		"--marketplace-name": func(p *Params, v string) { p.MarketplaceName = v },
		"--owner":            func(p *Params, v string) { p.Owner = v },
		"--platform-owners":  func(p *Params, v string) { p.PlatformOwners = []string{v} },
		"--ccshelf-ref":      func(p *Params, v string) { p.CcshelfRef = v },
		"--ccshelf-version":  func(p *Params, v string) { p.CcshelfVersion = v },
		"--runner-label":     func(p *Params, v string) { p.RunnerLabel = v },
		"--default-branch":   func(p *Params, v string) { p.DefaultBranch = v },
	}
	for flag, fn := range set {
		for _, h := range hostileInputs {
			p := baseParams()
			fn(&p, h)
			_, err := Build(Empty(), p)
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Flag != flag {
				t.Errorf("%s %q: err = %v, want a FieldError for the flag", flag, h, err)
			}
		}
	}
	// --org is free text: what Validate accepts must be encoded everywhere.
	for _, h := range hostileInputs {
		p := baseParams()
		p.Org = h
		p.ExampleProfile = true
		plan, err := Build(Empty(), p)
		if err != nil {
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Flag != "--org" {
				t.Errorf("--org %q: %v", h, err)
			}
			continue
		}
		auditFiles(t, "--org "+fmt.Sprintf("%q", h), plan)
	}
}

func fileMarketplace(name string, plugins ...string) string {
	return fmt.Sprintf(`{"name": %s, "owner": {"name": %s}, "description": %s, "plugins": [%s]}`,
		jsonString(name), jsonString(name), jsonString(name), strings.Join(plugins, ","))
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestHostileFilesNeverReachTheOutput feeds the values that adopt mode reads
// from the repository (marketplace.json name, owner, description and plugin
// entries; plugin.json fields; plugin directory names; CODEOWNERS) through the
// whole plan and audits every generated file.
func TestHostileFilesNeverReachTheOutput(t *testing.T) {
	for _, h := range hostileInputs {
		label := fmt.Sprintf("%q", h)
		scenarios := map[string]map[string]string{
			"marketplace name": {
				".claude-plugin/marketplace.json": fileMarketplace(h),
			},
			"marketplace entry": {
				".claude-plugin/marketplace.json": fileMarketplace("legacy",
					`{"name": `+jsonString(h)+`, "source": "./plugins/a", "description": `+jsonString(h)+`, "author": {"name": `+jsonString(h)+`}}`,
					`{"name": "ok", "source": `+jsonString("./"+h)+`, "description": `+jsonString(h)+`}`),
				"plugins/a/.claude-plugin/plugin.json": `{}`,
			},
			"plugin.json": {
				"plugins/a/.claude-plugin/plugin.json": fmt.Sprintf(`{"name": %s, "description": %s, "author": {"name": %s}}`, jsonString(h), jsonString(h), jsonString(h)),
				"plugins/b/.claude-plugin/plugin.json": fmt.Sprintf(`{"name": "b", "description": %s, "author": %s}`, jsonString(h), jsonString(h)),
			},
			"plugin dir": {
				"plugins/" + h + "/.claude-plugin/plugin.json": `{"name": "x", "description": "A description that is long enough."}`,
			},
			"CODEOWNERS": {
				".github/CODEOWNERS":                   h + "\n/plugins/a/ " + h + " @acme/real\n* " + h + "\n",
				"plugins/a/.claude-plugin/plugin.json": `{"name": "a"}`,
			},
		}
		for kind, files := range scenarios {
			for _, mp := range []string{"acme", ""} {
				m := newMem(files)
				p := baseParams()
				p.MarketplaceName = mp
				p.ExampleProfile = true
				plan, err := Build(m, p)
				if err != nil {
					var me *MissingError
					var fe *FieldError
					if errors.As(err, &me) || errors.As(err, &fe) {
						continue // refused or asks for a value: nothing was generated from the hostile one
					}
					// a plan-time conflict is acceptable too: nothing is written
					continue
				}
				auditFiles(t, kind+" "+label, plan)
				// What the files carry never reaches the workflows: they equal the ones of a clean run.
				cp := p
				cp.MarketplaceName = "acme"
				clean, err := Build(Empty(), cp)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range plan.Entries {
					if strings.HasPrefix(e.Path, ".github/workflows/") && e.Action == ActionCreate {
						if want := entryOf(t, clean, e.Path); string(want.Content) != string(e.Content) {
							t.Errorf("%s %s: the workflow depends on repository content", kind, label)
						}
					}
				}
				// The plan applies cleanly to a copy.
				if _, err := Apply(m, plan, ApplyOptions{}); err != nil {
					t.Errorf("%s %s: apply: %v", kind, label, err)
				}
			}
		}
	}
}

func TestInvalidExistingMarketplaceNameIsNotUsed(t *testing.T) {
	bad := "acme\"\n[evil]\nx = 1"
	m := newMem(map[string]string{".claude-plugin/marketplace.json": fileMarketplace(bad)})
	p := baseParams()
	p.MarketplaceName = ""
	_, err := Build(m, p)
	var me *MissingError
	if !errors.As(err, &me) || strings.Join(me.Flags, ",") != "--marketplace-name" {
		t.Fatalf("err = %v, want --marketplace-name to be required", err)
	}
	p.MarketplaceName = "acme"
	p.ExampleProfile = true
	plan, err := Build(m, p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), "not a valid marketplace name") {
		t.Errorf("notes = %v", plan.Notes)
	}
	if e := entryOf(t, plan, ".claude-plugin/marketplace.json"); e.Action == ActionCreate || e.Action == ActionOverwrite {
		t.Errorf("the existing marketplace.json is planned for a write: %s", e.Action)
	}
	for _, e := range plan.Entries {
		if strings.Contains(string(e.Content), "evil") {
			t.Errorf("%s carries the hostile name", e.Path)
		}
	}
	auditFiles(t, "invalid name", plan)
}

func TestExactKeysInPluginAndMarketplaceJSON(t *testing.T) {
	m := newMem(map[string]string{
		"plugins/a/.claude-plugin/plugin.json": `{"name": "a", "NAME": "evil"}`,
		"plugins/b/.claude-plugin/plugin.json": `{"name": "b", "name": "evil"}`,
		"plugins/c/.claude-plugin/plugin.json": `{"name": "c", "Description": "x"}`,
		"plugins/d/.claude-plugin/plugin.json": `{"name": "d", "description": "A description that is long enough."}`,
	})
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(plan.Notes, "\n")
	for _, dir := range []string{"plugins/a", "plugins/b"} {
		if !strings.Contains(notes, dir+"/.claude-plugin/plugin.json was skipped") {
			t.Errorf("no note for %s: %v", dir, plan.Notes)
		}
	}
	mk := string(entryOf(t, plan, ".claude-plugin/marketplace.json").Content)
	if strings.Contains(mk, "evil") || strings.Contains(mk, `"name": "a"`) || strings.Contains(mk, `"name": "b"`) || !strings.Contains(mk, `"name": "d"`) || !strings.Contains(mk, `"name": "c"`) {
		t.Errorf("marketplace.json:\n%s", mk)
	}

	dup := newMem(map[string]string{".claude-plugin/marketplace.json": `{"name": "good", "name": "evil", "plugins": []}`})
	p := baseParams()
	p.MarketplaceName = ""
	_, err = Build(dup, p)
	var me *MissingError
	if !errors.As(err, &me) {
		// an ambiguous marketplace.json is left alone and its name is not used
		t.Fatalf("err = %v, want a missing --marketplace-name", err)
	}
	p.MarketplaceName = "acme"
	plan, err = Build(dup, p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), "ambiguous") {
		t.Errorf("notes = %v", plan.Notes)
	}
}

// CODEOWNERS patterns come from marketplace sources: only plain relative
// directories may become rules.
func TestHostileSourcesNeverBecomeCodeownersRules(t *testing.T) {
	sources := []string{
		"./plugins/a\n/* @evil",
		"./plugins/a b",
		"./../outside",
		"./plugins/../../outside",
		"./plugins/a#x",
		"./!neg",
		"./plugins/a*",
		"./plugins/[ab]",
		"/abs/path",
		"./plugins/x\\y",
		"./plugins/a\t@evil",
		"./.git/hooks",
		"./plugins/\u202eb",
	}
	var entries []string
	for i, s := range sources {
		entries = append(entries, fmt.Sprintf(`{"name": "p%d", "source": %s, "description": "A description that is long enough."}`, i, jsonString(s)))
	}
	entries = append(entries, `{"name": "good", "source": "./plugins/good", "description": "A description that is long enough."}`)
	m := newMem(map[string]string{".claude-plugin/marketplace.json": fileMarketplace("legacy", entries...)})
	p := baseParams()
	p.MarketplaceName = ""
	plan, err := Build(m, p)
	if err != nil {
		t.Fatal(err)
	}
	co := string(entryOf(t, plan, ".github/CODEOWNERS").Content)
	for _, l := range strings.Split(co, "\n") {
		if strings.Contains(l, "evil") || strings.Contains(l, "outside") || strings.Contains(l, "abs") || strings.Contains(l, "neg") {
			t.Errorf("a hostile source reached CODEOWNERS: %q", l)
		}
	}
	if !strings.Contains(co, "/plugins/good/ ") {
		t.Errorf("the plain source has no rule:\n%s", co)
	}
	auditFiles(t, "sources", plan)
	// Every plugin still gets its sidecar, and the plan says why a source got no rule.
	for i := range sources {
		entryOf(t, plan, fmt.Sprintf("catalog/plugins/p%d.toml", i))
	}
	if n := strings.Count(strings.Join(plan.Notes, "\n"), "is not a plain directory below the repository"); n != len(sources)-1 {
		// "./plugins/x\\y" is cleaned to the plain directory plugins/x/y, like Claude Code does on Windows.
		t.Errorf("%d notes for %d hostile sources: %v", n, len(sources), plan.Notes)
	}
}

func TestSafeSourceDir(t *testing.T) {
	for _, ok := range []string{"plugins/a", "plugins/a.b_c-d", "tools/x/y", "a"} {
		if !safeSourceDir(ok) {
			t.Errorf("%q should be safe", ok)
		}
	}
	for _, bad := range []string{"", "..", "../x", "a/../b", "a//b", "/a", "a/", "a b", "a#", "!a", "a*", "a?", "a[", `a\b`, "a\nb", ".git/x", "a/.GIT", "...", "a/./b", strings.Repeat("a", 301)} {
		if safeSourceDir(bad) {
			t.Errorf("%q must not be safe", bad)
		}
	}
}

func TestPortableNames(t *testing.T) {
	for _, ok := range []string{"a", "tools-a", "x.y", "console", "com10", "nul-x", "lpt0"} {
		if good, why := portableName(ok); !good {
			t.Errorf("%q: %s", ok, why)
		}
	}
	for _, bad := range []string{"CON", "con", "NUL", "nul.txt", "Aux.v2", "PRN", "COM1", "com9.x", "LPT1", "lpt9", "trail.", "a..", "", "-a", "a b"} {
		if good, _ := portableName(bad); good {
			t.Errorf("%q must not be portable", bad)
		}
	}
}

func manifestFor(name string) string {
	return fmt.Sprintf(`{"name": %q, "description": "A description that is long enough."}`, name)
}

func TestCaseVariantPluginDirsAreSkippedWithANote(t *testing.T) {
	m := newMem(map[string]string{
		"plugins/Foo/.claude-plugin/plugin.json": manifestFor("Foo"),
		"plugins/foo/.claude-plugin/plugin.json": manifestFor("foo"),
	})
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range plan.Entries {
		if strings.HasPrefix(e.Path, "catalog/plugins/") {
			n++
		}
	}
	if n != 1 || !strings.Contains(strings.Join(plan.Notes, "\n"), "differ only in letter case") {
		t.Errorf("sidecars = %d, notes = %v", n, plan.Notes)
	}
	if _, err := Apply(m, plan, ApplyOptions{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func TestCaseVariantListedPluginsStopThePlan(t *testing.T) {
	m := newMem(map[string]string{
		".claude-plugin/marketplace.json": fileMarketplace("legacy",
			`{"name": "Foo", "source": "./plugins/foo1", "description": "A description that is long enough."}`,
			`{"name": "foo", "source": "./plugins/foo2", "description": "A description that is long enough."}`),
	})
	p := baseParams()
	p.MarketplaceName = ""
	_, err := Build(m, p)
	if err == nil || !strings.Contains(err.Error(), "differ only in letter case") || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("err = %v", err)
	}
	if len(m.writes) != 0 {
		t.Errorf("Build wrote: %v", m.writes)
	}
}

func TestDuplicateListedPluginIsNotedOnce(t *testing.T) {
	m := newMem(map[string]string{
		".claude-plugin/marketplace.json": fileMarketplace("legacy",
			`{"name": "foo", "source": "./plugins/foo1", "description": "A description that is long enough."}`,
			`{"name": "foo", "source": "./plugins/foo2", "description": "A description that is long enough."}`),
	})
	p := baseParams()
	p.MarketplaceName = ""
	plan, err := Build(m, p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), "twice (plugins/foo1 and plugins/foo2)") {
		t.Errorf("notes = %v", plan.Notes)
	}
	if _, err := Apply(m, plan, ApplyOptions{}); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func TestExistingSidecarOfAnotherCaseStopsThePlan(t *testing.T) {
	m := newMem(map[string]string{
		".claude-plugin/marketplace.json": fileMarketplace("legacy",
			`{"name": "foo", "source": "./plugins/foo", "description": "A description that is long enough."}`),
		"catalog/plugins/Foo.toml": "owner = \"@a/b\"\n",
	})
	p := baseParams()
	p.MarketplaceName = ""
	_, err := Build(m, p)
	if err == nil || !strings.Contains(err.Error(), "letter case") {
		t.Fatalf("err = %v", err)
	}
}

func TestReservedWindowsNamesAreSkipped(t *testing.T) {
	files := map[string]string{}
	var listed []string
	for _, n := range []string{"CON", "nul", "Aux", "com1", "LPT9", "trail."} {
		files["plugins/"+n+"/.claude-plugin/plugin.json"] = manifestFor("x")
		listed = append(listed, fmt.Sprintf(`{"name": %q, "source": "./plugins/%s", "description": "A description that is long enough."}`, n, n))
	}
	files["plugins/ok/.claude-plugin/plugin.json"] = manifestFor("ok")
	plan, err := Build(newMem(files), baseParams())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Entries {
		if strings.HasPrefix(e.Path, "catalog/plugins/") && e.Path != "catalog/plugins/ok.toml" {
			t.Errorf("a reserved name got a sidecar: %s", e.Path)
		}
	}
	if n := strings.Count(strings.Join(plan.Notes, "\n"), "was skipped"); n != 6 {
		t.Errorf("%d notes: %v", n, plan.Notes)
	}
	// A name given by plugin.json is not a way around it either.
	m := newMem(map[string]string{"plugins/a/.claude-plugin/plugin.json": manifestFor("CON")})
	plan, err = Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Entries {
		if strings.Contains(strings.ToLower(e.Path), "con.") {
			t.Errorf("sidecar for a reserved name: %s", e.Path)
		}
	}
	// Listed in a marketplace file.
	mk := newMem(map[string]string{".claude-plugin/marketplace.json": fileMarketplace("legacy", listed...)})
	p := baseParams()
	p.MarketplaceName = ""
	plan, err = Build(mk, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Entries {
		if strings.HasPrefix(e.Path, "catalog/plugins/") {
			t.Errorf("a reserved listed name got a sidecar: %s", e.Path)
		}
	}
}

// failingFS fails the create of one path and can alter another file first.
type failingFS struct {
	*memFS
	failOn string
	before func(m *memFS)
}

func (f *failingFS) WriteNew(name string, data []byte, perm os.FileMode) error {
	if name == f.failOn {
		if f.before != nil {
			f.before(f.memFS)
		}
		return errors.New("disk full")
	}
	return f.memFS.WriteNew(name, data, perm)
}

func TestApplyFailureRollsBackOnlyWhatItCreated(t *testing.T) {
	m := newMem(map[string]string{
		"plugins/a/.claude-plugin/plugin.json": manifestFor("a"),
		".gitignore":                           "dist/\n",
	})
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	before := m.names()
	f := &failingFS{memFS: m, failOn: "README.md"}
	res, err := Apply(f, plan, ApplyOptions{WriteSuggestions: true})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if len(res.Created) == 0 || len(res.RolledBack) != len(res.Created)+len(res.Suggestions) {
		t.Fatalf("created %v, rolled back %v", res.Created, res.RolledBack)
	}
	if got := m.names(); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Errorf("files after the rollback:\n got %v\nwant %v", got, before)
	}
	if m.read(".gitignore") != "dist/\n" {
		t.Error("a pre-existing file changed")
	}
	for _, d := range []string{"catalog", ".github", "catalog/plugins"} {
		if m.dirs[d] {
			t.Errorf("the directory %s created by the run is still there", d)
		}
	}
	if !m.dirs["plugins"] {
		t.Error("a pre-existing directory was removed")
	}
	// The suggestion file written by the failed run is gone too.
	if m.has(".gitignore.ccshelf-suggested") {
		t.Error("a suggestion written by the failed run stays")
	}
}

func TestRollbackLeavesAFileThatChangedSinceItWasWritten(t *testing.T) {
	m := newMem(map[string]string{"plugins/a/.claude-plugin/plugin.json": manifestFor("a")})
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	f := &failingFS{memFS: m, failOn: "README.md", before: func(m *memFS) {
		m.files[".gitattributes"] = []byte("edited by someone else\n")
	}}
	res, err := Apply(f, plan, ApplyOptions{})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if m.read(".gitattributes") != "edited by someone else\n" {
		t.Error("the rollback removed or reverted a file that someone changed")
	}
	if len(res.RolledBack) != len(res.Created)-1 {
		t.Errorf("created %v rolled back %v", res.Created, res.RolledBack)
	}
}

func TestOverwriteIsNotRolledBack(t *testing.T) {
	m := newMem(map[string]string{".gitattributes": "old\n"})
	p := baseParams()
	p.Force = true
	plan, err := Build(m, p)
	if err != nil {
		t.Fatal(err)
	}
	f := &failingFS{memFS: m, failOn: "README.md"}
	res, err := Apply(f, plan, ApplyOptions{})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if len(res.Overwritten) != 1 || m.read(".gitattributes") == "old\n" || !m.has(".gitattributes.bak") {
		t.Errorf("the replaced file and its backup must stay: %+v", res)
	}
}

func TestOSRemove(t *testing.T) {
	dir := t.TempDir()
	fsys, closer, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	if err := fsys.WriteNew("a/b/c.txt", []byte("x"), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Remove("a/b"); err == nil {
		t.Error("removed a directory with content")
	}
	if err := fsys.Remove("."); err == nil {
		t.Error("removed the target")
	}
	for _, n := range []string{"a/b/c.txt", "a/b", "a"} {
		if err := fsys.Remove(n); err != nil {
			t.Errorf("Remove(%s): %v", n, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Error("a is still there")
	}
	if err := fsys.Remove("missing"); err == nil {
		t.Error("removed a missing file without an error")
	}
}

func TestOSRemoveRefusesLinks(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "keep.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symbolic links are not available: %v", err)
	}
	fsys, closer, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	if err := fsys.Remove("link"); !errors.Is(err, ErrSymlink) {
		t.Errorf("Remove(link) = %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Error("the link target is gone")
	}
}

func TestExistingForeignSuggestionIsNotReplaced(t *testing.T) {
	m := newMem(map[string]string{
		".github/CODEOWNERS":                   "/plugins/x/ @legacy/team\n",
		".github/CODEOWNERS.ccshelf-suggested": "my own notes about CODEOWNERS\n",
		".gitignore":                           "dist/\n",
	})
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), "CODEOWNERS.ccshelf-suggested exists and ccshelf did not write it") {
		t.Errorf("notes = %v", plan.Notes)
	}
	res, err := Apply(m, plan, ApplyOptions{WriteSuggestions: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.read(".github/CODEOWNERS.ccshelf-suggested") != "my own notes about CODEOWNERS\n" {
		t.Error("a file the user wrote was replaced")
	}
	if len(res.Refused) != 1 || res.Refused[0] != ".github/CODEOWNERS.ccshelf-suggested" {
		t.Errorf("refused = %v", res.Refused)
	}
	// The other suggestion is written, and a second run refreshes our own file.
	if !strings.HasPrefix(m.read(".gitignore.ccshelf-suggested"), suggestionHeads[0]) {
		t.Errorf("gitignore suggestion = %q", m.read(".gitignore.ccshelf-suggested"))
	}
	m.files[".gitignore.ccshelf-suggested"] = []byte(suggestionHeads[0] + " an older text\n")
	plan, _ = Build(m, baseParams())
	if _, err := Apply(m, plan, ApplyOptions{WriteSuggestions: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.read(".gitignore.ccshelf-suggested"), "an older text") {
		t.Error("our own suggestion was not refreshed")
	}
}

func TestGitignoreTemplateIgnoresBackupsAndSuggestions(t *testing.T) {
	plan, err := Build(Empty(), baseParams())
	if err != nil {
		t.Fatal(err)
	}
	gi := string(entryOf(t, plan, ".gitignore").Content)
	for _, want := range []string{"*.bak\n", "*.ccshelf-suggested\n"} {
		if !strings.Contains(gi, want) {
			t.Errorf(".gitignore lacks %q", want)
		}
	}
}

func TestDefaultBranchInWorkflowsAndReadme(t *testing.T) {
	for _, b := range []string{"", "master", "trunk", "release/v1", "prod_main"} {
		p := baseParams()
		p.DefaultBranch = b
		plan, err := Build(Empty(), p)
		if err != nil {
			t.Fatalf("%q: %v", b, err)
		}
		want := b
		if want == "" {
			want = "main"
		}
		cat := string(entryOf(t, plan, ".github/workflows/catalog.yml").Content)
		if !strings.Contains(cat, `branches: ["`+want+`"]`) {
			t.Errorf("%q: catalog.yml:\n%s", b, cat[:400])
		}
		if strings.Count(string(entryOf(t, plan, "README.md").Content), "`"+want+"`") < 2 {
			t.Errorf("%q: the README does not name the branch", b)
		}
	}
	for _, bad := range append([]string{"", "-x", "x.", "a b", "a..b", "a//b", "a/", "x.lock", "a/.b", "main]", `main"`, "a*", "a?", "a@{b}", "a~b", ".hidden", strings.Repeat("a", 101)}, hostileInputs...) {
		if bad == "" {
			continue
		}
		if ValidBranch(bad) {
			t.Errorf("%q must not be a valid branch", bad)
		}
	}
}

func TestDetectedBranchIsNotedInThePlan(t *testing.T) {
	p := baseParams()
	p.DefaultBranch, p.BranchSource = "trunk", "origin/HEAD"
	plan, err := Build(Empty(), p)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Join(plan.Notes, "\n"); !strings.Contains(n, "trunk") || !strings.Contains(n, "origin/HEAD") {
		t.Errorf("notes = %v", plan.Notes)
	}
}

// guardJob returns the text of the "guard" job of a workflow, or "".
func guardJob(wf string) string {
	i := strings.Index(wf, "\n  guard:\n")
	if i < 0 {
		return ""
	}
	rest := wf[i+1:]
	lines := strings.Split(rest, "\n")
	out := []string{lines[0]}
	for _, l := range lines[1:] {
		if l != "" && !strings.HasPrefix(l, "   ") {
			break
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func TestPinGuardIsAJobOfItsOwn(t *testing.T) {
	sha := strings.Repeat("b", 40)
	cases := []struct {
		name         string
		ref, version string
		guarded      bool
	}{
		{"nothing", "", "", true},
		{"sha only", sha, "", true},
		{"tag only", "v0.1.0", "", true},
		{"pinned", sha, "v0.1.0", false},
	}
	for _, tc := range cases {
		p := baseParams()
		p.CcshelfRef, p.CcshelfVersion = tc.ref, tc.version
		plan, err := Build(Empty(), p)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range []struct{ file, job string }{{"validate", "validate"}, {"catalog", "build"}} {
			wf := string(entryOf(t, plan, ".github/workflows/"+f.file+".yml").Content)
			guard := guardJob(wf)
			if !tc.guarded {
				if guard != "" || strings.Contains(wf, "needs: guard") || strings.Contains(wf, "ccshelf is not pinned") {
					t.Errorf("%s %s: a pinned workflow keeps the guard", tc.name, f.file)
				}
				continue
			}
			if guard == "" {
				t.Errorf("%s %s: no guard job", tc.name, f.file)
				continue
			}
			if strings.Contains(guard, "uses:") || strings.Contains(guard, "checkout") || !strings.Contains(guard, "    permissions: {}\n") ||
				!strings.Contains(guard, "::error title=ccshelf is not pinned::") || !strings.Contains(guard, "exit 1") || strings.Count(guard, "run:") != 1 {
				t.Errorf("%s %s: the guard job is not a bare failing job:\n%s", tc.name, f.file, guard)
			}
			// The job that uses the action waits for the guard, and no other job does but the ones after it.
			i := strings.Index(wf, "\n  "+f.job+":\n")
			if i < 0 || !strings.HasPrefix(wf[i+len("\n  "+f.job+":\n"):], "    needs: guard\n") {
				t.Errorf("%s %s: job %s does not need the guard", tc.name, f.file, f.job)
			}
			if strings.Count(wf, "\n    needs: guard\n") != 1 {
				t.Errorf("%s %s: needs: guard %d times", tc.name, f.file, strings.Count(wf, "\n    needs: guard\n"))
			}
		}
	}
}

func TestPinFilesListOnlyTheWorkflowsWritten(t *testing.T) {
	m := newMem(map[string]string{".github/workflows/validate.yml": "name: mine\non: push\njobs: {}\n"})
	p := baseParams()
	p.CcshelfRef, p.CcshelfVersion = "", ""
	plan, err := Build(m, p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.PinFiles, ",") != ".github/workflows/catalog.yml" {
		t.Errorf("PinFiles = %v", plan.PinFiles)
	}
	todo := strings.Join(plan.Todos, "\n")
	if strings.Contains(todo, "validate.yml") || !strings.Contains(todo, "catalog.yml") || !strings.Contains(todo, "guard job") {
		t.Errorf("todos = %v", plan.Todos)
	}
	files := plan.MarkerFiles()
	if strings.Join(files, ",") != ".github/workflows/catalog.yml" {
		t.Errorf("MarkerFiles = %v", files)
	}
	// All workflows exist: nothing is left to pin.
	m = newMem(map[string]string{
		".github/workflows/validate.yml": "x: 1\n", ".github/workflows/catalog.yml": "x: 1\n", ".github/workflows/release.yml": "x: 1\n",
	})
	plan, err = Build(m, p)
	if err != nil {
		t.Fatal(err)
	}
	if plan.NeedsPin || len(plan.PinFiles) != 0 {
		t.Errorf("NeedsPin = %v, %v", plan.NeedsPin, plan.PinFiles)
	}
}

func TestReleaseWorkflowIsGHESCorrect(t *testing.T) {
	plan, err := Build(Empty(), baseParams())
	if err != nil {
		t.Fatal(err)
	}
	wf := string(entryOf(t, plan, ".github/workflows/release.yml").Content)
	for _, want := range []string{
		"GH_ENTERPRISE_TOKEN: ${{ github.token }}",
		"SERVER_URL: ${{ github.server_url }}",
		`GH_HOST="${SERVER_URL#*://}"`,
		"export GH_HOST",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}
	// The host is set before the first gh call.
	if strings.Index(wf, "export GH_HOST") > strings.Index(wf, "gh api") {
		t.Error("GH_HOST is exported after gh runs")
	}
}

// TestGeneratedWorkflowsPassActionlint runs actionlint (with shellcheck, when
// installed) over the generated workflows in every pin state.
func TestGeneratedWorkflowsPassActionlint(t *testing.T) {
	bin, err := exec.LookPath("actionlint")
	if err != nil {
		t.Skip("actionlint is not installed")
	}
	sha := strings.Repeat("c", 40)
	for _, pin := range []struct{ name, ref, version string }{
		{"unpinned", "", ""}, {"sha-only", sha, ""}, {"tag-only", "v0.1.0", ""}, {"pinned", sha, "v0.1.0"},
	} {
		for _, branch := range []string{"", "trunk"} {
			dir := t.TempDir()
			p := baseParams()
			p.CcshelfRef, p.CcshelfVersion, p.DefaultBranch = pin.ref, pin.version, branch
			p.RunnerLabel = "linux-runners"
			run(t, dir, p, ApplyOptions{})
			wf, _ := filepath.Glob(filepath.Join(dir, ".github", "workflows", "*.yml"))
			if len(wf) != 3 {
				t.Fatalf("workflows = %v", wf)
			}
			cmd := exec.Command(bin, append([]string{"-no-color"}, wf...)...)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("actionlint (%s, branch %q): %v\n%s", pin.name, branch, err, out)
			}
		}
	}
}

func TestStarterTemplateNeedsNoMergeOfCodeowners(t *testing.T) {
	example := filepath.Join("..", "..", "examples", "org-data-repo")
	if _, err := os.Stat(example); err != nil {
		t.Skip("the starter template is not part of this checkout")
	}
	fsys, closer, err := OpenDir(example)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	p := Params{MarketplaceName: "acme", PlatformOwners: []string{"@acme/platform"}, CcshelfVersion: "v0.1.0"}
	plan, err := Build(fsys, p)
	if err != nil {
		t.Fatal(err)
	}
	e := entryOf(t, plan, ".github/CODEOWNERS")
	if e.Action == ActionMerge || e.Action == ActionCreate {
		t.Errorf("catalog init on the starter wants to change CODEOWNERS: %s\n%s", e.Action, e.Suggestion)
	}
	for _, e := range plan.Entries {
		if e.Action == ActionMerge && e.Path != "README.md" {
			t.Errorf("the starter needs a merge of %s:\n%s", e.Path, e.Suggestion)
		}
	}
}

func TestBidiAndControlCharactersInPluginJSONAreDropped(t *testing.T) {
	m := newMem(map[string]string{
		"plugins/a/.claude-plugin/plugin.json": `{"name": "a", "description": "ok\u202edesc", "author": "Mal\u2066lory"}`,
		"plugins/b/.claude-plugin/plugin.json": `{"name": "b", "description": "line\none", "author": {"name": "tab\there"}}`,
		"plugins/c/.claude-plugin/plugin.json": `{"name": "c", "description": "zero\u200bwidth", "author": {"name": "bell\u0007"}}`,
		"plugins/d/.claude-plugin/plugin.json": `{"name": "d", "description": "Clean description here.", "author": "Clean Author"}`,
	})
	found, _ := discoverPlugins(m)
	if len(found) != 4 {
		t.Fatalf("found = %+v", found)
	}
	for _, f := range found[:3] {
		if f.Description != "" || f.Author != "" {
			t.Errorf("%s kept hostile text: %+v", f.Name, f)
		}
	}
	if found[3].Description != "Clean description here." || found[3].Author != "Clean Author" {
		t.Errorf("clean values lost: %+v", found[3])
	}
}

// The renderers encode what they are given even when validation is bypassed:
// a second line of defense, tested by building the plan with a name that
// Validate would have refused.
func TestRenderersEncodeWithoutValidation(t *testing.T) {
	for _, h := range hostileInputs {
		b := &builder{
			fs: Empty(), p: &Params{PlatformOwners: []string{"@acme/platform"}, ExampleProfile: true, Org: h}, plan: &Plan{},
			missing: map[string]string{}, name: h, cfg: orgconfig.Default(),
		}
		b.addConfig()
		b.addReadme()
		b.addExampleProfile()
		b.plan.Entries = append(b.plan.Entries, Entry{
			Path: "catalog/plugins/x.toml", Action: ActionCreate,
			Content: sidecarStub("x", h, []string{"owner", "avoid_when", "support"}),
		})
		auditFiles(t, fmt.Sprintf("unvalidated %q", h), b.plan)
	}
}

func TestPluginJSONFieldsOfTheWrongTypeOrSizeAreHandled(t *testing.T) {
	m := newMem(map[string]string{
		"plugins/a/.claude-plugin/plugin.json": `{"name": 5}`,
		"plugins/b/.claude-plugin/plugin.json": `{"name": "b", "description": ` + jsonString(strings.Repeat("d", 501)) + `}`,
		"plugins/c/.claude-plugin/plugin.json": `{"name": "c", "description": ` + jsonString(strings.Repeat("d", 500)) + `, "author": ` + jsonString(strings.Repeat("a", 101)) + `}`,
	})
	found, notes := discoverPlugins(m)
	if len(found) != 2 || found[0].Name != "b" || found[1].Name != "c" {
		t.Fatalf("found = %+v", found)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "plugins/a/.claude-plugin/plugin.json was skipped: name is not a string") {
		t.Errorf("notes = %v", notes)
	}
	if found[0].Description != "" || len(found[1].Description) != 500 || found[1].Author != "" {
		t.Errorf("limits: %+v", found)
	}
}

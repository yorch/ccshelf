package scaffold

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yorch/ccshelf/internal/catalog"
	"github.com/yorch/ccshelf/internal/catalog/lint"
	"github.com/yorch/ccshelf/internal/orgconfig"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for n, c := range files {
		p := filepath.Join(root, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b, err := os.ReadFile(p)
		out[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// run plans and applies against a real directory.
func run(t *testing.T, dir string, p Params, opt ApplyOptions) (*Plan, *Result) {
	t.Helper()
	fsys, closer, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	plan, err := Build(fsys, p)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	res, err := Apply(fsys, plan, opt)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return plan, res
}

func lintDir(t *testing.T, dir string) *lint.Report {
	t.Helper()
	cfg, err := orgconfig.Load(dir)
	if err != nil {
		t.Fatalf("the generated ccshelf.toml does not load: %v", err)
	}
	rep, err := lint.Run(dir, cfg, lint.Options{Now: func() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	if _, rep2, err := catalog.BuildContext(context.Background(), dir, cfg, catalog.Options{}); err != nil || rep2.HasErrors() {
		t.Fatalf("catalog build: %v\n%s", err, lint.FormatText(rep2))
	}
	return rep
}

func codesOf(rep *lint.Report) string {
	var out []string
	for _, f := range rep.Findings {
		if f.Severity != lint.Info {
			out = append(out, string(f.Severity)+":"+f.Code)
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func TestNewRepoLintsCleanAndBuilds(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, baseParams(), ApplyOptions{})
	rep := lintDir(t, dir)
	if got := codesOf(rep); got != "" {
		t.Fatalf("a new repo must lint without findings, got %s\n%s", got, lint.FormatText(rep))
	}
}

const legacyMarketplace = `{
  "name": "legacy",
  "owner": {"name": "Legacy Org"},
  "plugins": [
    {"name": "tools-a", "source": "./plugins/tools-a", "description": "Helpers for the A team, used every day.", "author": {"name": "A team"}},
    {"name": "tools-b", "source": "./plugins/tools-b", "description": "Helpers for the B team with a deploy hook.", "author": {"name": "B team"}},
    {"name": "partner", "source": {"source": "github", "repo": "partner/plugin"}, "description": "A plugin maintained outside this repository.", "author": {"name": "Partner"}}
  ]
}
`

func legacyRepo() map[string]string {
	return map[string]string{
		".claude-plugin/marketplace.json":            legacyMarketplace,
		"plugins/tools-a/.claude-plugin/plugin.json": `{"name": "tools-a", "description": "Helpers for the A team, used every day."}`,
		"plugins/tools-a/README.md":                  "# tools-a\n",
		"plugins/tools-b/.claude-plugin/plugin.json": `{"name": "tools-b", "description": "Helpers for the B team with a deploy hook."}`,
		"plugins/tools-b/hooks/hooks.json":           `{"hooks": {}}`,
		"plugins/tools-b/README.md":                  "# tools-b\n",
		"plugins/orphan/.claude-plugin/plugin.json":  `{"name": "orphan", "description": "Not listed in the marketplace."}`,
		"README.md":                "# Legacy marketplace\n\nHand written.\n",
		".github/workflows/ci.yml": "name: ci\non: push\njobs: {}\n",
		".github/CODEOWNERS": `/plugins/tools-a/ @legacy/a-team
/plugins/tools-b/ @legacy/b-team
/plugins/*/hooks/ @legacy/platform
/.github/ @legacy/platform
`,
	}
}

func TestAdoptLeavesExistingFilesAlone(t *testing.T) {
	dir := t.TempDir()
	repo := legacyRepo()
	writeTree(t, dir, repo)
	p := baseParams()
	p.MarketplaceName = ""
	p.Org = ""
	p.PlatformOwners = []string{"@legacy/platform"}
	plan, res := run(t, dir, p, ApplyOptions{})
	if plan.Mode != ModeAdopt {
		t.Fatalf("mode = %s", plan.Mode)
	}
	got := map[string]Action{}
	for _, e := range plan.Entries {
		got[e.Path] = e.Action
	}
	for path, want := range map[string]Action{
		".claude-plugin/marketplace.json": ActionSkip,
		"README.md":                       ActionSkip,
		".github/CODEOWNERS":              ActionMerge,
		"ccshelf.toml":                    ActionCreate,
		"catalog/plugins/tools-a.toml":    ActionCreate,
		"catalog/plugins/tools-b.toml":    ActionCreate,
		"catalog/plugins/partner.toml":    ActionCreate,
		".github/workflows/validate.yml":  ActionCreate,
		".gitattributes":                  ActionCreate,
		".gitignore":                      ActionCreate,
	} {
		if got[path] != want {
			t.Errorf("%s: %s, want %s", path, got[path], want)
		}
	}
	if _, ok := got[".github/workflows/ci.yml"]; ok {
		t.Error("an unrelated workflow is in the plan")
	}
	if _, ok := got["catalog/plugins/orphan.toml"]; ok {
		t.Error("an unlisted plugin got a sidecar (it would be an orphan)")
	}
	notes := strings.Join(plan.Notes, "\n")
	if !strings.Contains(notes, "plugins/orphan") {
		t.Errorf("no note about the unlisted plugin: %s", notes)
	}
	after := readTree(t, dir)
	for n, c := range repo {
		if after[n] != c {
			t.Errorf("existing file %s changed", n)
		}
	}
	if len(res.Suggestions) != 0 {
		t.Errorf("suggestions written without --write-suggestions: %v", res.Suggestions)
	}
	// The owner of tools-a comes from the existing CODEOWNERS.
	if !strings.Contains(after["catalog/plugins/tools-a.toml"], `owner = "@legacy/a-team"`) {
		t.Errorf("tools-a sidecar:\n%s", after["catalog/plugins/tools-a.toml"])
	}
	if !strings.Contains(after["catalog/plugins/partner.toml"], `owner = "@legacy/platform"`) {
		t.Errorf("partner sidecar:\n%s", after["catalog/plugins/partner.toml"])
	}
	// Lint: no errors; the placeholders and the CODEOWNERS gaps are warnings.
	rep := lintDir(t, dir)
	if rep.HasErrors() {
		t.Fatalf("lint errors:\n%s", lint.FormatText(rep))
	}
}

func TestAdoptSuggestionsAreWrittenOnlyOnRequestAndMergeCleans(t *testing.T) {
	dir := t.TempDir()
	repo := legacyRepo()
	writeTree(t, dir, repo)
	p := baseParams()
	p.PlatformOwners = []string{"@legacy/platform"}
	p.MarketplaceName = ""
	_, res := run(t, dir, p, ApplyOptions{WriteSuggestions: true})
	if len(res.Suggestions) != 1 || res.Suggestions[0] != ".github/CODEOWNERS.ccshelf-suggested" {
		t.Fatalf("suggestions = %v", res.Suggestions)
	}
	sug := readTree(t, dir)[".github/CODEOWNERS.ccshelf-suggested"]
	for _, want := range []string{"/ccshelf.toml", "/catalog/", "/plugins/*/.mcp.json", "/plugins/*/.claude-plugin/", "Put this catch-all at the TOP"} {
		if !strings.Contains(sug, want) {
			t.Errorf("suggestion lacks %s:\n%s", want, sug)
		}
	}
	for _, not := range []string{"/.github/ ", "/plugins/tools-a/"} {
		if strings.Contains(sug, not) {
			t.Errorf("suggestion repeats a covered rule %q:\n%s", not, sug)
		}
	}
	// A second run refreshes the suggestion without error and changes nothing else.
	_, res2 := run(t, dir, p, ApplyOptions{WriteSuggestions: true})
	if len(res2.Created) != 0 || len(res2.Overwritten) != 0 {
		t.Errorf("second run wrote %v %v", res2.Created, res2.Overwritten)
	}
	// Merging the suggestion (appending it) leaves only placeholder warnings.
	var appended []string
	for _, l := range strings.Split(sug, "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			appended = append(appended, l)
		}
	}
	co := readTree(t, dir)[".github/CODEOWNERS"]
	writeTree(t, dir, map[string]string{".github/CODEOWNERS": "* @legacy/platform\n" + co + strings.Join(appended, "\n") + "\n"})
	os.Remove(filepath.Join(dir, ".github", "CODEOWNERS.ccshelf-suggested"))
	rep := lintDir(t, dir)
	for _, f := range rep.Findings {
		if f.Severity != lint.Info && f.Code != "CAT048" && f.Code != "CAT044" {
			t.Errorf("unexpected finding after the merge: %s %s %s", f.Severity, f.Code, f.Message)
		}
	}
	again, _ := run(t, dir, p, ApplyOptions{})
	if e := entryOf(t, again, ".github/CODEOWNERS"); e.Action != ActionSkip {
		t.Errorf("after the merge CODEOWNERS is %s (%s)", e.Action, e.Reason)
	}
}

func TestAdoptWithoutMarketplaceListsDiscoveredPlugins(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"plugins/alpha/.claude-plugin/plugin.json":    `{"name": "alpha", "description": "Alpha does a useful thing for the team.", "author": {"name": "Alpha Team"}}`,
		"plugins/alpha/README.md":                     "# alpha\n",
		"plugins/beta/.claude-plugin/plugin.json":     `{"description": 7}`,
		"plugins/gamma/.claude-plugin/plugin.json":    `{"name": "gamma-renamed"}`,
		"plugins/notaplugin/readme.txt":               "x",
		"plugins/bad name/.claude-plugin/plugin.json": `{}`,
	})
	plan, _ := run(t, dir, baseParams(), ApplyOptions{})
	tree := readTree(t, dir)
	mk := tree[".claude-plugin/marketplace.json"]
	for _, want := range []string{`"name": "alpha"`, `"source": "./plugins/alpha"`, `"name": "gamma-renamed"`, `"source": "./plugins/gamma"`, "TODO(ccshelf): describe what gamma-renamed does", `"name": "Alpha Team"`} {
		if !strings.Contains(mk, want) {
			t.Errorf("marketplace.json lacks %s:\n%s", want, mk)
		}
	}
	if strings.Contains(mk, "beta") || strings.Contains(mk, "bad name") || strings.Contains(mk, "notaplugin") {
		t.Errorf("skipped plugins are listed:\n%s", mk)
	}
	for _, f := range []string{"catalog/plugins/alpha.toml", "catalog/plugins/gamma-renamed.toml"} {
		if _, ok := tree[f]; !ok {
			t.Errorf("missing %s", f)
		}
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), "plugins/beta") {
		t.Errorf("notes = %v", plan.Notes)
	}
	// Each discovered plugin gets a team rule so that its sidecar owner agrees with CODEOWNERS.
	if !strings.Contains(tree[".github/CODEOWNERS"], "/plugins/alpha/") {
		t.Errorf("CODEOWNERS:\n%s", tree[".github/CODEOWNERS"])
	}
	rep := lintDir(t, dir)
	if rep.HasErrors() {
		t.Fatalf("lint:\n%s", lint.FormatText(rep))
	}
	for _, f := range rep.Findings {
		if f.Severity == lint.Warning && f.Code != "CAT048" && f.Code != "CAT006" {
			t.Errorf("unexpected warning %s %s", f.Code, f.Message)
		}
	}
}

func TestMarketplaceNameMustMatchAnExistingFile(t *testing.T) {
	m := newMem(map[string]string{".claude-plugin/marketplace.json": legacyMarketplace})
	p := baseParams() // MarketplaceName acme
	_, err := Build(m, p)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Flag != "--marketplace-name" {
		t.Fatalf("err = %v", err)
	}
}

func TestForceOverwritesAfterBackup(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"README.md": "hand written\n", "ccshelf.toml": "[lint]\nrequire = [\"owner\"]\n", ".claude-plugin/marketplace.json": legacyMarketplace})
	p := baseParams()
	p.MarketplaceName = ""
	p.Force = true
	plan, res := run(t, dir, p, ApplyOptions{})
	if e := entryOf(t, plan, "README.md"); e.Action != ActionOverwrite {
		t.Fatalf("README.md: %s", e.Action)
	}
	if e := entryOf(t, plan, ".claude-plugin/marketplace.json"); e.Action != ActionSkip || !strings.Contains(e.Reason, "never rewritten") {
		t.Errorf("marketplace.json: %s %s", e.Action, e.Reason)
	}
	tree := readTree(t, dir)
	if tree["README.md.bak"] != "hand written\n" || tree["ccshelf.toml.bak"] != "[lint]\nrequire = [\"owner\"]\n" {
		t.Errorf("backups: %q", tree["README.md.bak"])
	}
	if tree[".claude-plugin/marketplace.json"] != legacyMarketplace || tree[".claude-plugin/marketplace.json.bak"] != "" {
		t.Error("marketplace.json was touched")
	}
	if len(res.Overwritten) != 2 || len(res.Backups) != 2 {
		t.Errorf("result = %+v", res)
	}
	// A second forced run finds everything up to date and writes no second backup.
	plan2, res2 := run(t, dir, p, ApplyOptions{})
	if plan2.Changes() || len(res2.Backups) != 0 {
		t.Errorf("second forced run: %v %+v", entryPaths(plan2), res2)
	}
}

func TestForceRefusesWhenTheBackupExists(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"README.md": "x\n", "README.md.bak": "older\n"})
	p := baseParams()
	p.Force = true
	fsys, closer, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	if _, err := Build(fsys, p); err == nil || !strings.Contains(err.Error(), "README.md.bak") {
		t.Fatalf("err = %v", err)
	}
}

func TestOverwriteDetectsAConcurrentEdit(t *testing.T) {
	m := newMem(map[string]string{"README.md": "one\n"})
	p := baseParams()
	p.Force = true
	plan, err := Build(m, p)
	if err != nil {
		t.Fatal(err)
	}
	m.put("README.md", "two\n")
	if _, err := Apply(m, plan, ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "changed after ccshelf made the plan") {
		t.Fatalf("err = %v", err)
	}
	if m.read("README.md") != "two\n" {
		t.Error("the concurrent edit was lost")
	}
}

func TestCreateDetectsAFileThatAppeared(t *testing.T) {
	m := newMem(nil)
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	m.put("ccshelf.toml", "mine\n")
	if _, err := Apply(m, plan, ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "appeared") {
		t.Fatalf("err = %v", err)
	}
	if m.read("ccshelf.toml") != "mine\n" {
		t.Error("an existing file was replaced")
	}
}

func TestSidecarStubHonoursLintRequire(t *testing.T) {
	m := newMem(map[string]string{
		".claude-plugin/marketplace.json": legacyMarketplace,
		"ccshelf.toml":                    "[lint]\nrequire = [\"owner\", \"status\", \"avoid_when\", \"support\", \"review_by\", \"docs\"]\nplatform_owners = [\"@legacy/platform\"]\n",
	})
	plan, err := Build(m, Params{})
	if err != nil {
		t.Fatal(err)
	}
	c := string(entryOf(t, plan, "catalog/plugins/tools-a.toml").Content)
	for _, want := range []string{`owner = "@legacy/platform"`, `status = "experimental"`, "when_to_use = [", "avoid_when = [", "support = "} {
		if !strings.Contains(c, want) {
			t.Errorf("stub lacks %s:\n%s", want, c)
		}
	}
	for _, not := range []string{"review_by =", "docs ="} {
		if strings.Contains(c, not) {
			t.Errorf("stub invents %s:\n%s", not, c)
		}
	}
}

func TestDiscoverySkipsLinkedPluginDirectoriesWithANote(t *testing.T) {
	m := newMem(map[string]string{
		"plugins/real/.claude-plugin/plugin.json": `{"name": "real", "description": "A real plugin with a description."}`,
	})
	m.symlink("plugins/linked")
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), "plugins/linked is a symbolic link") {
		t.Errorf("notes = %v", plan.Notes)
	}
	for _, e := range plan.Entries {
		if strings.Contains(e.Path, "linked") {
			t.Errorf("a linked directory got an entry: %s", e.Path)
		}
	}
	if !strings.Contains(string(entryOf(t, plan, ".claude-plugin/marketplace.json").Content), `"real"`) {
		t.Error("the real plugin is missing")
	}
}

func TestExistingFilesWithCRLFAreComparedByContent(t *testing.T) {
	m := newMem(map[string]string{
		".gitignore":     "dist/\r\n.DS_Store\r\n*.ccshelf-suggested\r\n*.bak\r\n",
		".gitattributes": "* text=auto eol=lf\r\n",
	})
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	if e := entryOf(t, plan, ".gitignore"); e.Action != ActionSkip || !strings.Contains(e.Reason, "every generated line") {
		t.Errorf(".gitignore: %s (%s)", e.Action, e.Reason)
	}
	e := entryOf(t, plan, ".gitattributes")
	if e.Action != ActionMerge || !strings.Contains(string(e.Suggestion), "*.json text eol=lf") || strings.Contains(string(e.Suggestion), "* text=auto") {
		t.Errorf(".gitattributes: %s\n%s", e.Action, e.Suggestion)
	}
}

func TestInvalidExistingFilesAreReportedNotFatal(t *testing.T) {
	m := newMem(map[string]string{
		".claude-plugin/marketplace.json":      "{not json",
		"ccshelf.toml":                         "[lint\n",
		"plugins/a/.claude-plugin/plugin.json": `{"name": "a"}`,
	})
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(plan.Notes, "\n")
	for _, want := range []string{"marketplace.json exists but is not a valid marketplace file", "ccshelf.toml exists but is not valid"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	if e := entryOf(t, plan, ".claude-plugin/marketplace.json"); e.Action != ActionSkip {
		t.Errorf("marketplace.json: %s", e.Action)
	}
	if e := entryOf(t, plan, "ccshelf.toml"); e.Action != ActionSkip {
		t.Errorf("ccshelf.toml: %s", e.Action)
	}
}

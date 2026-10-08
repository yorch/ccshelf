package scaffold

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

func baseParams() Params {
	return Params{MarketplaceName: "acme", Org: "Acme", PlatformOwners: []string{"@acme/platform"}, CcshelfRef: strings.Repeat("a", 40), CcshelfVersion: "v0.1.0"}
}

func entryOf(t *testing.T, p *Plan, path string) Entry {
	t.Helper()
	for _, e := range p.Entries {
		if e.Path == path {
			return e
		}
	}
	t.Fatalf("no entry for %s in %v", path, entryPaths(p))
	return Entry{}
}

func entryPaths(p *Plan) []string {
	var out []string
	for _, e := range p.Entries {
		out = append(out, e.Path+"="+string(e.Action))
	}
	return out
}

func TestNewPlanListsEveryFile(t *testing.T) {
	plan, err := Build(Empty(), baseParams())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != ModeNew {
		t.Errorf("mode = %s", plan.Mode)
	}
	want := []string{
		".claude-plugin/marketplace.json", ".gitattributes", ".github/CODEOWNERS", ".github/workflows/catalog.yml", ".github/workflows/release.yml",
		".github/workflows/validate.yml", ".gitignore", "README.md", "ccshelf.toml",
	}
	var got []string
	for _, e := range plan.Entries {
		if e.Action != ActionCreate {
			t.Errorf("%s: action %s", e.Path, e.Action)
		}
		got = append(got, e.Path)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if len(plan.Todos) != 1 || !strings.Contains(plan.Todos[0], "sidecars") {
		// pinned workflows: only the sidecar todo would appear, and there are no plugins
		if len(plan.Todos) != 0 {
			t.Errorf("todos = %v", plan.Todos)
		}
	}
}

func TestMissingValuesAreAllListed(t *testing.T) {
	_, err := Build(Empty(), Params{})
	var me *MissingError
	if !errors.As(err, &me) {
		t.Fatalf("err = %v", err)
	}
	got := strings.Join(me.Flags, ",")
	if got != "--platform-owners,--marketplace-name" && got != "--marketplace-name,--platform-owners" {
		t.Errorf("flags = %s", got)
	}
	if !strings.Contains(me.Error(), "--marketplace-name") {
		t.Errorf("message = %s", me)
	}
}

func TestSkippedGroupsNeedNoValues(t *testing.T) {
	p := Params{Skip: map[Group]bool{GroupConfig: true, GroupMarketplace: true, GroupSidecars: true, GroupCodeowners: true, GroupReadme: true}}
	plan, err := Build(Empty(), p)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Entries {
		if e.Group != GroupWorkflows && e.Group != GroupGitattributes && e.Group != GroupGitignore {
			t.Errorf("unexpected %s", e.Path)
		}
	}
	if len(plan.Todos) == 0 {
		t.Error("an unpinned workflow must leave a todo")
	}
}

func TestPinStates(t *testing.T) {
	sha := strings.Repeat("b", 40)
	for _, tc := range []struct {
		name     string
		ref, ver string
		guard    bool
		uses     string
		version  string
	}{
		{"nothing", "", "", true, zeroSHA, "v0.0.0"},
		{"sha only", sha, "", true, sha, "v0.0.0"},
		{"tag only", "v1.2.3", "", true, zeroSHA, "v1.2.3"},
		{"sha and version", sha, "v1.2.3", false, sha, "v1.2.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := baseParams()
			p.CcshelfRef, p.CcshelfVersion = tc.ref, tc.ver
			plan, err := Build(Empty(), p)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range []string{".github/workflows/validate.yml", ".github/workflows/catalog.yml"} {
				got := string(entryOf(t, plan, f).Content)
				if strings.Contains(got, "Refuse to run until ccshelf is pinned") != tc.guard {
					t.Errorf("%s: guard = %v, want %v", f, !tc.guard, tc.guard)
				}
				if !strings.Contains(got, "yorch/ccshelf/action@"+tc.uses) || !strings.Contains(got, "version: "+tc.version+"\n") {
					t.Errorf("%s: wrong pin:\n%s", f, got)
				}
			}
			if tc.guard && len(plan.Todos) == 0 {
				t.Error("no pin todo")
			}
			if !tc.guard && len(plan.Todos) != 0 {
				t.Errorf("todos = %v", plan.Todos)
			}
			if strings.Contains(string(entryOf(t, plan, ".github/workflows/release.yml").Content), "Refuse") {
				t.Error("release.yml does not use the action")
			}
		})
	}
}

func TestRunnerLabelFallback(t *testing.T) {
	p := baseParams()
	p.RunnerLabel = "self-hosted-linux"
	plan, err := Build(Empty(), p)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"validate", "catalog", "release"} {
		got := string(entryOf(t, plan, ".github/workflows/"+f+".yml").Content)
		if !strings.Contains(got, "runs-on: ${{ vars.RUNNER_LABEL || 'self-hosted-linux' }}") || strings.Contains(got, DefaultRunnerLabel) {
			t.Errorf("%s: runs-on wrong", f)
		}
	}
}

func TestWorkflowsHaveNoHardcodedHost(t *testing.T) {
	plan, err := Build(Empty(), baseParams())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Entries {
		if e.Group != GroupWorkflows {
			continue
		}
		for _, l := range strings.Split(string(e.Content), "\n") {
			if strings.Contains(l, "github.com") && !strings.Contains(l, "yorch/ccshelf/action@") {
				t.Errorf("%s: hard-coded host: %s", e.Path, l)
			}
		}
	}
}

func TestDeterministic(t *testing.T) {
	p := baseParams()
	a, err := Build(Empty(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(Empty(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Entries) != len(b.Entries) {
		t.Fatal("different entry counts")
	}
	for i := range a.Entries {
		if a.Entries[i].Path != b.Entries[i].Path || string(a.Entries[i].Content) != string(b.Entries[i].Content) {
			t.Errorf("%s differs between runs", a.Entries[i].Path)
		}
		if strings.Contains(string(a.Entries[i].Content), "\r") {
			t.Errorf("%s has a CR", a.Entries[i].Path)
		}
		if !strings.HasSuffix(string(a.Entries[i].Content), "\n") {
			t.Errorf("%s has no trailing newline", a.Entries[i].Path)
		}
	}
}

func TestApplyThenReapplyIsANoOp(t *testing.T) {
	m := newMem(nil)
	p := baseParams()
	plan, err := Build(m, p)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(m, plan, ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != len(plan.Entries) {
		t.Fatalf("created %d of %d", len(res.Created), len(plan.Entries))
	}
	for _, n := range m.names() {
		if m.modes[n] != 0o644 {
			t.Errorf("%s mode %v", n, m.modes[n])
		}
	}
	m.writes = nil
	again, err := Build(m, p)
	if err != nil {
		t.Fatal(err)
	}
	if again.Mode != ModeAdopt {
		t.Errorf("second run mode = %s", again.Mode)
	}
	if again.Changes() {
		t.Errorf("second run plans changes: %v", entryPaths(again))
	}
	for _, e := range again.Entries {
		if e.Action != ActionSkip || e.Reason != "up to date" {
			t.Errorf("%s: %s (%s)", e.Path, e.Action, e.Reason)
		}
	}
	if _, err := Apply(m, again, ApplyOptions{WriteSuggestions: true}); err != nil {
		t.Fatal(err)
	}
	if len(m.writes) != 0 {
		t.Errorf("second run wrote %v", m.writes)
	}
}

func TestSecondRunNeedsNoFlags(t *testing.T) {
	m := newMem(nil)
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(m, plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	again, err := Build(m, Params{})
	if err != nil {
		t.Fatalf("a re-run without flags must not ask for anything: %v", err)
	}
	if again.Changes() {
		t.Errorf("changes: %v", entryPaths(again))
	}
}

func TestExampleProfileOnRequest(t *testing.T) {
	plan, err := Build(Empty(), baseParams())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Entries {
		if strings.HasPrefix(e.Path, "profiles/") {
			t.Fatalf("a profile was generated without --example-profile: %s", e.Path)
		}
	}
	p := baseParams()
	p.ExampleProfile = true
	plan, err = Build(Empty(), p)
	if err != nil {
		t.Fatal(err)
	}
	e := entryOf(t, plan, "profiles/example.toml.sample")
	for _, l := range strings.Split(string(e.Content), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			t.Errorf("uncommented line in the sample: %q", l)
		}
	}
	if !strings.Contains(string(e.Content), "my-plugin@acme") {
		t.Error("sample does not use the marketplace name")
	}
}

func TestModeDetection(t *testing.T) {
	cases := []struct {
		name  string
		fs    FS
		mode  Mode
		force Mode
		err   bool
	}{
		{"missing", Empty(), ModeNew, "", false},
		{"empty", newMem(nil), ModeNew, "", false},
		{"only .git", func() FS { m := newMem(nil); m.mkdir(".git"); return m }(), ModeNew, "", false},
		{"content", newMem(map[string]string{"x.txt": "x"}), ModeAdopt, "", false},
		{"new forced on content", newMem(map[string]string{"x.txt": "x"}), "", ModeNew, true},
		{"adopt forced on empty", newMem(nil), ModeAdopt, ModeAdopt, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := baseParams()
			p.Mode = tc.force
			plan, err := Build(tc.fs, p)
			if tc.err {
				var fe *FieldError
				if !errors.As(err, &fe) || fe.Flag != "--mode" {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if plan.Mode != tc.mode {
				t.Errorf("mode = %s, want %s", plan.Mode, tc.mode)
			}
		})
	}
}

func TestNeverTouchesGit(t *testing.T) {
	m := newMem(nil)
	m.mkdir(".git")
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasGit {
		t.Error("HasGit")
	}
	for _, e := range plan.Entries {
		if strings.HasPrefix(e.Path, ".git/") || e.Path == ".git" {
			t.Errorf("plan touches %s", e.Path)
		}
	}
}

func TestSkipFlagsOmitGroups(t *testing.T) {
	for _, g := range DefaultGroups() {
		t.Run(string(g), func(t *testing.T) {
			p := baseParams()
			p.Skip = map[Group]bool{g: true}
			plan, err := Build(Empty(), p)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range plan.Entries {
				if e.Group == g {
					t.Errorf("group %s still planned %s", g, e.Path)
				}
			}
		})
	}
}

func TestExistingDirectoryAtFilePathIsAConflict(t *testing.T) {
	m := newMem(map[string]string{"README.md/x": "x"})
	if _, err := Build(m, baseParams()); err == nil || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("err = %v", err)
	}
}

func TestSymlinkedFilesAreLeftAlone(t *testing.T) {
	m := newMem(map[string]string{"keep.txt": "x"})
	m.symlink(".github/CODEOWNERS")
	m.symlink("README.md")
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{".github/CODEOWNERS", "README.md"} {
		e := entryOf(t, plan, f)
		if e.Action != ActionSkip || !strings.Contains(e.Reason, "symbolic link") {
			t.Errorf("%s: %s %s", f, e.Action, e.Reason)
		}
	}
	p := baseParams()
	p.Force = true
	if _, err := Build(m, p); err == nil {
		t.Error("--force must not replace a link")
	}
}

func TestSymlinkedDirectoryIsLeftAlone(t *testing.T) {
	m := newMem(map[string]string{"keep.txt": "x"})
	m.symlink(".github")
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan.Entries {
		if strings.HasPrefix(e.Path, ".github/") && (e.Action != ActionSkip || !strings.Contains(e.Reason, "symbolic link")) {
			t.Errorf("%s: %s (%s)", e.Path, e.Action, e.Reason)
		}
	}
	if _, err := Apply(m, plan, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, n := range m.names() {
		if strings.HasPrefix(n, ".github/") {
			t.Errorf("wrote %s through the link", n)
		}
	}
	// Even a plan that was made before the link appeared cannot write through it.
	forged := &Plan{Entries: []Entry{{Path: ".github/CODEOWNERS", Action: ActionCreate, Content: []byte("x")}}}
	if _, err := Apply(m, forged, ApplyOptions{}); !errors.Is(err, ErrSymlink) {
		t.Errorf("Apply through a link: %v", err)
	}
}

var _ = fs.ErrNotExist

func TestProfilesOnlyPlan(t *testing.T) {
	p := baseParams()
	p.MarketplaceName = ""
	p.ProfilesOnly = true
	plan, err := Build(newMem(nil), p)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range plan.Entries {
		paths = append(paths, e.Path)
	}
	want := ".gitattributes .github/CODEOWNERS .github/workflows/validate.yml .gitignore README.md ccshelf.toml"
	if got := strings.Join(paths, " "); got != want {
		t.Errorf("entries = %s, want %s", got, want)
	}
	// Without a platform owner it still asks for that, and never for a marketplace name.
	p.PlatformOwners = nil
	_, err = Build(newMem(nil), p)
	var me *MissingError
	if !errors.As(err, &me) || strings.Join(me.Flags, ",") != "--platform-owners" {
		t.Errorf("err = %v", err)
	}
}

func TestProfilesOnlyConflicts(t *testing.T) {
	p := baseParams() // has a marketplace name
	p.ProfilesOnly = true
	_, err := Build(newMem(nil), p)
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Flag != "--marketplace-name" {
		t.Errorf("--marketplace-name with --profiles-only: %v", err)
	}
	p.MarketplaceName = ""
	fsys := newMem(map[string]string{".claude-plugin/marketplace.json": `{"name":"x","plugins":[]}`})
	_, err = Build(fsys, p)
	if !errors.As(err, &fe) || fe.Flag != "--profiles-only" {
		t.Errorf("existing marketplace: %v", err)
	}
	// An existing profiles-only config without the flag would grow a marketplace.
	q := baseParams()
	fsys = newMem(map[string]string{"ccshelf.toml": "[catalog]\nenabled = false\n"})
	_, err = Build(fsys, q)
	if !errors.As(err, &fe) || fe.Flag != "--profiles-only" {
		t.Errorf("disabled catalog without the flag: %v", err)
	}
}

func TestProfilesOnlyAdoptExistingConfig(t *testing.T) {
	p := baseParams()
	p.MarketplaceName = ""
	p.ProfilesOnly = true
	var fe *FieldError
	// A config without enabled = false: usage error, unless it is replaced.
	files := map[string]string{"ccshelf.toml": "[lint]\nrequire = [\"owner\"]\n", "README.md": "x"}
	_, err := Build(newMem(files), p)
	if !errors.As(err, &fe) || fe.Flag != "--profiles-only" || !strings.Contains(fe.Msg, "enabled = false") {
		t.Errorf("config without enabled = false: %v", err)
	}
	p.Force = true
	plan, err := Build(newMem(files), p)
	if err != nil || entryOf(t, plan, "ccshelf.toml").Action != ActionOverwrite {
		t.Errorf("--force: %v", err)
	}
	// An existing profiles-only config is fine.
	p.Force = false
	files["ccshelf.toml"] = "[catalog]\nenabled = false\n"
	if _, err := Build(newMem(files), p); err != nil {
		t.Errorf("profiles-only config: %v", err)
	}
	// A marketplace listed by the config counts as an existing marketplace.
	files["ccshelf.toml"] = "[catalog]\nenabled = false\nmarketplaces = [\"team/market.json\"]\n"
	files["team/market.json"] = "{}"
	if _, err := Build(newMem(files), p); !errors.As(err, &fe) || !strings.Contains(fe.Msg, "team/market.json") {
		t.Errorf("configured marketplace: %v", err)
	}
}

func TestProfilesOnlyNoConfigAndOwner(t *testing.T) {
	p := baseParams()
	p.MarketplaceName = ""
	p.ProfilesOnly = true
	p.Skip = map[Group]bool{GroupConfig: true}
	var fe *FieldError
	if _, err := Build(newMem(nil), p); !errors.As(err, &fe) || fe.Flag != "--no-config" {
		t.Errorf("--no-config: %v", err)
	}
	if _, err := Build(newMem(map[string]string{"ccshelf.toml": "[catalog]\nenabled = false\n"}), p); err != nil {
		t.Errorf("--no-config with an existing profiles-only config: %v", err)
	}
	p.Skip = nil
	p.Owner = "@acme/x"
	if _, err := Build(newMem(nil), p); !errors.As(err, &fe) || fe.Flag != "--owner" {
		t.Errorf("--owner: %v", err)
	}
}

func TestProfilesOnlyNotesPluginsDir(t *testing.T) {
	p := baseParams()
	p.MarketplaceName = ""
	p.ProfilesOnly = true
	plan, err := Build(newMem(map[string]string{"plugins/x/README.md": "x"}), p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Notes, "\n"), "plugins/ is ignored") {
		t.Errorf("notes = %v", plan.Notes)
	}
}

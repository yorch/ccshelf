package orgcmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/scaffold"
)

func TestCatalogInitRefusesClaudeAndCcshelfDirectories(t *testing.T) {
	h := newHarness(t, "")
	base := h.cwd
	home := filepath.Join(base, "people", "me")
	claudeCfg := filepath.Join(base, "claude-cfg")
	xdgConfig := filepath.Join(base, "xdg-config")
	xdgCache := filepath.Join(base, "xdg-cache")
	for _, d := range []string{home, claudeCfg, xdgConfig, xdgCache} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.env.Getenv = func(k string) string {
		switch k {
		case "HOME", "USERPROFILE":
			return home
		case "CLAUDE_CONFIG_DIR":
			return claudeCfg
		case "XDG_CONFIG_HOME":
			return xdgConfig
		case "XDG_CACHE_HOME":
			return xdgCache
		}
		return ""
	}
	refused := map[string]string{
		"~/.claude":                    filepath.Join(home, ".claude"),
		"inside ~/.claude":             filepath.Join(home, ".claude", "org"),
		"inside CLAUDE_CONFIG_DIR":     filepath.Join(claudeCfg, "plugins", "org"),
		"CLAUDE_CONFIG_DIR itself":     claudeCfg,
		"the ccshelf config directory": filepath.Join(xdgConfig, "ccshelf"),
		"inside the ccshelf cache":     filepath.Join(xdgCache, "ccshelf", "x"),
		"default ccshelf config":       filepath.Join(home, ".config", "ccshelf"),
		"default ccshelf cache":        filepath.Join(home, ".cache", "ccshelf", "y"),
		"parent of the home":           filepath.Join(base, "people"),
		"grandparent of the home":      base,
	}
	for name, dir := range refused {
		r := h.run(initArgs(dir, "--yes")...)
		if r.code != 2 {
			t.Errorf("%s (%s): code %d\n%s\n%s", name, dir, r.code, r.out, r.err)
		}
		if _, err := os.Stat(filepath.Join(dir, "ccshelf.toml")); err == nil {
			t.Errorf("%s: wrote into %s", name, dir)
		}
	}
	// A directory that merely starts like one of them is fine.
	for _, dir := range []string{filepath.Join(home, ".claudex"), filepath.Join(home, "code", "org"), filepath.Join(xdgConfig, "ccshelf-org")} {
		if r := h.run(initArgs(dir, "--dry-run")...); r.code != 0 {
			t.Errorf("%s: code %d\n%s\n%s", dir, r.code, r.out, r.err)
		}
	}
	// Reaching ~/.claude through a link is refused as well.
	link := filepath.Join(base, "viaLink")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, ".claude"), link); err == nil {
		if r := h.run(initArgs(filepath.Join(link, "org"), "--yes")...); r.code != 2 {
			t.Errorf("through a link: code %d\n%s\n%s", r.code, r.out, r.err)
		}
	}
}

func TestCatalogInitShowsTheSuggestedLines(t *testing.T) {
	h := newHarness(t, "")
	dir := legacy(t)
	args := []string{"catalog", "init", dir, "--platform-owners", "@legacy/platform", "--ccshelf-ref", testSHA, "--ccshelf-version", "v0.1.0", "--dry-run"}
	r := h.run(args...)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if !strings.Contains(r.out, "needs-merge .github/CODEOWNERS") || !strings.Contains(r.out, "      | /ccshelf.toml") || !strings.Contains(r.out, "      | # Rules that") {
		t.Errorf("the plan does not show the suggestion:\n%s", r.out)
	}
	for _, l := range strings.Split(r.out, "\n") {
		if strings.HasPrefix(l, "      | ") && len(l) > 220 {
			t.Errorf("an unbounded suggestion line: %d bytes", len(l))
		}
	}
	// --quiet leaves them out of the text.
	r = h.run(append(args, "--quiet")...)
	if strings.Contains(r.out, "      | ") || !strings.Contains(r.out, "needs-merge .github/CODEOWNERS") {
		t.Errorf("--quiet:\n%s", r.out)
	}
	// JSON carries them either way.
	r = h.run(append(args, "--json", "--quiet")...)
	var env struct{ Data initJSON }
	if err := json.Unmarshal([]byte(r.out), &env); err != nil {
		t.Fatalf("%v\n%s", err, r.out)
	}
	found := false
	for _, e := range env.Data.Entries {
		if e.Path == ".github/CODEOWNERS" {
			found = strings.Contains(e.Suggestion, "/ccshelf.toml")
		} else if e.Suggestion != "" {
			t.Errorf("%s carries a suggestion although it is %s", e.Path, e.Action)
		}
	}
	if !found {
		t.Errorf("no suggestion in the JSON entries: %+v", env.Data.Entries)
	}
}

func TestBoundedSuggestion(t *testing.T) {
	if boundedSuggestion(nil) != "" {
		t.Error("nil")
	}
	big := strings.Repeat("/x/y/z @acme/team\n", 1000)
	got := boundedSuggestion([]byte(big))
	if len(got) > maxSuggestionBytes+100 || !strings.Contains(got, "cut") {
		t.Errorf("len %d", len(got))
	}
	var w strings.Builder
	printSuggestion(&w, []byte(big))
	lines := strings.Split(strings.TrimRight(w.String(), "\n"), "\n")
	if len(lines) != maxSuggestionLines+1 || !strings.Contains(lines[len(lines)-1], "more lines") {
		t.Errorf("%d lines:\n%s", len(lines), w.String())
	}
	w.Reset()
	printSuggestion(&w, []byte("a\u202eb "+strings.Repeat("y", 400)+"\n"))
	if strings.Contains(w.String(), "\u202e") || len(w.String()) > 260 {
		t.Errorf("not sanitized or bounded: %q", w.String())
	}
}

func TestCatalogInitDoesNotReplaceAForeignSuggestionFile(t *testing.T) {
	h := newHarness(t, "")
	dir := legacy(t)
	write(t, dir, ".github/CODEOWNERS.ccshelf-suggested", "the user's own notes\n")
	args := []string{"catalog", "init", dir, "--platform-owners", "@legacy/platform", "--ccshelf-ref", testSHA, "--ccshelf-version", "v0.1.0", "--yes", "--write-suggestions"}
	r := h.run(args...)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if read(t, dir, ".github/CODEOWNERS.ccshelf-suggested") != "the user's own notes\n" {
		t.Error("a file the user wrote was replaced")
	}
	if !strings.Contains(r.out, "was not written by ccshelf") {
		t.Errorf("no note:\n%s", r.out)
	}
	r = h.run(append(args, "--json")...)
	var env struct{ Data initJSON }
	if err := json.Unmarshal([]byte(r.out), &env); err != nil || len(env.Data.Refused) != 1 {
		t.Errorf("%v: refused = %v\n%s", err, env.Data.Refused, r.out)
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	// An empty file, not os.DevNull: git for Windows on arm64 cannot open NUL.
	empty := filepath.Join(t.TempDir(), "empty.gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+empty, "GIT_CONFIG_SYSTEM="+empty, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestCatalogInitDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	h := newHarness(t, "")
	catalogYML := func(dir string) string { return read(t, dir, ".github/workflows/catalog.yml") }

	// A flag wins over everything.
	flagDir := filepath.Join(h.cwd, "flag")
	if r := h.run(initArgs(flagDir, "--yes", "--default-branch", "release/v1")...); r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if !strings.Contains(catalogYML(flagDir), `branches: ["release/v1"]`) {
		t.Errorf("catalog.yml:\n%s", catalogYML(flagDir))
	}
	// No repository and no flag: main.
	plain := filepath.Join(h.cwd, "plain")
	if r := h.run(initArgs(plain, "--yes")...); r.code != 0 {
		t.Fatalf("code %d\n%s", r.code, r.err)
	}
	if !strings.Contains(catalogYML(plain), `branches: ["main"]`) {
		t.Errorf("catalog.yml:\n%s", catalogYML(plain))
	}
	// An existing repository: its branch is read, read-only, and noted.
	for _, branch := range []string{"master", "trunk"} {
		repo := filepath.Join(h.cwd, "repo-"+branch)
		write(t, repo, "plugins/a/.claude-plugin/plugin.json", `{"name": "a", "description": "A description that is long enough."}`)
		gitIn(t, repo, "init", "-q", "-b", branch)
		headBefore := read(t, repo, ".git/HEAD")
		r := h.run(initArgs(repo, "--yes")...)
		if r.code != 0 {
			t.Fatalf("%s: code %d\n%s\n%s", branch, r.code, r.out, r.err)
		}
		if !strings.Contains(catalogYML(repo), `branches: ["`+branch+`"]`) || !strings.Contains(r.out, "default branch "+branch) {
			t.Errorf("%s: catalog.yml:\n%s\n%s", branch, catalogYML(repo), r.out)
		}
		if read(t, repo, ".git/HEAD") != headBefore {
			t.Errorf("%s: .git/HEAD changed", branch)
		}
		// The flag overrides the detection.
		other := filepath.Join(h.cwd, "other-"+branch)
		write(t, other, "keep.txt", "x")
		gitIn(t, other, "init", "-q", "-b", branch)
		if r := h.run(initArgs(other, "--yes", "--default-branch", "develop")...); r.code != 0 || !strings.Contains(catalogYML(other), `["develop"]`) {
			t.Errorf("flag over detection: %d\n%s", r.code, r.err)
		}
	}
	// A hostile branch name in an existing repository is ignored, not written.
	if runtime.GOOS != "windows" {
		hostile := filepath.Join(h.cwd, "hostile")
		write(t, hostile, "keep.txt", "x")
		gitIn(t, hostile, "init", "-q")
		if err := os.WriteFile(filepath.Join(hostile, ".git", "HEAD"), []byte("ref: refs/heads/x\"];evil$(d)\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if r := h.run(initArgs(hostile, "--yes")...); r.code != 0 {
			t.Fatalf("code %d\n%s", r.code, r.err)
		}
		if got := catalogYML(hostile); strings.Contains(got, "evil") || !strings.Contains(got, `branches: ["main"]`) {
			t.Errorf("catalog.yml:\n%s", got)
		}
	}
	// A subdirectory of a repository does not read the parent's branch.
	parent := filepath.Join(h.cwd, "parent")
	write(t, parent, "x", "x")
	gitIn(t, parent, "init", "-q", "-b", "elsewhere")
	sub := filepath.Join(parent, "org")
	write(t, sub, "keep.txt", "x")
	if r := h.run(initArgs(sub, "--yes")...); r.code != 0 || !strings.Contains(catalogYML(sub), `["main"]`) {
		t.Errorf("subdir: %d\n%s", r.code, r.err)
	}
}

func TestCatalogInitRejectsAHostileDefaultBranch(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	for _, b := range []string{"main\n[x]", "a b", "..", "x;y", "a/../b", "-flag", "main\"]", "refs/heads/x*", "x.lock", "x\u202ey"} {
		r := h.run(initArgs(dir, "--yes", "--default-branch", b)...)
		if r.code != 2 || !strings.Contains(r.err, "--default-branch") {
			t.Errorf("%q: code %d\n%s", b, r.code, r.err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the directory was created")
	}
}

func TestCatalogInitFailureReportsAndRollsBack(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory that cannot be written")
	}
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "repo")
	write(t, dir, "keep.txt", "x")
	locked := filepath.Join(dir, ".github")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	before := treeSnapshot(t, dir)

	r := h.run(initArgs(dir, "--yes", "--mode", "adopt")...)
	if r.code != 1 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	for _, want := range []string{".github/CODEOWNERS", "written before the failure", "rolled back"} {
		if !strings.Contains(r.err, want) {
			t.Errorf("the error lacks %q:\n%s", want, r.err)
		}
	}
	if got := treeSnapshot(t, dir); got != before {
		t.Errorf("the rollback left files behind:\n%s\n--\n%s", before, got)
	}
}

func TestApplyFailureData(t *testing.T) {
	f := &applyFailure{err: os.ErrPermission, res: &scaffold.Result{Created: []string{"a", "b", "c"}, RolledBack: []string{"b"}}}
	if h := f.Hint(); !strings.Contains(h, "written before the failure: a, b, c") || !strings.Contains(h, "Rolled back: b") || !strings.Contains(h, "2 stay") {
		t.Errorf("hint = %q", h)
	}
	data := f.ErrorData()
	if w, _ := data["written"].([]string); len(w) != 3 {
		t.Errorf("data = %v", data)
	}
	if r, _ := data["rolled_back"].([]string); len(r) != 1 {
		t.Errorf("data = %v", data)
	}
	all := &applyFailure{err: os.ErrPermission, res: &scaffold.Result{Created: []string{"a"}, RolledBack: []string{"a"}}}
	if h := all.Hint(); !strings.Contains(h, "removed again") {
		t.Errorf("hint = %q", h)
	}
	none := &applyFailure{err: os.ErrPermission, res: &scaffold.Result{}}
	if h := none.Hint(); !strings.Contains(h, "nothing new was written") {
		t.Errorf("hint = %q", h)
	}
	many := make([]string, 30)
	if got := listFiles(many); !strings.Contains(got, "and 18 more") {
		t.Errorf("listFiles = %q", got)
	}
}

func TestCatalogInitNextStepsNameTheFilesWritten(t *testing.T) {
	h := newHarness(t, "")
	dir := legacy(t)
	// validate.yml exists: only catalog.yml needs a pin.
	write(t, dir, ".github/workflows/validate.yml", "name: mine\non: push\njobs: {}\n")
	r := h.run("catalog", "init", dir, "--platform-owners", "@legacy/platform", "--yes")
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if strings.Contains(r.out, "validate.yml and") || !strings.Contains(r.out, "pin the ccshelf action in .github/workflows/catalog.yml:") || !strings.Contains(r.out, "delete the guard job") {
		t.Errorf("the pin step:\n%s", r.out)
	}
	if !strings.Contains(r.out, "replace every TODO(ccshelf) in the files written: ") || !strings.Contains(r.out, "catalog/plugins/tools-a.toml") ||
		!strings.Contains(r.out, "does not read the workflows or the README") {
		t.Errorf("the TODO step:\n%s", r.out)
	}
	if strings.Contains(r.out, "then remove the guard step") {
		t.Errorf("stale wording:\n%s", r.out)
	}
}

func TestCatalogInitNestedGitInitIsNoted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	h := newHarness(t, "")
	outer := filepath.Join(h.cwd, "outer")
	write(t, outer, "x", "x")
	gitIn(t, outer, "init", "-q")
	dir := filepath.Join(outer, "org")
	r := h.run(initArgs(dir, "--yes", "--git-init")...)
	if r.code != 0 || !strings.Contains(r.out, "nested repository") {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	// Without --git-init there is nothing to warn about.
	r = h.run(initArgs(filepath.Join(outer, "org2"), "--dry-run")...)
	if strings.Contains(r.out, "nested repository") {
		t.Errorf("a warning without --git-init:\n%s", r.out)
	}
}

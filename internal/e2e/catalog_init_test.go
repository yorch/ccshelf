package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const initSHA = "fedcba9876543210fedcba9876543210fedcba98"

func initFlags(dir string, extra ...string) []string {
	args := []string{"catalog", "init", dir, "--marketplace-name", "acme", "--org", "Acme Corp", "--platform-owners", "@acme/platform", "--ccshelf-ref", initSHA, "--ccshelf-version", "v0.1.0"}
	return append(args, extra...)
}

func tree(t *testing.T, root string) map[string]string {
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

func sameTree(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

func TestCatalogInitNewRepoPassesTheRealCommands(t *testing.T) {
	s := newSandbox(t)
	dir := filepath.Join(s.Work, "acme-claude")

	r := s.run(initFlags(dir, "--dry-run")...)
	if r.Code != 0 {
		t.Fatalf("dry run: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	contains(t, "dry run", r.Stdout, "mode: new", "dry run: nothing was written")
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("--dry-run created the directory")
	}

	r = s.run(initFlags(dir, "--yes")...)
	if r.Code != 0 {
		t.Fatalf("init: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	contains(t, "init", r.Stdout, "wrote 9 files", "next steps:")

	for _, c := range [][]string{{"lint", "--strict"}, {"compile", "--check"}, {"catalog", "build", "--out", filepath.Join(s.Work, "site")}} {
		if r := s.runIn(dir, "", c...); r.Code != 0 {
			t.Errorf("%v in the generated repo: exit %d\n%s%s", c, r.Code, r.Stdout, r.Stderr)
		}
	}
	if b, err := os.ReadFile(filepath.Join(s.Work, "site", "catalog.json")); err != nil || !json.Valid(b) {
		t.Errorf("catalog.json: %v", err)
	}

	before := tree(t, dir)
	r = s.run(initFlags(dir, "--yes")...)
	if r.Code != 0 {
		t.Fatalf("second run: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	contains(t, "second run", r.Stdout, "mode: adopt", "nothing to do")
	if !sameTree(before, tree(t, dir)) {
		t.Error("the second run changed the repository")
	}
	if n := len(s.anyStart()); n != 0 {
		t.Errorf("catalog init started claude %d times", n)
	}
}

func TestCatalogInitAdoptsAMarketplaceRepo(t *testing.T) {
	s := newSandbox(t)
	dir := filepath.Join(s.Work, "legacy")
	write(t, filepath.Join(dir, ".claude-plugin", "marketplace.json"), `{"name": "legacy", "owner": {"name": "Legacy"}, "plugins": [
  {"name": "tools-a", "source": "./plugins/tools-a", "description": "Helpers for the A team, used every day.", "author": {"name": "A"}},
  {"name": "tools-b", "source": "./plugins/tools-b", "description": "Helpers for the B team, with a deploy hook.", "author": {"name": "B"}}]}
`)
	write(t, filepath.Join(dir, "plugins", "tools-a", ".claude-plugin", "plugin.json"), `{"name": "tools-a"}`)
	write(t, filepath.Join(dir, "plugins", "tools-a", "README.md"), "# a\n")
	write(t, filepath.Join(dir, "plugins", "tools-b", ".claude-plugin", "plugin.json"), `{"name": "tools-b"}`)
	write(t, filepath.Join(dir, "plugins", "tools-b", "hooks", "hooks.json"), `{"hooks": {}}`)
	write(t, filepath.Join(dir, "plugins", "tools-b", "README.md"), "# b\n")
	write(t, filepath.Join(dir, ".github", "CODEOWNERS"), "/plugins/tools-a/ @legacy/a\n/plugins/tools-b/ @legacy/b\n/plugins/*/hooks/ @legacy/platform\n/.github/ @legacy/platform\n")
	write(t, filepath.Join(dir, ".github", "workflows", "ci.yml"), "name: ci\non: push\njobs: {}\n")
	write(t, filepath.Join(dir, "README.md"), "# Legacy\n")
	before := tree(t, dir)
	args := []string{"catalog", "init", dir, "--platform-owners", "@legacy/platform", "--ccshelf-ref", initSHA, "--ccshelf-version", "v0.1.0"}

	if r := s.run(append(args, "--dry-run")...); r.Code != 0 {
		t.Fatalf("dry run: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	} else {
		contains(t, "dry run", r.Stdout, "mode: adopt", "needs-merge .github/CODEOWNERS", "create      catalog/plugins/tools-b.toml", "skip-exists README.md")
	}
	if !sameTree(before, tree(t, dir)) {
		t.Fatal("--dry-run changed the repository")
	}

	r := s.run(append(args, "--yes", "--write-suggestions")...)
	if r.Code != 0 {
		t.Fatalf("adopt: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	after := tree(t, dir)
	for name, content := range before {
		if after[name] != content {
			t.Errorf("existing file %s changed", name)
		}
	}
	for _, name := range []string{"ccshelf.toml", "catalog/plugins/tools-a.toml", "catalog/plugins/tools-b.toml", ".github/workflows/validate.yml", ".github/CODEOWNERS.ccshelf-suggested", ".gitignore"} {
		if _, ok := after[name]; !ok {
			t.Errorf("%s was not created", name)
		}
	}
	if !strings.Contains(after["catalog/plugins/tools-a.toml"], `owner = "@legacy/a"`) {
		t.Errorf("tools-a sidecar owner:\n%s", after["catalog/plugins/tools-a.toml"])
	}
	if r := s.runIn(dir, "", "lint"); r.Code != 0 {
		t.Errorf("lint after adopt: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	} else {
		contains(t, "lint after adopt", r.Stdout, "CAT048")
	}
	for _, c := range [][]string{{"compile", "--check"}, {"catalog", "build", "--out", filepath.Join(s.Work, "site")}} {
		if r := s.runIn(dir, "", c...); r.Code != 0 {
			t.Errorf("%v after adopt: exit %d\n%s%s", c, r.Code, r.Stdout, r.Stderr)
		}
	}

	r = s.run(append(args, "--yes", "--write-suggestions")...)
	if r.Code != 0 {
		t.Fatalf("second run: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if !sameTree(after, tree(t, dir)) {
		t.Error("the second run changed the repository")
	}
}

func TestCatalogInitUsageErrors(t *testing.T) {
	s := newSandbox(t)
	dir := filepath.Join(s.Work, "org")

	r := s.run("catalog", "init", dir)
	if r.Code != 2 {
		t.Fatalf("exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	contains(t, "missing flags", r.Stderr, "--marketplace-name", "--platform-owners", "--yes")
	if r := s.run("catalog", "init", dir, "--json"); r.Code != 2 || !json.Valid([]byte(r.Stderr)) {
		t.Errorf("--json error: exit %d\n%s", r.Code, r.Stderr)
	}
	if r := s.run(initFlags(dir)...); r.Code != 2 {
		t.Errorf("without --yes: exit %d\n%s", r.Code, r.Stderr)
	}
	for _, bad := range [][]string{
		{"--marketplace-name", "a\"b"},
		{"--marketplace-name", "Upper"},
		{"--org", "line\nbreak"},
		{"--owner", "@a b"},
		{"--platform-owners", "@ok,@bad owner"},
		{"--ccshelf-ref", "main"},
		{"--runner-label", "x'}}"},
		{"--mode", "other"},
	} {
		if r := s.run(append(initFlags(dir, "--yes"), bad...)...); r.Code != 2 {
			t.Errorf("%v: exit %d\n%s%s", bad, r.Code, r.Stdout, r.Stderr)
		}
	}
	for _, d := range []string{"../escape", filepath.Join("a", "..", "..", "escape")} {
		if r := s.run(initFlags(d, "--yes")...); r.Code != 2 {
			t.Errorf("dir %q: exit %d\n%s", d, r.Code, r.Stderr)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("a refused run created the directory")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.Work), "escape")); !os.IsNotExist(err) {
		t.Error("a path with .. escaped")
	}
}

func TestCatalogInitRefusesDangerousTargets(t *testing.T) {
	s := newSandbox(t)
	if r := s.run(initFlags(s.Home, "--yes")...); r.Code != 2 || !strings.Contains(r.Stderr, "home directory") {
		t.Errorf("home: exit %d\n%s", r.Code, r.Stderr)
	}
	if r := s.runIn(s.Home, "", initFlags(".", "--yes")...); r.Code != 2 || !strings.Contains(r.Stderr, "home directory") {
		t.Errorf("home as the working directory: exit %d\n%s", r.Code, r.Stderr)
	}
	tool := filepath.Join(s.Work, "tool")
	write(t, filepath.Join(tool, "go.mod"), "module github.com/yorch/ccshelf\n\ngo 1.27\n")
	if r := s.run(initFlags(filepath.Join(tool, "examples", "x"), "--yes")...); r.Code != 2 || !strings.Contains(r.Stderr, "tool repository") {
		t.Errorf("tool repository: exit %d\n%s", r.Code, r.Stderr)
	}
	if _, err := os.Stat(filepath.Join(tool, "examples")); !os.IsNotExist(err) {
		t.Error("wrote into the tool repository")
	}
	real := filepath.Join(s.Work, "real")
	write(t, filepath.Join(real, "keep.txt"), "x")
	link := filepath.Join(s.Work, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if r := s.run(initFlags("link", "--yes")...); r.Code != 1 || !strings.Contains(r.Stderr, "symbolic link") {
		t.Errorf("symbolic link: exit %d\n%s", r.Code, r.Stderr)
	}
	if got := tree(t, real); len(got) != 1 {
		t.Errorf("wrote through a symbolic link: %v", got)
	}
}

func TestCatalogInitGitInit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	s := newSandbox(t)
	dir := filepath.Join(s.Work, "org")
	r := s.run(initFlags(dir, "--yes", "--git-init")...)
	if r.Code != 0 {
		t.Fatalf("exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if st, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !st.IsDir() {
		t.Fatalf(".git: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "refs", "heads", "main")); err == nil {
		t.Error("something was committed")
	}
}

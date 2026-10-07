package orgcmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/ui"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

// initArgs are the flags of a complete, non-interactive new-repo run.
func initArgs(dir string, extra ...string) []string {
	args := []string{"catalog", "init", dir, "--marketplace-name", "acme", "--org", "Acme Corp", "--platform-owners", "@acme/platform", "--ccshelf-ref", testSHA, "--ccshelf-version", "v0.1.0"}
	return append(args, extra...)
}

func TestCatalogInitNewRepo(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	r := h.run(initArgs(dir, "--yes")...)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	for _, want := range []string{"mode: new", "create      ccshelf.toml", "wrote 9 files", "next steps:", "ccshelf lint"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("output lacks %q:\n%s", want, r.out)
		}
	}
	if strings.Contains(r.out, "todo:") {
		t.Errorf("a pinned repo has no pin todo:\n%s", r.out)
	}
	if runtime.GOOS != "windows" {
		for _, f := range []string{"ccshelf.toml", ".github/CODEOWNERS", ".github/workflows/validate.yml"} {
			if st, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil || st.Mode().Perm()&0o022 != 0 || st.Mode().Perm()&0o444 != 0o444 {
				t.Errorf("%s: %v %v", f, st, err)
			}
		}
	}

	// The generated repo passes the real commands.
	lh := newHarness(t, dir)
	if r := lh.run("lint"); r.code != 0 || !strings.Contains(r.out, "no findings") {
		t.Errorf("lint: %d\n%s\n%s", r.code, r.out, r.err)
	}
	if r := lh.run("lint", "--strict"); r.code != 0 {
		t.Errorf("lint --strict: %d\n%s\n%s", r.code, r.out, r.err)
	}
	if r := lh.run("compile", "--check"); r.code != 0 {
		t.Errorf("compile --check: %d\n%s\n%s", r.code, r.out, r.err)
	}
	if r := lh.run("catalog", "build", "--out", filepath.Join(h.cwd, "site")); r.code != 0 {
		t.Errorf("catalog build: %d\n%s\n%s", r.code, r.out, r.err)
	}

	// A second run changes nothing and says so.
	before := treeSnapshot(t, dir)
	r = h.run(initArgs(dir, "--yes")...)
	if r.code != 0 || !strings.Contains(r.out, "nothing to do") || strings.Contains(r.out, "next steps") {
		t.Errorf("second run: %d\n%s\n%s", r.code, r.out, r.err)
	}
	if got := treeSnapshot(t, dir); got != before {
		t.Errorf("second run changed the directory:\n%s\n--\n%s", before, got)
	}
	// ... also without any of the value flags.
	r = h.run("catalog", "init", dir, "--yes")
	if r.code != 0 || !strings.Contains(r.out, "nothing to do") {
		t.Errorf("flagless re-run: %d\n%s\n%s", r.code, r.out, r.err)
	}
}

func treeSnapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			b.WriteString("d " + filepath.ToSlash(rel) + "\n")
			return nil
		}
		data, err := os.ReadFile(p)
		b.WriteString("f " + filepath.ToSlash(rel) + " " + string(data) + "\n")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestCatalogInitDryRunWritesNothing(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org", "deep")
	r := h.run(initArgs(dir, "--dry-run")...)
	if r.code != 0 || !strings.Contains(r.out, "dry run: nothing was written") || !strings.Contains(r.out, "create      ccshelf.toml") {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if _, err := os.Stat(filepath.Join(h.cwd, "org")); !os.IsNotExist(err) {
		t.Errorf("--dry-run created a directory: %v", err)
	}
	// Also against a directory that exists.
	existing := filepath.Join(h.cwd, "existing")
	write(t, existing, "keep.txt", "x")
	before := treeSnapshot(t, existing)
	if r := h.run(initArgs(existing, "--dry-run")...); r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if treeSnapshot(t, existing) != before {
		t.Error("--dry-run changed an existing directory")
	}
}

func TestCatalogInitNonInteractiveMissingValues(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	r := h.run("catalog", "init", dir)
	if r.code != 2 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	for _, flag := range []string{"--marketplace-name", "--platform-owners", "--yes"} {
		if !strings.Contains(r.err, flag) {
			t.Errorf("error does not name %s:\n%s", flag, r.err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("a failed run created the directory")
	}
	// Only the confirmation is missing.
	r = h.run(initArgs(dir)...)
	if r.code != 2 || !strings.Contains(r.err, "--yes") || strings.Contains(r.err, "--marketplace-name") {
		t.Errorf("code %d\n%s", r.code, r.err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("a run without --yes created the directory")
	}
	// --dry-run needs no --yes, but still the values.
	r = h.run("catalog", "init", dir, "--dry-run")
	if r.code != 2 || strings.Contains(r.err, "--yes") {
		t.Errorf("code %d\n%s", r.code, r.err)
	}
}

func TestCatalogInitHostileValuesAreRejected(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	bad := [][]string{
		{"--marketplace-name", "acme\"\n[protect]"},
		{"--marketplace-name", "Acme"},
		{"--marketplace-name", "../x"},
		{"--org", "Acme\nCorp"},
		{"--org", "Acme\u202eCorp"},
		{"--org", "Acme\x00Corp"},
		{"--org", " padded "},
		{"--org", strings.Repeat("a", 101)},
		{"--owner", "@a b"},
		{"--owner", "@acme/team\n* @evil"},
		{"--owner", "not an owner"},
		{"--platform-owners", "@acme/platform,@a#b"},
		{"--platform-owners", "@ok/x\u200b"},
		{"--ccshelf-ref", "main"},
		{"--ccshelf-ref", strings.Repeat("A", 40)},
		{"--ccshelf-ref", "v1.2"},
		{"--ccshelf-version", "latest"},
		{"--ccshelf-version", "v1.2.3\nrun: x"},
		{"--runner-label", "x'}} ${{ secrets.X }}"},
		{"--runner-label", "a b"},
		{"--mode", "both"},
		{"--sidecars", "all"},
	}
	for _, b := range bad {
		args := append(initArgs(dir, "--yes"), b...) // the later flag wins
		r := h.run(args...)
		if r.code != 2 {
			t.Errorf("%v: code %d\n%s\n%s", b, r.code, r.out, r.err)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("%v: the directory was created", b)
		}
		if strings.ContainsAny(r.err, "\x00\u202e\u200b") {
			t.Errorf("%v: the error echoes a hostile character: %q", b, r.err)
		}
	}
	for _, d := range []string{"../escape", "a/../../escape", `a\..\escape`} {
		if r := h.run(initArgs(d, "--yes")...); r.code != 2 || !strings.Contains(r.err, "..") {
			t.Errorf("dir %q: code %d\n%s", d, r.code, r.err)
		}
	}
}

func TestCatalogInitValuesAreEncodedNotInjected(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	org := `Acme "Quoted" \ [link](x) <b>*bold*</b> # #{x} ${{ x }}`
	r := h.run("catalog", "init", dir, "--yes", "--marketplace-name", "acme", "--org", org, "--platform-owners", "@acme/platform", "--platform-owners", "ops@acme.example",
		"--ccshelf-ref", testSHA, "--ccshelf-version", "v0.1.0")
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	// ccshelf.toml parses and carries the exact name; marketplace.json too.
	lh := newHarness(t, dir)
	if r := lh.run("lint", "--json"); r.code != 0 {
		t.Fatalf("lint: %s %s", r.out, r.err)
	}
	if !strings.Contains(read(t, dir, "ccshelf.toml"), `title = "Acme \"Quoted\" \\ [link](x) <b>*bold*</b> # #{x} ${{ x }} plugin catalog"`) {
		t.Errorf("ccshelf.toml:\n%s", read(t, dir, "ccshelf.toml"))
	}
	var mk struct{ Owner struct{ Name string } }
	if err := json.Unmarshal([]byte(read(t, dir, ".claude-plugin/marketplace.json")), &mk); err != nil || mk.Owner.Name != org {
		t.Errorf("marketplace.json owner = %q, %v", mk.Owner.Name, err)
	}
	readme := read(t, dir, "README.md")
	if strings.Contains(readme, "<b>") || strings.Contains(readme, "[link](x)") || !strings.Contains(readme, `\[link\]\(x\) \<b\>\*bold\*\<\/b\>`) {
		t.Errorf("README.md did not escape the name:\n%s", strings.SplitN(readme, "\n", 3)[0])
	}
	// No workflow carries the org name or an owner.
	for _, w := range []string{"validate", "catalog", "release"} {
		if got := read(t, dir, ".github/workflows/"+w+".yml"); strings.Contains(got, "Quoted") || strings.Contains(got, "acme") {
			t.Errorf("%s.yml carries a user value", w)
		}
	}
}

func TestCatalogInitJSON(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	r := h.run(initArgs(dir, "--json", "--dry-run")...)
	_ = r
	h.g.JSON = true
	r = h.run(append(initArgs(dir, "--dry-run"), "--json")...)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	var env struct {
		Kind string
		Data initJSON
	}
	if err := json.Unmarshal([]byte(r.out), &env); err != nil {
		t.Fatalf("%v\n%s", err, r.out)
	}
	if env.Kind != "catalog-init" || env.Data.Mode != "new" || !env.Data.DryRun || len(env.Data.Entries) != 9 || len(env.Data.Written) != 0 {
		t.Errorf("data = %+v", env.Data)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("--dry-run --json wrote")
	}
	// A real run reports what it wrote; JSON never prompts, so --yes is needed.
	r = h.run(append(initArgs(dir), "--json")...)
	if r.code != 2 {
		t.Errorf("--json without --yes: code %d\n%s\n%s", r.code, r.out, r.err)
	}
	r = h.run(append(initArgs(dir, "--yes"), "--json")...)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if err := json.Unmarshal([]byte(r.out), &env); err != nil || len(env.Data.Written) != 9 {
		t.Errorf("%v %+v", err, env.Data)
	}
}

func TestCatalogInitSkipGroupsAndExampleProfile(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	r := h.run(initArgs(dir, "--yes", "--no-workflows", "--no-codeowners", "--no-readme", "--no-gitattributes", "--no-gitignore", "--no-config", "--example-profile", "--sidecars", "none")...)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	var got []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	if strings.Join(got, ",") != ".claude-plugin/marketplace.json,profiles/example.toml.sample" {
		t.Errorf("files = %v", got)
	}
}

// legacy is a marketplace repo that was never set up for ccshelf.
func legacy(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "legacy")
	write(t, dir, ".claude-plugin/marketplace.json", `{"name": "legacy", "owner": {"name": "Legacy"}, "plugins": [
  {"name": "tools-a", "source": "./plugins/tools-a", "description": "Helpers for the A team, used every day.", "author": {"name": "A"}}]}
`)
	write(t, dir, "plugins/tools-a/.claude-plugin/plugin.json", `{"name": "tools-a", "description": "Helpers for the A team, used every day."}`)
	write(t, dir, "plugins/tools-a/README.md", "# tools-a\n")
	write(t, dir, "plugins/extra/.claude-plugin/plugin.json", `{"name": "extra", "description": "Not listed yet in the marketplace file."}`)
	write(t, dir, ".github/CODEOWNERS", "/plugins/tools-a/ @legacy/a\n/.github/ @legacy/platform\n")
	write(t, dir, ".github/workflows/ci.yml", "name: ci\non: push\njobs: {}\n")
	write(t, dir, "README.md", "# Legacy\n")
	return dir
}

func TestCatalogInitAdopt(t *testing.T) {
	h := newHarness(t, "")
	dir := legacy(t)
	before := treeFiles(t, dir)
	args := []string{"catalog", "init", dir, "--platform-owners", "@legacy/platform", "--ccshelf-ref", testSHA, "--ccshelf-version", "v0.1.0"}

	r := h.run(append(args, "--dry-run")...)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	for _, want := range []string{
		"mode: adopt", "skip-exists .claude-plugin/marketplace.json  (exists; never rewritten", "needs-merge .github/CODEOWNERS", "create      catalog/plugins/tools-a.toml",
		"skip-exists README.md", "note: plugins/extra hold plugins that .claude-plugin/marketplace.json does not list", "--write-suggestions saves",
	} {
		if !strings.Contains(r.out, want) {
			t.Errorf("plan lacks %q:\n%s", want, r.out)
		}
	}
	if got := treeFiles(t, dir); !sameFiles(got, before) {
		t.Fatal("--dry-run changed the repo")
	}

	r = h.run(append(args, "--yes")...)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	for f, content := range before {
		if read(t, dir, f) != content {
			t.Errorf("%s was changed", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".github", "CODEOWNERS.ccshelf-suggested")); !os.IsNotExist(err) {
		t.Error("a suggestion was written without --write-suggestions")
	}
	if !strings.Contains(read(t, dir, "catalog/plugins/tools-a.toml"), `owner = "@legacy/a"`) {
		t.Errorf("sidecar owner:\n%s", read(t, dir, "catalog/plugins/tools-a.toml"))
	}
	lh := newHarness(t, dir)
	if r := lh.run("lint"); r.code != 0 {
		t.Errorf("lint after adopt: %d\n%s", r.code, r.out)
	}
	if r := lh.run("catalog", "build", "--out", filepath.Join(t.TempDir(), "site")); r.code != 0 {
		t.Errorf("catalog build after adopt: %d\n%s\n%s", r.code, r.out, r.err)
	}

	// The second run is a no-op; --write-suggestions then writes the one
	// suggestion and still never edits CODEOWNERS.
	if r := h.run(append(args, "--yes")...); r.code != 0 || !strings.Contains(r.out, "nothing to do") {
		t.Errorf("second run: %d\n%s\n%s", r.code, r.out, r.err)
	}
	r = h.run(append(args, "--yes", "--write-suggestions")...)
	if r.code != 0 || !strings.Contains(r.out, "wrote 1 suggestion") {
		t.Errorf("suggestions: %d\n%s\n%s", r.code, r.out, r.err)
	}
	if !strings.Contains(read(t, dir, ".github/CODEOWNERS.ccshelf-suggested"), "/ccshelf.toml") {
		t.Errorf("suggestion:\n%s", read(t, dir, ".github/CODEOWNERS.ccshelf-suggested"))
	}
	if read(t, dir, ".github/CODEOWNERS") != before[".github/CODEOWNERS"] {
		t.Error("CODEOWNERS changed")
	}
}

// treeFiles reads every regular file below root.
func treeFiles(t *testing.T, root string) map[string]string {
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

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestCatalogInitForceKeepsBackups(t *testing.T) {
	h := newHarness(t, "")
	dir := legacy(t)
	r := h.run("catalog", "init", dir, "--platform-owners", "@legacy/platform", "--ccshelf-ref", testSHA, "--ccshelf-version", "v0.1.0", "--force", "--yes")
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if read(t, dir, "README.md.bak") != "# Legacy\n" || !strings.Contains(read(t, dir, "README.md"), "org data repo") {
		t.Errorf("README: %q", read(t, dir, "README.md"))
	}
	if read(t, dir, ".github/CODEOWNERS.bak") != "/plugins/tools-a/ @legacy/a\n/.github/ @legacy/platform\n" {
		t.Error("CODEOWNERS backup is wrong")
	}
	if !strings.Contains(read(t, dir, ".claude-plugin/marketplace.json"), `"name": "legacy"`) {
		t.Error("marketplace.json was rewritten")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude-plugin", "marketplace.json.bak")); !os.IsNotExist(err) {
		t.Error("marketplace.json was backed up, so it was touched")
	}
	if read(t, dir, ".github/workflows/ci.yml") != "name: ci\non: push\njobs: {}\n" {
		t.Error("an unrelated workflow changed")
	}
}

func TestCatalogInitTargetGuards(t *testing.T) {
	h := newHarness(t, "")
	// The home directory.
	home := filepath.Join(h.cwd, "home")
	write(t, home, "x", "x")
	h.env.Getenv = func(k string) string {
		if k == "HOME" || k == "USERPROFILE" {
			return home
		}
		return ""
	}
	if r := h.run(initArgs(home, "--yes")...); r.code != 2 || !strings.Contains(r.err, "home directory") {
		t.Errorf("home: %d\n%s", r.code, r.err)
	}
	// The file system root.
	root := string(filepath.Separator)
	if v := filepath.VolumeName(h.cwd); v != "" {
		root = v + root
	}
	if r := h.run(initArgs(root, "--yes")...); r.code != 2 || !strings.Contains(r.err, "root") {
		t.Errorf("root: %d\n%s", r.code, r.err)
	}
	// The tool repository itself, and a directory inside it.
	tool := filepath.Join(h.cwd, "tool")
	write(t, tool, "go.mod", "module github.com/yorch/ccshelf\n\ngo 1.27\n")
	for _, d := range []string{tool, filepath.Join(tool, "examples", "x")} {
		if r := h.run(initArgs(d, "--yes")...); r.code != 2 || !strings.Contains(r.err, "tool repository") {
			t.Errorf("%s: %d\n%s", d, r.code, r.err)
		}
	}
	if _, err := os.Stat(filepath.Join(tool, "examples")); !os.IsNotExist(err) {
		t.Error("a directory was created inside the tool repository")
	}
	// A file where the directory should be.
	file := filepath.Join(h.cwd, "afile")
	write(t, h.cwd, "afile", "x")
	if r := h.run(initArgs(file, "--yes")...); r.code != 1 || !strings.Contains(r.err, "not a directory") {
		t.Errorf("file: %d\n%s", r.code, r.err)
	}
	// A symbolic link as the target, and as a component of a relative path.
	real := filepath.Join(h.cwd, "real")
	write(t, real, "x", "x")
	link := filepath.Join(h.cwd, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if r := h.run(initArgs(link, "--yes")...); r.code != 1 || !strings.Contains(r.err, "symbolic link") {
		t.Errorf("link: %d\n%s", r.code, r.err)
	}
	if r := h.run(initArgs(filepath.Join("link", "sub"), "--yes")...); r.code != 1 || !strings.Contains(r.err, "symbolic link") {
		t.Errorf("relative through a link: %d\n%s", r.code, r.err)
	}
	if _, err := os.Stat(filepath.Join(real, "sub")); !os.IsNotExist(err) {
		t.Error("wrote through a symbolic link")
	}
}

func TestCatalogInitDoesNotWriteThroughLinks(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	outside := filepath.Join(h.cwd, "outside")
	write(t, outside, "keep", "x")
	write(t, dir, "keep.txt", "x")
	if err := os.Symlink(outside, filepath.Join(dir, ".github")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "keep"), filepath.Join(dir, "README.md")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	r := h.run(initArgs(dir, "--yes", "--force")...)
	if r.code == 0 {
		t.Fatalf("--force replaced a symbolic link:\n%s", r.out)
	}
	r = h.run(initArgs(dir, "--yes")...)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if got := treeSnapshot(t, outside); got != "d .\nf keep x\n" {
		t.Errorf("something was written outside the target:\n%s", got)
	}
	if read(t, outside, "keep") != "x" {
		t.Error("a file was written through a link")
	}
}

func TestCatalogInitInteractive(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "acme-claude")
	sp := ui.NewScripted("acme", "@acme/platform", testSHA, "v0.1.0", true)
	h.env.Prompter = sp
	r := h.run("catalog", "init", dir)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if err := sp.Done(); err != nil {
		t.Error(err)
	}
	if !strings.Contains(r.err, "Equivalent: ccshelf catalog init") || !strings.Contains(r.err, "--marketplace-name acme") || !strings.Contains(r.err, "--platform-owners @acme/platform") || !strings.Contains(r.err, "--yes") {
		t.Errorf("no replayable equivalent command:\n%s", r.err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ccshelf.toml")); err != nil {
		t.Error(err)
	}
	// Flags beat prompts: only the confirmation is asked when everything is given.
	dir2 := filepath.Join(h.cwd, "second")
	sp = ui.NewScripted(true)
	h.env.Prompter = sp
	if r := h.run(initArgs(dir2)...); r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if err := sp.Done(); err != nil {
		t.Error(err)
	}
}

func TestCatalogInitInteractiveDeclined(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	sp := ui.NewScripted(false)
	h.env.Prompter = sp
	r := h.run(initArgs(dir)...)
	if r.code != 1 || !strings.Contains(r.err, "nothing was written") {
		t.Errorf("code %d\n%s", r.code, r.err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("declining still wrote")
	}
}

func TestCatalogInitGitInit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	r := h.run(initArgs(dir, "--yes", "--git-init")...)
	if r.code != 0 || !strings.Contains(r.out, "ran git init") {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if st, err := os.Stat(filepath.Join(dir, ".git")); err != nil || !st.IsDir() {
		t.Fatalf(".git: %v", err)
	}
	// Nothing was committed.
	if _, err := os.Stat(filepath.Join(dir, ".git", "refs", "heads", "main")); !os.IsNotExist(err) {
		t.Error("a branch exists: something was committed")
	}
	r = h.run(initArgs(dir, "--yes", "--git-init", "--dry-run")...)
	if !strings.Contains(r.out, "git init is not needed") {
		t.Errorf("%s", r.out)
	}
	// An empty directory holding only .git is still "new".
	dir2 := filepath.Join(h.cwd, "second")
	if err := os.MkdirAll(filepath.Join(dir2, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := h.run(initArgs(dir2, "--dry-run")...); !strings.Contains(r.out, "mode: new") {
		t.Errorf("%s", r.out)
	}
}

func TestCatalogInitModeFlag(t *testing.T) {
	h := newHarness(t, "")
	dir := legacy(t)
	r := h.run("catalog", "init", dir, "--mode", "new", "--platform-owners", "@legacy/platform", "--dry-run")
	if r.code != 2 || !strings.Contains(r.err, "--mode") {
		t.Errorf("code %d\n%s", r.code, r.err)
	}
}

func TestCatalogInitUnpinnedWorkflowsFailWithAMessage(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	r := h.run("catalog", "init", dir, "--yes", "--marketplace-name", "acme", "--platform-owners", "@acme/platform")
	if r.code != 0 || !strings.Contains(r.out, "todo: pin the ccshelf action") {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	got := read(t, dir, ".github/workflows/validate.yml")
	if !strings.Contains(got, "yorch/ccshelf/action@0000000000000000000000000000000000000000") || !strings.Contains(got, "::error title=ccshelf is not pinned::") {
		t.Errorf("validate.yml:\n%s", got)
	}
}

func TestCatalogInitTooManyArguments(t *testing.T) {
	h := newHarness(t, "")
	if r := h.run("catalog", "init", "a", "b"); r.code != 2 {
		t.Errorf("code %d\n%s", r.code, r.err)
	}
}

func TestRemoteOrg(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.example.com:acme/repo.git":    "acme",
		"https://ghe.example.com/acme/repo":       "acme",
		"ssh://git@ghe.example.com/acme/sub/repo": "acme",
		"https://ghe.example.com/onlyone":         "",
		"":                                        "",
		"https://h/%40bad/repo":                   "",
		"git@h:acme space/repo":                   "",
		"file:///srv/acme/repo.git":               "",
	} {
		if got := remoteOrg(in); got != want {
			t.Errorf("remoteOrg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultMarketplaceName(t *testing.T) {
	for in, want := range map[string]string{
		"/x/Acme Claude Marketplace":    "acme-claude-marketplace",
		"/x/acme_data.repo":             "acme-data-repo",
		"/x/---":                        "",
		"/x/" + strings.Repeat("a", 80): strings.Repeat("a", 64),
	} {
		if got := defaultMarketplaceName(in); got != want {
			t.Errorf("defaultMarketplaceName(%q) = %q, want %q", in, got, want)
		}
	}
}

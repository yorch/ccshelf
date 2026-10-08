package orgcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/ui"
)

// poArgs is a complete non-interactive profiles-only run.
func poArgs(dir string, extra ...string) []string {
	args := []string{"catalog", "init", dir, "--profiles-only", "--platform-owners", "@acme/platform", "--ccshelf-ref", testSHA, "--ccshelf-version", "v0.1.0"}
	return append(args, extra...)
}

func TestCatalogInitProfilesOnly(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "profiles-repo")
	r := h.run(poArgs(dir, "--example-profile", "--yes")...)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	for _, want := range []string{"wrote 7 files", "next steps:", "ccshelf lint", "add your profiles"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("output lacks %q:\n%s", want, r.out)
		}
	}
	for _, bad := range []string{"compile", "catalog build", "github-pages", "marketplace.json", "catalog/plugins"} {
		if strings.Contains(r.out, bad) {
			t.Errorf("a profiles-only run mentions %q:\n%s", bad, r.out)
		}
	}
	got := treeSnapshot(t, dir)
	for _, absent := range []string{".claude-plugin", "catalog.yml", "release.yml", "catalog/plugins"} {
		if strings.Contains(got, absent) {
			t.Errorf("%s was written:\n%s", absent, got)
		}
	}
	for _, f := range []string{"ccshelf.toml", ".github/CODEOWNERS", ".github/workflows/validate.yml", "README.md", ".gitattributes", ".gitignore", "profiles/example.toml.sample"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	if !strings.Contains(read(t, dir, "ccshelf.toml"), "enabled = false") {
		t.Error("ccshelf.toml does not disable the catalog")
	}
	wf := read(t, dir, ".github/workflows/validate.yml")
	if !strings.Contains(wf, "args: lint") || strings.Contains(wf, "compile") || strings.Contains(wf, "catalog build") || strings.Contains(wf, "upload-artifact") {
		t.Errorf("validate.yml is not lint only:\n%s", wf)
	}
	if !strings.Contains(wf, "yorch/ccshelf/action@"+testSHA) || !strings.Contains(wf, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1") {
		t.Errorf("validate.yml lost its SHA pins:\n%s", wf)
	}

	// The result passes the real commands, and the catalog commands refuse.
	lh := newHarness(t, dir)
	write(t, dir, "profiles/dev.toml", "name = \"dev\"\ndescription = \"Everyday work\"\nowner = \"@acme/platform\"\nstatus = \"active\"\n[plugins]\ninclude = [\"tools@acme-tools\"]\n")
	if r := lh.run("lint", "--strict"); r.code != 0 || !strings.Contains(r.out, "no findings") {
		t.Errorf("lint --strict: %d\n%s\n%s", r.code, r.out, r.err)
	}
	for _, cmd := range [][]string{{"compile"}, {"compile", "--check"}, {"catalog", "build"}, {"search", "x"}, {"recommend"}} {
		r := lh.run(cmd...)
		if r.code != 1 || !strings.Contains(r.err, "[catalog] enabled = false in ccshelf.toml: this repo has no marketplace, so there are no bundles/catalog") {
			t.Errorf("%v: code %d\n%s\n%s", cmd, r.code, r.out, r.err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "bundles")); !os.IsNotExist(err) {
		t.Error("compile wrote bundles/ in a profiles-only repo")
	}
	if r := lh.run("doctor"); r.code != 0 {
		t.Errorf("doctor: %d\n%s\n%s", r.code, r.out, r.err)
	}

	// A second run changes nothing, with or without the value flags.
	before := treeSnapshot(t, dir)
	if r := h.run(poArgs(dir, "--example-profile", "--yes")...); r.code != 0 || !strings.Contains(r.out, "nothing to do") {
		t.Errorf("second run: %d\n%s\n%s", r.code, r.out, r.err)
	}
	if got := treeSnapshot(t, dir); got != before {
		t.Errorf("second run changed the directory")
	}
}

func TestCatalogInitProfilesOnlyUsageErrors(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "p")
	for _, tc := range []struct {
		name  string
		extra []string
		want  string
	}{
		{"marketplace name", []string{"--marketplace-name", "acme"}, "--marketplace-name"},
		{"sidecars stub", []string{"--sidecars", "stub"}, "--sidecars stub"},
	} {
		r := h.run(poArgs(dir, append(tc.extra, "--dry-run")...)...)
		if r.code != 2 || !strings.Contains(r.err, tc.want) {
			t.Errorf("%s: code %d\n%s", tc.name, r.code, r.err)
		}
	}
	if r := h.run(poArgs(dir, "--sidecars", "none", "--dry-run")...); r.code != 0 {
		t.Errorf("--sidecars none: code %d\n%s", r.code, r.err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("a usage error or dry run wrote the directory")
	}
}

func TestCatalogInitProfilesOnlyAdopt(t *testing.T) {
	h := newHarness(t, "")
	// An existing marketplace is a conflict.
	dir := legacy(t)
	r := h.run(poArgs(dir, "--dry-run")...)
	if r.code != 2 || !strings.Contains(r.err, ".claude-plugin/marketplace.json already exists") {
		t.Errorf("code %d\n%s", r.code, r.err)
	}
	// A directory with profiles and no marketplace is adopted; nothing existing changes.
	dir2 := filepath.Join(h.cwd, "existing")
	write(t, dir2, "profiles/dev.toml", "name = \"dev\"\n")
	write(t, dir2, "README.md", "# Mine\n")
	r = h.run(poArgs(dir2, "--yes")...)
	if r.code != 0 || !strings.Contains(r.out, "mode: adopt") {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if read(t, dir2, "README.md") != "# Mine\n" {
		t.Error("the README was replaced")
	}
	// Re-running without --profiles-only must not add a marketplace.
	r = h.run("catalog", "init", dir2, "--platform-owners", "@acme/platform", "--marketplace-name", "acme", "--dry-run")
	if r.code != 2 || !strings.Contains(r.err, "--profiles-only") {
		t.Errorf("code %d\n%s", r.code, r.err)
	}
}

func TestCatalogInitProfilesOnlyInteractive(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "acme-profiles")
	// No marketplace name is asked: platform owners, then the confirmation.
	sp := ui.NewScripted("@acme/platform", testSHA, "v0.1.0", true)
	h.env.Prompter = sp
	r := h.run("catalog", "init", dir, "--profiles-only")
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if err := sp.Done(); err != nil {
		t.Error(err)
	}
	if !strings.Contains(r.err, "Equivalent: ccshelf catalog init") || !strings.Contains(r.err, "--profiles-only") || strings.Contains(r.err, "--marketplace-name") {
		t.Errorf("the equivalent command is wrong:\n%s", r.err)
	}
}

func TestCatalogDisabledStrayAndCommands(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	r := h.run(poArgs(dir, "--yes")...)
	if r.code != 0 {
		t.Fatalf("%d\n%s\n%s", r.code, r.out, r.err)
	}
	write(t, dir, ".claude-plugin/marketplace.json", "{")
	lh := newHarness(t, dir)
	r = lh.run("lint")
	if r.code != 0 || !strings.Contains(r.out, "CAT061") {
		t.Errorf("lint with a stray marketplace: %d\n%s\n%s", r.code, r.out, r.err)
	}
	if r := lh.run("lint", "--strict"); r.code != 1 {
		t.Errorf("lint --strict should fail on the CAT061 warning: %d", r.code)
	}
}

func TestProfilesOnlyCLIConflicts(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "p")
	for _, extra := range [][]string{{"--no-config"}, {"--owner", "@acme/x"}} {
		if r := h.run(poArgs(dir, append(extra, "--dry-run")...)...); r.code != 2 {
			t.Errorf("%v: code %d\n%s", extra, r.code, r.err)
		}
	}
	dir2 := filepath.Join(h.cwd, "existing")
	write(t, dir2, "ccshelf.toml", "[lint]\nmax_review_age_days = 90\n")
	r := h.run(poArgs(dir2, "--yes")...)
	if r.code != 2 || !strings.Contains(r.err, "enabled = false") {
		t.Errorf("existing config without the switch: code %d\n%s", r.code, r.err)
	}
	write(t, dir2, "plugins/x/README.md", "x")
	r = h.run(poArgs(dir2, "--yes", "--force")...)
	if r.code != 0 || !strings.Contains(r.out, "ccshelf ignores plugins/") {
		t.Errorf("--force: code %d\n%s\n%s", r.code, r.out, r.err)
	}
}

func TestDoctorJSONExplainsSkippedCatalog(t *testing.T) {
	h := newHarness(t, "")
	dir := filepath.Join(h.cwd, "org")
	if r := h.run(poArgs(dir, "--yes")...); r.code != 0 {
		t.Fatalf("%d\n%s", r.code, r.err)
	}
	r := newHarness(t, dir).run("doctor", "--json")
	if r.code != 0 || !strings.Contains(r.out, "DOC000") || !strings.Contains(r.out, "[catalog] enabled = false") {
		t.Errorf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
}

package orgcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/ui"
)

func TestCatalogBuildExample(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, root)
	dest := filepath.Join(t.TempDir(), "site")
	r := h.run("catalog", "build", "--out", dest)
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	golden(t, "catalog-build.golden.txt", strings.ReplaceAll(r.out, dest, "<out>"))
	for _, f := range []string{"catalog.json", "CATALOG.md", "index.html", "app.js", "style.css"} {
		if st, err := os.Stat(filepath.Join(dest, f)); err != nil || !st.Mode().IsRegular() {
			t.Errorf("%s missing: %v", f, err)
		}
	}
	var cat struct {
		GeneratedAt string `json:"generated_at"`
		Plugins     []struct{ Name string }
		Profiles    []struct{ Name string }
	}
	if err := json.Unmarshal([]byte(read(t, dest, "catalog.json")), &cat); err != nil {
		t.Fatal(err)
	}
	if len(cat.Plugins) != 7 || len(cat.Profiles) != 4 || cat.GeneratedAt != "" {
		t.Errorf("catalog: %d plugins, %d profiles, stamp %q", len(cat.Plugins), len(cat.Profiles), cat.GeneratedAt)
	}
	md := read(t, dest, "CATALOG.md")
	if !strings.Contains(md, "design-kit") {
		t.Errorf("CATALOG.md lacks design-kit:\n%s", md)
	}
	// Nothing was written into the repo.
	if _, err := os.Stat(filepath.Join(root, "CATALOG.md")); !os.IsNotExist(err) {
		t.Error("CATALOG.md leaked into the repo root")
	}
}

func TestCatalogBuildDeterministicAndOptions(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, root)
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	if h.run("catalog", "build", "--out", a).code != 0 || h.run("catalog", "build", "--out", b).code != 0 {
		t.Fatal("build failed")
	}
	for _, f := range []string{"catalog.json", "CATALOG.md", "index.html"} {
		if read(t, a, f) != read(t, b, f) {
			t.Errorf("%s is not reproducible", f)
		}
	}

	c := filepath.Join(t.TempDir(), "c")
	if r := h.run("catalog", "build", "--out", c, "--no-site", "--timestamp"); r.code != 0 {
		t.Fatalf("%d %s", r.code, r.err)
	}
	if _, err := os.Stat(filepath.Join(c, "index.html")); !os.IsNotExist(err) {
		t.Error("--no-site still wrote the site")
	}
	if !strings.Contains(read(t, c, "catalog.json"), `"generated_at": "2026-10-06T12:00:00Z"`) {
		t.Errorf("--timestamp not applied:\n%s", read(t, c, "catalog.json")[:200])
	}

	j := h.run("--json", "catalog", "build", "--out", a)
	kind, data := decode(t, j.out)
	if kind != "catalog-build" || data["plugins"] != float64(7) || data["profiles"] != float64(4) || data["out"] != a {
		t.Errorf("json: %v %v", kind, data)
	}
	if fs, _ := data["files"].([]any); len(fs) != 5 || fs[0] != "CATALOG.md" {
		t.Errorf("files = %v", data["files"])
	}
}

func TestCatalogBuildRelativeOutAndDefault(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, root)
	if r := h.run("catalog", "build", "--out", "rel"); r.code != 0 {
		t.Fatalf("%d %s", r.code, r.err)
	}
	if _, err := os.Stat(filepath.Join(h.cwd, "rel", "catalog.json")); err != nil {
		t.Errorf("relative --out is resolved against the working directory: %v", err)
	}
	if r := h.run("catalog", "build"); r.code != 0 {
		t.Fatalf("%d %s", r.code, r.err)
	}
	if _, err := os.Stat(filepath.Join(h.cwd, "dist", "catalog", "CATALOG.md")); err != nil {
		t.Errorf("default out: %v", err)
	}
}

func TestCatalogBuildRefusals(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, root)
	if r := h.run("catalog", "build", "--out", root); r.code != ui.ExitUsage {
		t.Errorf("--out = repo root: code %d", r.code)
	}
	if r := h.run("catalog", "build", "--out", root+string(filepath.Separator)+"."); r.code != ui.ExitUsage {
		t.Errorf("--out = repo root spelled differently: code %d", r.code)
	}
	// An output name that is a directory is not replaced.
	dest := t.TempDir()
	if err := os.Mkdir(filepath.Join(dest, "catalog.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := h.run("catalog", "build", "--out", dest); r.code != 1 || !strings.Contains(r.err, "not a regular file") {
		t.Errorf("code %d %s", r.code, r.err)
	}
	// --out below a regular file.
	f := filepath.Join(t.TempDir(), "file")
	write(t, filepath.Dir(f), "file", "x")
	if r := h.run("catalog", "build", "--out", filepath.Join(f, "sub")); r.code != 1 {
		t.Errorf("--out under a file: code %d", r.code)
	}
	if r := h.run("catalog", "build", "extra"); r.code != ui.ExitUsage {
		t.Errorf("positional arg: %d", r.code)
	}
	if r := h.run("catalog"); r.code != 0 || !strings.Contains(r.out, "build") {
		t.Errorf("bare catalog should show help: %d %s", r.code, r.out)
	}
}

func TestCatalogBuildSymlinkOut(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, root)
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	r := h.run("catalog", "build", "--out", link)
	if r.code != 1 || !strings.Contains(r.err, "symbolic link") {
		t.Errorf("code %d %s", r.code, r.err)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Error("wrote through the symlink")
	}
}

func TestCatalogBuildLintErrorStillWrites(t *testing.T) {
	root := copyExample(t)
	if err := os.Remove(filepath.Join(root, "catalog", "plugins", "docs-writer.toml")); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, root)
	dest := filepath.Join(t.TempDir(), "o")
	r := h.run("catalog", "build", "--out", dest)
	if r.code != ui.ExitFailure || !strings.Contains(r.err, "CAT010") || !strings.Contains(r.err, "lint found 1 error") {
		t.Errorf("code %d\n%s", r.code, r.err)
	}
	if _, err := os.Stat(filepath.Join(dest, "CATALOG.md")); err != nil {
		t.Errorf("preview was not written: %v", err)
	}
}

func TestSearch(t *testing.T) {
	h := newHarness(t, copyExample(t))
	r := h.run("search", "seo")
	if r.code != 0 {
		t.Fatalf("%d %s", r.code, r.err)
	}
	golden(t, "search-seo.golden.txt", r.out)
	if !strings.Contains(r.out, "seo-tools@acme") {
		t.Errorf("out: %s", r.out)
	}

	j := h.run("--json", "search", "seo", "--limit", "1")
	kind, data := decode(t, j.out)
	ms, _ := data["matches"].([]any)
	if kind != "search" || data["query"] != "seo" || len(ms) != 1 {
		t.Fatalf("json: %v %v", kind, data)
	}
	m := ms[0].(map[string]any)
	if m["name"] != "seo-tools" || m["marketplace"] != "acme" || m["score"].(float64) <= 0 {
		t.Errorf("match = %v", m)
	}

	words := h.run("search", "on-page", "audit")
	if !strings.Contains(words.out, "seo-tools") {
		t.Errorf("multi-word query: %s", words.out)
	}
	none := h.run("search", "zzzz-nothing")
	if none.code != 0 || !strings.Contains(none.out, "no plugin matches") {
		t.Errorf("no match: %d %s", none.code, none.out)
	}
	noneJSON := h.run("--json", "search", "zzzz-nothing")
	if _, d := decode(t, noneJSON.out); d["matches"] == nil {
		t.Error("matches must be [] not null")
	}
	all := h.run("--json", "search", "a", "--limit", "0")
	if _, d := decode(t, all.out); len(d["matches"].([]any)) < 2 {
		t.Errorf("limit 0 should list all: %v", d)
	}
}

func TestSearchUsage(t *testing.T) {
	h := newHarness(t, copyExample(t))
	if r := h.run("search"); r.code != ui.ExitUsage || !strings.Contains(r.err, "query") {
		t.Errorf("no query: %d %s", r.code, r.err)
	}
	if r := h.run("search", "x", "--limit", "-1"); r.code != ui.ExitUsage {
		t.Errorf("negative limit: %d", r.code)
	}
}

func TestSearchHostileText(t *testing.T) {
	root := copyExample(t)
	p := "catalog/plugins/docs-writer.toml"
	write(t, root, p, strings.Replace(read(t, root, p), "status = ", "when_to_use = [\"zzmark \\u001b[31mred\"]\nstatus = ", 1))
	h := newHarness(t, root)
	r := h.run("search", "zzmark")
	if strings.Contains(r.out, "\x1b") {
		t.Errorf("escape sequence reached the terminal: %q", r.out)
	}
}

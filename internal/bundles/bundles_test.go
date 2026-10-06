package bundles

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func mustCompile(t *testing.T, in []Input) *Result {
	t.Helper()
	res, err := Compile(in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCompile(t *testing.T) {
	res := mustCompile(t, []Input{
		{Profile: "sre", Marketplace: "acme", Plugins: []string{"sre-kit@acme", "partner-linter@acme", "sre-kit@acme"}},
		{Profile: "frontend", Marketplace: "acme", Plugins: []string{"docs-writer@acme", "design-kit@acme"}},
	})
	files := res.Files
	if len(files) != 2 || files[0].Path != "bundles/profile-frontend/.claude-plugin/plugin.json" || files[1].Path != "bundles/profile-sre/.claude-plugin/plugin.json" {
		t.Fatalf("paths: %+v", files)
	}
	want := "{\n  \"dependencies\": [\n    \"design-kit\",\n    \"docs-writer\"\n  ],\n  \"name\": \"profile-frontend\"\n}\n"
	if string(files[0].Content) != want {
		t.Fatalf("content:\n%s", files[0].Content)
	}
	if bytes.Contains(files[1].Content, []byte("\r")) || bytes.Contains(files[1].Content, []byte("version")) {
		t.Fatal("must be LF and have no version")
	}
	if !strings.Contains(string(files[1].Content), `"partner-linter",`) {
		t.Fatalf("deps not sorted/deduped:\n%s", files[1].Content)
	}
	if len(res.Bundles) != 2 || res.Bundles[0].Profile != "frontend" || len(res.Bundles[0].CrossMarketplaces) != 0 {
		t.Fatalf("bundles: %+v", res.Bundles)
	}
}

// TestCompileCrossMarketplace pins the exact bytes of the object form (B2).
func TestCompileCrossMarketplace(t *testing.T) {
	res := mustCompile(t, []Input{{Profile: "x", Marketplace: "acme", Plugins: []string{
		"zeta@shared", "own@acme", "alpha@shared", "audit@other", "alpha@acme", "own@acme",
	}}})
	want := `{
  "dependencies": [
    "alpha",
    {
      "marketplace": "shared",
      "name": "alpha"
    },
    {
      "marketplace": "other",
      "name": "audit"
    },
    "own",
    {
      "marketplace": "shared",
      "name": "zeta"
    }
  ],
  "name": "profile-x"
}
`
	if got := string(res.Files[0].Content); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := res.Bundles[0].CrossMarketplaces; len(got) != 2 || got[0] != "other" || got[1] != "shared" {
		t.Fatalf("cross: %v", got)
	}
}

func TestCompileSkipsAbstract(t *testing.T) {
	res := mustCompile(t, []Input{
		{Profile: "base", Marketplace: "acme"},
		{Profile: "a", Marketplace: "acme", Plugins: []string{"p@acme"}},
		{Profile: "abstract", Marketplace: "acme", Plugins: []string{}},
	})
	if len(res.Files) != 1 || len(res.Skipped) != 2 || res.Skipped[0] != "abstract" || res.Skipped[1] != "base" {
		t.Fatalf("%+v", res)
	}
}

func TestCompileErrors(t *testing.T) {
	p := []string{"a@b"}
	tests := []struct {
		name string
		in   []Input
		want string
	}{
		{"bad profile upper", []Input{{Profile: "Front", Marketplace: "b", Plugins: p}}, "invalid profile name"},
		{"bad profile dots", []Input{{Profile: "../x", Marketplace: "b", Plugins: p}}, "invalid profile name"},
		{"empty profile", []Input{{Profile: "", Marketplace: "b", Plugins: p}}, "invalid profile name"},
		{"long profile", []Input{{Profile: strings.Repeat("a", 64), Marketplace: "b", Plugins: p}}, "invalid profile name"},
		{"no marketplace", []Input{{Profile: "x", Plugins: p}}, "hosting marketplace"},
		{"bad marketplace", []Input{{Profile: "x", Marketplace: "a/b", Plugins: p}}, "hosting marketplace"},
		{"no marketplace in id", []Input{{Profile: "x", Marketplace: "b", Plugins: []string{"plain"}}}, "invalid plugin id"},
		{"two at", []Input{{Profile: "x", Marketplace: "b", Plugins: []string{"a@b@c"}}}, "invalid plugin id"},
		{"bad name", []Input{{Profile: "x", Marketplace: "b", Plugins: []string{"a/b@m"}}}, "invalid plugin id"},
		{"empty part", []Input{{Profile: "x", Marketplace: "b", Plugins: []string{"@m"}}}, "invalid plugin id"},
		{"duplicate profile", []Input{{Profile: "x", Marketplace: "b", Plugins: p}, {Profile: "x", Marketplace: "b", Plugins: p}}, "more than once"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Compile(tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// templateInputs derives the compile inputs from the real starter profiles:
// plugins.include (minus plugins.exclude) of the profile and its extends
// chain, parsed directly so this test does not depend on the resolver.
func templateInputs(t *testing.T, base string) []Input {
	t.Helper()
	type pf struct {
		Extends []string `toml:"extends"`
		Plugins struct {
			Include []string `toml:"include"`
			Exclude []string `toml:"exclude"`
		} `toml:"plugins"`
	}
	dir := filepath.Join(base, "profiles")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	load := map[string]pf{}
	for _, e := range ents {
		n, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var p pf
		if err := toml.Unmarshal(b, &p); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		load[n] = p
	}
	var resolve func(n string, depth int) []string
	resolve = func(n string, depth int) []string {
		if depth > 8 {
			t.Fatalf("extends too deep at %s", n)
		}
		p := load[n]
		var out []string
		for _, par := range p.Extends {
			out = append(out, resolve(par, depth+1)...)
		}
		out = append(out, p.Plugins.Include...)
		var kept []string
		for _, id := range out {
			drop := false
			for _, x := range p.Plugins.Exclude {
				drop = drop || x == id
			}
			if !drop {
				kept = append(kept, id)
			}
		}
		return kept
	}
	var in []Input
	for n := range load {
		in = append(in, Input{Profile: n, Marketplace: "acme", Plugins: resolve(n, 0)})
	}
	return in
}

// TestMatchesTemplate pins the format to the committed starter template. It
// compares bytes exactly (no CRLF normalization) and checks the whole tree.
func TestMatchesTemplate(t *testing.T) {
	base := filepath.Join("..", "..", "examples", "org-data-repo")
	if _, err := os.Stat(filepath.Join(base, "bundles")); err != nil {
		t.Skip("starter template not present")
	}
	res := mustCompile(t, templateInputs(t, base))
	if len(res.Skipped) != 1 || res.Skipped[0] != "base" || len(res.Files) < 3 {
		t.Fatalf("expected base skipped and at least three bundles: %+v", res)
	}
	for _, f := range res.Files {
		got, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, f.Content) {
			t.Errorf("%s differs from the template:\n%q\nvs\n%q", f.Path, got, f.Content)
		}
	}
	d, err := Check(base, res.Files)
	if err != nil {
		t.Fatal(err)
	}
	if d.HasDrift() {
		t.Errorf("template drifts from generated output: %+v", d)
	}
}

func sample(t *testing.T) []File {
	t.Helper()
	return mustCompile(t, []Input{
		{Profile: "a", Marketplace: "m", Plugins: []string{"x@m"}},
		{Profile: "b", Marketplace: "m", Plugins: []string{"y@m", "z@m"}},
	}).Files
}

func TestWriteAndCheck(t *testing.T) {
	root := t.TempDir()
	files := sample(t)

	d, err := Check(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if !d.HasDrift() || len(d.Missing) != 2 || !strings.Contains(d.Diff, "--- /dev/null") {
		t.Fatalf("expected missing: %+v", d)
	}

	if err := Write(root, files); err != nil {
		t.Fatal(err)
	}
	if err := Write(root, files); err != nil { // idempotent
		t.Fatal(err)
	}
	d, err = Check(root, files)
	if err != nil || d.HasDrift() {
		t.Fatalf("clean check: %+v %v", d, err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "bundles", "profile-a", ".claude-plugin", "plugin.json"))
	if !bytes.Equal(got, files[0].Content) {
		t.Fatal("content mismatch")
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(root, "bundles", "profile-a", ".claude-plugin", "plugin.json"))
		if fi.Mode().Perm() != 0o644 {
			t.Fatalf("mode %v", fi.Mode().Perm())
		}
	}
	// No temporary files remain.
	ents, _ := os.ReadDir(filepath.Join(root, "bundles", "profile-a", ".claude-plugin"))
	if len(ents) != 1 {
		t.Fatalf("leftovers: %v", ents)
	}
}

func TestCheckModified(t *testing.T) {
	root := t.TempDir()
	files := sample(t)
	if err := Write(root, files); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "bundles", "profile-b", ".claude-plugin", "plugin.json")
	if err := os.WriteFile(p, bytes.Replace(files[1].Content, []byte("y"), []byte("w"), 1), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Check(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Modified) != 1 || d.Modified[0] != files[1].Path {
		t.Fatalf("%+v", d)
	}
	for _, s := range []string{"-    \"w\",", "+    \"y\","} {
		if !strings.Contains(d.Diff, s) {
			t.Errorf("diff lacks %q:\n%s", s, d.Diff)
		}
	}
}

func TestCheckCRLFIsDrift(t *testing.T) {
	root := t.TempDir()
	files := sample(t)
	if err := Write(root, files); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, filepath.FromSlash(files[0].Path))
	crlf := bytes.ReplaceAll(files[0].Content, []byte("\n"), []byte("\r\n"))
	if err := os.WriteFile(p, crlf, 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Check(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Modified) != 1 || !strings.Contains(d.Diff, "line endings differ") {
		t.Fatalf("CRLF must be drift: %+v", d)
	}
	// Write repairs it to LF.
	if err := Write(root, files); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if bytes.Contains(got, []byte("\r")) {
		t.Fatal("CRLF survived Write")
	}
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// TestBundlesTreeIsGenerated covers B1: everything below bundles/ that is not
// a wanted manifest is drift, generated-looking or not, and prune removes
// exactly that.
func TestBundlesTreeIsGenerated(t *testing.T) {
	root := t.TempDir()
	files := sample(t)
	if err := Write(root, files); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(root, "bundles")
	old := mustCompile(t, []Input{{Profile: "old", Marketplace: "m", Plugins: []string{"q@m"}}})
	if err := Write(root, old.Files); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(b, "profile-hand", ".claude-plugin", "plugin.json"), `{"name":"profile-hand"}`)
	writeFile(t, filepath.Join(b, "profile-a", "hooks", "hooks.json"), "{}")
	writeFile(t, filepath.Join(b, "profile-a", ".mcp.json"), "{}")
	writeFile(t, filepath.Join(b, "profile-b", ".claude-plugin", "README.md"), "x")
	writeFile(t, filepath.Join(b, "README.md"), "keep?")
	writeFile(t, filepath.Join(b, "other", "x"), "x")

	d, err := Check(root, files)
	if err != nil {
		t.Fatal(err)
	}
	wantStale := []string{
		"bundles/profile-hand/.claude-plugin", "bundles/profile-hand/.claude-plugin/plugin.json",
		"bundles/profile-old/.claude-plugin", "bundles/profile-old/.claude-plugin/plugin.json",
	}
	wantExtra := []string{
		"bundles/README.md", "bundles/other", "bundles/other/x",
		"bundles/profile-a/.mcp.json", "bundles/profile-a/hooks", "bundles/profile-a/hooks/hooks.json",
		"bundles/profile-b/.claude-plugin/README.md",
	}
	wantStale = append(wantStale[:0:0], append([]string{"bundles/profile-hand", "bundles/profile-old"}, wantStale...)...)
	sort.Strings(wantStale)
	if !reflect.DeepEqual(d.Stale, wantStale) || !reflect.DeepEqual(d.Extra, wantExtra) {
		t.Fatalf("stale %v\nextra %v", d.Stale, d.Extra)
	}
	for _, s := range []string{"bundles/profile-a/hooks/hooks.json", "bundles/profile-hand/.claude-plugin/plugin.json", "--- a/bundles/README.md"} {
		if !strings.Contains(d.Diff, s) {
			t.Errorf("diff lacks %s:\n%s", s, d.Diff)
		}
	}

	if err := Write(root, files); err != nil { // no prune requested
		t.Fatal(err)
	}
	if !exists(filepath.Join(b, "profile-old")) || !exists(filepath.Join(b, "README.md")) {
		t.Fatal("pruned without PruneStale")
	}
	if err := Write(root, files, WriteOptions{PruneStale: true}); err != nil {
		t.Fatal(err)
	}
	d, err = Check(root, files)
	if err != nil || d.HasDrift() {
		t.Fatalf("after prune: %+v %v", d, err)
	}
	for _, f := range files {
		if !exists(filepath.Join(root, filepath.FromSlash(f.Path))) {
			t.Fatalf("prune removed wanted %s", f.Path)
		}
	}
}

// TestPruneRefusesSymlink: a symlink or special file among the entries to
// remove makes prune refuse before deleting anything.
func TestPruneRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	files := sample(t)
	if err := Write(root, files); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(root, "bundles")
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep"), "k")
	writeFile(t, filepath.Join(b, "profile-old", ".claude-plugin", "plugin.json"), "{}")
	writeFile(t, filepath.Join(b, "zzz-file"), "x")
	if err := os.Symlink(outside, filepath.Join(b, "profile-old", "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	d, err := Check(root, files)
	if err != nil || !contains(d.Stale, "bundles/profile-old/link") {
		t.Fatalf("symlink must be reported: %+v %v", d, err)
	}
	if err := Write(root, files, WriteOptions{PruneStale: true}); err == nil {
		t.Fatal("prune must refuse a tree holding a symlink")
	}
	for _, p := range []string{
		filepath.Join(b, "profile-old", ".claude-plugin", "plugin.json"),
		filepath.Join(b, "zzz-file"),
		filepath.Join(outside, "keep"),
	} {
		if !exists(p) {
			t.Fatalf("refused prune still removed %s", p)
		}
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestPruneRefusesSymlinkedBundles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "profile-x", "f"), "x")
	if err := os.Symlink(outside, filepath.Join(root, "bundles")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := prune(root, sample(t)); err == nil {
		t.Fatal("prune followed a symlinked bundles directory")
	}
	if !exists(filepath.Join(outside, "profile-x", "f")) {
		t.Fatal("removed through the symlink")
	}
}

func TestHostile(t *testing.T) {
	good := sample(t)
	root := t.TempDir()
	for _, p := range []string{
		"bundles/../evil/plugin.json", "../x", "/abs/x", "bundles", "other/x", "bundles//x",
		"bundles/./x", `bundles\x`, "bundles/c:/x", "", "bundles/a/../../x",
	} {
		if err := Write(root, []File{{Path: p, Content: []byte("x")}}); err == nil {
			t.Errorf("Write accepted %q", p)
		}
		if _, err := Check(root, []File{{Path: p, Content: []byte("x")}}); err == nil {
			t.Errorf("Check accepted %q", p)
		}
	}
	if err := Write(root, []File{good[0], good[0]}); err == nil {
		t.Error("duplicate accepted")
	}
	if err := Write(filepath.Join(root, "missing"), good); err == nil {
		t.Error("missing root accepted")
	}
	f := filepath.Join(root, "file")
	_ = os.WriteFile(f, nil, 0o644)
	if err := Write(f, good); err == nil {
		t.Error("file root accepted")
	}
}

func TestSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "bundles")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	files := sample(t)
	if err := Write(root, files); err == nil {
		t.Fatal("Write followed a symlinked bundles directory")
	}
	if _, err := Check(root, files); err == nil {
		t.Fatal("Check followed a symlinked bundles directory")
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Fatalf("wrote outside the root: %v", ents)
	}

	// Symlinked profile directory and symlinked target file.
	root2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root2, "bundles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root2, "bundles", "profile-a")); err != nil {
		t.Fatal(err)
	}
	if err := Write(root2, files); err == nil {
		t.Fatal("Write followed a symlinked profile directory")
	}
	if err := os.Remove(filepath.Join(root2, "bundles", "profile-a")); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root2, "bundles", "profile-a", ".claude-plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret")
	_ = os.WriteFile(secret, []byte("s"), 0o644)
	if err := os.Symlink(secret, filepath.Join(dir, "plugin.json")); err != nil {
		t.Fatal(err)
	}
	if err := Write(root2, files); err == nil {
		t.Fatal("Write replaced a symlinked file")
	}
	d, err := Check(root2, files)
	if err != nil || len(d.Modified) != 1 {
		t.Fatalf("symlinked file must be modified: %+v %v", d, err)
	}
	if b, _ := os.ReadFile(secret); string(b) != "s" {
		t.Fatal("symlink target was modified")
	}
}

func TestRootSymlinkAllowed(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := Write(link, sample(t)); err != nil {
		t.Fatalf("a symlinked root is the caller's choice: %v", err)
	}
}

func TestDiffCap(t *testing.T) {
	root := t.TempDir()
	files := []File{{Path: "bundles/profile-a/.claude-plugin/plugin.json", Content: []byte(strings.Repeat("line\n", 100000))}}
	d, err := Check(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Diff) > maxDiff+100 || !strings.Contains(d.Diff, "truncated") {
		t.Fatalf("diff not capped: %d", len(d.Diff))
	}
}

func TestLinesAndDiffEdges(t *testing.T) {
	if got := unified("p", nil, nil, ""); !strings.Contains(got, "@@ -0,0 +0,0 @@") {
		t.Fatal(got)
	}
	big := make([]string, 2001)
	if out := lcsDiff(big, nil); len(out) != 2001 {
		t.Fatalf("fallback diff: %d", len(out))
	}
	if got := lines([]byte("a\r\nb\n")); len(got) != 2 || got[0] != `a\r` {
		t.Fatalf("%q", got)
	}
}

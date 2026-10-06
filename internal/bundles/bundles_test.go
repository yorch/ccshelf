package bundles

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompile(t *testing.T) {
	files, err := Compile([]Input{
		{Profile: "sre", Plugins: []string{"sre-kit@acme", "partner-linter@acme", "sre-kit@acme"}},
		{Profile: "frontend", Plugins: []string{"docs-writer@acme", "design-kit@acme"}},
	})
	if err != nil {
		t.Fatal(err)
	}
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
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name string
		in   []Input
		want string
	}{
		{"bad profile upper", []Input{{Profile: "Front", Plugins: []string{"a@b"}}}, "invalid profile name"},
		{"bad profile dots", []Input{{Profile: "../x", Plugins: []string{"a@b"}}}, "invalid profile name"},
		{"empty profile", []Input{{Profile: "", Plugins: []string{"a@b"}}}, "invalid profile name"},
		{"long profile", []Input{{Profile: strings.Repeat("a", 64), Plugins: []string{"a@b"}}}, "invalid profile name"},
		{"no plugins", []Input{{Profile: "x"}}, "no plugins"},
		{"no marketplace", []Input{{Profile: "x", Plugins: []string{"plain"}}}, "invalid plugin id"},
		{"two at", []Input{{Profile: "x", Plugins: []string{"a@b@c"}}}, "invalid plugin id"},
		{"bad name", []Input{{Profile: "x", Plugins: []string{"a/b@m"}}}, "invalid plugin id"},
		{"empty part", []Input{{Profile: "x", Plugins: []string{"@m"}}}, "invalid plugin id"},
		{"ambiguous", []Input{{Profile: "x", Plugins: []string{"a@m1", "a@m2"}}}, "two marketplaces"},
		{"duplicate profile", []Input{{Profile: "x", Plugins: []string{"a@m"}}, {Profile: "x", Plugins: []string{"a@m"}}}, "more than once"},
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

// TestMatchesTemplate pins the format to the committed starter template.
func TestMatchesTemplate(t *testing.T) {
	base := filepath.Join("..", "..", "examples", "org-data-repo")
	if _, err := os.Stat(filepath.Join(base, "bundles")); err != nil {
		t.Skip("starter template not present")
	}
	files, err := Compile([]Input{
		{Profile: "frontend", Plugins: []string{"design-kit@acme", "docs-writer@acme"}},
		{Profile: "sre", Plugins: []string{"sre-kit@acme", "partner-linter@acme"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(base, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatal(err)
		}
		// Normalize in case a Windows checkout converted line endings.
		got = bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
		if !bytes.Equal(got, f.Content) {
			t.Errorf("%s differs from the template:\n%s\nvs\n%s", f.Path, got, f.Content)
		}
	}
}

func sample(t *testing.T) []File {
	t.Helper()
	files, err := Compile([]Input{
		{Profile: "a", Plugins: []string{"x@m"}},
		{Profile: "b", Plugins: []string{"y@m", "z@m"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
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

func TestStalePrune(t *testing.T) {
	root := t.TempDir()
	files := sample(t)
	if err := Write(root, files); err != nil {
		t.Fatal(err)
	}
	// Generated bundle without a profile, a hand-written one, a bundle with
	// an extra file, and a non-bundle directory.
	old, err := Compile([]Input{{Profile: "old", Plugins: []string{"q@m"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(root, old); err != nil {
		t.Fatal(err)
	}
	hand := filepath.Join(root, "bundles", "profile-hand", ".claude-plugin")
	extra := filepath.Join(root, "bundles", "profile-extra", ".claude-plugin")
	for _, d := range []string{hand, extra, filepath.Join(root, "bundles", "other")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(hand, "plugin.json"), []byte(`{"name":"profile-hand","description":"mine"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	extraOld, _ := Compile([]Input{{Profile: "extra", Plugins: []string{"q@m"}}})
	if err := os.WriteFile(filepath.Join(extra, "plugin.json"), extraOld[0].Content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extra, "..", "README.md"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := Check(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Stale) != 1 || d.Stale[0] != "bundles/profile-old/.claude-plugin/plugin.json" {
		t.Fatalf("stale: %+v", d.Stale)
	}

	if err := Write(root, files); err != nil { // no prune requested
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "bundles", "profile-old")); err != nil {
		t.Fatal("pruned without PruneStale")
	}
	if err := Write(root, files, WriteOptions{PruneStale: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "bundles", "profile-old")); !os.IsNotExist(err) {
		t.Fatal("stale generated bundle was not removed")
	}
	for _, keep := range []string{filepath.Join(hand, "plugin.json"), filepath.Join(extra, "..", "README.md"), filepath.Join(root, "bundles", "other")} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("must keep %s: %v", keep, err)
		}
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

func TestIsGeneratedRejects(t *testing.T) {
	good, _ := render("profile-a", []string{"x"})
	for name, c := range map[string][]byte{
		"good":    good,
		"version": []byte("{\n  \"dependencies\": [\n    \"x\"\n  ],\n  \"name\": \"profile-a\",\n  \"version\": \"1\"\n}\n"),
		"crlf":    bytes.ReplaceAll(good, []byte("\n"), []byte("\r\n")),
		"name":    []byte("{\n  \"dependencies\": [\n    \"x\"\n  ],\n  \"name\": \"profile-z\"\n}\n"),
		"empty":   []byte("{\n  \"dependencies\": [],\n  \"name\": \"profile-a\"\n}\n"),
		"junk":    []byte("nope"),
	} {
		if got, want := isGenerated("profile-a", c), name == "good"; got != want {
			t.Errorf("%s: isGenerated = %v", name, got)
		}
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

package recommend

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// repo creates a temp repository (with a .git directory) and returns its root.
func repo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, root, ".git/config", "[core]\n")
	for rel, c := range files {
		write(t, root, rel, c)
	}
	return root
}

func terraformRepo(t *testing.T) string {
	return repo(t, map[string]string{
		"infra/main.tf":          "resource {}",
		"infra/prod/vars.tfvars": "a = 1",
		"Makefile":               "all:",
		"README.md":              "x",
	})
}

func reactRepo(t *testing.T) string {
	return repo(t, map[string]string{
		"package.json":      `{"dependencies":{"react":"^18","react-dom":"^18"},"devDependencies":{"jest":"1"}}`,
		"src/App.tsx":       "x",
		"src/index.ts":      "x",
		"tsconfig.json":     "{}",
		"node_modules/x.js": "ignored",
	})
}

func goRepo(t *testing.T) string {
	return repo(t, map[string]string{
		"go.mod":         "module example.com/x\n\ngo 1.27\n",
		"cmd/x/main.go":  "package main",
		"internal/a.go":  "package a",
		"vendor/v/v.go":  "ignored",
		"Dockerfile":     "FROM scratch",
		"docs/guide.txt": "x",
	})
}

func TestCollectTerraformRepo(t *testing.T) {
	root := terraformRepo(t)
	sig, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{"Makefile", "README.md", "infra/main.tf", "infra/prod/vars.tfvars"}
	if !reflect.DeepEqual(sig.Files, wantFiles) {
		t.Errorf("Files = %v, want %v", sig.Files, wantFiles)
	}
	if !reflect.DeepEqual(sig.CLIs, []string{"make", "terraform"}) {
		t.Errorf("CLIs = %v", sig.CLIs)
	}
	if sig.RepoRoot == "" || !sameDir(sig.RepoRoot, root) {
		t.Errorf("RepoRoot = %q, want %q", sig.RepoRoot, root)
	}
	if len(sig.Hosts) != 0 || sig.Truncated {
		t.Errorf("Hosts=%v Truncated=%v", sig.Hosts, sig.Truncated)
	}
}

func sameDir(a, b string) bool {
	x, e1 := filepath.EvalSymlinks(a)
	y, e2 := filepath.EvalSymlinks(b)
	return e1 == nil && e2 == nil && x == y
}

func TestCollectReactAndGoRepos(t *testing.T) {
	sig, err := Collect(context.Background(), reactRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range sig.Files {
		if strings.HasPrefix(f, "node_modules") {
			t.Errorf("node_modules not skipped: %s", f)
		}
	}
	if !strings.Contains(string(sig.ManifestFiles["package.json"]), "react") {
		t.Errorf("package.json not collected: %v", sig.ManifestFiles)
	}
	if !reflect.DeepEqual(sig.CLIs, []string{"node", "npm"}) {
		t.Errorf("CLIs = %v", sig.CLIs)
	}

	sig, err = Collect(context.Background(), goRepo(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range sig.Files {
		if strings.HasPrefix(f, "vendor") {
			t.Errorf("vendor not skipped: %s", f)
		}
	}
	if !reflect.DeepEqual(sig.CLIs, []string{"docker", "go"}) {
		t.Errorf("CLIs = %v", sig.CLIs)
	}
	if _, ok := sig.ManifestFiles["go.mod"]; !ok {
		t.Error("go.mod missing")
	}
}

func TestCollectCaps(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < MaxFiles+50; i++ {
		write(t, root, filepath.Join("d", "f"+strings.Repeat("x", 3)+itoa(i)+".txt"), "")
	}
	write(t, root, "package.json", strings.Repeat("a", MaxManifestSize+1000))
	sig, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig.Files) != MaxFiles || !sig.Truncated {
		t.Errorf("files=%d truncated=%v", len(sig.Files), sig.Truncated)
	}
	if len(sig.ManifestFiles["package.json"]) != MaxManifestSize {
		t.Errorf("manifest size = %d", len(sig.ManifestFiles["package.json"]))
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{digits[i%10]}, b...)
	}
	return string(b)
}

func TestCollectDepthLimit(t *testing.T) {
	root := t.TempDir()
	deep := strings.Repeat("d/", MaxDepth+2) + "deep.txt"
	write(t, root, deep, "")
	write(t, root, "top.txt", "")
	sig, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sig.Files, []string{"top.txt"}) || !sig.Truncated {
		t.Errorf("files=%v truncated=%v", sig.Files, sig.Truncated)
	}
}

func TestCollectSkipsSymlinksAndHostileNames(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, outside, "secret.tf", "x")
	write(t, root, "ok.txt", "")
	linked := true
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		linked = false
	}
	if err := os.Symlink(filepath.Join(outside, "secret.tf"), filepath.Join(root, "go.mod")); err != nil {
		linked = false
	}
	hostile := []string{"bad\x1bname.txt", "bidi\u202ename.txt", "\xff\xfe.txt"}
	created := 0
	for _, n := range hostile {
		if err := os.WriteFile(filepath.Join(root, n), nil, 0o600); err == nil {
			created++
		}
	}
	sig, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sig.Files, []string{"ok.txt"}) {
		t.Errorf("Files = %q (symlinks=%v hostile=%d)", sig.Files, linked, created)
	}
	if len(sig.ManifestFiles) != 0 {
		t.Errorf("a symlinked manifest was read: %v", sig.ManifestFiles)
	}
}

func TestCollectPermissionErrorsSkipped(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions are not enforced here")
	}
	root := t.TempDir()
	write(t, root, "ok.txt", "")
	write(t, root, "locked/secret.txt", "")
	if err := os.Chmod(filepath.Join(root, "locked"), 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(filepath.Join(root, "locked"), 0o700) }()
	sig, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sig.Files, []string{"ok.txt"}) {
		t.Errorf("Files = %v", sig.Files)
	}
}

func TestCollectErrors(t *testing.T) {
	if _, err := Collect(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing dir: want error")
	}
	root := t.TempDir()
	write(t, root, "f.txt", "")
	if _, err := Collect(context.Background(), filepath.Join(root, "f.txt")); err == nil {
		t.Error("file: want error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Collect(ctx, root); err == nil {
		t.Error("canceled: want error")
	}
}

func TestCollectNoRepoRoot(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.txt", "")
	sig, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	// A temp dir normally has no .git ancestor; accept either way but never panic.
	_ = sig.RepoRoot
}

func TestSafeName(t *testing.T) {
	for name, want := range map[string]bool{
		"ok.txt": true, "": false, "a\\b": false, "a\nb": false, "a\u202eb": false,
		"\xff": false, strings.Repeat("a", 256): false, "日本語.md": true,
	} {
		if safeName(name) != want {
			t.Errorf("safeName(%q) != %v", name, want)
		}
	}
}

func TestCollectManyEmptyDirsIsBounded(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < MaxDirs+200; i++ {
		if err := os.Mkdir(filepath.Join(root, "e"+itoa(i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sig, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !sig.Truncated {
		t.Error("a tree with more than MaxDirs directories must report Truncated")
	}
}

func TestCollectStopsWalkingAtFileCap(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < MaxFiles; i++ {
		write(t, root, "f"+itoa(i)+".txt", "")
	}
	write(t, root, "sub/go.mod", "module x\n")
	sig, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig.Files) != MaxFiles || !sig.Truncated {
		t.Errorf("files=%d truncated=%v", len(sig.Files), sig.Truncated)
	}
	if _, ok := sig.ManifestFiles["sub/go.mod"]; ok {
		t.Error("the walk continued past the file cap")
	}
}

func TestCollectManifestDepthLimit(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a/b/go.mod", "module deep2\n")     // depth 2: read
	write(t, root, "a/b/c/go.mod", "module deep3\n")   // depth 3: not read
	write(t, root, "a/b/c/d/go.mod", "module deep4\n") // depth 4: not read
	sig, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sig.ManifestFiles["a/b/go.mod"]; !ok {
		t.Errorf("a manifest two levels down must be read: %v", sig.ManifestFiles)
	}
	for _, rel := range []string{"a/b/c/go.mod", "a/b/c/d/go.mod"} {
		if _, ok := sig.ManifestFiles[rel]; ok {
			t.Errorf("manifest %s is deeper than the limit and must not be read", rel)
		}
	}
	if len(sig.Files) != 3 {
		t.Errorf("files = %v", sig.Files)
	}
}

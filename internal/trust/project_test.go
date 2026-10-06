package trust

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/config"
)

func newProjects(t *testing.T) *ProjectStore {
	t.Helper()
	ps, err := OpenProjects(filepath.Join(t.TempDir(), "cfg", "project-trust.json"))
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	put(t, root, ".ccshelf/profiles/p.toml", "name = \"p\"\ndescription = \"d\"\n")
	put(t, root, ".ccshelf/notes.txt", "hello")
	return root
}

func TestProjectTrustLifecycle(t *testing.T) {
	ps := newProjects(t)
	root := repo(t)
	if ok, err := ps.IsTrusted(root); ok || err != nil {
		t.Fatalf("before trust: %v %v", ok, err)
	}
	if err := ps.Trust(root); err != nil {
		t.Fatal(err)
	}
	if ok, err := ps.IsTrusted(root); !ok || err != nil {
		t.Fatalf("after trust: %v %v", ok, err)
	}
	recs := ps.List()
	real, _ := filepath.EvalSymlinks(root)
	if len(recs) != 1 || recs[0].Path != real || len(recs[0].ContentHash) != 64 || recs[0].TrustedAt.IsZero() {
		t.Fatalf("records = %+v", recs)
	}
	// persists
	ps2, err := OpenProjects(ps.path)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := ps2.IsTrusted(root); !ok {
		t.Error("trust must persist")
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(ps.path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v %v", fi.Mode().Perm(), err)
		}
	}
	// re-trust replaces
	put(t, root, ".ccshelf/notes.txt", "changed")
	if ok, _ := ps.IsTrusted(root); ok {
		t.Error("editing a file must revoke trust")
	}
	if err := ps.Trust(root); err != nil {
		t.Fatal(err)
	}
	if len(ps.List()) != 1 {
		t.Errorf("re-trust must replace: %+v", ps.List())
	}
	if ok, _ := ps.IsTrusted(root); !ok {
		t.Error("re-trusted")
	}
	if err := ps.Revoke(root); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ps.IsTrusted(root); ok {
		t.Error("revoked")
	}
	if err := ps.Revoke(root); err == nil {
		t.Error("second revoke must report not found")
	}
}

func TestProjectEditsRevokeTrust(t *testing.T) {
	edits := map[string]func(t *testing.T, root string){
		"edit": func(t *testing.T, r string) {
			put(t, r, ".ccshelf/profiles/p.toml", "name = \"p\"\ndescription = \"e\"\n")
		},
		"add file": func(t *testing.T, r string) { put(t, r, ".ccshelf/profiles/q.toml", "x") },
		"add dir":  func(t *testing.T, r string) { _ = os.MkdirAll(filepath.Join(r, ".ccshelf", "empty"), 0o700) },
		"delete":   func(t *testing.T, r string) { _ = os.Remove(filepath.Join(r, ".ccshelf", "notes.txt")) },
		"rename": func(t *testing.T, r string) {
			_ = os.Rename(filepath.Join(r, ".ccshelf", "notes.txt"), filepath.Join(r, ".ccshelf", "n.txt"))
		},
		"swap bytes": func(t *testing.T, r string) { put(t, r, ".ccshelf/notes.txt", "hellp") },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			ps := newProjects(t)
			root := repo(t)
			if err := ps.Trust(root); err != nil {
				t.Fatal(err)
			}
			edit(t, root)
			if ok, err := ps.IsTrusted(root); ok || err != nil {
				t.Fatalf("trusted = %v, err = %v", ok, err)
			}
		})
	}
}

func TestProjectHashIsStableAndPositionSensitive(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, r := range []string{a, b} {
		put(t, r, ".ccshelf/a", "1")
		put(t, r, ".ccshelf/b", "2")
	}
	ha, err := HashProjectFolder(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, _ := HashProjectFolder(b)
	if ha != hb {
		t.Error("same content must hash the same regardless of creation order or location")
	}
	c := t.TempDir()
	put(t, c, ".ccshelf/a", "2")
	put(t, c, ".ccshelf/b", "1")
	if hc, _ := HashProjectFolder(c); hc == ha {
		t.Error("swapping contents must change the hash")
	}
	d := t.TempDir()
	put(t, d, ".ccshelf/a", "12")
	put(t, d, ".ccshelf/b", "")
	if hd, _ := HashProjectFolder(d); hd == ha {
		t.Error("length prefixes must prevent concatenation collisions")
	}
}

func TestProjectNoFolderAndBadRoots(t *testing.T) {
	ps := newProjects(t)
	root := t.TempDir()
	if err := ps.Trust(root); err == nil || !strings.Contains(err.Error(), "no .ccshelf folder") {
		t.Errorf("Trust without folder: %v", err)
	}
	if ok, err := ps.IsTrusted(root); ok || err != nil {
		t.Errorf("IsTrusted without folder: %v %v", ok, err)
	}
	if err := ps.Trust(filepath.Join(root, "missing")); err == nil {
		t.Error("missing root must fail")
	}
	if ok, err := ps.IsTrusted(filepath.Join(root, "missing")); ok || err != nil {
		t.Errorf("IsTrusted missing root: %v %v", ok, err)
	}
	f := filepath.Join(root, "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ps.Trust(f); err == nil {
		t.Error("a file root must fail")
	}
}

func TestProjectFolderDeletedAfterTrust(t *testing.T) {
	ps := newProjects(t)
	root := repo(t)
	if err := ps.Trust(root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".ccshelf")); err != nil {
		t.Fatal(err)
	}
	if ok, err := ps.IsTrusted(root); ok || err != nil {
		t.Errorf("%v %v", ok, err)
	}
	// a root that disappeared can still be revoked
	gone := repo(t)
	if err := ps.Trust(gone); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	if err := ps.Revoke(gone); err != nil {
		t.Errorf("revoke of a vanished root: %v", err)
	}
}

func TestProjectSymlinksRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	ps := newProjects(t)
	root := repo(t)
	if err := os.Symlink("/etc/passwd", filepath.Join(root, ".ccshelf", "evil")); err != nil {
		t.Fatal(err)
	}
	if err := ps.Trust(root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("Trust: %v", err)
	}
	// a trusted folder that later gains a symlink is an error, not trust
	root2 := repo(t)
	if err := ps.Trust(root2); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root2, ".ccshelf", "evil")); err != nil {
		t.Fatal(err)
	}
	if ok, err := ps.IsTrusted(root2); ok || err == nil {
		t.Errorf("IsTrusted = %v, %v", ok, err)
	}
	// .ccshelf itself a symlink
	root3 := t.TempDir()
	if err := os.Symlink(repo(t), filepath.Join(root3, ".ccshelf")); err != nil {
		t.Fatal(err)
	}
	if err := ps.Trust(root3); err == nil {
		t.Error("symlinked .ccshelf must be refused")
	}
	// trusting through a symlinked root records the real path
	real := repo(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := ps.Trust(link); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ps.IsTrusted(real); !ok {
		t.Error("the real path must be trusted")
	}
	if ok, _ := ps.IsTrusted(link); !ok {
		t.Error("the link resolves to the trusted real path")
	}
}

func TestProjectHashLimits(t *testing.T) {
	root := t.TempDir()
	put(t, root, ".ccshelf/big", strings.Repeat("a", maxProjectFile+1))
	if _, err := HashProjectFolder(root); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("big file: %v", err)
	}
	root2 := t.TempDir()
	for i := 0; i < 9; i++ {
		put(t, root2, ".ccshelf/f"+string(rune('a'+i)), strings.Repeat("a", maxProjectFile))
	}
	if _, err := HashProjectFolder(root2); err == nil || !strings.Contains(err.Error(), "larger") {
		t.Errorf("total size: %v", err)
	}
	root3 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root3, ".ccshelf"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= maxProjectFiles; i++ {
		if err := os.WriteFile(filepath.Join(root3, ".ccshelf", fmt.Sprintf("f%05d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := HashProjectFolder(root3); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("file count: %v", err)
	}
}

func TestOpenProjectsRejectsBadFiles(t *testing.T) {
	good := strings.Repeat("a", 64)
	tests := []struct{ name, body, want string }{
		{"not json", "x", "parsing"},
		{"unknown key", `{"version":1,"projects":[],"x":1}`, "unknown field"},
		{"version", `{"version":3,"projects":[]}`, "version 3"},
		{"relative path", `{"version":1,"projects":[{"path":"rel","contentHash":"` + good + `","trustedAt":"2026-01-01T00:00:00Z"}]}`, "invalid entry"},
		{"bad hash", `{"version":1,"projects":[{"path":"` + filepath.ToSlash(t.TempDir()) + `","contentHash":"zz","trustedAt":"2026-01-01T00:00:00Z"}]}`, "invalid entry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "p.json")
			if err := os.WriteFile(p, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenProjects(p); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
	dir := t.TempDir()
	dup := `{"version":1,"projects":[{"path":"` + filepath.ToSlash(dir) + `","contentHash":"` + good + `","trustedAt":"2026-01-01T00:00:00Z"},{"path":"` + filepath.ToSlash(dir) + `","contentHash":"` + good + `","trustedAt":"2026-01-01T00:00:00Z"}]}`
	p := filepath.Join(t.TempDir(), "p.json")
	if err := os.WriteFile(p, []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenProjects(p); err == nil {
		t.Error("duplicates must fail")
	}
}

func TestProjectAllowed(t *testing.T) {
	ps := newProjects(t)
	root := repo(t)
	if err := ps.Trust(root); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	if cfg.Trust.TrustProjectProfiles {
		t.Fatal("the default must keep project profiles off")
	}
	if ok, err := ProjectAllowed(cfg, ps, root); ok || err != nil {
		t.Errorf("default config: %v %v", ok, err)
	}
	cfg.Trust.TrustProjectProfiles = true
	if ok, err := ProjectAllowed(cfg, ps, root); !ok || err != nil {
		t.Errorf("enabled and trusted: %v %v", ok, err)
	}
	put(t, root, ".ccshelf/notes.txt", "edited")
	if ok, _ := ProjectAllowed(cfg, ps, root); ok {
		t.Error("an edited folder must not be allowed")
	}
	for _, c := range []struct {
		cfg  *config.Config
		ps   *ProjectStore
		root string
	}{{nil, ps, root}, {cfg, nil, root}, {cfg, ps, " "}} {
		if ok, err := ProjectAllowed(c.cfg, c.ps, c.root); ok || err != nil {
			t.Errorf("%+v: %v %v", c, ok, err)
		}
	}
}

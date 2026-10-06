package safepath

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckRel(t *testing.T) {
	tests := []struct {
		rel string
		ok  bool
	}{
		{"a/b.toml", true},
		{".claude-plugin/marketplace.json", true},
		{"./x", true},
		{"", false},
		{"/etc/passwd", false},
		{"../x", false},
		{"a/../../x", false},
		{"a/..", false},
		{"a\\b", false},
		{"C:/x", false},
		{"c:x", false},
		{"a\x00b", false},
	}
	for _, tt := range tests {
		err := CheckRel(tt.rel)
		if (err == nil) != tt.ok {
			t.Errorf("CheckRel(%q) = %v, want ok=%v", tt.rel, err, tt.ok)
		}
	}
}

func TestResolveAndRead(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "f.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(root, "a/f.txt"); err != nil {
		t.Fatal(err)
	}
	// Non-existent paths resolve lexically.
	p, err := Resolve(root, "a/new/dir/x")
	if err != nil || !strings.HasSuffix(p, filepath.Join("new", "dir", "x")) {
		t.Fatalf("Resolve nonexistent = %q, %v", p, err)
	}
	if _, err := Resolve(root, "../x"); !errors.Is(err, ErrEscape) {
		t.Errorf("want ErrEscape, got %v", err)
	}
	if _, err := Resolve(filepath.Join(root, "missing"), "x"); err == nil {
		t.Error("missing root should fail")
	}
	b, err := ReadFile(root, "a/f.txt", 100)
	if err != nil || string(b) != "hello" {
		t.Fatalf("ReadFile = %q, %v", b, err)
	}
	if _, err := ReadFile(root, "a/f.txt", 3); !errors.Is(err, ErrTooLarge) {
		t.Errorf("want ErrTooLarge, got %v", err)
	}
	if _, err := ReadFile(root, "a", 10); !errors.Is(err, ErrNotRegular) {
		t.Errorf("want ErrNotRegular, got %v", err)
	}
	if _, err := ReadFile(root, "a/none", 10); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want not exist, got %v", err)
	}
	if !IsDir(root, "a/b") || IsDir(root, "a/f.txt") || !IsFile(root, "a/f.txt") || IsFile(root, "a/b") {
		t.Error("IsDir/IsFile wrong")
	}
	if ok, err := Exists(root, "a/f.txt"); !ok || err != nil {
		t.Errorf("Exists = %v, %v", ok, err)
	}
	if ok, err := Exists(root, "a/zzz"); ok || err != nil {
		t.Errorf("Exists missing = %v, %v", ok, err)
	}
	if ok, err := Exists(root, "../zzz"); ok || err == nil {
		t.Errorf("Exists escape = %v, %v", ok, err)
	}
	ents, err := ReadDir(root, "a")
	if err != nil || len(ents) != 2 {
		t.Errorf("ReadDir = %v, %v", ents, err)
	}
}

func TestSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "inside")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"link", "link/secret", "link/new", "dangling"} {
		if _, err := Resolve(root, rel); !errors.Is(err, ErrEscape) {
			t.Errorf("Resolve(%q) = %v, want ErrEscape", rel, err)
		}
		if _, err := ReadFile(root, rel, 10); err == nil {
			t.Errorf("ReadFile(%q) should fail", rel)
		}
	}
	if b, err := ReadFile(root, "inside", 10); err != nil || string(b) != "ok" {
		t.Errorf("symlink inside root = %q, %v", b, err)
	}
}

package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	sha1a = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sha1b = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sha1c = "cccccccccccccccccccccccccccccccccccccccc"
	key   = "0123456789abcdef"
)

func age(t *testing.T, p string, d time.Duration) {
	t.Helper()
	old := time.Now().Add(-d)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
}

func mkCheckout(t *testing.T, dir, k, sha string, d time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, "git", k, sha)
	if err := os.MkdirAll(filepath.Join(p, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "profiles", "a.toml"), []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	age(t, p, d)
	return p
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestPruneDirRemovesOldKeepsRecent(t *testing.T) {
	dir := newDir(t)
	oldFile, _ := Write(dir, "settings", "json", []byte("old"))
	newFile, _ := Write(dir, "settings", "json", []byte("new"))
	age(t, oldFile, 40*24*time.Hour)
	oldCo := mkCheckout(t, dir, key, sha1a, 40*24*time.Hour)
	newCo := mkCheckout(t, dir, key, sha1b, time.Hour)
	keptCo := mkCheckout(t, dir, key, sha1c, 90*24*time.Hour)
	otherKey := mkCheckout(t, dir, "fedcba9876543210", sha1a, 40*24*time.Hour)
	stray := filepath.Join(dir, "git", key, "notes.txt")
	if err := os.WriteFile(stray, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	age(t, stray, 90*24*time.Hour)
	tmp := filepath.Join(dir, "git", key, ".tmp-12345")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	age(t, tmp, 40*24*time.Hour)
	hooks := filepath.Join(dir, "git", ".nohooks")
	if err := os.MkdirAll(hooks, 0o700); err != nil {
		t.Fatal(err)
	}
	age(t, hooks, 90*24*time.Hour)

	removed, err := PruneDir(dir, 30*24*time.Hour, func(p string) bool { return p == keptCo })
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{oldFile, oldCo, tmp, otherKey} {
		if exists(gone) {
			t.Errorf("%s should have been removed", gone)
		}
	}
	for _, kept := range []string{newFile, newCo, keptCo, stray, hooks} {
		if !exists(kept) {
			t.Errorf("%s should have been kept", kept)
		}
	}
	if exists(filepath.Dir(otherKey)) {
		t.Error("an emptied url folder should be removed")
	}
	if len(removed) != 4 {
		t.Errorf("removed = %v", removed)
	}
}

func TestPruneDirDefaultsAndMissing(t *testing.T) {
	dir := newDir(t)
	if removed, err := PruneDir(dir, 0, nil); err != nil || len(removed) != 0 {
		t.Errorf("empty cache: %v %v", removed, err)
	}
	co := mkCheckout(t, dir, key, sha1a, DefaultMaxAge+time.Hour)
	if removed, err := PruneDir(dir, -1, nil); err != nil || len(removed) != 1 || exists(co) {
		t.Errorf("default max age: %v %v", removed, err)
	}
	if removed, err := PruneDir(filepath.Join(dir, "missing"), time.Hour, nil); err != nil || removed != nil {
		t.Errorf("missing dir: %v %v", removed, err)
	}
}

func TestPruneDirLeavesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := newDir(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "git", key), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "git", key, sha1a)
	if err := os.Symlink(outside, link); err != nil {
		t.Skip(err)
	}
	if removed, err := PruneDir(dir, time.Nanosecond, nil); err != nil || len(removed) != 0 {
		t.Errorf("removed = %v, err = %v", removed, err)
	}
	if !exists(filepath.Join(outside, "f")) || !exists(link) {
		t.Error("a symlink must be neither followed nor removed")
	}
}

func TestPruneUsesTheCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "xdg"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "local"))
	d, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	co := mkCheckout(t, d, key, sha1a, 100*24*time.Hour)
	removed, err := Prune(time.Hour, nil)
	if err != nil || len(removed) != 1 || exists(co) {
		t.Errorf("Prune: %v %v", removed, err)
	}
}

func TestTamperedErrorsSayWhatToDelete(t *testing.T) {
	dir := newDir(t)
	p, _ := Write(dir, "mcp", "json", []byte("good"))
	if err := os.WriteFile(p, []byte("evil"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Write(dir, "mcp", "json", []byte("good"))
	if err == nil || !strings.Contains(err.Error(), "Delete "+p) {
		t.Errorf("Write error lacks the hint: %v", err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(dir, filepath.Base(p)); err == nil || !strings.Contains(err.Error(), "Delete "+p) {
		t.Errorf("ReadFile error lacks the hint: %v", err)
	}
}

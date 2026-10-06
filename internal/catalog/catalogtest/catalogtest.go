// Package catalogtest holds helpers shared by the tests of the catalog
// packages: the fictional org data repo fixture and small file helpers. It is
// only meant to be imported from tests.
package catalogtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// FixtureDir returns the absolute path of the read-only fixture repo
// internal/catalog/testdata/orgrepo.
func FixtureDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the fixture")
	}
	return filepath.Join(filepath.Dir(filepath.Dir(file)), "testdata", "orgrepo")
}

// CopyFixture copies the fixture into a fresh temporary directory so a test
// can mutate it, and returns that directory.
func CopyFixture(t testing.TB) string {
	t.Helper()
	src := FixtureDir(t)
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return dst
}

// Write creates or replaces the file rel (slash separated) below root.
func Write(t testing.TB, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Read returns the content of the file rel below root.
func Read(t testing.TB, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Remove deletes the file or directory rel below root.
func Remove(t testing.TB, root, rel string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

// Replace edits the file rel below root, replacing old with new exactly once.
func Replace(t testing.TB, root, rel, old, new string) {
	t.Helper()
	s := Read(t, root, rel)
	if strings.Count(s, old) != 1 {
		t.Fatalf("%s: expected exactly one occurrence of %q, found %d", rel, old, strings.Count(s, old))
	}
	Write(t, root, rel, strings.Replace(s, old, new, 1))
}

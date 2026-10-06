//go:build unix

package account

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestEnsureDirModes(t *testing.T) {
	old := syscall.Umask(0) // a permissive umask must not leave 0777 behind
	t.Cleanup(func() { syscall.Umask(old) })

	root := t.TempDir()
	fresh := filepath.Join(root, "fresh")
	top, err := ensureDir(fresh, false)
	if err != nil || top != fresh {
		t.Fatalf("%q %v", top, err)
	}
	if fi, _ := os.Stat(fresh); fi.Mode().Perm() != 0o700 {
		t.Errorf("new directory mode %v, want 0700", fi.Mode().Perm())
	}

	existing := filepath.Join(root, "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if top, err = ensureDir(existing, false); err != nil || top != "" {
		t.Fatalf("%q %v", top, err)
	}
	if fi, _ := os.Stat(existing); fi.Mode().Perm() != 0o700 {
		t.Errorf("existing empty directory mode %v, want 0700", fi.Mode().Perm())
	}
}

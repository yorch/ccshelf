//go:build unix

package orgcmd

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func checkModes(t *testing.T, dest string) {
	t.Helper()
	for p := dest; p != filepath.Dir(filepath.Dir(dest)); p = filepath.Dir(p) {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != publishDirMode {
			t.Errorf("%s: mode %v %v, want %v", p, st.Mode().Perm(), err, publishDirMode)
		}
	}
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) == 0 {
		t.Fatalf("%v %d", err, len(entries))
	}
	for _, e := range entries {
		st, _ := e.Info()
		if st.Mode().Perm() != publishFileMode {
			t.Errorf("%s: mode %v, want %v", e.Name(), st.Mode().Perm(), publishFileMode)
		}
	}
}

func TestPublishedModesIgnoreTheUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	h := newHarness(t, copyExample(t))
	dest := filepath.Join(h.cwd, "x", "y")
	h.cwd = filepath.Dir(h.cwd)
	if r := h.run("catalog", "build", "--out", dest); r.code != 0 {
		t.Fatalf("%d %s", r.code, r.err)
	}
	checkModes(t, dest)
}

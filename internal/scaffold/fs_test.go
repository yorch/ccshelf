package scaffold

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func openTemp(t *testing.T) (FS, string) {
	t.Helper()
	dir := t.TempDir()
	f, closer, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closer.Close() })
	return f, dir
}

func TestOSFSRefusesBadNames(t *testing.T) {
	f, dir := openTemp(t)
	for _, n := range []string{"../x", "a/../../x", "/abs", `a\b`, "a//b", "./a", "a/", "", "C:x", "a\x00b"} {
		if _, err := f.Lstat(n); err == nil {
			t.Errorf("Lstat(%q) accepted", n)
		}
		if err := f.WriteNew(n, []byte("x"), 0o644); err == nil {
			t.Errorf("WriteNew(%q) accepted", n)
		}
		if err := f.MkdirAll(n); err == nil && n != "" {
			t.Errorf("MkdirAll(%q) accepted", n)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "x")); err == nil {
		t.Error("something was written beside the target")
	}
}

func TestOSFSWriteNewIsExclusiveAndLeavesNoTemporaryFiles(t *testing.T) {
	f, dir := openTemp(t)
	if err := f.WriteNew("a/b/c.txt", []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := f.WriteNew("a/b/c.txt", []byte("two"), 0o644)
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("second WriteNew: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a", "b", "c.txt")); string(got) != "one" {
		t.Errorf("content = %q", got)
	}
	ents, _ := os.ReadDir(filepath.Join(dir, "a", "b"))
	if len(ents) != 1 {
		t.Errorf("leftovers: %v", ents)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{"a", "a/b"} {
			if st, _ := os.Stat(filepath.Join(dir, p)); st.Mode().Perm() != 0o755 {
				t.Errorf("%s mode %v", p, st.Mode().Perm())
			}
		}
		if st, _ := os.Stat(filepath.Join(dir, "a", "b", "c.txt")); st.Mode().Perm() != 0o644 {
			t.Errorf("file mode %v", st.Mode().Perm())
		}
	}
}

func TestOSFSWriteNewKeepsAFileThatIsAlreadyThere(t *testing.T) {
	f, dir := openTemp(t)
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteNew("x", []byte("tool"), 0o644); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "x"))
	st, _ := os.Stat(filepath.Join(dir, "x"))
	if string(got) != "mine" || (runtime.GOOS != "windows" && st.Mode().Perm() != 0o600) {
		t.Errorf("the existing file changed: %q %v", got, st.Mode())
	}
}

func TestOSFSReplace(t *testing.T) {
	f, dir := openTemp(t)
	if err := f.Replace("missing", []byte("x"), 0o644); err == nil {
		t.Error("Replace created a file")
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.Replace("x", []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "x"))
	if string(got) != "new" {
		t.Error(string(got))
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Errorf("leftovers: %v", ents)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.Replace("d", []byte("x"), 0o644); err == nil {
		t.Error("Replace replaced a directory")
	}
}

func TestOSFSReadFileLimits(t *testing.T) {
	f, dir := openTemp(t)
	if err := os.WriteFile(filepath.Join(dir, "big"), []byte(strings.Repeat("x", 100)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadFile("big", 99); !errors.Is(err, ErrTooLarge) {
		t.Errorf("err = %v", err)
	}
	if b, err := f.ReadFile("big", 100); err != nil || len(b) != 100 {
		t.Errorf("%d %v", len(b), err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadFile("d", 10); !errors.Is(err, ErrNotRegular) {
		t.Errorf("err = %v", err)
	}
	if _, err := f.ReadFile("nope", 10); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
	if _, err := f.ReadFile("nope/deeper", 10); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
}

func TestOSFSNeverFollowsSymlinks(t *testing.T) {
	f, dir := openTemp(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linkdir")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "linkfile")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := os.Symlink("nowhere", filepath.Join(dir, "dangling")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if _, err := f.ReadFile("linkfile", 10); !errors.Is(err, ErrSymlink) {
		t.Errorf("ReadFile through a link: %v", err)
	}
	if _, err := f.ReadFile("linkdir/secret", 10); !errors.Is(err, ErrSymlink) {
		t.Errorf("ReadFile below a link: %v", err)
	}
	if _, err := f.ReadDir("linkdir"); !errors.Is(err, ErrSymlink) {
		t.Errorf("ReadDir of a link: %v", err)
	}
	if _, err := f.Lstat("linkdir/secret"); !errors.Is(err, ErrSymlink) {
		t.Errorf("Lstat below a link: %v", err)
	}
	if fi, err := f.Lstat("linkfile"); err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("Lstat of the link itself: %v %v", fi, err)
	}
	for _, name := range []string{"linkdir/new", "linkdir/a/b", "linkfile", "dangling"} {
		if err := f.WriteNew(name, []byte("x"), 0o644); err == nil {
			t.Errorf("WriteNew(%q) went through a link", name)
		}
	}
	if err := f.MkdirAll("linkdir/sub"); !errors.Is(err, ErrSymlink) {
		t.Errorf("MkdirAll below a link: %v", err)
	}
	if err := f.Replace("linkfile", []byte("x"), 0o644); err == nil {
		t.Error("Replace replaced a link")
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret")); string(b) != "s" {
		t.Error("the file behind a link changed")
	}
	ents, _ := os.ReadDir(outside)
	if len(ents) != 1 {
		t.Errorf("something was written outside: %v", ents)
	}
	if _, err := os.Lstat(filepath.Join(dir, "nowhere")); err == nil {
		t.Error("a dangling link was written through")
	}
}

func TestEmptyFS(t *testing.T) {
	e := Empty()
	if _, err := e.Lstat("x"); !errors.Is(err, fs.ErrNotExist) {
		t.Error(err)
	}
	if _, err := e.ReadFile("x", 1); !errors.Is(err, fs.ErrNotExist) {
		t.Error(err)
	}
	if _, err := e.ReadDir("."); !errors.Is(err, fs.ErrNotExist) {
		t.Error(err)
	}
	if err := e.WriteNew("x", nil, 0o644); err == nil {
		t.Error("the empty FS writes")
	}
	if err := e.MkdirAll("x"); err == nil {
		t.Error("the empty FS makes directories")
	}
	if err := e.Replace("x", nil, 0o644); err == nil {
		t.Error("the empty FS replaces")
	}
	if _, err := e.Lstat("../x"); err == nil {
		t.Error("bad names are not checked")
	}
}

func TestApplyStopsAtTheFirstFailureAndReportsProgress(t *testing.T) {
	m := newMem(nil)
	m.failWrite = "README.md"
	plan, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	res, err := Apply(m, plan, ApplyOptions{})
	if err == nil || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("err = %v", err)
	}
	if len(res.Created) == 0 || len(res.Created) >= len(plan.Entries) {
		t.Errorf("created %v", res.Created)
	}
	// Applying again completes the rest without touching what exists.
	m.failWrite = ""
	again, err := Build(m, baseParams())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(m, again, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if !m.has("README.md") {
		t.Error("README.md was not created on the second run")
	}
}

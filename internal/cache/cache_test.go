package cache

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func newDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "ccshelf")
	if err := Ensure(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPathAndDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "local"))
	p, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".cache", "ccshelf")
	if runtime.GOOS == "windows" {
		want = filepath.Join(home, "local", "ccshelf")
	}
	if p != want {
		t.Fatalf("Path = %q, want %q", p, want)
	}
	if runtime.GOOS != "windows" {
		xdg := filepath.Join(home, "xdg")
		t.Setenv("XDG_CACHE_HOME", xdg)
		if p, _ := Path(); p != filepath.Join(xdg, "ccshelf") {
			t.Fatalf("XDG Path = %q", p)
		}
		t.Setenv("XDG_CACHE_HOME", "relative")
		if p, _ := Path(); p != filepath.Join(home, ".cache", "ccshelf") {
			t.Fatalf("relative XDG should be ignored: %q", p)
		}
	}
	d, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		t.Fatalf("Dir not created: %v", err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(d)
		if fi.Mode().Perm() != 0o700 {
			t.Fatalf("mode %o", fi.Mode().Perm())
		}
	}
}

func TestEnsureRefusals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode and symlink checks are Unix behavior")
	}
	base := t.TempDir()
	t.Run("wide modes are repaired", func(t *testing.T) {
		for _, mode := range []os.FileMode{0o755, 0o777, 0o770, 0o702, 0o707, 0o750, 0o500, 0o600} {
			d := filepath.Join(base, fmt.Sprintf("wide-%o", mode))
			if err := os.Mkdir(d, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(d, mode); err != nil {
				t.Fatal(err)
			}
			if err := Ensure(d); err != nil {
				t.Fatalf("mode %o: %v", mode, err)
			}
			if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
				t.Fatalf("mode %o was not repaired to 0700: %v %v", mode, fi, err)
			}
		}
	})
	t.Run("symlink", func(t *testing.T) {
		target := filepath.Join(base, "target")
		_ = os.Mkdir(target, 0o700)
		link := filepath.Join(base, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Skip("symlinks unavailable")
		}
		if err := Ensure(link); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("file", func(t *testing.T) {
		f := filepath.Join(base, "file")
		_ = os.WriteFile(f, nil, 0o600)
		if err := Ensure(f); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestWriteBasics(t *testing.T) {
	dir := newDir(t)
	p, err := Write(dir, "settings", "json", []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !namePattern.MatchString(filepath.Base(p)) || !strings.HasPrefix(filepath.Base(p), "settings-") {
		t.Fatalf("name %q", p)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %o", fi.Mode().Perm())
		}
	}
	p2, err := Write(dir, "settings", "json", []byte(`{"a":1}`))
	if err != nil || p2 != p {
		t.Fatalf("reuse: %v %q", err, p2)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
	got, err := ReadFile(dir, filepath.Base(p))
	if err != nil || string(got) != `{"a":1}` {
		t.Fatalf("ReadFile: %v %q", err, got)
	}
}

func TestWriteInvalidNames(t *testing.T) {
	dir := newDir(t)
	for _, c := range [][2]string{{"", "json"}, {"a/b", "json"}, {"..", "json"}, {"a", ""}, {"a", "j.son"}, {"a", "../x"}, {"a b", "json"}} {
		if _, err := Write(dir, c[0], c[1], []byte("x")); err == nil {
			t.Errorf("Write(%q,%q) accepted", c[0], c[1])
		}
	}
	if _, err := ReadFile(dir, "../etc"); err == nil {
		t.Error("ReadFile accepted traversal")
	}
	if err := WriteReplace(dir, "bad", nil); err == nil {
		t.Error("WriteReplace accepted bad name")
	}
}

func TestWriteTampered(t *testing.T) {
	dir := newDir(t)
	p, _ := Write(dir, "mcp", "json", []byte("good"))
	if err := os.WriteFile(p, []byte("evil"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, "mcp", "json", []byte("good")); !errors.Is(err, ErrTampered) {
		t.Fatalf("err = %v", err)
	}
	// Longer content is also detected.
	if err := os.WriteFile(p, []byte("good plus more"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, "mcp", "json", []byte("good")); !errors.Is(err, ErrTampered) {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteSymlinkAndDirTarget(t *testing.T) {
	dir := newDir(t)
	name, _ := Name("s", "json", []byte("x"))
	path := filepath.Join(dir, name)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := Write(dir, "s", "json", []byte("x")); err == nil {
		t.Fatal("followed a symlink")
	}
	_ = os.Remove(path)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, "s", "json", []byte("x")); !errors.Is(err, ErrTampered) {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteMissingDir(t *testing.T) {
	if _, err := Write(filepath.Join(t.TempDir(), "nope"), "a", "json", []byte("x")); err == nil {
		t.Fatal("expected error")
	}
}

func TestConcurrentGoroutines(t *testing.T) {
	dir := newDir(t)
	var wg sync.WaitGroup
	paths := make([]string, 50)
	errs := make([]error, 50)
	for i := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paths[i], errs[i] = Write(dir, "settings", "json", []byte("same content"))
		}()
	}
	wg.Wait()
	for i := range paths {
		if errs[i] != nil || paths[i] != paths[0] {
			t.Fatalf("goroutine %d: %v %q", i, errs[i], paths[i])
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}
}

func TestHelperProcess(t *testing.T) {
	dir := os.Getenv("CCSHELF_CACHE_TEST_DIR")
	if dir == "" {
		t.Skip("helper for multi-process test")
	}
	for i := 0; i < 20; i++ {
		if _, err := Write(dir, "settings", "json", []byte("shared across processes")); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
	}
}

func TestConcurrentProcesses(t *testing.T) {
	dir := newDir(t)
	exe, err := os.Executable()
	if err != nil {
		t.Skip("no executable")
	}
	var cmds []*exec.Cmd
	for i := 0; i < 6; i++ {
		c := exec.Command(exe, "-test.run=^TestHelperProcess$")
		c.Env = append(os.Environ(), "CCSHELF_CACHE_TEST_DIR="+dir)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, c)
	}
	for _, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatalf("helper failed: %v", err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %v", entries)
	}
}

func TestWriteReplaceAndRead(t *testing.T) {
	dir := newDir(t)
	name := "plugins-0123456789abcdef0123456789abcdef.json"
	if _, err := ReadFile(dir, name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
	for _, v := range []string{"one", "two"} {
		if err := WriteReplace(dir, name, []byte(v)); err != nil {
			t.Fatal(err)
		}
		got, err := ReadFile(dir, name)
		if err != nil || string(got) != v {
			t.Fatalf("%v %q", err, got)
		}
	}
	if err := WriteReplace(dir, name, make([]byte, MaxFileSize+1)); err == nil {
		t.Fatal("expected size error")
	}
	// Directory in place of the file.
	_ = os.Remove(filepath.Join(dir, name))
	_ = os.Mkdir(filepath.Join(dir, name), 0o700)
	if _, err := ReadFile(dir, name); err == nil {
		t.Fatal("expected error for directory")
	}
}

func TestReadFileTooLarge(t *testing.T) {
	dir := newDir(t)
	p := filepath.Join(dir, "x-0123456789abcdef0123456789abcdef.bin")
	if err := os.WriteFile(p, make([]byte, 100), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readRegular(p, 10); !errors.Is(err, ErrTampered) {
		t.Fatalf("err = %v", err)
	}
}

func TestGC(t *testing.T) {
	dir := newDir(t)
	old := time.Now().Add(-60 * 24 * time.Hour)
	mk := func(name string, age time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(p, age, age)
		return p
	}
	oldFile := mk("settings-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json", old)
	busy := mk("settings-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.json", old)
	fresh := mk("settings-cccccccccccccccccccccccccccccccc.json", time.Now())
	other := mk("notes.txt", old)
	tmp := mk(".ccshelf-tmp-0123456789abcdef", old)
	_ = os.Mkdir(filepath.Join(dir, "sub-dddddddddddddddddddddddddddddddd.json"), 0o700)
	removed, err := GC(dir, 0, func(p string) bool { return p == busy })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %v", removed)
	}
	for _, p := range []string{oldFile, tmp} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s survived", p)
		}
	}
	for _, p := range []string{busy, fresh, other, filepath.Join(dir, "sub-dddddddddddddddddddddddddddddddd.json")} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s removed", p)
		}
	}
	if r, err := GC(filepath.Join(dir, "missing"), time.Hour, nil); err != nil || r != nil {
		t.Fatalf("missing dir: %v %v", r, err)
	}
}

func TestGCSkipsSymlink(t *testing.T) {
	dir := newDir(t)
	target := filepath.Join(t.TempDir(), "t")
	_ = os.WriteFile(target, []byte("x"), 0o600)
	link := filepath.Join(dir, "s-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	_ = os.Chtimes(target, old, old)
	removed, _ := GC(dir, time.Hour, nil)
	if len(removed) != 0 {
		t.Fatalf("removed symlink: %v", removed)
	}
}

func TestEnsureParentIsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(filepath.Join(f, "sub", "ccshelf")); err == nil {
		t.Fatal("expected error")
	}
	if err := Ensure(filepath.Join(f, "ccshelf")); err == nil {
		t.Fatal("expected error")
	}
}

func TestDirError(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", f)
	t.Setenv("LOCALAPPDATA", f)
	if _, err := Dir(); err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteReplaceOverDirectoryFails(t *testing.T) {
	dir := newDir(t)
	name := "plugins-0123456789abcdef0123456789abcdef.json"
	if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteReplace(dir, name, []byte("x")); err == nil {
		t.Fatal("expected error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary file leaked: %v", entries)
	}
}

func TestWriteInReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root Unix user")
	}
	dir := newDir(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if _, err := Write(dir, "a", "json", []byte("x")); err == nil {
		t.Fatal("expected error")
	}
}

func TestPathFor(t *testing.T) {
	abs := t.TempDir()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	fail := func() (string, error) { return "", errors.New("no dir") }
	ok := func() (string, error) { return filepath.Join(abs, "uc"), nil }
	home := func() (string, error) { return filepath.Join(abs, "home"), nil }
	if p, err := pathFor("windows", env(map[string]string{"LOCALAPPDATA": abs}), home, ok); err != nil || p != filepath.Join(abs, "ccshelf") {
		t.Errorf("%q %v", p, err)
	}
	if p, err := pathFor("windows", env(nil), home, ok); err != nil || p != filepath.Join(abs, "uc", "ccshelf") {
		t.Errorf("%q %v", p, err)
	}
	if _, err := pathFor("windows", env(nil), home, fail); err == nil {
		t.Error("expected error")
	}
	if _, err := pathFor("linux", env(nil), fail, ok); err == nil {
		t.Error("expected error")
	}
	if p, _ := pathFor("darwin", env(nil), home, ok); p != filepath.Join(abs, "home", ".cache", "ccshelf") {
		t.Errorf("%q", p)
	}
}

func TestNameUses128Bits(t *testing.T) {
	name, err := Name("settings", "json", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	hexPart := strings.TrimSuffix(strings.TrimPrefix(name, "settings-"), ".json")
	if len(hexPart) != 32 {
		t.Fatalf("%q has %d hex chars, want 32", name, len(hexPart))
	}
	if !namePattern.MatchString(name) {
		t.Fatalf("%q does not match namePattern", name)
	}
	if namePattern.MatchString("settings-0123456789abcdef.json") {
		t.Fatal("a 64-bit name must not match")
	}
}

func TestWriteTouchesOnReuse(t *testing.T) {
	dir := newDir(t)
	p, err := Write(dir, "s", "json", []byte("live"))
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, "s", "json", []byte("live")); err != nil {
		t.Fatal(err)
	}
	removed, err := GC(dir, DefaultMaxAge, nil)
	if err != nil || len(removed) != 0 {
		t.Fatalf("GC removed a reused file: %v %v", removed, err)
	}
	if fi, err := os.Stat(p); err != nil || time.Since(fi.ModTime()) > time.Hour {
		t.Fatalf("mtime not refreshed: %v %v", fi, err)
	}
}

func TestPublishVerifiesAfterRename(t *testing.T) {
	dir := newDir(t)
	testAfterRename = func(path string) { _ = os.WriteFile(path, []byte("evil"), 0o600) }
	defer func() { testAfterRename = nil }()
	if _, err := Write(dir, "s", "json", []byte("good")); !errors.Is(err, ErrTampered) {
		t.Fatalf("err = %v, want ErrTampered", err)
	}
}

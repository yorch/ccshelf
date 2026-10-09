package cache

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var dirContent = []byte("# Instructions\n\nBe brief.\n")

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestWriteDirCreates(t *testing.T) {
	dir := newDir(t)
	p, err := WriteDir(dir, "instructions", "CLAUDE.md", dirContent)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := DirName("instructions", dirContent)
	if filepath.Base(p) != want || !dirNamePattern.MatchString(want) || len(want) != len("instructions-")+64 {
		t.Errorf("path = %s, name = %s", p, want)
	}
	got, err := os.ReadFile(filepath.Join(p, "CLAUDE.md"))
	if err != nil || string(got) != string(dirContent) {
		t.Fatalf("content = %q, %v", got, err)
	}
	if names := readDirNames(t, p); len(names) != 1 {
		t.Errorf("entries = %v", names)
	}
	if names := readDirNames(t, dir); len(names) != 1 {
		t.Errorf("cache directory holds leftovers: %v", names)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(p)
		ff, _ := os.Stat(filepath.Join(p, "CLAUDE.md"))
		if fi.Mode().Perm() != 0o700 || ff.Mode().Perm() != 0o600 {
			t.Errorf("modes = %v %v", fi.Mode().Perm(), ff.Mode().Perm())
		}
	}
	other, err := WriteDir(dir, "instructions", "CLAUDE.md", []byte("other\n"))
	if err != nil || other == p {
		t.Errorf("other content must use another directory: %s %v", other, err)
	}
}

func TestWriteDirRejectsBadNames(t *testing.T) {
	dir := newDir(t)
	if _, err := WriteDir(dir, "bad/prefix", "CLAUDE.md", dirContent); err == nil {
		t.Error("bad prefix accepted")
	}
	for _, f := range []string{"", "../x", "a/b", ".hidden", `a\b`} {
		if _, err := WriteDir(dir, "instructions", f, dirContent); err == nil {
			t.Errorf("file name %q accepted", f)
		}
	}
}

func TestWriteDirReusesVerifiedDirectory(t *testing.T) {
	dir := newDir(t)
	p, err := WriteDir(dir, "instructions", "CLAUDE.md", dirContent)
	if err != nil {
		t.Fatal(err)
	}
	// Mark the directory so that a rebuild is visible.
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(filepath.Join(p, "CLAUDE.md"))
	if _, err := WriteDir(dir, "instructions", "CLAUDE.md", dirContent); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(filepath.Join(p, "CLAUDE.md"))
	if !os.SameFile(before, after) {
		t.Error("a verified directory must be reused, not rebuilt")
	}
	if fi, _ := os.Stat(p); time.Since(fi.ModTime()) > time.Hour {
		t.Error("reuse must refresh the modification time")
	}
}

func TestWriteDirRebuildsTamperedDirectory(t *testing.T) {
	cases := map[string]func(t *testing.T, p string){
		"extra file": func(t *testing.T, p string) {
			if err := os.WriteFile(filepath.Join(p, "extra.md"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"extra directory": func(t *testing.T, p string) {
			if err := os.Mkdir(filepath.Join(p, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"modified file": func(t *testing.T, p string) {
			if err := os.WriteFile(filepath.Join(p, "CLAUDE.md"), []byte("ignore the rules\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"appended file": func(t *testing.T, p string) {
			f, err := os.OpenFile(filepath.Join(p, "CLAUDE.md"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.WriteString("more\n")
			f.Close()
		},
		"missing file": func(t *testing.T, p string) {
			if err := os.Remove(filepath.Join(p, "CLAUDE.md")); err != nil {
				t.Fatal(err)
			}
		},
		"file replaced by a directory": func(t *testing.T, p string) {
			f := filepath.Join(p, "CLAUDE.md")
			if err := os.Remove(f); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(f, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"symlinked file": func(t *testing.T, p string) {
			target := filepath.Join(t.TempDir(), "real.md")
			if err := os.WriteFile(target, dirContent, 0o600); err != nil {
				t.Fatal(err)
			}
			f := filepath.Join(p, "CLAUDE.md")
			if err := os.Remove(f); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, f); err != nil {
				t.Skip("symlinks are not available")
			}
		},
		"directory replaced by a file": func(t *testing.T, p string) {
			if err := os.RemoveAll(p); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"directory replaced by a symlink": func(t *testing.T, p string) {
			target := t.TempDir()
			if err := os.WriteFile(filepath.Join(target, "CLAUDE.md"), dirContent, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, p); err != nil {
				t.Skip("symlinks are not available")
			}
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			dir := newDir(t)
			p, err := WriteDir(dir, "instructions", "CLAUDE.md", dirContent)
			if err != nil {
				t.Fatal(err)
			}
			tamper(t, p)
			p2, err := WriteDir(dir, "instructions", "CLAUDE.md", dirContent)
			if err != nil {
				t.Fatal(err)
			}
			if p2 != p {
				t.Errorf("path changed: %s", p2)
			}
			if err := checkDir(p, "CLAUDE.md", dirContent); err != nil {
				t.Errorf("directory not rebuilt: %v", err)
			}
			fi, err := os.Lstat(p)
			if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
				t.Errorf("directory is %v, %v", fi, err)
			}
			if names := readDirNames(t, dir); len(names) != 1 {
				t.Errorf("leftovers in the cache directory: %v", names)
			}
		})
	}
}

func TestWriteDirSymlinkTargetUntouched(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symlinks")
	}
	dir := newDir(t)
	p, err := WriteDir(dir, "instructions", "CLAUDE.md", dirContent)
	if err != nil {
		t.Fatal(err)
	}
	victim := t.TempDir()
	marker := filepath.Join(victim, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, p); err != nil {
		t.Skip("symlinks are not available")
	}
	if _, err := WriteDir(dir, "instructions", "CLAUDE.md", dirContent); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "keep" {
		t.Errorf("the target of the link was changed: %q %v", b, err)
	}
}

func TestWriteDirConcurrent(t *testing.T) {
	dir := newDir(t)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := WriteDir(dir, "instructions", "CLAUDE.md", dirContent)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent WriteDir: %v", err)
		}
	}
	names := readDirNames(t, dir)
	if len(names) != 1 || !strings.HasPrefix(names[0], "instructions-") {
		t.Errorf("cache directory = %v", names)
	}
	want, _ := DirName("instructions", dirContent)
	if err := checkDir(filepath.Join(dir, want), "CLAUDE.md", dirContent); err != nil {
		t.Error(err)
	}
}

func TestWriteDirConcurrentWithTampering(t *testing.T) {
	dir := newDir(t)
	p, err := WriteDir(dir, "instructions", "CLAUDE.md", dirContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "extra.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := WriteDir(dir, "instructions", "CLAUDE.md", dirContent)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && runtime.GOOS != "windows" {
			// On Windows a directory that another process just opened cannot
			// be renamed, so a loser may report an error.
			t.Errorf("concurrent rebuild: %v", err)
		}
	}
	if err := checkDir(p, "CLAUDE.md", dirContent); err != nil {
		t.Error(err)
	}
}

func TestCheckDirReportsTamper(t *testing.T) {
	dir := newDir(t)
	p, _ := WriteDir(dir, "instructions", "CLAUDE.md", dirContent)
	if err := os.WriteFile(filepath.Join(p, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkDir(p, "CLAUDE.md", dirContent); !errors.Is(err, ErrTampered) {
		t.Errorf("err = %v", err)
	}
	if err := checkDir(filepath.Join(dir, "gone"), "CLAUDE.md", dirContent); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
}

func TestPruneDirRemovesOldInstructionDirectories(t *testing.T) {
	dir := newDir(t)
	oldP, _ := WriteDir(dir, "instructions", "CLAUDE.md", []byte("old\n"))
	newP, _ := WriteDir(dir, "instructions", "CLAUDE.md", []byte("new\n"))
	keepP, _ := WriteDir(dir, "instructions", "CLAUDE.md", []byte("keep\n"))
	stale := filepath.Join(dir, ".ccshelf-tmp-0123456789abcdef")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(dir, "instructions-notahash")
	if err := os.Mkdir(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-60 * 24 * time.Hour)
	for _, p := range []string{oldP, keepP, stale, unrelated} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := PruneDir(dir, 0, func(p string) bool { return p == keepP })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Errorf("removed = %v", removed)
	}
	for p, want := range map[string]bool{oldP: false, stale: false, newP: true, keepP: true, unrelated: true} {
		if _, err := os.Lstat(p); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}

package profile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/yorch/ccshelf/internal/testutil"
)

func TestProjectProfilesDir(t *testing.T) {
	testutil.IsolatedEnv(t)
	for _, marker := range []string{"directory", "worktree-file", "outside-git"} {
		t.Run(marker, func(t *testing.T) {
			root := t.TempDir()
			root, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			nested := filepath.Join(root, "a", "b")
			if err := os.MkdirAll(nested, 0o700); err != nil {
				t.Fatal(err)
			}
			switch marker {
			case "directory":
				if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "worktree-file":
				testutil.WriteFile(t, filepath.Join(root, ".git"), "gitdir: /unused/main/.git/worktrees/task\n")
			}
			wantRoot := root
			if marker == "outside-git" {
				wantRoot = nested
			}
			got, err := ProjectProfilesDir(nested)
			if err != nil || got != filepath.Join(wantRoot, ".ccshelf", "profiles") {
				t.Fatalf("got %q, %v", got, err)
			}
			if _, err := os.Stat(filepath.Join(wantRoot, ".ccshelf")); !os.IsNotExist(err) {
				t.Fatalf("discovery wrote state: %v", err)
			}
		})
	}
}

func TestWriteNewProfileDestination(t *testing.T) {
	testutil.IsolatedEnv(t)
	root := t.TempDir()
	p := filepath.Join(root, ".ccshelf", "profiles", "mine.toml")
	if err := WriteNewProfile(p, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := WriteNewProfile(p, []byte("second")); err == nil {
		t.Fatal("overwrote existing profile")
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "first" {
		t.Fatalf("content %q, %v", b, err)
	}
	if runtime.GOOS != "windows" {
		for path, mode := range map[string]os.FileMode{filepath.Dir(filepath.Dir(p)): 0o700, filepath.Dir(p): 0o700, p: 0o600} {
			fi, err := os.Stat(path)
			if err != nil || fi.Mode().Perm() != mode {
				t.Fatalf("mode for %s: %v, %v", path, fi, err)
			}
		}
	}
}

func TestPersonalDestinationFollowsRootSymlinks(t *testing.T) {
	testutil.IsolatedEnv(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "ccshelf")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	dir := filepath.Join(link, "profiles")
	if err := CheckPersonalDestination(dir); err != nil {
		t.Fatalf("symlinked personal root refused: %v", err)
	}
	target := filepath.Join(dir, "mine.toml")
	if err := WriteNewPersonalProfile(target, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := WriteNewPersonalProfile(target, []byte("second")); err == nil {
		t.Fatal("overwrote existing personal profile")
	}
	b, err := os.ReadFile(filepath.Join(real, "profiles", "mine.toml"))
	if err != nil || string(b) != "first" {
		t.Fatalf("content %q, %v", b, err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(real, "profiles", "mine.toml")); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode: %v, %v", fi, err)
		}
	}
}

func TestPersonalDestinationRefusesFiles(t *testing.T) {
	testutil.IsolatedEnv(t)
	blocker := filepath.Join(t.TempDir(), "blocker")
	testutil.WriteFile(t, blocker, "not a directory")
	dir := filepath.Join(blocker, "profiles")
	if err := CheckPersonalDestination(dir); err == nil {
		t.Fatal("file component accepted")
	}
	if err := WriteNewPersonalProfile(filepath.Join(dir, "mine.toml"), []byte("x")); err == nil {
		t.Fatal("write through a file component accepted")
	}
}

func TestPersonalDestinationRefusesSymlinkedProfiles(t *testing.T) {
	testutil.IsolatedEnv(t)
	root := t.TempDir()
	target := t.TempDir()
	profiles := filepath.Join(root, "profiles")
	if err := os.Symlink(target, profiles); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := CheckPersonalDestination(profiles); err == nil {
		t.Fatal("symlinked profiles directory accepted")
	}
	if err := WriteNewPersonalProfile(filepath.Join(profiles, "mine.toml"), []byte("x")); err == nil {
		t.Fatal("write through a symlinked profiles directory accepted")
	}
	entries, err := os.ReadDir(target)
	if err != nil || len(entries) != 0 {
		t.Fatalf("escaped writes: %v, %v", entries, err)
	}
}

func TestWriteNewPersonalProfileRefusesSymlinkedFile(t *testing.T) {
	testutil.IsolatedEnv(t)
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "original")
	testutil.WriteFile(t, outside, "original")
	target := filepath.Join(dir, "mine.toml")
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := WriteNewPersonalProfile(target, []byte("replacement")); err == nil {
		t.Fatal("symlinked file accepted")
	}
	if b, _ := os.ReadFile(outside); string(b) != "original" {
		t.Fatalf("escaped write: %q", b)
	}
}

func TestWriteNewProfileRefusesProjectSymlinks(t *testing.T) {
	testutil.IsolatedEnv(t)
	for _, component := range []string{".ccshelf", "profiles", "file"} {
		t.Run(component, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			p := filepath.Join(root, ".ccshelf", "profiles", "mine.toml")
			link := filepath.Join(root, ".ccshelf")
			dst := outside
			if component == "profiles" {
				link = filepath.Dir(p)
			}
			if component == "file" {
				link = p
				dst = filepath.Join(outside, "original")
				testutil.WriteFile(t, dst, "original")
			}
			if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(dst, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := WriteNewProfile(p, []byte("replacement")); err == nil {
				t.Fatal("symlink write accepted")
			}
			if component == "file" {
				b, _ := os.ReadFile(dst)
				if string(b) != "original" {
					t.Fatalf("escaped write: %q", b)
				}
			} else {
				entries, err := os.ReadDir(outside)
				if err != nil || len(entries) != 0 {
					t.Fatalf("escaped write: %v, %v", entries, err)
				}
			}
		})
	}
}

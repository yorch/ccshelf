package update

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writeExe(t *testing.T, dir, name, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// Both strategies are run on every OS: the Unix one (atomic rename) and the
// Windows one (rename the old file aside) only use renames, which behave the
// same on a file that is not running.
var strategies = map[string]struct {
	install func(newPath, exe, old string) error
	swap    func(exe, old string) error
}{
	"atomic": {installAtomic, swapAtomic},
	"aside":  {installAside, swapAside},
}

func TestInstallStrategies(t *testing.T) {
	for name, s := range strategies {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := writeExe(t, dir, "ccshelf", "old", 0o755)
			nw := writeExe(t, dir, "new", "new", 0o755)
			old := exe + BackupSuffix
			if err := s.install(nw, exe, old); err != nil {
				t.Fatal(err)
			}
			if readFile(t, exe) != "new" || readFile(t, old) != "old" {
				t.Errorf("exe=%q backup=%q", readFile(t, exe), readFile(t, old))
			}
			if _, err := os.Stat(nw); !os.IsNotExist(err) {
				t.Error("the temporary file must be gone after the move")
			}
			// A second update replaces the previous backup.
			nw2 := writeExe(t, dir, "new2", "newer", 0o755)
			if err := s.install(nw2, exe, old); err != nil {
				t.Fatal(err)
			}
			if readFile(t, exe) != "newer" || readFile(t, old) != "new" {
				t.Errorf("after the second update: exe=%q backup=%q", readFile(t, exe), readFile(t, old))
			}
		})
	}
}

func TestInstallKeepsExecutableMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes")
	}
	dir := t.TempDir()
	exe := writeExe(t, dir, "ccshelf", "old", 0o755)
	f, err := CreateTemp(dir, "linux")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("new")
	if fi, _ := os.Stat(f.Name()); fi.Mode().Perm() != 0o600 {
		t.Errorf("the temporary file starts private, got %v", fi.Mode().Perm())
	}
	if err := Finish(f, exe); err != nil {
		t.Fatal(err)
	}
	if err := Install(f.Name(), exe); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{exe, exe + BackupSuffix} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s mode = %v %v, want 0755", filepath.Base(p), fi.Mode().Perm(), err)
		}
	}
	// A binary that was not executable becomes 0755 (it has to run).
	exe2 := writeExe(t, dir, "plain", "old", 0o644)
	f2, _ := CreateTemp(dir, "linux")
	if err := Finish(f2, exe2); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(f2.Name()); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", fi.Mode().Perm())
	}
	// A narrower mode of the old binary is preserved.
	exe3 := writeExe(t, dir, "narrow", "old", 0o700)
	if err := os.Chmod(exe3, 0o700); err != nil {
		t.Fatal(err)
	}
	f3, _ := CreateTemp(dir, "linux")
	if err := Finish(f3, exe3); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(f3.Name()); fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700", fi.Mode().Perm())
	}
}

func TestInstallFailureLeavesOldBinary(t *testing.T) {
	for name, s := range strategies {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := writeExe(t, dir, "ccshelf", "old", 0o755)
			err := s.install(filepath.Join(dir, "does-not-exist"), exe, exe+BackupSuffix)
			if err == nil {
				t.Fatal("installing a missing file must fail")
			}
			if readFile(t, exe) != "old" {
				t.Errorf("exe = %q after a failed install, want the old binary", readFile(t, exe))
			}
		})
	}
}

func TestSwapStrategies(t *testing.T) {
	for name, s := range strategies {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := writeExe(t, dir, "ccshelf", "v2", 0o755)
			old := writeExe(t, dir, "ccshelf"+BackupSuffix, "v1", 0o755)
			if err := s.swap(exe, old); err != nil {
				t.Fatal(err)
			}
			if readFile(t, exe) != "v1" || readFile(t, old) != "v2" {
				t.Errorf("after rollback: exe=%q backup=%q", readFile(t, exe), readFile(t, old))
			}
			if _, err := os.Stat(exe + ".swap"); !os.IsNotExist(err) {
				t.Error("the swap file must be gone")
			}
			// Rolling back again returns to where we were.
			if err := s.swap(exe, old); err != nil {
				t.Fatal(err)
			}
			if readFile(t, exe) != "v2" || readFile(t, old) != "v1" {
				t.Errorf("after the second swap: exe=%q backup=%q", readFile(t, exe), readFile(t, old))
			}
		})
	}
}

func TestSwapWithoutBackupChangesNothing(t *testing.T) {
	for name, s := range strategies {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			exe := writeExe(t, dir, "ccshelf", "v2", 0o755)
			if err := s.swap(exe, exe+BackupSuffix); err == nil {
				t.Fatal("a swap without a backup must fail")
			}
			if readFile(t, exe) != "v2" {
				t.Errorf("exe = %q, want it untouched", readFile(t, exe))
			}
			if _, err := os.Stat(exe + ".swap"); !os.IsNotExist(err) {
				t.Error("no swap file may be left behind")
			}
		})
	}
}

func TestKeepBackupFallsBackToCopy(t *testing.T) {
	dir := t.TempDir()
	exe := writeExe(t, dir, "ccshelf", "binary", 0o755)
	old := exe + BackupSuffix
	if err := copyFile(exe, old); err != nil {
		t.Fatal(err)
	}
	if readFile(t, old) != "binary" {
		t.Error("copy content")
	}
	if err := copyFile(exe, old); err == nil {
		t.Error("copyFile must not overwrite an existing file")
	}
	if err := copyFile(filepath.Join(dir, "missing"), filepath.Join(dir, "x")); err == nil {
		t.Error("copying a missing file must fail")
	}
}

func TestCheckWritable(t *testing.T) {
	dir := t.TempDir()
	if err := CheckWritable(dir); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("the probe left %d file(s) behind", len(entries))
	}
	var nw *NotWritableError
	if err := CheckWritable(filepath.Join(dir, "missing")); !errors.As(err, &nw) {
		t.Errorf("a missing directory = %v, want NotWritableError", err)
	}
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	err := CheckWritable(ro)
	if !errors.As(err, &nw) || nw.Dir != ro {
		t.Fatalf("a read-only directory = %v, want NotWritableError for %s", err, ro)
	}
	if !strings.Contains(err.Error(), ro) {
		t.Errorf("message lacks the directory: %v", err)
	}
}

func TestResolveExecutable(t *testing.T) {
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	real := writeExe(t, root, "ccshelf", "x", 0o755)
	got, wasLink, err := ResolveExecutable(real)
	if err != nil || got != real || wasLink {
		t.Errorf("plain file = %q %v %v", got, wasLink, err)
	}
	if _, _, err := ResolveExecutable("relative/ccshelf"); err == nil {
		t.Error("a relative path must be refused")
	}
	if _, _, err := ResolveExecutable(filepath.Join(root, "missing")); err == nil {
		t.Error("a missing file must be refused")
	}
	if runtime.GOOS == "windows" {
		return
	}
	linkDir := filepath.Join(root, "links")
	if err := os.Mkdir(linkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "ccshelf")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got, wasLink, err = ResolveExecutable(link)
	if err != nil || got != real || !wasLink {
		t.Errorf("link into a directory we own = %q %v %v, want %q", got, wasLink, err, real)
	}
	// A link whose target directory we cannot write is refused.
	if os.Geteuid() != 0 {
		ro := filepath.Join(root, "ro")
		if err := os.Mkdir(ro, 0o700); err != nil {
			t.Fatal(err)
		}
		inner := writeExe(t, ro, "ccshelf", "x", 0o755)
		if err := os.Chmod(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
		l2 := filepath.Join(linkDir, "ro-link")
		if err := os.Symlink(inner, l2); err != nil {
			t.Fatal(err)
		}
		if _, _, err := ResolveExecutable(l2); err == nil {
			t.Error("a link into a read-only directory must be refused")
		}
	}
	// A link to a directory owned by someone else is refused: /usr/bin is
	// root's on every Unix.
	if os.Geteuid() != 0 {
		if _, err := os.Stat("/bin/sh"); err == nil {
			l3 := filepath.Join(linkDir, "sh-link")
			if err := os.Symlink("/bin/sh", l3); err != nil {
				t.Fatal(err)
			}
			if _, _, err := ResolveExecutable(l3); err == nil {
				t.Error("a link into a directory owned by another user must be refused")
			}
		}
	}
	dangling := filepath.Join(linkDir, "dangling")
	if err := os.Symlink(filepath.Join(root, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveExecutable(dangling); err == nil {
		t.Error("a dangling link must be refused")
	}
}

func TestRemoveStale(t *testing.T) {
	dir := t.TempDir()
	cutoff := time.Now().Add(-time.Hour)
	stale := writeExe(t, dir, tempPrefix+"old.tmp", "x", 0o600)
	fresh := writeExe(t, dir, tempPrefix+"new.tmp", "x", 0o600)
	backup := writeExe(t, dir, "ccshelf"+BackupSuffix, "x", 0o755)
	other := writeExe(t, dir, "unrelated", "x", 0o600)
	old := time.Now().Add(-3 * time.Hour)
	for _, p := range []string{stale, backup, other} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	RemoveStale(dir, cutoff)
	for p, want := range map[string]bool{stale: false, fresh: true, backup: true, other: true} {
		_, err := os.Stat(p)
		if (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(p), err == nil, want)
		}
	}
	RemoveStale(filepath.Join(dir, "missing"), cutoff) // must not panic
}

func TestCreateTempName(t *testing.T) {
	dir := t.TempDir()
	f, err := CreateTemp(dir, "windows")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.Close()
	if !strings.HasSuffix(f.Name(), ".exe") || filepath.Dir(f.Name()) != dir {
		t.Errorf("windows temp name = %s", f.Name())
	}
	g, _ := CreateTemp(dir, "linux")
	defer os.Remove(g.Name())
	g.Close()
	if strings.HasSuffix(g.Name(), ".exe") || !strings.HasPrefix(filepath.Base(g.Name()), tempPrefix) {
		t.Errorf("unix temp name = %s", g.Name())
	}
}

//go:build !windows

package update

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The ownership rule is what keeps a link planted in a place we can write from
// steering the update at somebody else's directory. Writability alone must not
// be enough: here the directory is ours and writable, and only the ownership
// check can refuse it.
func TestResolveExecutableRefusesADirectoryWeDoNotOwn(t *testing.T) {
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	real := writeExe(t, root, "ccshelf", "x", 0o755)
	link := filepath.Join(t.TempDir(), "ccshelf")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if got, _, err := ResolveExecutable(link); err != nil || got != real {
		t.Fatalf("our own writable directory = %q %v", got, err)
	}
	old := geteuid
	geteuid = func() int { return os.Geteuid() + 4242 }
	t.Cleanup(func() { geteuid = old })
	_, wasLink, err := ResolveExecutable(link)
	if err == nil || !wasLink || !strings.Contains(err.Error(), "not one you own") || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("a directory of another user = %v (wasLink %v), want a refusal naming ownership", err, wasLink)
	}
	// A plain (not linked) executable is not subject to the rule.
	if got, wasLink, err := ResolveExecutable(real); err != nil || wasLink || got != real {
		t.Errorf("plain file as another user = %q %v %v", got, wasLink, err)
	}
}

func TestDirOwnedByUser(t *testing.T) {
	dir := t.TempDir()
	if err := dirOwnedByUser(dir); err != nil {
		t.Errorf("own directory: %v", err)
	}
	if err := dirOwnedByUser(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing directory must be an error")
	}
	old := geteuid
	geteuid = func() int { return os.Geteuid() + 1 }
	defer func() { geteuid = old }()
	if err := dirOwnedByUser(dir); err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("someone else's directory = %v, want a permission error", err)
	}
}

// dirUntouched pins a directory's modification time to the past and returns a
// function that reports whether anything was created or removed in it since.
func dirUntouched(t *testing.T, dir string) func() bool {
	t.Helper()
	past := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(dir, past, past); err != nil {
		t.Fatal(err)
	}
	return func() bool {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		return fi.ModTime().Equal(past)
	}
}

// --check, --dry-run, the cached notice and a rollback plan resolve the
// executable; for a symlinked one they must not create (and delete) a probe
// file in the link's target directory.
func TestResolvingASymlinkedExecutableWritesNothing(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	targetDir := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(targetDir); err == nil {
		targetDir = r
	}
	real := writeExe(t, targetDir, "ccshelf", string(fakeBinary("0.1.0")), 0o755)
	link := filepath.Join(fx.binDir, "ccshelf-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	fx.u.Executable = func() (string, error) { return link, nil }
	untouched := dirUntouched(t, targetDir)

	if _, _, err := ResolveExecutable(link); err != nil {
		t.Fatal(err)
	}
	if !untouched() {
		t.Fatal("ResolveExecutable wrote into the target directory")
	}
	p, err := fx.u.Discover(context.Background(), Request{})
	if err != nil || p.Exe != real {
		t.Fatalf("Discover = %+v %v", p, err)
	}
	if !untouched() {
		t.Fatal("Discover (--check and --dry-run) wrote into the target directory")
	}
	var s said
	fx.u.CachedNotice(AutoOptions{Mode: "notify", TTY: true}, s.say)
	if !untouched() {
		t.Fatal("the cached notice wrote into the target directory")
	}
	if err := os.WriteFile(real+BackupSuffix, fakeBinary("0.0.9"), 0o755); err != nil { //nolint:gosec // test
		t.Fatal(err)
	}
	untouched = dirUntouched(t, targetDir)
	if _, err := fx.u.PrepareRollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !untouched() {
		t.Fatal("PrepareRollback wrote into the target directory")
	}
}

// Writability is probed where a replacement is about to happen.
func TestApplyRefusesAnUnwritableLinkTarget(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	targetDir := filepath.Join(t.TempDir(), "real")
	if err := os.Mkdir(targetDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(targetDir); err == nil {
		targetDir = r
	}
	real := writeExe(t, targetDir, "ccshelf", string(fakeBinary("0.1.0")), 0o755)
	link := filepath.Join(fx.binDir, "ccshelf-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	fx.u.Executable = func() (string, error) { return link, nil }
	if err := os.Chmod(targetDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(targetDir, 0o700) })
	_, err := fx.apply(Request{})
	mustKind(t, err, KindNotWritable)
	if readFile(t, real) != string(fakeBinary("0.1.0")) {
		t.Error("the binary changed")
	}
}

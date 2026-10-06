package account

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/ui"
)

func TestSamePathForCaseRules(t *testing.T) {
	a, b := "/nonexistent-root/Home/.Claude", "/nonexistent-root/home/.claude"
	for goos, want := range map[string]bool{"darwin": true, "windows": true, "linux": false} {
		if got := samePathFor(goos, a, b); got != want {
			t.Errorf("samePathFor(%s) = %v, want %v", goos, got, want)
		}
	}
	if !samePathFor("linux", "/x/y/../z", "/x/z") {
		t.Error("paths must be cleaned")
	}
}

func TestInsideFoldsCase(t *testing.T) {
	home := t.TempDir()
	def := filepath.Join(home, ".claude") // does not exist, so only names count
	for goos, want := range map[string]bool{"darwin": true, "windows": true, "linux": false} {
		if goos == "linux" && runtime.GOOS == "windows" {
			// filepath.Rel compares case-insensitively on a Windows host
			// whatever goos says, so the case-sensitive rule cannot be shown.
			continue
		}
		if got := inside(goos, def, filepath.Join(home, ".Claude", "work")); got != want {
			t.Errorf("inside(%s) = %v, want %v", goos, got, want)
		}
	}
}

func TestAddRefusesDifferentlyCasedDefaultDir(t *testing.T) {
	if !foldsCase(runtime.GOOS) {
		t.Skip("the host file system is case-sensitive")
	}
	home, cfg := setup(t)
	dir := filepath.Join(home, ".Claude", "work")
	if _, err := Add(context.Background(), cfg, "work", dir, Options{}); err == nil {
		t.Fatal("a directory inside ~/.Claude was accepted")
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude")); err == nil {
		t.Error("~/.claude was created")
	}
}

func TestAddRefusesAncestorOfDefaultDir(t *testing.T) {
	home, cfg := setup(t)
	_, err := Add(context.Background(), cfg, "work", home, Options{})
	if err == nil || !strings.Contains(err.Error(), "contains Claude Code's default directory") {
		t.Errorf("ancestor of ~/.claude: %v", err)
	}
	_, err = Add(context.Background(), cfg, "work", filepath.Join(home, ".claude", "x"), Options{})
	if err == nil || !strings.Contains(err.Error(), "or inside it") {
		t.Errorf("inside ~/.claude: %v", err)
	}
}

func TestEnsureDirRefusesSymlinkFinal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	top, err := ensureDir(link, true)
	if !errors.Is(err, ErrDirInUse) || top != "" {
		t.Errorf("symlink accepted: %q %v", top, err)
	}
}

func TestAddRefusesSymlinkComponent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	home, cfg := setup(t)
	real := filepath.Join(home, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	_, err := Add(context.Background(), cfg, "work", filepath.Join(link, "acct"), Options{})
	if !errors.Is(err, ErrDirInUse) || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlinked parent accepted: %v", err)
	}
	if _, serr := os.Lstat(filepath.Join(real, "acct")); serr == nil {
		t.Error("directory created through the symlink")
	}
}

func TestSamePathResolvesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if !samePath(link, target) || !samePath(filepath.Join(link, "new"), filepath.Join(target, "new")) {
		t.Error("symlink and target must compare equal")
	}
	if samePath(target, filepath.Join(root, "other")) {
		t.Error("different paths compared equal")
	}
}

func TestAddRejectsControlCharAfterExpansion(t *testing.T) {
	home, cfg := setup(t)
	t.Setenv("EVIL", filepath.Join(home, "a\nb"))
	_, err := Add(context.Background(), cfg, "work", "$EVIL", Options{})
	if err == nil || !strings.Contains(err.Error(), "control character") {
		t.Errorf("got %v", err)
	}
	t.Setenv("EVIL", filepath.Join(home, "a\u200Bb"))
	if _, err = Add(context.Background(), cfg, "work", "$EVIL", Options{}); err == nil {
		t.Error("invisible character accepted")
	}
}

func TestRollbackRemovesCreatedParents(t *testing.T) {
	home, cfg := setup(t)
	dir := filepath.Join(home, "p1", "p2", "acct")
	blocker := filepath.Join(home, "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Saving under a regular file fails, which must undo the directories.
	_, err := Add(context.Background(), cfg, "work", dir, Options{Persist: true, ConfigPath: filepath.Join(blocker, "cfg.toml")})
	if err == nil {
		t.Fatal("expected a save failure")
	}
	if _, serr := os.Lstat(filepath.Join(home, "p1")); serr == nil {
		t.Error("parent directories created by Add were left behind")
	}
	// An existing parent stays.
	keep := filepath.Join(home, "keep")
	if err := os.Mkdir(keep, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err = Add(context.Background(), cfg, "work", filepath.Join(keep, "acct"), Options{Persist: true, ConfigPath: filepath.Join(blocker, "cfg.toml")}); err == nil {
		t.Fatal("expected a save failure")
	}
	if _, serr := os.Lstat(keep); serr != nil {
		t.Errorf("existing parent removed: %v", serr)
	}
	if _, serr := os.Lstat(filepath.Join(keep, "acct")); serr == nil {
		t.Error("account directory left behind")
	}
}

func TestRemoveCreatedStopsAtNonEmpty(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeCreated(d, filepath.Join(root, "a")); err == nil {
		t.Error("expected failure at the non-empty parent")
	}
	if _, err := os.Lstat(d); err == nil {
		t.Error("empty child not removed")
	}
	if _, err := os.Lstat(filepath.Join(root, "a")); err != nil {
		t.Error("non-empty parent removed")
	}
}

func TestPlanLinesCmdRejectsQuote(t *testing.T) {
	plan := &Plan{Name: "w", Steps: []Step{{Description: "s", Env: []string{`CLAUDE_CONFIG_DIR=C:\a"b`}, Argv: []string{"claude"}}}}
	if _, err := plan.Lines("cmd"); !errors.Is(err, ui.ErrUnquotable) {
		t.Errorf("got %v", err)
	}
	ok := &Plan{Name: "w", Steps: []Step{{Description: "s", Env: []string{`CLAUDE_CONFIG_DIR=C:\a b`}, Argv: []string{"claude"}}}}
	lines, err := ok.Lines("cmd")
	if err != nil || !strings.Contains(lines[2], `set "CLAUDE_CONFIG_DIR=C:\a b"`) {
		t.Errorf("%v %v", lines, err)
	}
}

package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func targetErr(t *testing.T, o TargetOptions) *TargetError {
	t.Helper()
	_, err := ResolveTarget(o)
	var te *TargetError
	if !errors.As(err, &te) {
		t.Fatalf("ResolveTarget(%+v) = %v, want a TargetError", o, err)
	}
	return te
}

func TestResolveTarget(t *testing.T) {
	wd := t.TempDir()
	tg, err := ResolveTarget(TargetOptions{Wd: wd})
	if err != nil || tg.Dir != filepath.Clean(wd) || !tg.Exists {
		t.Fatalf("%+v %v", tg, err)
	}
	tg, err = ResolveTarget(TargetOptions{Arg: "new/sub", Wd: wd})
	if err != nil || tg.Dir != filepath.Join(wd, "new", "sub") || tg.Exists {
		t.Fatalf("%+v %v", tg, err)
	}
	tg, err = ResolveTarget(TargetOptions{Arg: filepath.Join(wd, "abs"), Wd: "/elsewhere"})
	if err != nil || tg.Dir != filepath.Join(wd, "abs") {
		t.Fatalf("%+v %v", tg, err)
	}
}

func TestResolveTargetRefusals(t *testing.T) {
	wd := t.TempDir()
	home := filepath.Join(wd, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"..", "../x", "a/../b", `a\..\b`} {
		if te := targetErr(t, TargetOptions{Arg: arg, Wd: wd}); !te.Usage || !strings.Contains(te.Msg, "..") {
			t.Errorf("%q: %+v", arg, te)
		}
	}
	for _, arg := range []string{"a\x00b", "a\nb", "a\u202eb"} {
		if te := targetErr(t, TargetOptions{Arg: arg, Wd: wd}); !te.Usage {
			t.Errorf("%q: %+v", arg, te)
		}
	}
	root := string(filepath.Separator)
	if v := filepath.VolumeName(wd); v != "" {
		root = v + root
	}
	if te := targetErr(t, TargetOptions{Arg: root, Wd: wd}); !te.Usage || !strings.Contains(te.Msg, "root") {
		t.Errorf("root: %+v", te)
	}
	if te := targetErr(t, TargetOptions{Arg: home, Wd: wd, Home: home}); !te.Usage || !strings.Contains(te.Msg, "home") {
		t.Errorf("home: %+v", te)
	}
	if te := targetErr(t, TargetOptions{Arg: "home", Wd: wd, Home: home}); !te.Usage {
		t.Errorf("home relative: %+v", te)
	}
	if te := targetErr(t, TargetOptions{Arg: "x", Wd: ""}); te.Usage {
		t.Errorf("no working directory: %+v", te)
	}
	// A path that does not exist yet but equals the home directory.
	if te := targetErr(t, TargetOptions{Arg: filepath.Join(wd, "later"), Wd: wd, Home: filepath.Join(wd, "later")}); !te.Usage {
		t.Errorf("missing home: %+v", te)
	}
	if err := os.WriteFile(filepath.Join(wd, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if te := targetErr(t, TargetOptions{Arg: "file", Wd: wd}); te.Usage || !strings.Contains(te.Msg, "not a directory") {
		t.Errorf("file: %+v", te)
	}
	if te := targetErr(t, TargetOptions{Arg: "file/sub", Wd: wd}); te.Usage {
		t.Errorf("below a file: %+v", te)
	}
}

func TestResolveTargetToolRepository(t *testing.T) {
	wd := t.TempDir()
	for _, mod := range []string{"module github.com/yorch/ccshelf\n", "module   github.com/yorch/ccshelf // comment\n", "module \"github.com/someone/ccshelf\"\n", "// header\n\nmodule ghe.example.com/team/ccshelf\n"} {
		dir := filepath.Join(wd, strings.NewReplacer("/", "_", " ", "_", "\"", "", "\n", "").Replace(mod))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, arg := range []string{dir, filepath.Join(dir, "deep", "er")} {
			if te := targetErr(t, TargetOptions{Arg: arg, Wd: wd}); !te.Usage || !strings.Contains(te.Msg, "tool repository") {
				t.Errorf("%q: %+v", arg, te)
			}
		}
	}
	other := filepath.Join(wd, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "go.mod"), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveTarget(TargetOptions{Arg: other, Wd: wd}); err != nil {
		t.Errorf("another module is not the tool repository: %v", err)
	}
}

func TestResolveTargetSymlinks(t *testing.T) {
	wd := t.TempDir()
	real := filepath.Join(wd, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(wd, "link")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	for _, arg := range []string{"link", "link/sub", filepath.Join(wd, "link")} {
		if te := targetErr(t, TargetOptions{Arg: arg, Wd: wd}); te.Usage || !strings.Contains(te.Msg, "symbolic link") {
			t.Errorf("%q: %+v", arg, te)
		}
	}
	// A link above an absolute path is the caller's explicit choice.
	if _, err := ResolveTarget(TargetOptions{Arg: filepath.Join(wd, "link", "sub"), Wd: "/elsewhere"}); err != nil {
		t.Errorf("absolute path below a link: %v", err)
	}
	// The working directory itself may be reached through a link.
	if tg, err := ResolveTarget(TargetOptions{Wd: filepath.Join(wd, "link")}); err != nil || !tg.Exists {
		t.Errorf("working directory through a link: %+v %v", tg, err)
	}
}

func TestTargetOpenCreate(t *testing.T) {
	wd := t.TempDir()
	tg, err := ResolveTarget(TargetOptions{Arg: "a/b", Wd: wd})
	if err != nil {
		t.Fatal(err)
	}
	f, c, err := tg.Open()
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	if _, err := f.Lstat("x"); !errors.Is(err, os.ErrNotExist) {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(wd, "a")); err == nil {
		t.Fatal("Open created the directory")
	}
	wf, wc, err := tg.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer wc.Close()
	if err := wf.WriteNew("x", []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(wd, "a", "b", "x")); string(b) != "y" {
		t.Error("not written")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(filepath.Join(wd, "a", "b")); st.Mode().Perm() != 0o755 {
			t.Errorf("mode %v", st.Mode().Perm())
		}
	}
	if !tg.Exists {
		t.Error("Exists is false after Create")
	}
}

func TestTargetOpenDetectsASwap(t *testing.T) {
	wd := t.TempDir()
	tg, err := ResolveTarget(TargetOptions{Arg: "t", Wd: wd})
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(wd, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, tg.Dir); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	tg.Exists = true // as if the directory had existed when it was resolved
	if _, _, err := tg.Open(); err == nil {
		t.Error("a target swapped for a link was opened")
	}
}

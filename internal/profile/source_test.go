package profile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKindString(t *testing.T) {
	if KindInvalid != 0 || KindInvalid.Valid() || KindInvalid.String() != "invalid" || !KindPersonal.Valid() || !KindProject.Valid() || !KindOrg.Valid() || Kind(4).Valid() || Kind(-1).Valid() {
		t.Error("Kind zero value must be invalid (B8)")
	}
	if KindPersonal.String() != "personal" || KindProject.String() != "project" || KindOrg.String() != "org" || !strings.HasPrefix(Kind(9).String(), "kind(") {
		t.Error("Kind.String")
	}
}

func TestPersonalDir(t *testing.T) {
	isolate(t)
	d, err := PersonalDir()
	if err != nil || filepath.Base(d) != "profiles" || filepath.Base(filepath.Dir(d)) != "ccshelf" {
		t.Errorf("PersonalDir = %q, %v", d, err)
	}
}

func TestDirSource(t *testing.T) {
	root := fixture(t, "tree", "org")
	s := DirSource(KindOrg, filepath.Join(root, "profiles"))
	if s.Kind() != KindOrg || s.Commit() != "" || s.Root() != root || !strings.HasPrefix(s.ID(), "dir:") {
		t.Errorf("source: %v %v %v %v", s.Kind(), s.Commit(), s.Root(), s.ID())
	}
	names, err := s.Names()
	if err != nil || strings.Join(names, ",") != "base,frontend,old" {
		t.Errorf("Names = %v, %v", names, err)
	}
	f, err := s.Open("frontend")
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "frontend" || f.Manifest.Owner != "@web-platform" || len(f.Raw) == 0 || f.Source != s || filepath.Base(f.Path) != "frontend.toml" {
		t.Errorf("file: %+v", f)
	}
}

func TestDirSourceMissingAndRelative(t *testing.T) {
	s := DirSource(KindPersonal, filepath.Join(t.TempDir(), "nope"))
	if names, err := s.Names(); err != nil || len(names) != 0 {
		t.Errorf("missing dir: %v %v", names, err)
	}
	if _, err := s.Open("x"); err == nil {
		t.Error("open on missing dir")
	}
	rel := DirSource(KindPersonal, "testdata/tree/org/profiles")
	if !filepath.IsAbs(rel.Root()) {
		t.Error("relative dir not made absolute")
	}
}

func TestDirSourceOpenRejects(t *testing.T) {
	root := mk(t, map[string]string{
		"profiles/good.toml":     "name = \"good\"\n",
		"profiles/mismatch.toml": "name = \"other\"\n",
		"profiles/Upper.toml":    "name = \"upper\"\n",
		"profiles/.hidden.toml":  "name = \"hidden\"\n",
		"profiles/note.txt":      "x",
		"profiles/sub/x.toml":    "name = \"x\"\n",
		"secret.toml":            "name = \"secret\"\n",
	})
	s := src(KindOrg, root)
	names, _ := s.Names()
	if strings.Join(names, ",") != "Upper,good,mismatch" {
		t.Errorf("Names = %v", names)
	}
	for _, bad := range []string{"../secret", "Upper", "a/b", "", "sub", `..\secret`} {
		if _, err := s.Open(bad); err == nil {
			t.Errorf("Open(%q) accepted", bad)
		}
	}
	_, err := s.Open("mismatch")
	mustErrContain(t, err, "does not match the file name")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Error("error should wrap *ValidationError")
	}
}

func TestDirSourceSymlinks(t *testing.T) {
	root := mk(t, map[string]string{
		"profiles/real.toml": "name = \"real\"\n",
		"outside/evil.toml":  "name = \"evil\"\n",
	})
	if err := os.Symlink(filepath.Join(root, "profiles", "real.toml"), filepath.Join(root, "profiles", "inside.toml")); err != nil {
		t.Skip("symlinks unavailable: " + err.Error())
	}
	if err := os.Symlink(filepath.Join(root, "outside", "evil.toml"), filepath.Join(root, "profiles", "evil.toml")); err != nil {
		t.Skip("symlinks unavailable: " + err.Error())
	}
	s := src(KindOrg, root)
	_, err := s.Open("evil")
	if !errors.Is(err, ErrPath) {
		t.Errorf("escaping symlink: %v", err)
	}
	// B7: symlinks are refused even when they point inside the source
	_, err = s.Open("inside")
	if !errors.Is(err, ErrPath) {
		t.Errorf("in-root symlink: %v", err)
	}
}

func TestDirSourceNotRegular(t *testing.T) {
	root := mk(t, map[string]string{"profiles/x.toml/inner": "y"})
	s := src(KindOrg, root)
	if names, _ := s.Names(); len(names) != 0 {
		t.Errorf("directory listed as profile: %v", names)
	}
	if _, err := s.Open("x"); err == nil {
		t.Error("directory opened as profile")
	}
}

func TestReadConfined(t *testing.T) {
	root := mk(t, map[string]string{"a/ok.md": "hi", "a/big.md": strings.Repeat("x", 100), "out.md": "secret"})
	if b, _, err := readConfined(filepath.Join(root, "a"), "ok.md", 10); err != nil || string(b) != "hi" {
		t.Errorf("ok: %q %v", b, err)
	}
	if _, _, err := readConfined(filepath.Join(root, "a"), "big.md", 10); err == nil {
		t.Error("size limit")
	}
	if _, _, err := readConfined(filepath.Join(root, "nope"), "x", 10); err == nil {
		t.Error("missing root")
	}
	if _, _, err := readConfined(filepath.Join(root, "a"), "missing.md", 10); err == nil {
		t.Error("missing file")
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(root, "out.md"), filepath.Join(root, "a", "link.md")); err == nil {
			if _, _, err := readConfined(filepath.Join(root, "a"), "link.md", 100); !errors.Is(err, ErrPath) {
				t.Errorf("symlink escape: %v", err)
			}
		}
	}
}

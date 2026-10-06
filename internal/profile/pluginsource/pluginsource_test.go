package pluginsource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/claude"
	"github.com/ccshelf/ccshelf/internal/profile"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func list(ps ...claude.Plugin) func(context.Context) ([]claude.Plugin, error) {
	return func(context.Context) ([]claude.Plugin, error) { return ps, nil }
}

func TestNewValidation(t *testing.T) {
	ok := list()
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"no marketplace", Options{Plugin: "audit", Installed: ok}, "name@marketplace"},
		{"empty", Options{Installed: ok}, "name@marketplace"},
		{"dash", Options{Plugin: "-x@y", Installed: ok}, "name@marketplace"},
		{"space", Options{Plugin: "a b@y", Installed: ok}, "name@marketplace"},
		{"no installed func", Options{Plugin: "a@b"}, "Installed"},
		{"abs path", Options{Plugin: "a@b", Path: "/etc", Installed: ok}, "relative"},
		{"dotdot", Options{Plugin: "a@b", Path: "x/../..", Installed: ok}, ".."},
		{"dot", Options{Plugin: "a@b", Path: ".", Installed: ok}, "folder"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.opts); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPrepareAndRead(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "profiles/base.toml", "name = \"base\"\ndescription = \"d\"\n")
	write(t, dir, "mcp/registry.toml", "[servers.docs]\ntype = \"http\"\nurl = \"https://mcp.example.com/docs\"\n")
	s, err := New(Options{Plugin: "acme-profiles@acme", Installed: list(
		claude.Plugin{ID: "other@acme", InstallPath: t.TempDir()},
		claude.Plugin{ID: "acme-profiles@acme", Version: "1.2.3", InstallPath: dir, Enabled: true},
	)})
	if err != nil {
		t.Fatal(err)
	}
	if s.Commit() != "" || s.Root() != "" {
		t.Error("Commit and Root must be empty before Prepare")
	}
	if _, err := s.Names(); !errors.Is(err, ErrNotPrepared) {
		t.Errorf("Names: %v", err)
	}
	if _, err := s.Open("base"); !errors.Is(err, ErrNotPrepared) {
		t.Errorf("Open: %v", err)
	}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.ID() != "plugin:acme-profiles@acme" || s.Locator() != s.ID() || s.Plugin() != "acme-profiles@acme" {
		t.Errorf("ID = %q", s.ID())
	}
	if s.Commit() != "plugin:1.2.3" || s.Ref() != "1.2.3" || s.Kind() != profile.KindOrg {
		t.Errorf("Commit = %q Ref = %q", s.Commit(), s.Ref())
	}
	if got := s.ProtectedPluginIDs(); len(got) != 1 || got[0] != "acme-profiles@acme" {
		t.Errorf("ProtectedPluginIDs = %v", got)
	}
	names, err := s.Names()
	if err != nil || len(names) != 1 || names[0] != "base" {
		t.Fatalf("Names = %v, %v", names, err)
	}
	f, err := s.Open("base")
	if err != nil || f.Source != profile.Source(s) {
		t.Fatalf("Open: %v %v", f, err)
	}
	if _, err := s.Open("../x"); err == nil {
		t.Error("an invalid name must fail")
	}
	r, err := profile.Resolve("base", []profile.Source{s}, profile.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(profile.PortableSourceID(s), dir) {
		t.Error("the source id must not contain a machine path")
	}
	if r.Closure.Hash == "" {
		t.Error("empty closure")
	}
}

func TestUnversioned(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", InstallPath: dir})})
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Commit() != "plugin:unversioned" {
		t.Errorf("Commit = %q", s.Commit())
	}
	if names, err := s.Names(); err != nil || len(names) != 0 {
		t.Errorf("a plugin without profiles: %v %v", names, err)
	}
}

func TestCustomPath(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "data/profiles/x.toml", "name = \"x\"\ndescription = \"d\"\n")
	s, _ := New(Options{Plugin: "a@b", Path: "data/profiles", Installed: list(claude.Plugin{ID: "a@b", InstallPath: dir})})
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(s.Root()) != "data" {
		t.Errorf("Root = %s", s.Root())
	}
	if names, _ := s.Names(); len(names) != 1 {
		t.Errorf("names = %v", names)
	}
}

func TestPrepareErrors(t *testing.T) {
	ctx := context.Background()
	s, _ := New(Options{Plugin: "a@b", Installed: list()})
	err := s.Prepare(ctx)
	if !errors.Is(err, ErrNotInstalled) || !strings.Contains(err.Error(), "/plugin install a@b") {
		t.Errorf("not installed: %v", err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b"})})
	if err := s.Prepare(ctx); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("no install path: %v", err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: func(context.Context) ([]claude.Plugin, error) { return nil, errors.New("boom") }})
	if err := s.Prepare(ctx); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("list error: %v", err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", InstallPath: "rel/path"})})
	if err := s.Prepare(ctx); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Errorf("relative: %v", err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", InstallPath: filepath.Join(t.TempDir(), "gone")})})
	if err := s.Prepare(ctx); err == nil || !strings.Contains(err.Error(), "/plugin install a@b") {
		t.Errorf("missing dir: %v", err)
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", InstallPath: file})})
	if err := s.Prepare(ctx); err == nil {
		t.Error("file as install path must fail")
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	outside := t.TempDir()
	write(t, outside, "x.toml", "name = \"x\"\ndescription = \"d\"\n")
	// profiles folder is a symlink out of the install dir
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "profiles")); err != nil {
		t.Fatal(err)
	}
	s, _ := New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", InstallPath: dir})})
	if err := s.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("profiles symlink: %v", err)
	}
	// root (parent of the profiles folder) is a symlink out of the install dir
	dir2 := t.TempDir()
	write(t, outside, "profiles/y.toml", "name = \"y\"\ndescription = \"d\"\n")
	if err := os.Symlink(outside, filepath.Join(dir2, "data")); err != nil {
		t.Fatal(err)
	}
	s, _ = New(Options{Plugin: "a@b", Path: "data/profiles", Installed: list(claude.Plugin{ID: "a@b", InstallPath: dir2})})
	if err := s.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("root symlink: %v", err)
	}
	// a symlinked install dir that is itself fine is accepted
	real := t.TempDir()
	write(t, real, "profiles/z.toml", "name = \"z\"\ndescription = \"d\"\n")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", InstallPath: link})})
	if err := s.Prepare(context.Background()); err != nil {
		t.Errorf("symlinked install dir: %v", err)
	}
	// a profile file that is a symlink out of the folder is rejected by the profile package
	dir3 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir3, "profiles"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "x.toml"), filepath.Join(dir3, "profiles", "x.toml")); err != nil {
		t.Fatal(err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", InstallPath: dir3})})
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open("x"); err == nil {
		t.Error("a profile symlinked out of the plugin must be rejected")
	}
}

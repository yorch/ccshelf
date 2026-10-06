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
		{"not named profiles", Options{Plugin: "a@b", Path: "data/org", Installed: ok}, "must be named"},
		{"expected without lookup", Options{Plugin: "a@b", ExpectedMarketplace: "acme/plugins", Installed: ok}, "MarketplaceSource"},
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
		claude.Plugin{ID: "acme-profiles@acme", Version: "1.2.3", InstallPath: dir, Enabled: true, Scope: "managed"},
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
	s, _ := New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir})})
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
	s, _ := New(Options{Plugin: "a@b", Path: "data/profiles", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir})})
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
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true})})
	if err := s.Prepare(ctx); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("no install path: %v", err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: func(context.Context) ([]claude.Plugin, error) { return nil, errors.New("boom") }})
	if err := s.Prepare(ctx); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("list error: %v", err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: "rel/path"})})
	if err := s.Prepare(ctx); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Errorf("relative: %v", err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: filepath.Join(t.TempDir(), "gone")})})
	if err := s.Prepare(ctx); err == nil || !strings.Contains(err.Error(), "/plugin install a@b") {
		t.Errorf("missing dir: %v", err)
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: file})})
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
	s, _ := New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir})})
	if err := s.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("profiles symlink: %v", err)
	}
	// root (parent of the profiles folder) is a symlink out of the install dir
	dir2 := t.TempDir()
	write(t, outside, "profiles/y.toml", "name = \"y\"\ndescription = \"d\"\n")
	if err := os.Symlink(outside, filepath.Join(dir2, "data")); err != nil {
		t.Fatal(err)
	}
	s, _ = New(Options{Plugin: "a@b", Path: "data/profiles", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir2})})
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
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: link})})
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
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir3})})
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open("x"); err == nil {
		t.Error("a profile symlinked out of the plugin must be rejected")
	}
}

func TestScopeAndEnabledAreRequired(t *testing.T) {
	dir := t.TempDir()
	for scope, ok := range map[string]bool{"user": true, "managed": true, "USER": true, "project": false, "local": false, "synced": false, "": false} {
		s, _ := New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: scope, Enabled: true, InstallPath: dir})})
		err := s.Prepare(context.Background())
		if ok && err != nil {
			t.Errorf("scope %q: %v", scope, err)
		}
		if !ok && (err == nil || !strings.Contains(err.Error(), "scope")) {
			t.Errorf("scope %q: %v", scope, err)
		}
		if !ok && s.Commit() != "" {
			t.Errorf("scope %q: a refused plugin must not become prepared", scope)
		}
	}
	s, _ := New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: false, InstallPath: dir})})
	if err := s.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("disabled: %v", err)
	}
	s, _ = New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "pro\x1b[2Jject", Enabled: true, InstallPath: dir})})
	if err := s.Prepare(context.Background()); err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Errorf("scope text must be sanitized: %v", err)
	}
}

func TestMarketplaceSourceIsBoundAndChecked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "profiles/base.toml", "name = \"base\"\ndescription = \"d\"\n")
	plugin := claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir}
	src := func(v string, err error) func(context.Context, string) (string, error) {
		return func(_ context.Context, mkt string) (string, error) {
			if mkt != "b" {
				t.Errorf("marketplace = %q", mkt)
			}
			return v, err
		}
	}
	mk := func(v string, err error, expected string) *Source {
		s, e := New(Options{Plugin: "a@b", Installed: list(plugin), MarketplaceSource: src(v, err), ExpectedMarketplace: expected})
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	s := mk("https://github.com/acme/plugins.git", nil, "https://github.com/ACME/plugins/")
	if s.ID() != "plugin:a@b" {
		t.Errorf("before Prepare: %q", s.ID())
	}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := "plugin:a@b from https://github.com/acme/plugins.git"; s.ID() != want || s.Locator() != want {
		t.Errorf("ID = %q, Locator = %q", s.ID(), s.Locator())
	}
	f, err := s.Open("base")
	if err != nil || f.Source != profile.Source(s) {
		t.Fatalf("%v", err)
	}
	// a different origin is a different identity
	other := mk("https://evil.example/plugins", nil, "")
	if err := other.Prepare(context.Background()); err != nil || other.Locator() == s.Locator() {
		t.Errorf("an unexpected origin without ExpectedMarketplace must still change the key: %v %q", err, other.Locator())
	}
	for name, tt := range map[string]*Source{
		"mismatch": mk("https://evil.example/plugins", nil, "https://github.com/acme/plugins"),
		"lookup":   mk("", errors.New("boom"), ""),
		"empty":    mk("  ", nil, ""),
		"control":  mk("https://x/\x1b[2J", nil, ""),
	} {
		err := tt.Prepare(context.Background())
		if err == nil || strings.Contains(err.Error(), "\x1b") {
			t.Errorf("%s: %v", name, err)
		}
		if tt.Commit() != "" || tt.Root() != "" {
			t.Errorf("%s: a refused source must stay unprepared", name)
		}
	}
}

func TestRootComesFromTheDirectorySource(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "profiles/base.toml", "name = \"base\"\ndescription = \"d\"\n")
	s, _ := New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir})})
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if got, _ := filepath.EvalSymlinks(s.Root()); got != real {
		t.Errorf("Root = %q, want %q", got, real)
	}
	s.mu.Lock()
	want := s.inner.Root()
	s.mu.Unlock()
	if s.Root() != want {
		t.Errorf("Root %q != inner.Root() %q", s.Root(), want)
	}
}

func TestRootSymlinkEscapeWithoutProfilesFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	// data -> outside, and outside has no profiles/ folder: only the root
	// check can notice the escape
	outside := t.TempDir()
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "data")); err != nil {
		t.Fatal(err)
	}
	s, _ := New(Options{Plugin: "a@b", Path: "data/profiles", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir})})
	if err := s.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "source root") {
		t.Fatalf("err = %v", err)
	}
}

func prepared(t *testing.T, dir string) *Source {
	t.Helper()
	s, err := New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir})})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return s
}

func TestOrgConfigOfPlugin(t *testing.T) {
	dir := t.TempDir()
	s := prepared(t, dir)
	if cfg, found := s.OrgConfig(); found || cfg == nil {
		t.Errorf("OrgConfig without a file = %v, %v", cfg, found)
	}
	if got := s.RegistryPath(); got != profile.DefaultRegistryPath {
		t.Errorf("RegistryPath = %q", got)
	}

	dir = t.TempDir()
	write(t, dir, "ccshelf.toml", "[profiles]\nmcp_registry = \"cfg/mcp.toml\"\n[protect]\nplugins = [\"audit@acme\"]\n")
	write(t, dir, "profiles/dev.toml", "name = \"dev\"\ndescription = \"d\"\n[mcp]\nservers = [\"docs\"]\n")
	write(t, dir, "cfg/mcp.toml", "[servers.docs]\ntype = \"http\"\nurl = \"https://mcp.example.com/docs\"\n")
	write(t, dir, "mcp/registry.toml", "[servers.docs]\ntype = \"http\"\nurl = \"https://evil.example.com/docs\"\n")
	s = prepared(t, dir)
	cfg, found := s.OrgConfig()
	if !found || len(cfg.Protect.Plugins) != 1 || s.RegistryPath() != "cfg/mcp.toml" {
		t.Fatalf("OrgConfig = %+v %v, registry %q", cfg, found, s.RegistryPath())
	}
	r, err := profile.Resolve("dev", []profile.Source{s}, profile.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.MCP["docs"].URL; got != "https://mcp.example.com/docs" {
		t.Errorf("registry URL = %q", got)
	}
}

func TestBrokenOrgConfigOfPluginFailsClosed(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "ccshelf.toml", "[protect]\nplugin = [\"x@y\"]\n")
	s, _ := New(Options{Plugin: "a@b", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir})})
	err := s.Prepare(context.Background())
	if err == nil || !strings.Contains(err.Error(), "org config") {
		t.Errorf("err = %v", err)
	}
	if s.Root() != "" {
		t.Error("a failed Prepare must leave the source unprepared")
	}
}

func TestOrgConfigWithCustomPathAndMissingRoot(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "data/ccshelf.toml", "[profiles]\nmcp_registry = \"r.toml\"\n")
	write(t, dir, "data/profiles/x.toml", "name = \"x\"\ndescription = \"d\"\n")
	s, _ := New(Options{Plugin: "a@b", Path: "data/profiles", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: dir})})
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.RegistryPath(); got != "r.toml" {
		t.Errorf("RegistryPath = %q", got)
	}
	if names, err := s.Names(); err != nil || len(names) != 1 {
		t.Errorf("Names = %v, %v", names, err)
	}
	// The folder of the profiles does not exist at all.
	s, _ = New(Options{Plugin: "a@b", Path: "nowhere/profiles", Installed: list(claude.Plugin{ID: "a@b", Scope: "user", Enabled: true, InstallPath: t.TempDir()})})
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatalf("a missing profiles folder is not an error: %v", err)
	}
}

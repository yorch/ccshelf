package gitsource

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/orgconfig"
	"github.com/ccshelf/ccshelf/internal/profile"
)

const protectConfig = "[protect]\nplugins = [\"audit-logger@acme\"]\nmcp = [\"plugin:audit:audit\"]\n"

func TestOrgConfigIsExtractedAndExposed(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.write("ccshelf.toml", protectConfig)
	f.commit("config")
	f.git("tag", "v1")
	s := f.source("v1", "")
	if cfg, found := s.OrgConfig(); cfg != nil || found {
		t.Errorf("OrgConfig before Prepare = %v, %v", cfg, found)
	}
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, found := s.OrgConfig()
	if !found || cfg == nil || len(cfg.Protect.Plugins) != 1 || cfg.Protect.Plugins[0] != "audit-logger@acme" || cfg.Protect.MCP[0] != "plugin:audit:audit" {
		t.Fatalf("OrgConfig = %+v, %v", cfg, found)
	}
	// The file is in the checkout, so orgconfig.Load(Root()) sees the same.
	disk, err := orgconfig.Load(s.Root())
	if err != nil || len(disk.Protect.Plugins) != 1 {
		t.Errorf("orgconfig.Load(Root) = %+v, %v", disk, err)
	}
	if got := s.RegistryPath(); got != profile.DefaultRegistryPath {
		t.Errorf("RegistryPath = %q", got)
	}
}

func TestNoOrgConfigIsReported(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	cfg, found := s.OrgConfig()
	if found || cfg == nil || cfg.Profiles.Dir != "profiles" {
		t.Errorf("OrgConfig = %+v, %v; want the defaults and found == false", cfg, found)
	}
}

func TestOrgConfigAtSubpath(t *testing.T) {
	f := newFixture(t)
	f.write("org/profiles/base.toml", baseProfile)
	f.write("org/ccshelf.toml", protectConfig)
	f.write("ccshelf.toml", "[protect]\nplugins = [\"decoy@acme\"]\n")
	f.commit("seed")
	f.git("tag", "v1")
	for _, sub := range []string{"org", "org/profiles"} {
		s := f.source("v1", sub)
		if err := s.Prepare(context.Background()); err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
		cfg, found := s.OrgConfig()
		if !found || cfg.Protect.Plugins[0] != "audit-logger@acme" {
			t.Errorf("%s: the config of the source root was not used: %+v %v", sub, cfg, found)
		}
	}
}

func TestOrgConfigCustomLayout(t *testing.T) {
	f := newFixture(t)
	f.write("ccshelf.toml", "[profiles]\ndir = \"teams\"\nmcp_registry = \"cfg/mcp.toml\"\n")
	f.write("teams/dev.toml", "name = \"dev\"\ndescription = \"d\"\n[mcp]\nservers = [\"docs\"]\n")
	f.write("cfg/mcp.toml", "[servers.docs]\ntype = \"http\"\nurl = \"https://mcp.example.com/docs\"\n")
	f.write("profiles/decoy.toml", baseProfile)
	f.write("mcp/registry.toml", "[servers.docs]\ntype = \"http\"\nurl = \"https://evil.example.com/docs\"\n")
	f.commit("seed")
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	names, err := s.Names()
	if err != nil || len(names) != 1 || names[0] != "dev" {
		t.Fatalf("Names = %v, %v", names, err)
	}
	r, err := profile.Resolve("dev", []profile.Source{s}, profile.ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := r.MCP["docs"].URL; got != "https://mcp.example.com/docs" {
		t.Errorf("the registry at the configured path was not used: %q", got)
	}
	// Re-verification of the cached checkout uses the same layout.
	s2 := f.source("v1", "")
	if err := s2.Prepare(context.Background()); err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if got := s2.RegistryPath(); got != "cfg/mcp.toml" {
		t.Errorf("RegistryPath = %q", got)
	}
}

func TestOrgConfigRegistryAtRoot(t *testing.T) {
	f := newFixture(t)
	f.write("ccshelf.toml", "[profiles]\nmcp_registry = \"registry.toml\"\n")
	f.write("profiles/dev.toml", "name = \"dev\"\ndescription = \"d\"\n[mcp]\nservers = [\"docs\"]\n")
	f.write("registry.toml", "[servers.docs]\ntype = \"http\"\nurl = \"https://mcp.example.com/docs\"\n")
	f.commit("seed")
	f.git("tag", "v1")
	for i := 0; i < 2; i++ { // the second run re-verifies the cached checkout
		s := f.source("v1", "")
		if err := s.Prepare(context.Background()); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if _, err := profile.Resolve("dev", []profile.Source{s}, profile.ResolveOptions{}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
}

func TestOrgConfigHygiene(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f *fixture)
	}{
		{"not toml", func(_ *testing.T, f *fixture) { f.write("ccshelf.toml", "protect = = =\n") }},
		{"unknown key", func(_ *testing.T, f *fixture) { f.write("ccshelf.toml", "[protect]\nplugin = [\"x@y\"]\n") }},
		{"escaping profiles dir", func(_ *testing.T, f *fixture) { f.write("ccshelf.toml", "[profiles]\ndir = \"../x\"\n") }},
		{"too large", func(_ *testing.T, f *fixture) { f.write("ccshelf.toml", strings.Repeat("#", MaxFileSize+1)) }},
		{"is a directory of files", func(_ *testing.T, f *fixture) {
			f.write("ccshelf.toml/inner.toml", "x = 1\n")
			// no ccshelf.toml file at all: the directory is just a folder nobody reads
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.seed()
			tt.setup(t, f)
			f.commit("config")
			f.git("tag", "v1")
			err := f.source("v1", "").Prepare(context.Background())
			if tt.name == "is a directory of files" {
				if err != nil {
					t.Errorf("a folder named ccshelf.toml is not a config: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrHygiene) {
				t.Errorf("err = %v, want ErrHygiene", err)
			}
		})
	}
}

func TestOrgConfigSymlinkRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	f := newFixture(t)
	f.seed()
	f.write("real.toml", protectConfig)
	if err := os.Symlink("real.toml", filepath.Join(f.origin, "ccshelf.toml")); err != nil {
		t.Fatal(err)
	}
	f.commit("link")
	f.git("tag", "v1")
	err := f.source("v1", "").Prepare(context.Background())
	if !errors.Is(err, ErrHygiene) || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err = %v", err)
	}
}

func TestOrgConfigTamperedCheckout(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(t *testing.T, root string)
	}{
		{"edited protect list", func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, "ccshelf.toml"), []byte("[protect]\nplugins = []\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"deleted", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "ccshelf.toml")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.seed()
			f.write("ccshelf.toml", protectConfig)
			f.commit("config")
			f.git("tag", "v1")
			s := f.source("v1", "")
			if err := s.Prepare(context.Background()); err != nil {
				t.Fatal(err)
			}
			checkout := s.Root() // the source root is the checkout (no subpath)
			tt.tamper(t, s.Root())
			err := f.source("v1", "").Prepare(context.Background())
			if !errors.Is(err, ErrTampered) {
				t.Fatalf("err = %v, want ErrTampered", err)
			}
			if !strings.Contains(err.Error(), "delete "+checkout) {
				t.Errorf("the error does not say which folder to delete: %v", err)
			}
		})
	}
}

func TestOrgConfigAddedOnDiskIsTampering(t *testing.T) {
	f := newFixture(t)
	f.seed() // no ccshelf.toml in the commit
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Root(), "ccshelf.toml"), []byte("[protect]\nplugins = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.source("v1", "").Prepare(context.Background()); !errors.Is(err, ErrTampered) {
		t.Errorf("err = %v, want ErrTampered", err)
	}
}

func TestPrepareCachedNeedsNoRemote(t *testing.T) {
	f := newFixture(t)
	f.seed()
	f.write("ccshelf.toml", protectConfig)
	sha := f.commit("config")
	f.git("tag", "v1")
	if err := f.source("v1", "").Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The remote disappears: the cached commit is still usable.
	if err := os.RemoveAll(f.origin); err != nil {
		t.Fatal(err)
	}
	s := f.source("v1", "")
	if err := s.PrepareCached(context.Background(), strings.ToUpper(sha)); err != nil {
		t.Fatalf("PrepareCached: %v", err)
	}
	if s.Commit() != sha {
		t.Errorf("Commit = %q, want %q", s.Commit(), sha)
	}
	if _, found := s.OrgConfig(); !found {
		t.Error("the org config was not read from the cache")
	}
	if names, err := s.Names(); err != nil || len(names) != 1 {
		t.Errorf("Names = %v, %v", names, err)
	}
	// Prepare, which resolves the tag, now fails: that is the case the
	// launcher isolates.
	if err := f.source("v1", "").Prepare(context.Background()); err == nil {
		t.Error("Prepare without a remote must fail")
	}
}

func TestPrepareCachedErrors(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.PrepareCached(context.Background(), "not-a-sha"); !errors.Is(err, ErrNotPinned) {
		t.Errorf("bad sha: %v", err)
	}
	if err := s.PrepareCached(context.Background(), sha); !errors.Is(err, ErrNotCached) {
		t.Errorf("not cached: %v", err)
	}
	if s.Commit() != "" || s.Root() != "" {
		t.Error("a failed PrepareCached must leave the source unprepared")
	}
	// A tampered cached checkout is refused, with a hint.
	p := f.source("v1", "")
	if err := p.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Root(), "profiles", "base.toml"), []byte("name = \"evil\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := f.source("v1", "").PrepareCached(context.Background(), sha)
	if !errors.Is(err, ErrTampered) || !strings.Contains(err.Error(), "delete ") {
		t.Errorf("tampered: %v", err)
	}
}

func TestCheckoutDirMatchesTheCache(t *testing.T) {
	f := newFixture(t)
	sha := f.seed()
	f.git("tag", "v1")
	s := f.source("v1", "")
	if err := s.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := CheckoutDir(f.cache, f.url(), sha)
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Errorf("CheckoutDir = %s: %v", want, err)
	}
	if !strings.HasPrefix(s.Root(), want) {
		t.Errorf("Root %s is not in %s", s.Root(), want)
	}
}

func TestWatchSet(t *testing.T) {
	w := newWatch(nil)
	for _, p := range []string{"profiles/a.toml", "prompts/p.md", "mcp/registry.toml", "mcp/other.txt", "ccshelf.toml"} {
		if !w.has(p) {
			t.Errorf("%s should be watched by default", p)
		}
	}
	for _, p := range []string{"README.md", "ccshelf.toml/x", "profilesX/a.toml", "bundles/x/plugin.json"} {
		if w.has(p) {
			t.Errorf("%s should not be watched", p)
		}
	}
	cfg := orgconfig.Default()
	cfg.Profiles = orgconfig.Profiles{Dir: "a/b", MCPRegistry: "reg.toml"}
	w = newWatch(cfg)
	if !w.has("a/b/x.toml") || !w.has("reg.toml") || w.has("a/c.toml") || w.has("mcp/registry.toml") {
		t.Errorf("custom watch set wrong: %+v", w)
	}
	if got := w.loneFiles(); len(got) != 3 {
		t.Errorf("loneFiles = %v", got)
	}
	// The catalog data is watched too: the sidecar folder and the marketplace files.
	for _, p := range []string{"catalog/plugins/a.toml", ".claude-plugin/marketplace.json"} {
		if !newWatch(nil).has(p) {
			t.Errorf("%s should be watched by default", p)
		}
	}
	if newWatch(nil).has("catalog/taxonomy.toml") {
		t.Error("the taxonomy is not catalog data the launcher reads")
	}
	cfg.Catalog.Marketplaces = []string{"market/a.json", "market/b.json"}
	if w = newWatch(cfg); !w.has("market/b.json") || w.has(".claude-plugin/marketplace.json") {
		t.Errorf("configured marketplace files not watched: %+v", w)
	}
	if got := newWatch(nil).loneFiles(); len(got) != 2 || got[0] != "ccshelf.toml" {
		t.Errorf("default loneFiles = %v", got)
	}
}

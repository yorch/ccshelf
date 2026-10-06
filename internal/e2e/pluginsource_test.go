package e2e

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"testing"
)

// A plugin profile source is bound to the real source of its marketplace
// (SR2): the "@acme" of a plugin id is only a local alias.
func TestPluginSourceMarketplaceBinding(t *testing.T) {
	s := newSandbox(t)
	install := filepath.Join(t.TempDir(), "orgplugin")
	write(t, filepath.Join(install, "profiles", "pp.toml"), "name = \"pp\"\ndescription = \"d\"\n[plugins]\ninclude = [\"design-kit@acme\"]\n")
	list, _ := json.Marshal([]map[string]any{{
		"id": "orgprofiles@acme", "version": "1.0.0", "scope": "user", "enabled": true, "installPath": filepath.ToSlash(install),
		"installedAt": "2026-01-01T00:00:00.000Z", "lastUpdated": "2026-01-01T00:00:00.000Z", "projectEnabled": false,
	}, {
		"id": "design-kit@acme", "version": "1.0.0", "scope": "user", "enabled": true, "installPath": "/fake/design-kit",
		"installedAt": "2026-01-01T00:00:00.000Z", "lastUpdated": "2026-01-01T00:00:00.000Z", "projectEnabled": false,
	}})
	plugins := filepath.Join(t.TempDir(), "plugins.json")
	write(t, plugins, string(list))
	s.Setenv("FAKE_CLAUDE_PLUGINS", plugins)
	cfg := func(expected string) {
		write(t, filepath.Join(s.ConfigDir(), "config.toml"),
			"[[sources]]\ntype = \"plugin\"\nplugin = \"orgprofiles@acme\"\nmarketplace = \""+expected+"\"\n")
	}

	// The fake's marketplace "acme" was added from github acme/plugins.
	cfg("acme/plugins")
	r := s.run("run", "pp")
	if r.Code != 4 {
		t.Fatalf("first run: exit %d, want 4 (trust)\n%s", r.Code, r.Stderr)
	}
	hash := regexp.MustCompile(`--accept ([0-9a-f]{64})`).FindStringSubmatch(r.Stderr)
	if hash == nil {
		t.Fatalf("no trust hint:\n%s", r.Stderr)
	}
	s.mustRun("trust", "pp", "--accept", hash[1])
	s.mustRun("run", "pp")

	// The organization expects another source: refused, claude is not started again.
	cfg("evil/plugins")
	r = s.run("run", "pp")
	if r.Code != 1 {
		t.Fatalf("a lookalike marketplace: exit %d, want 1\n%s", r.Code, r.Stderr)
	}
	contains(t, "stderr", r.Stderr, `was added from "acme/plugins", not from the expected "evil/plugins"`)
	contains(t, "stderr", r.Stderr, "/plugin marketplace")
	if n := len(s.launches()); n != 1 {
		t.Errorf("claude started %d times, want 1", n)
	}
}

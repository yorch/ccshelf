package launcher

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/testutil"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// pluginSourceHarness installs a fake plugin "orgprofiles@acme" that carries
// the profile "pp" and returns the harness.
func pluginSourceHarness(t *testing.T, marketplaces string) *harness {
	t.Helper()
	h := newHarness(t)
	install := filepath.Join(t.TempDir(), "orgplugin")
	testutil.WriteFile(t, filepath.Join(install, "profiles", "pp.toml"), "name = \"pp\"\ndescription = \"d\"\n[plugins]\ninclude = [\"design-kit@acme\"]\n")
	list := []map[string]any{{
		"id": "orgprofiles@acme", "version": "1.0.0", "scope": "user", "enabled": true, "installPath": filepath.ToSlash(install),
		"installedAt": "2026-01-01T00:00:00.000Z", "lastUpdated": "2026-01-01T00:00:00.000Z", "projectEnabled": false,
	}}
	b, _ := json.Marshal(list)
	pl := filepath.Join(t.TempDir(), "plugins.json")
	testutil.WriteFile(t, pl, string(b))
	t.Setenv("FAKE_CLAUDE_PLUGINS", pl)
	t.Setenv("FAKE_CLAUDE_MARKETPLACES", "")
	t.Setenv("FAKE_CLAUDE_MARKETPLACES_FAIL", "")
	if marketplaces != "" {
		mp := filepath.Join(t.TempDir(), "mk.json")
		testutil.WriteFile(t, mp, marketplaces)
		t.Setenv("FAKE_CLAUDE_MARKETPLACES", mp)
	}
	return h
}

func TestPluginSourceExpectedMarketplace(t *testing.T) {
	cfg := func(expected string) string {
		c := "[[sources]]\ntype = \"plugin\"\nplugin = \"orgprofiles@acme\"\n"
		if expected != "" {
			c += "marketplace = \"" + expected + "\"\n"
		}
		return c
	}
	tests := []struct {
		name         string
		marketplaces string // "" = the fake's default (acme is github acme/plugins)
		config       string
		wantOK       bool
		wantErr      string
	}{
		{"matches the shorthand", "", cfg("acme/plugins"), true, ""},
		{"matches a github url", "", cfg("https://github.com/acme/plugins.git"), true, ""},
		{"no expected source still binds", "", cfg(""), true, ""},
		{"lookalike marketplace", `[{"name":"acme","source":"github","repo":"evil/plugins","installLocation":"/x"}]`, cfg("acme/plugins"), false, "was added from \"evil/plugins\""},
		{"marketplace not configured", `[]`, cfg("acme/plugins"), false, "not configured"},
		{"unknown shape", `{"marketplaces":[]}`, cfg("acme/plugins"), false, "expected a JSON array"},
		{"unknown source kind", `[{"name":"acme","source":"weird","installLocation":"/x"}]`, cfg(""), false, "unknown source kind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := pluginSourceHarness(t, tt.marketplaces)
			h.writeConfig(tt.config)
			code := h.run("run", "pp")
			if tt.wantOK {
				if code == ui.ExitTrust {
					m := regexp.MustCompile(`--accept ([0-9a-f]{64})`).FindStringSubmatch(h.errb.String())
					if m == nil {
						t.Fatalf("no trust hint:\n%s", h.errb)
					}
					h.mustRun("trust", "pp", "--accept", m[1])
					h.errb.Reset()
					code = h.run("run", "pp")
				}
				if code != 0 || h.started != 1 {
					t.Fatalf("code %d\n%s", code, h.errb)
				}
				return
			}
			if code != ui.ExitFailure || h.started != 0 {
				t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
			}
			out := h.errb.String()
			if !strings.Contains(out, tt.wantErr) || !strings.Contains(out, "/plugin marketplace") {
				t.Errorf("stderr lacks %q or the hint:\n%s", tt.wantErr, out)
			}
		})
	}
}

func TestPluginSourceMarketplaceListFailsClosed(t *testing.T) {
	h := pluginSourceHarness(t, "")
	t.Setenv("FAKE_CLAUDE_MARKETPLACES_FAIL", "1")
	h.writeConfig("[[sources]]\ntype = \"plugin\"\nplugin = \"orgprofiles@acme\"\n")
	if code := h.run("run", "pp"); code == 0 || h.started != 0 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if !strings.Contains(h.errb.String(), "marketplace") {
		t.Errorf("stderr: %s", h.errb)
	}
}

// The trust record is keyed to the real marketplace source: the same plugin id
// served by a marketplace added from somewhere else needs trust again (SR2).
func TestPluginSourceTrustFollowsMarketplaceSource(t *testing.T) {
	h := pluginSourceHarness(t, "")
	h.writeConfig("[[sources]]\ntype = \"plugin\"\nplugin = \"orgprofiles@acme\"\n")
	if code := h.run("run", "pp"); code != ui.ExitTrust {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	m := regexp.MustCompile(`--accept ([0-9a-f]{64})`).FindStringSubmatch(h.errb.String())
	if m == nil {
		t.Fatalf("no trust hint:\n%s", h.errb)
	}
	h.mustRun("trust", "pp", "--accept", m[1])
	h.errb.Reset()
	if code := h.run("run", "pp"); code != 0 {
		t.Fatalf("trusted run: code %d\n%s", code, h.errb)
	}
	mp := filepath.Join(t.TempDir(), "mk.json")
	testutil.WriteFile(t, mp, `[{"name":"acme","source":"github","repo":"evil/plugins","installLocation":"/x"}]`)
	t.Setenv("FAKE_CLAUDE_MARKETPLACES", mp)
	h.errb.Reset()
	if code := h.run("run", "pp"); code != ui.ExitTrust {
		t.Fatalf("a different marketplace source must need trust again: code %d\n%s", code, h.errb)
	}
}

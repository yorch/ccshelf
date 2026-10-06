package lint

import (
	"strings"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/catalog/catalogtest"
	"github.com/ccshelf/ccshelf/internal/orgconfig"
)

const (
	fxMkt = ".claude-plugin/marketplace.json"
	fxCO  = ".github/CODEOWNERS"
)

// has reports whether r holds a finding with the code, severity and plugin
// whose message contains sub.
func has(r *Report, code string, sev Severity, plugin, sub string) bool {
	for _, f := range r.Findings {
		if f.Code == code && f.Severity == sev && (plugin == "" || f.Plugin == plugin) && strings.Contains(f.Message, sub) {
			return true
		}
	}
	return false
}

func mutated(t *testing.T, now func() time.Time, mutate func(root string)) *Report {
	t.Helper()
	root := catalogtest.CopyFixture(t)
	if mutate != nil {
		mutate(root)
	}
	return run(t, root, now)
}

func TestC1ExecutablePathsNeedPlatformOwner(t *testing.T) {
	tests := []struct {
		name   string
		plugin string
		sev    Severity
		sub    string
		setup  func(t *testing.T, root string)
	}{
		{"inline hooks in plugin.json", "sre-kit", Error, ".claude-plugin/plugin.json", func(t *testing.T, root string) {
			catalogtest.Write(t, root, "plugins/sre-kit/.claude-plugin/plugin.json", `{"name":"sre-kit","hooks":{"SessionStart":[]}}`)
		}},
		{"inline mcpServers in plugin.json", "design-kit", Error, ".claude-plugin/plugin.json", func(t *testing.T, root string) {
			catalogtest.Write(t, root, "plugins/design-kit/.claude-plugin/plugin.json", `{"name":"design-kit","mcpServers":{"x":{"command":"y"}}}`)
		}},
		{"inline lspServers in plugin.json", "design-kit", Error, ".claude-plugin/plugin.json", func(t *testing.T, root string) {
			catalogtest.Write(t, root, "plugins/design-kit/.claude-plugin/plugin.json", `{"name":"design-kit","lspServers":{"go":{"command":"gopls"}}}`)
		}},
		{"hooks path in plugin.json", "data-tools", Error, "plugins/data-tools/config/h.json", func(t *testing.T, root string) {
			catalogtest.Write(t, root, "plugins/data-tools/.claude-plugin/plugin.json", `{"name":"data-tools","hooks":"./config/h.json"}`)
			catalogtest.Write(t, root, "plugins/data-tools/config/h.json", `{"hooks":{}}`)
		}},
		{"hooks array path", "data-tools", Error, "plugins/data-tools/config/h.json", func(t *testing.T, root string) {
			catalogtest.Write(t, root, "plugins/data-tools/.claude-plugin/plugin.json", `{"name":"data-tools","hooks":["./config/h.json",{"x":1}]}`)
			catalogtest.Write(t, root, "plugins/data-tools/config/h.json", `{"hooks":{}}`)
		}},
		{"lsp file", "design-kit", Error, "plugins/design-kit/.lsp.json", func(t *testing.T, root string) {
			catalogtest.Write(t, root, "plugins/design-kit/.lsp.json", `{"go":{"command":"gopls"}}`)
		}},
		{"script run by a hook", "sre-kit", Warning, "plugins/sre-kit/scripts/run.sh", func(t *testing.T, root string) {
			catalogtest.Write(t, root, "plugins/sre-kit/hooks/hooks.json", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/scripts/run.sh --x"}]}]}}`)
			catalogtest.Write(t, root, "plugins/sre-kit/scripts/run.sh", "#!/bin/sh\n")
		}},
		{"script run by an MCP server", "data-tools", Warning, "plugins/data-tools/server/main.js", func(t *testing.T, root string) {
			catalogtest.Write(t, root, "plugins/data-tools/.mcp.json", `{"mcpServers":{"w":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/server/main.js"]}}}`)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := mutated(t, nil, func(root string) { tt.setup(t, root) })
			if !has(r, "CAT042", tt.sev, tt.plugin, tt.sub) {
				t.Fatalf("want %s CAT042 for %s containing %q:\n%s", tt.sev, tt.plugin, tt.sub, FormatText(r))
			}
		})
	}
}

func TestC1PlatformOwnedPathsAreQuiet(t *testing.T) {
	r := mutated(t, nil, func(root string) {
		catalogtest.Write(t, root, "plugins/sre-kit/hooks/hooks.json", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/hooks/run.sh"}]}]}}`)
		catalogtest.Write(t, root, "plugins/sre-kit/hooks/run.sh", "#!/bin/sh\n")
	})
	for _, f := range r.Findings {
		if f.Code == "CAT042" {
			t.Fatalf("unexpected %v", f)
		}
	}
	r = mutated(t, nil, func(root string) {
		catalogtest.Write(t, root, "plugins/sre-kit/.claude-plugin/plugin.json", `{"name":"sre-kit","hooks":{"a":[]}}`)
		catalogtest.Replace(t, root, fxCO, "/plugins/*/.mcp.json               @acme/platform\n", "/plugins/*/.mcp.json               @acme/platform\n/plugins/*/.claude-plugin/         @acme/platform\n")
	})
	if len(codes(r)["CAT042"]) != 0 {
		t.Fatalf("platform-owned manifest still flagged:\n%s", FormatText(r))
	}
}

func TestC1DeclaredPathEscapes(t *testing.T) {
	r := mutated(t, nil, func(root string) {
		catalogtest.Write(t, root, "plugins/data-tools/.claude-plugin/plugin.json", `{"name":"data-tools","hooks":"./../sre-kit/hooks/hooks.json"}`)
	})
	if !has(r, "CAT033", Error, "data-tools", "outside the plugin directory") {
		t.Fatalf("escaping declared path not reported:\n%s", FormatText(r))
	}
}

func TestC3CaseVariantKeysAreRejected(t *testing.T) {
	for _, body := range []string{
		`{"name":"sre-kit","hooks":{"a":[]},"HOOKS":null}`,
		`{"name":"sre-kit","hooks":{"a":[]},"hooks":null}`,
	} {
		r := mutated(t, nil, func(root string) { catalogtest.Write(t, root, "plugins/sre-kit/.claude-plugin/plugin.json", body) })
		if !has(r, "CAT033", Error, "sre-kit", "") {
			t.Fatalf("%s: not reported:\n%s", body, FormatText(r))
		}
	}
}

func TestC5OwnerProbeIgnoresManifestRule(t *testing.T) {
	r := mutated(t, nil, func(root string) {
		catalogtest.Replace(t, root, fxCO, "/plugins/*/.mcp.json               @acme/platform\n", "/plugins/*/.mcp.json               @acme/platform\n/plugins/*/.claude-plugin/         @acme/platform\n")
	})
	if len(codes(r)["CAT046"]) != 0 || len(codes(r)["CAT044"]) != 0 {
		t.Fatalf("manifest rule changed the owner checks:\n%s", FormatText(r))
	}
}

func TestC9PlatformPathsAndOrphans(t *testing.T) {
	tests := []struct {
		name, sub string
		setup     func(t *testing.T, root string)
	}{
		{"mcp registry", "mcp/registry.toml", func(t *testing.T, root string) {
			catalogtest.Replace(t, root, fxCO, "/mcp/                              @acme/platform\n", "/mcp/ @acme/web\n")
		}},
		{"ccshelf.toml", "ccshelf.toml", func(t *testing.T, root string) {
			catalogtest.Replace(t, root, fxCO, "/ccshelf.toml                      @acme/platform\n", "/ccshelf.toml @acme/web\n")
		}},
		{"marketplace.json", ".claude-plugin/marketplace.json", func(t *testing.T, root string) {
			catalogtest.Replace(t, root, fxCO, "/.claude-plugin/marketplace.json   @acme/platform\n", "")
		}},
		{"workflows", ".github/workflows/", func(t *testing.T, root string) {
			catalogtest.Replace(t, root, fxCO, "/.github/                          @acme/platform\n", "/.github/ @acme/platform\n/.github/workflows/ @acme/web\n")
		}},
		{"CODEOWNERS itself", ".github/CODEOWNERS", func(t *testing.T, root string) {
			catalogtest.Replace(t, root, fxCO, "/.github/                          @acme/platform\n", "/.github/ @acme/platform\n/.github/CODEOWNERS @acme/web\n")
		}},
		{"bundles", "bundles/", func(t *testing.T, root string) {
			catalogtest.Replace(t, root, fxCO, "/bundles/                          @acme/platform\n", "")
		}},
		{"profiles", "profiles/", func(t *testing.T, root string) {
			catalogtest.Replace(t, root, fxCO, "/profiles/                         @acme/platform\n", "")
		}},
		{"catalog", "catalog/", func(t *testing.T, root string) {
			catalogtest.Replace(t, root, fxCO, "/catalog/                          @acme/platform\n", "")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := mutated(t, nil, func(root string) { tt.setup(t, root) })
			if !has(r, "CAT045", Warning, "", tt.sub) {
				t.Fatalf("want CAT045 mentioning %q:\n%s", tt.sub, FormatText(r))
			}
		})
	}
	t.Run("orphan directory", func(t *testing.T) {
		r := mutated(t, nil, func(root string) { catalogtest.Write(t, root, "plugins/orphan/x.txt", "x") })
		if !has(r, "CAT044", Warning, "", "plugins/orphan/") {
			t.Fatalf("orphan not reported:\n%s", FormatText(r))
		}
	})
	t.Run("covered orphan is fine", func(t *testing.T) {
		r := mutated(t, nil, func(root string) {
			catalogtest.Write(t, root, "plugins/orphan/x.txt", "x")
			catalogtest.Replace(t, root, fxCO, "/plugins/data-tools/               @acme/data\n", "/plugins/data-tools/               @acme/data\n/plugins/orphan/ @acme/data\n")
		})
		if has(r, "CAT044", Warning, "", "orphan") {
			t.Fatalf("covered orphan reported:\n%s", FormatText(r))
		}
	})
}

func TestC10NameFormat(t *testing.T) {
	long := strings.Repeat("a", 65)
	tests := []struct {
		name string
		sev  Severity
		bad  bool
	}{
		{"Figma_Bridge", Warning, true},
		{"figma--bridge", Warning, true},
		{"-figma", Warning, true},
		{long, Warning, true},
		{"fig@ma", Error, true},
		{"fig:ma", Error, true},
		{"fig/ma", Error, true},
		{"fig\x01ma", Error, true},
		{"fig\u202ema", Error, true},
		{strings.Repeat("a", 129), Error, true},
		{"figma-bridge2", Warning, false},
		{strings.Repeat("a", 64), Warning, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Build the JSON with encoding-safe escapes.
			esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\x01", `\u0001`, "\u202e", `\u202e`).Replace(tt.name)
			r := mutated(t, nil, func(root string) {
				catalogtest.Replace(t, root, fxMkt, `"name": "figma-bridge"`, `"name": "`+esc+`"`)
			})
			if got := has(r, "CAT009", tt.sev, "", ""); got != tt.bad {
				t.Fatalf("CAT009 %s = %v, want %v:\n%s", tt.sev, got, tt.bad, FormatText(r))
			}
		})
	}
}

func TestC10PrefixWithoutManifestIsNotABundle(t *testing.T) {
	root := catalogtest.CopyFixture(t)
	catalogtest.Remove(t, root, "profiles/sre.toml")
	cfg, _ := orgconfig.Load(root)
	d, err := LoadData(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range d.Plugins {
		switch p.Plugin.Name {
		case "profile-sre":
			if p.IsBundle() || !p.HasBundlePrefix() {
				t.Errorf("profile-sre without manifest: IsBundle=%v prefix=%v", p.IsBundle(), p.HasBundlePrefix())
			}
		case "profile-frontend":
			if !p.IsBundle() {
				t.Error("profile-frontend should be a bundle")
			}
		}
	}
}

func TestC11ReviewByFollowsRequire(t *testing.T) {
	// Not required: no error.
	r := mutated(t, nil, func(root string) {
		catalogtest.Replace(t, root, "catalog/plugins/sre-kit.toml", `review_by = "2027-03-01"`, ``)
	})
	if len(codes(r)["CAT018"]) != 0 || len(codes(r)["CAT013"]) != 0 {
		t.Fatalf("review_by demanded although not required:\n%s", FormatText(r))
	}
	// Required and active: CAT018 only, not CAT013 as well.
	r = mutated(t, nil, func(root string) {
		catalogtest.Replace(t, root, "ccshelf.toml", `require = ["owner", "status"]`, `require = ["owner", "status", "review_by"]`)
		catalogtest.Replace(t, root, "catalog/plugins/sre-kit.toml", `review_by = "2027-03-01"`, ``)
	})
	if len(codes(r)["CAT018"]) != 1 || len(codes(r)["CAT013"]) != 0 {
		t.Fatalf("want exactly one CAT018:\n%s", FormatText(r))
	}
	// Required and experimental: CAT013.
	r = mutated(t, nil, func(root string) {
		catalogtest.Replace(t, root, "ccshelf.toml", `require = ["owner", "status"]`, `require = ["owner", "status", "review_by"]`)
		catalogtest.Replace(t, root, "catalog/plugins/data-tools.toml", `review_by = "2027-01-15"`, ``)
	})
	if len(codes(r)["CAT013"]) != 1 || !has(r, "CAT013", Error, "data-tools", "review_by") {
		t.Fatalf("want CAT013 for the experimental plugin:\n%s", FormatText(r))
	}
	// A malformed date is always an error.
	r = mutated(t, nil, func(root string) {
		catalogtest.Replace(t, root, "catalog/plugins/sre-kit.toml", `review_by = "2027-03-01"`, `review_by = "soon"`)
	})
	if len(codes(r)["CAT018"]) != 1 {
		t.Fatalf("bad date:\n%s", FormatText(r))
	}
}

func TestC13EntryLinesTokenLevel(t *testing.T) {
	data := []byte(`{
  "description": "plugins": "name": "decoy",
  "name": "m",
  "plugins": [
    {
      "description": "has \"name\": \"fake\" inside and a } brace",
      "author": {"name": "Zed"},
      "name": "real-one",
      "source": "./a"
    },
    {"source": "./b", "name": "second"}
  ]
}`)
	// The first line is invalid JSON on purpose: nothing is found.
	if got := entryLines(data); len(got) != 0 {
		t.Fatalf("malformed input: %v", got)
	}
	good := []byte(`{
  "metadata": {"description": "\"plugins\": [ \"name\": \"decoy\""},
  "name": "m",
  "plugins": [
    {
      "description": "has \"name\": \"fake\" inside and a } brace",
      "author": {"name": "Zed"},
      "name": "real-one",
      "source": "./a"
    },
    {"source": "./b", "name": "second"},
    {"name": "real-one", "source": "./c"}
  ]
}`)
	got := entryLines(good)
	if got["real-one"] != 8 || got["second"] != 11 || got["Zed"] != 0 || got["fake"] != 0 || got["decoy"] != 0 || len(got) != 2 {
		t.Fatalf("lines = %v", got)
	}
}

func TestC16RootPluginIsChecked(t *testing.T) {
	root := t.TempDir()
	catalogtest.Write(t, root, "ccshelf.toml", "[lint]\nplatform_owners = [\"@a/platform\"]\n")
	catalogtest.Write(t, root, fxMkt, `{"name":"m","plugins":[{"name":"rooty","source":"./","description":"A plugin that lives at the repository root."}]}`)
	catalogtest.Write(t, root, ".claude-plugin/plugin.json", `{"name":"rooty"}`)
	catalogtest.Write(t, root, "hooks/hooks.json", `{"hooks":{}}`)
	catalogtest.Write(t, root, fxCO, "* @a/team\n")
	r := run(t, root, nil)
	if !has(r, "CAT042", Error, "rooty", "/hooks/hooks.json") {
		t.Fatalf("root plugin hooks not checked:\n%s", FormatText(r))
	}
	// And with a platform rule it is fine.
	catalogtest.Write(t, root, fxCO, "* @a/team\n/hooks/ @a/platform\n")
	if r = run(t, root, nil); len(codes(r)["CAT042"]) != 0 {
		t.Fatalf("platform-owned hooks flagged:\n%s", FormatText(r))
	}
	// CAT044 for an uncovered root plugin.
	catalogtest.Write(t, root, fxCO, "/hooks/ @a/platform\n")
	if r = run(t, root, nil); !has(r, "CAT044", Warning, "rooty", "repository root") {
		t.Fatalf("uncovered root:\n%s", FormatText(r))
	}
}

func TestAbstractProfileGetsNoBundleEntry(t *testing.T) {
	r := mutated(t, nil, func(root string) {
		catalogtest.Write(t, root, "profiles/base.toml", "name = \"base\"\n[plugins]\ninclude = []\n")
		catalogtest.Write(t, root, "profiles/child.toml", "name = \"child\"\nextends = [\"base\"]\n")
		catalogtest.Write(t, root, "profiles/solo.toml", "name = \"solo\"\n")
	})
	if len(codes(r)["CAT050"]) != 0 {
		t.Fatalf("plugin-less profiles need no bundle:\n%s", FormatText(r))
	}
	// A parent that has plugins makes the child need a bundle.
	r = mutated(t, nil, func(root string) {
		catalogtest.Write(t, root, "profiles/base.toml", "name = \"base\"\n[plugins]\ninclude = [\"sre-kit@acme-tools\"]\n")
		catalogtest.Write(t, root, "profiles/child.toml", "name = \"child\"\nextends = [\"base\"]\n")
	})
	if len(codes(r)["CAT050"]) != 2 {
		t.Fatalf("want CAT050 for base and child:\n%s", FormatText(r))
	}
	// An exclude can empty an inherited list.
	r = mutated(t, nil, func(root string) {
		catalogtest.Write(t, root, "profiles/base.toml", "name = \"base\"\n[plugins]\ninclude = [\"sre-kit@acme-tools\"]\n")
		catalogtest.Write(t, root, "profiles/child.toml", "name = \"child\"\nextends = [\"base\"]\n[plugins]\nexclude = [\"sre-kit@acme-tools\"]\n")
		catalogtest.Write(t, root, "profiles/loop.toml", "extends = [\"loop\"]\n")
		catalogtest.Write(t, root, "profiles/bad.toml", "this is = = not toml")
	})
	if !has(r, "CAT050", Error, "profile-base", "") || has(r, "CAT050", Error, "profile-child", "") {
		t.Fatalf("exclude handling:\n%s", FormatText(r))
	}
	// Unparseable or cyclic profiles are not exempt: the launcher reports them.
	if !has(r, "CAT050", Error, "profile-bad", "") || !has(r, "CAT050", Error, "profile-loop", "") {
		t.Fatalf("unreadable profiles must still need a bundle:\n%s", FormatText(r))
	}
}

func TestReviewDateBoundaries(t *testing.T) {
	set := func(root string) {
		catalogtest.Replace(t, root, "catalog/plugins/design-kit.toml", `review_by = "2027-03-01"`, `review_by = "2026-01-01"`)
		catalogtest.Replace(t, root, "catalog/plugins/sre-kit.toml", `review_by = "2027-03-01"`, `review_by = "2099-01-01"`)
	}
	day := func(m time.Month, d int) func() time.Time { return at(2026, m, d) }
	tests := []struct {
		name string
		now  func() time.Time
		want string // "", "CAT019" or "CAT020"
		age  string
	}{
		{"on the day", day(1, 1), "", ""},
		{"day before", at(2025, 12, 31), "", ""},
		{"one day late", day(1, 2), "CAT020", "passed 1 days"},
		{"at the limit", day(6, 30), "CAT020", "passed 180 days"},
		{"one past the limit", day(7, 1), "CAT019", "181 days ago"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := mutated(t, tt.now, set)
			for _, code := range []string{"CAT019", "CAT020"} {
				got := false
				for _, f := range codes(r)[code] {
					if f.Plugin == "design-kit" {
						got = true
						if !strings.Contains(f.Message, tt.age) {
							t.Errorf("message %q lacks %q", f.Message, tt.age)
						}
					}
				}
				if got != (code == tt.want) {
					t.Errorf("%s present=%v, want code %q:\n%s", code, got, tt.want, FormatText(r))
				}
			}
		})
	}
}

func TestSupersessionCycleMessage(t *testing.T) {
	r := mutated(t, nil, func(root string) {
		catalogtest.Replace(t, root, "catalog/plugins/sre-kit.toml", `status = "active"`, `status = "deprecated"`+"\nsuperseded_by = \"ops-helper\"")
	})
	if !has(r, "CAT017", Error, "ops-helper", "cycle") {
		t.Fatalf("cycle not named:\n%s", FormatText(r))
	}
	// A deprecated target that does not lead back is not called a cycle.
	r = mutated(t, nil, func(root string) {
		catalogtest.Replace(t, root, "catalog/plugins/sre-kit.toml", `status = "active"`, `status = "deprecated"`+"\nsuperseded_by = \"data-tools\"")
	})
	if !has(r, "CAT017", Error, "ops-helper", "itself deprecated") || has(r, "CAT017", Error, "ops-helper", "cycle") {
		t.Fatalf("chain mislabelled:\n%s", FormatText(r))
	}
}

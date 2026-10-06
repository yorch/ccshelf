package orgcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/policy"
	"github.com/yorch/ccshelf/internal/ui"
)

// symlinkOrSkip creates link -> target, or skips where symbolic links are
// unavailable (Windows without the privilege).
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
}

func mustBeEmpty(t *testing.T, dir, what string) {
	t.Helper()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%s: %d entries were written into %s", what, len(entries), dir)
	}
}

func TestCatalogBuildRefusesSymlinkedComponents(t *testing.T) {
	t.Run("dist is a link out of the repo (relative --out, cwd is the repo)", func(t *testing.T) {
		root := copyExample(t)
		elsewhere := t.TempDir()
		symlinkOrSkip(t, elsewhere, filepath.Join(root, "dist"))
		h := newHarness(t, root)
		h.cwd = root
		r := h.run("catalog", "build", "--out", filepath.Join("dist", "catalog"))
		if r.code != ui.ExitFailure || !strings.Contains(r.err, "symbolic link") {
			t.Errorf("code %d\n%s", r.code, r.err)
		}
		mustBeEmpty(t, elsewhere, "through the dist link")
	})
	t.Run("absolute --out below the repo, cwd elsewhere", func(t *testing.T) {
		root := copyExample(t)
		elsewhere := t.TempDir()
		symlinkOrSkip(t, elsewhere, filepath.Join(root, "dist"))
		h := newHarness(t, root)
		r := h.run("catalog", "build", "--out", filepath.Join(root, "dist", "catalog"))
		if r.code == 0 || !strings.Contains(r.err, "symbolic link") {
			t.Errorf("code %d\n%s", r.code, r.err)
		}
		mustBeEmpty(t, elsewhere, "through the dist link")
	})
	t.Run("the last component is a link", func(t *testing.T) {
		root := copyExample(t)
		elsewhere := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "dist"), 0o755); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, elsewhere, filepath.Join(root, "dist", "catalog"))
		h := newHarness(t, root)
		h.cwd = root
		r := h.run("catalog", "build")
		if r.code != ui.ExitFailure || !strings.Contains(r.err, "symbolic link") {
			t.Errorf("code %d\n%s", r.code, r.err)
		}
		mustBeEmpty(t, elsewhere, "through dist/catalog")
	})
	t.Run("a link to a source directory of the repo", func(t *testing.T) {
		root := copyExample(t)
		symlinkOrSkip(t, filepath.Join(root, "profiles"), filepath.Join(root, "dist"))
		before := read(t, root, "profiles/sre.toml")
		h := newHarness(t, root)
		h.cwd = root
		if r := h.run("catalog", "build", "--out", "dist"); r.code == 0 {
			t.Errorf("code %d\n%s", r.code, r.err)
		}
		if read(t, root, "profiles/sre.toml") != before {
			t.Error("a profile was modified")
		}
		entries, _ := os.ReadDir(filepath.Join(root, "profiles"))
		for _, e := range entries {
			if e.Name() == "catalog.json" || e.Name() == markdownName {
				t.Errorf("output leaked into profiles: %s", e.Name())
			}
		}
	})
	t.Run("an ordinary directory below the repo is allowed", func(t *testing.T) {
		root := copyExample(t)
		h := newHarness(t, root)
		h.cwd = root
		if r := h.run("catalog", "build", "--out", filepath.Join("dist", "catalog")); r.code != 0 {
			t.Fatalf("code %d\n%s", r.code, r.err)
		}
		if _, err := os.Stat(filepath.Join(root, "dist", "catalog", "catalog.json")); err != nil {
			t.Error(err)
		}
	})
}

func TestCatalogBuildRefusesRepoAndSourceDirectories(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, root)
	h.cwd = root
	for _, tt := range []struct {
		name string
		out  string
	}{
		{"repo root", root},
		{"repo root as dot", "."},
		{"parent of the repo", filepath.Dir(root)},
		{"grandparent of the repo", filepath.Dir(filepath.Dir(root))},
		{"profiles", filepath.Join(root, "profiles")},
		{"below profiles", filepath.Join(root, "profiles", "out")},
		{"bundles", filepath.Join(root, "bundles")},
		{"catalog sidecars", filepath.Join(root, "catalog", "plugins")},
		{"catalog", filepath.Join(root, "catalog")},
		{"plugins", filepath.Join(root, "plugins", "sre-kit")},
		{"mcp registry dir", filepath.Join(root, "mcp")},
		{"workflows", filepath.Join(root, ".github", "workflows")},
		{"marketplace dir", filepath.Join(root, ".claude-plugin")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := read(t, root, "profiles/sre.toml")
			r := h.run("catalog", "build", "--out", tt.out)
			if r.code != ui.ExitUsage {
				t.Errorf("code %d, want a usage error\n%s", r.code, r.err)
			}
			if read(t, root, "profiles/sre.toml") != before {
				t.Error("a source file was modified")
			}
			for _, rel := range []string{markdownName, "catalog.json", "profiles/catalog.json", "profiles/" + markdownName} {
				if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
					t.Errorf("%s was written", rel)
				}
			}
		})
	}
}

func TestCatalogBuildRepoGuardUsesFileIdentity(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, root)
	// A link to the repo root is the repo root.
	link := filepath.Join(t.TempDir(), "alias")
	symlinkOrSkip(t, root, link)
	if r := h.run("catalog", "build", "--out", link); r.code != ui.ExitUsage {
		t.Errorf("link to the root: code %d\n%s", r.code, r.err)
	}
	// A different spelling of the same directory (case-insensitive file
	// systems: macOS and Windows defaults).
	alt := filepath.Join(filepath.Dir(root), strings.ToUpper(filepath.Base(root)))
	ri, err1 := os.Stat(root)
	ai, err2 := os.Stat(alt)
	if err1 != nil || err2 != nil || !os.SameFile(ri, ai) || alt == root {
		t.Log("this file system is case sensitive; the case-variant check is skipped")
	} else if r := h.run("catalog", "build", "--out", alt); r.code != ui.ExitUsage {
		t.Errorf("case variant of the root: code %d\n%s", r.code, r.err)
	}
	if _, err := os.Stat(filepath.Join(root, markdownName)); err == nil {
		t.Error("CATALOG.md was written into the repo root")
	}
}

func TestCatalogBuildPublishedModesAndNoSitePrune(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, root)
	dest := filepath.Join(t.TempDir(), "a", "b", "site")
	if r := h.run("catalog", "build", "--out", dest); r.code != 0 {
		t.Fatalf("%d %s", r.code, r.err)
	}
	checkModes(t, dest)

	// --no-site removes the files of the earlier site build.
	r := h.run("--json", "catalog", "build", "--out", dest, "--no-site")
	if r.code != 0 {
		t.Fatalf("%d %s", r.code, r.err)
	}
	_, data := decode(t, r.out)
	pruned, _ := data["pruned"].([]any)
	if len(pruned) != 3 {
		t.Errorf("pruned = %v", data["pruned"])
	}
	for _, f := range []string{"index.html", "app.js", "style.css"} {
		if _, err := os.Stat(filepath.Join(dest, f)); !os.IsNotExist(err) {
			t.Errorf("stale %s is still there", f)
		}
	}
	for _, f := range []string{"catalog.json", markdownName} {
		if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	// A second --no-site has nothing to prune and says nothing about it.
	if r := h.run("catalog", "build", "--out", dest, "--no-site"); r.code != 0 || strings.Contains(r.out, "removed") {
		t.Errorf("second run: %d\n%s", r.code, r.out)
	}
	// Text output mentions what it removed.
	if r := h.run("catalog", "build", "--out", dest); r.code != 0 {
		t.Fatal(r.err)
	}
	if r := h.run("catalog", "build", "--out", dest, "--no-site"); !strings.Contains(r.out, "removed the stale index.html") {
		t.Errorf("text output:\n%s", r.out)
	}

	// Something that is not a regular file is never removed.
	other := filepath.Join(t.TempDir(), "o")
	if err := os.MkdirAll(filepath.Join(other, "index.html"), 0o755); err != nil {
		t.Fatal(err)
	}
	if r := h.run("catalog", "build", "--out", other, "--no-site"); r.code != ui.ExitFailure || !strings.Contains(r.err, "not a regular file") {
		t.Errorf("directory named index.html: %d %s", r.code, r.err)
	}
	if st, err := os.Stat(filepath.Join(other, "index.html")); err != nil || !st.IsDir() {
		t.Error("the directory was removed")
	}
}

func TestNotAnOrgRepoFailsEverywhere(t *testing.T) {
	empty := t.TempDir()
	for _, args := range [][]string{
		{"search", "x"}, {"recommend"}, {"doctor"}, {"catalog", "build", "--out", filepath.Join(t.TempDir(), "o")}, {"lint"}, {"compile"},
	} {
		t.Run(strings.Join(args[:1], ""), func(t *testing.T) {
			h := newHarness(t, empty)
			r := h.run(args...)
			if r.code != ui.ExitFailure {
				t.Errorf("%v: code %d, want 1\n%s%s", args, r.code, r.out, r.err)
			}
			if args[0] == "search" || args[0] == "doctor" || args[0] == "recommend" || args[0] == "catalog" {
				if !strings.Contains(r.err, "not an org data repo") {
					t.Errorf("%v: err = %s", args, r.err)
				}
			}
		})
	}
	// Nothing was written by a failed catalog build.
	out := filepath.Join(t.TempDir(), "o")
	newHarness(t, empty).run("catalog", "build", "--out", out)
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("the output directory was created: %v", err)
	}
	// An empty result in a real repo is still not an error.
	h := newHarness(t, copyExample(t))
	if r := h.run("search", "zzzz-nothing"); r.code != 0 {
		t.Errorf("no match in a real repo: %d", r.code)
	}
}

func TestDoctorSkillsDir(t *testing.T) {
	h := newHarness(t, copyExample(t))
	skills := t.TempDir()
	write(t, skills, "pdf/SKILL.md", "x")
	write(t, skills, "empty-dir/readme.md", "x")
	write(t, skills, "loose-file", "x")
	write(t, skills, ".hidden/SKILL.md", "x")
	other := t.TempDir()
	write(t, other, "SKILL.md", "x")
	symlinkTo := filepath.Join(skills, "linked")
	if err := os.Symlink(other, symlinkTo); err != nil {
		t.Logf("symlinks unavailable, skipping the link case: %v", err)
	}
	r := h.run("doctor", "--skills-dir", skills)
	if r.code != 0 {
		t.Fatalf("%d\n%s", r.code, r.err)
	}
	if strings.Contains(r.out, "skipped DOC006") || !strings.Contains(r.out, "DOC006 warning standalone-skills") || !strings.Contains(r.out, "pdf") {
		t.Errorf("DOC006 should run and name pdf:\n%s", r.out)
	}
	for _, not := range []string{"empty-dir", "loose-file", "hidden"} {
		if strings.Contains(r.out, not) {
			t.Errorf("%s is not a skill:\n%s", not, r.out)
		}
	}
	// A directory with no skills ran the check and found nothing.
	if r := h.run("doctor", "--skills-dir", t.TempDir()); strings.Contains(r.out, "skipped DOC006") || strings.Contains(r.out, "DOC006 warning") {
		t.Errorf("empty skills dir:\n%s", r.out)
	}
	if r := h.run("doctor", "--skills-dir", filepath.Join(skills, "missing")); r.code != ui.ExitFailure || !strings.Contains(r.err, "--skills-dir") {
		t.Errorf("missing dir: %d %s", r.code, r.err)
	}
	// Nothing outside the given directory is read: no skills dir, DOC006 is skipped with a reason.
	if r := h.run("doctor"); !strings.Contains(r.out, "skipped DOC006") {
		t.Errorf("without the flag:\n%s", r.out)
	}
}

func TestDoctorUsageAPIWithoutKeyIsADedicatedUsageError(t *testing.T) {
	h := newHarness(t, copyExample(t))
	r := h.run("doctor", "--usage-api")
	if r.code != ui.ExitUsage {
		t.Fatalf("code %d\n%s", r.code, r.err)
	}
	if !strings.Contains(r.err, "CCSHELF_ANALYTICS_KEY") || strings.Contains(r.err, "missing required flag") || strings.Contains(r.err, "--usage-file\n") {
		t.Errorf("err = %q", r.err)
	}
}

func TestDoctorUsageWindowAndRedaction(t *testing.T) {
	root := copyExample(t)
	// seo-tools is in no profile now.
	write(t, root, "profiles/seo.toml", strings.Replace(read(t, root, "profiles/seo.toml"), `"seo-tools@acme", `, "", 1))
	h := newHarness(t, root)
	dir := t.TempDir()
	old := `{"event.name":"claude_code.skill_activated","plugin.name":"seo-tools","plugin.marketplace":"acme","timestamp":"2026-01-15T10:00:00Z"}` + "\n"
	recent := `{"event.name":"claude_code.skill_activated","plugin.name":"seo-tools","plugin.marketplace":"acme","timestamp":"2026-10-01T10:00:00Z"}` + "\n"
	redacted := `{"event.name":"claude_code.skill_activated","plugin.name":"<REDACTED>","timestamp":"2026-10-01T10:00:00Z"}` + "\n"

	write(t, dir, "old.jsonl", old)
	r := h.run("doctor", "--usage-file", filepath.Join(dir, "old.jsonl"))
	if !strings.Contains(r.out, "DOC002 info    unused: seo-tools@acme is in no profile and has 0 skill_activated events between 2026-09-07 and 2026-10-06") {
		t.Errorf("an event outside --usage-days must not count:\n%s", r.out)
	}
	r = h.run("doctor", "--usage-file", filepath.Join(dir, "old.jsonl"), "--usage-days", "365")
	if strings.Contains(r.out, "DOC002 info    unused: seo-tools") {
		t.Errorf("a wide window must count the old event:\n%s", r.out)
	}
	write(t, dir, "recent.jsonl", recent)
	if r := h.run("doctor", "--usage-file", filepath.Join(dir, "recent.jsonl")); strings.Contains(r.out, "DOC002 info    unused: seo-tools") {
		t.Errorf("an event in the window counts:\n%s", r.out)
	}
	write(t, dir, "redacted.jsonl", redacted)
	r = h.run("doctor", "--usage-file", filepath.Join(dir, "redacted.jsonl"))
	if !strings.Contains(r.out, "cannot be confirmed unused") || strings.Contains(r.out, "has 0 skill_activated events") {
		t.Errorf("redacted names must not produce an unused claim:\n%s", r.out)
	}
}

func TestDoctorProtectedMCPConflict(t *testing.T) {
	root := copyExample(t)
	cfg := read(t, root, "ccshelf.toml")
	write(t, root, "ccshelf.toml", strings.Replace(cfg, "[protect]\n", "[protect]\nmcp = [\"claude.ai Shopify\"]\n", 1))
	h := newHarness(t, root)
	r := h.run("doctor")
	if r.code != ui.ExitFailure || !strings.Contains(r.out, "DOC012") || !strings.Contains(r.out, "claude.ai Shopify") {
		t.Errorf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	// The abstract base profile (no plugins) is not reported.
	if strings.Contains(r.out, "profile base ") {
		t.Errorf("an abstract profile was reported:\n%s", r.out)
	}
}

func TestDoctorNoLongerCallsProtectedPluginsMasked(t *testing.T) {
	h := newHarness(t, copyExample(t))
	r := h.run("doctor")
	if strings.Contains(r.out, "would mask") || strings.Contains(r.out, "DOC011") || strings.Contains(r.out, "unused: audit-logger") {
		t.Errorf("protected plugins are left alone by the launcher:\n%s", r.out)
	}
}

func TestDoctorPolicyProfileNeeds(t *testing.T) {
	tr := true
	t.Run("sideload flags disabled blocks the profiles' MCP needs", func(t *testing.T) {
		fakePolicy(t, &policy.Policy{DisableSideloadFlags: &tr}, nil)
		h := newHarness(t, copyExample(t))
		r := h.run("doctor", "--policy")
		if r.code != ui.ExitPolicy {
			t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
		}
		for _, want := range []string{"POL005", "profile sre needs add-mcp-config", "disableSideloadFlags", `policy.on_blocked = "fail"`, "profile seo needs strict-mcp-config", "drops it"} {
			if !strings.Contains(r.out, want) {
				t.Errorf("missing %q:\n%s", want, r.out)
			}
		}
		if strings.Contains(r.out, "profile base needs") {
			t.Errorf("an abstract profile has no needs:\n%s", r.out)
		}
		if j := h.run("--json", "doctor", "--policy"); j.code != ui.ExitPolicy {
			t.Errorf("json code %d", j.code)
		}
	})
	t.Run("managed-mcp.json blocks it too", func(t *testing.T) {
		fakePolicy(t, &policy.Policy{ManagedMCPFile: true}, nil)
		h := newHarness(t, copyExample(t))
		if r := h.run("doctor", "--policy"); r.code != ui.ExitPolicy || !strings.Contains(r.out, "managed-mcp.json") {
			t.Errorf("code %d\n%s", r.code, r.out)
		}
	})
	t.Run("a profile without such needs is fine", func(t *testing.T) {
		fakePolicy(t, &policy.Policy{DisableSideloadFlags: &tr}, nil)
		root := copyExample(t)
		for _, p := range []string{"frontend", "seo", "sre"} {
			f := "profiles/" + p + ".toml"
			s := read(t, root, f)
			s = strings.Replace(s, "strict = true", "strict = false", 1)
			s = strings.Replace(s, `servers = [`, `servers = [] # [`, 1)
			write(t, root, f, s)
		}
		h := newHarness(t, root)
		if r := h.run("doctor", "--policy"); strings.Contains(r.out, "POL005") && strings.Contains(r.out, "strict-mcp-config") {
			t.Errorf("strict = false needs no strict flag:\n%s", r.out)
		}
	})
	t.Run("unknown state is a warning and never fails", func(t *testing.T) {
		fakePolicy(t, &policy.Policy{PartialVisibility: true, PartialReasons: []string{"WSL without a readable Windows policy"}}, nil)
		h := newHarness(t, copyExample(t))
		r := h.run("doctor", "--policy", "--strict")
		if r.code != 0 || !strings.Contains(r.out, "POL007 warning") || !strings.Contains(r.out, "state unknown") || strings.Contains(r.out, "POL005") {
			t.Errorf("code %d\n%s\n%s", r.code, r.out, r.err)
		}
		// Features that are unknown for the same reason share one finding.
		if n := strings.Count(r.out, "profile sre needs"); n != 1 {
			t.Errorf("want one finding for profile sre, got %d:\n%s", n, r.out)
		}
	})
	t.Run("unreadable policy is a warning, exit 3 only with --strict", func(t *testing.T) {
		fakePolicy(t, &policy.Policy{Unreadable: true, Unknown: []string{"managed-settings.json: permission denied"}}, nil)
		h := newHarness(t, copyExample(t))
		r := h.run("doctor", "--policy")
		if r.code != 0 || !strings.Contains(r.out, "POL001 warning") {
			t.Errorf("without --strict: code %d\n%s", r.code, r.out)
		}
		r = h.run("doctor", "--policy", "--strict")
		if r.code != ui.ExitPolicy || !strings.Contains(r.out, "POL001 error") {
			t.Errorf("with --strict: code %d\n%s", r.code, r.out)
		}
	})
	t.Run("blocked marketplace", func(t *testing.T) {
		src := policy.MarketplaceSource{Kind: "github", Ref: "acme/plugins"}
		tests := []struct {
			name    string
			pol     *policy.Policy
			wantErr bool
		}{
			{"listed in blockedMarketplaces", &policy.Policy{
				ExtraKnownMarketplaces: map[string]policy.MarketplaceSource{"acme": src}, BlockedMarketplaces: []policy.MarketplaceSource{src},
			}, true},
			{"empty strictKnownMarketplaces allows nothing", &policy.Policy{StrictKnownMarketplaces: []policy.MarketplaceSource{}}, true},
			{"not among strictKnownMarketplaces", &policy.Policy{
				ExtraKnownMarketplaces:  map[string]policy.MarketplaceSource{"acme": src},
				StrictKnownMarketplaces: []policy.MarketplaceSource{{Kind: "github", Ref: "other/repo"}},
			}, true},
			{"among strictKnownMarketplaces", &policy.Policy{
				ExtraKnownMarketplaces:  map[string]policy.MarketplaceSource{"acme": src},
				StrictKnownMarketplaces: []policy.MarketplaceSource{src},
			}, false},
			{"a pattern entry is not evaluated", &policy.Policy{
				ExtraKnownMarketplaces:  map[string]policy.MarketplaceSource{"acme": src},
				StrictKnownMarketplaces: []policy.MarketplaceSource{{Kind: "hostPattern", Ref: "^github\\.com$"}},
			}, false},
			{"unknown marketplace source is not guessed", &policy.Policy{
				StrictKnownMarketplaces: []policy.MarketplaceSource{{Kind: "github", Ref: "other/repo"}},
			}, false},
			{"blocked source of another marketplace", &policy.Policy{
				ExtraKnownMarketplaces: map[string]policy.MarketplaceSource{"acme": src},
				BlockedMarketplaces:    []policy.MarketplaceSource{{Kind: "github", Ref: "bad/repo"}},
			}, false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				fakePolicy(t, tt.pol, nil)
				h := newHarness(t, copyExample(t))
				r := h.run("doctor", "--policy")
				got := strings.Contains(r.out, "POL006")
				if got != tt.wantErr || (tt.wantErr && r.code != ui.ExitPolicy) || (!tt.wantErr && r.code != 0) {
					t.Errorf("POL006=%v code=%d, want error=%v\n%s", got, r.code, tt.wantErr, r.out)
				}
			})
		}
	})
}

func TestPolicyHelpers(t *testing.T) {
	if got := neededFeatures(policy.Needs{}); len(got) != 0 {
		t.Errorf("no needs: %v", got)
	}
	got := neededFeatures(policy.Needs{HideConnectors: true, ExtraMCPServers: true, StrictMCPConfig: true, DropUserSettingSources: true, AppendSystemPromptFile: true})
	if len(got) != 5 || got[0] != policy.HideConnectors || got[4] != policy.AppendSystemPromptFile {
		t.Errorf("needs: %v", got)
	}
}

package catalog_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yorch/ccshelf/internal/bundles"
	"github.com/yorch/ccshelf/internal/catalog"
	"github.com/yorch/ccshelf/internal/catalog/lint"
	"github.com/yorch/ccshelf/internal/orgconfig"
	"github.com/yorch/ccshelf/internal/profile"
)

// TestStarterTemplate runs the whole toolchain over the committed starter
// template examples/org-data-repo: the org config loads, the lint reports
// exactly the expected findings, every profile resolves, the MCP registry
// parses, the generated bundles match byte for byte and the catalog builds.
func TestStarterTemplate(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "examples", "org-data-repo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Skip("starter template not present")
	}
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(k, home)
	}
	now := func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }

	cfg, err := orgconfig.Load(root)
	if err != nil {
		t.Fatalf("orgconfig.Load: %v", err)
	}
	if len(cfg.Lint.PlatformOwners) != 1 || cfg.Lint.PlatformOwners[0] != "@acme/platform" {
		t.Errorf("platform_owners = %v", cfg.Lint.PlatformOwners)
	}

	// Lint: no errors and no warnings. The only findings are the informational
	// notes about code that runs on developer machines and the external plugin.
	report, err := lint.Run(root, cfg, lint.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range report.Findings {
		got = append(got, string(f.Severity)+" "+f.Code+" "+f.Plugin)
	}
	sort.Strings(got)
	want := []string{
		"info CAT031 partner-linter",
		"info CAT040 audit-logger",
		"info CAT040 sre-kit",
		"info CAT041 seo-tools",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lint findings:\n%s\nwant:\n%s\nfull report:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"), lint.FormatText(report))
	}

	// Profiles resolve and the bundles match what the compiler generates.
	src := profile.DirSource(profile.KindOrg, filepath.Join(root, cfg.Profiles.Dir))
	entries, err := os.ReadDir(filepath.Join(root, cfg.Profiles.Dir))
	if err != nil {
		t.Fatal(err)
	}
	var inputs []bundles.Input
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok {
			continue
		}
		r, err := profile.Resolve(name, []profile.Source{src}, profile.ResolveOptions{})
		if err != nil {
			t.Fatalf("resolve %s: %v", name, err)
		}
		inputs = append(inputs, bundles.Input{Profile: name, Marketplace: "acme", Plugins: r.Merged.Plugins.Include})
	}
	res, err := bundles.Compile(inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "base" || len(res.Files) != 3 {
		t.Errorf("compile: skipped %v, %d files", res.Skipped, len(res.Files))
	}
	drift, err := bundles.Check(root, res.Files)
	if err != nil {
		t.Fatal(err)
	}
	if drift.HasDrift() {
		t.Errorf("bundles drift from the compiler output: %+v", drift)
	}

	// The MCP registry obeys the closed schema.
	reg, err := profile.LoadRegistry(filepath.Join(root, cfg.Profiles.MCPRegistry))
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if len(reg) != 2 || reg["figma"].Command == "" || reg["pagerduty-ro"].URL == "" {
		t.Errorf("registry = %+v", reg)
	}

	// The catalog builds from the same data.
	c, rep, err := catalog.Build(root, cfg, catalog.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if rep.HasErrors() || rep.Counts().Warnings != 0 {
		t.Errorf("catalog report:\n%s", lint.FormatText(rep))
	}
	var names []string
	for _, p := range c.Plugins {
		names = append(names, p.Name)
	}
	wantNames := "audit-logger,design-kit,docs-writer,partner-linter,release-notes,seo-tools,sre-kit"
	if strings.Join(names, ",") != wantNames {
		t.Errorf("catalog plugins = %v (bundles must be left out)", names)
	}
}

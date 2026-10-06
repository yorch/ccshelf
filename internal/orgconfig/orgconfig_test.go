package orgconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/catalog/catalogtest"
	toml "github.com/pelletier/go-toml/v2"
)

const full = `
[lint]
require = ["owner", "status", "support"]
require_when_deprecated = ["superseded_by", "review_by"]
max_review_age_days = 90
taxonomy = "meta/tax.toml"
min_description_length = 10
platform_owners = ["@acme/platform", "sec@example.com"]

[catalog]
title = "Acme plugin catalog"
metadata_source = "marketplace"
marketplaces = [".claude-plugin/marketplace.json", "team/marketplace.json"]
git_data = true
base_url = "https://catalog.example.com/"

[profiles]
dir = "profs"
mcp_registry = "mcp.toml"

[protect]
plugins = ["audit-log@acme-tools"]
mcp = ["plugin:audit:audit", "claude.ai Shopify"]
`

func TestDefaults(t *testing.T) {
	root := t.TempDir()
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Errorf("missing file should give defaults: %+v", cfg)
	}
	d := Default()
	if d.Lint.MaxReviewAgeDays != 180 || d.Lint.MinDescriptionLength != 30 || d.Catalog.MetadataSource != "sidecar" ||
		d.Profiles.Dir != "profiles" || d.Profiles.MCPRegistry != "mcp/registry.toml" || d.Lint.Taxonomy != "catalog/taxonomy.toml" ||
		strings.Join(d.Lint.Require, ",") != "owner,status" || strings.Join(d.Lint.RequireWhenDeprecated, ",") != "superseded_by" {
		t.Errorf("defaults wrong: %+v", d)
	}
	if err := d.Validate(); err != nil {
		t.Errorf("defaults invalid: %v", err)
	}
}

func TestParseFull(t *testing.T) {
	cfg, err := Parse([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Lint.MaxReviewAgeDays != 90 || len(cfg.Lint.Require) != 3 || cfg.Catalog.Title != "Acme plugin catalog" ||
		!cfg.Catalog.GitData || cfg.Profiles.Dir != "profs" || !cfg.IsProtectedPlugin("audit-log@acme-tools") ||
		cfg.IsProtectedPlugin("x@y") || !cfg.IsProtectedMCP("claude.ai Shopify") || cfg.IsProtectedMCP("nope") {
		t.Errorf("parsed = %+v", cfg)
	}
}

func TestPartialKeepsDefaults(t *testing.T) {
	cfg, err := Parse([]byte("[catalog]\ntitle = \"T\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Catalog.Title != "T" || cfg.Lint.MaxReviewAgeDays != 180 || cfg.Catalog.Marketplaces[0] != marketplaceDefault {
		t.Errorf("partial = %+v", cfg)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"unknown key", "[lint]\nrequire_owner = true\n", "unknown key"},
		{"unknown section", "[extra]\na = 1\n", "unknown key"},
		{"syntax", "[lint\n", "line"},
		{"type", "[lint]\nmax_review_age_days = \"x\"\n", "lint.max_review_age_days"},
		{"bad field", "[lint]\nrequire = [\"owner\", \"nope\"]\n", "unknown sidecar field"},
		{"dup field", "[lint]\nrequire = [\"owner\", \"owner\"]\n", "listed twice"},
		{"bad deprecated field", "[lint]\nrequire_when_deprecated = [\"x\"]\n", "unknown sidecar field"},
		{"age", "[lint]\nmax_review_age_days = 0\n", "must be positive"},
		{"min len", "[lint]\nmin_description_length = -1\n", "must not be negative"},
		{"taxonomy abs", "[lint]\ntaxonomy = \"/etc/x\"\n", "lint.taxonomy"},
		{"taxonomy dotdot", "[lint]\ntaxonomy = \"../x\"\n", "lint.taxonomy"},
		{"owner", "[lint]\nplatform_owners = [\"nobody\"]\n", "platform_owners"},
		{"source enum", "[catalog]\nmetadata_source = \"db\"\n", "metadata_source"},
		{"no marketplaces", "[catalog]\nmarketplaces = []\n", "at least one"},
		{"marketplace not json", "[catalog]\nmarketplaces = [\"m.toml\"]\n", "must be a .json"},
		{"marketplace dup", "[catalog]\nmarketplaces = [\"a.json\", \"a.json\"]\n", "listed twice"},
		{"marketplace backslash", "[catalog]\nmarketplaces = [\"a\\\\b.json\"]\n", "backslash"},
		{"title newline", "[catalog]\ntitle = \"a\\nb\"\n", "catalog.title"},
		{"base url", "[catalog]\nbase_url = \"javascript:alert(1)\"\n", "base_url"},
		{"profiles dir empty", "[profiles]\ndir = \"\"\n", "profiles.dir"},
		{"profiles abs", "[profiles]\nmcp_registry = \"/x\"\n", "profiles.mcp_registry"},
		{"protect plugin", "[protect]\nplugins = [\"audit\"]\n", "name@marketplace"},
		{"protect plugin injection", "[protect]\nplugins = [\"a@b;rm\"]\n", "name@marketplace"},
		{"protect mcp", "[protect]\nmcp = [\"bad\\nlabel\"]\n", "MCP server label"},
		{"protect mcp empty", "[protect]\nmcp = [\"\"]\n", "MCP server label"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.in))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadFileAndErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil || cfg.Profiles.Dir != "profs" {
		t.Fatalf("Load = %+v, %v", cfg, err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("[bogus]\nx=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), FileName) {
		t.Errorf("err = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(strings.Repeat("#", MaxFileSize+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("oversized: %v", err)
	}
}

func TestFindReportsMissingFile(t *testing.T) {
	root := t.TempDir()
	cfg, found, err := Find(root)
	if err != nil || found || cfg == nil || cfg.Profiles.Dir != "profiles" {
		t.Fatalf("Find(empty) = %+v, %v, %v", cfg, found, err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err = Find(root); err != nil || !found {
		t.Errorf("an empty ccshelf.toml is a present file: found=%v err=%v", found, err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("[bogus]\nx=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg, found, err = Find(root); err == nil || !found || cfg != nil {
		t.Errorf("a broken file is found and an error: %+v %v %v", cfg, found, err)
	}
}

func TestCheckPathsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	outside := t.TempDir()
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "profiles")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), []byte("[catalog]\ntitle=\"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "profiles.dir") {
		t.Errorf("symlink escape not caught: %v", err)
	}
}

func TestSidecarFieldsStable(t *testing.T) {
	if got := len(SidecarFields()); got != 9 {
		t.Errorf("SidecarFields = %d entries", got)
	}
	if !ValidOwner("@acme/sre") || !ValidOwner("@user") || !ValidOwner("a@b.co") || ValidOwner("sre") || ValidOwner("@a b") {
		t.Error("ValidOwner")
	}
}

func tomlKeys(v any) []string {
	rt := reflect.TypeOf(v)
	var keys []string
	for i := 0; i < rt.NumField(); i++ {
		if k := rt.Field(i).Tag.Get("toml"); k != "" && k != "-" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func TestSchemaMatchesStructs(t *testing.T) {
	s := catalogtest.LoadSchema(t, "orgconfig.schema.json")
	for section, v := range map[string]any{"lint": Lint{}, "catalog": Catalog{}, "profiles": Profiles{}, "protect": Protect{}} {
		if got, want := catalogtest.SchemaProperties(s, section), tomlKeys(v); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: schema %v, struct %v", section, got, want)
		}
	}
	if got, want := catalogtest.SchemaProperties(s), tomlKeys(Config{}); !reflect.DeepEqual(got, want) {
		t.Errorf("top level: schema %v, struct %v", got, want)
	}
	// The sample config validates against the schema.
	var doc map[string]any
	if err := toml.Unmarshal([]byte(full), &doc); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(doc)
	var asJSON any
	_ = json.Unmarshal(b, &asJSON)
	if probs := catalogtest.Validate(s, asJSON); len(probs) > 0 {
		t.Errorf("schema problems: %v", probs)
	}
}

func TestErrorOrderIsStable(t *testing.T) {
	const bad = "[profiles]\ndir = \"/abs\"\nmcp_registry = \"../up\"\n[lint]\ntaxonomy = \"../t\"\n"
	var first string
	for i := 0; i < 20; i++ {
		_, err := Parse([]byte(bad))
		if err == nil {
			t.Fatal("want an error")
		}
		if i == 0 {
			first = err.Error()
			if strings.Index(first, "profiles.dir") > strings.Index(first, "profiles.mcp_registry") {
				t.Fatalf("keys out of order:\n%s", first)
			}
		} else if err.Error() != first {
			t.Fatalf("error text changed between runs:\n%s\n---\n%s", first, err.Error())
		}
	}
}

func TestCheckPathsOrderIsStable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	out := t.TempDir()
	for _, n := range []string{"t", "p", "r"} {
		if err := os.Symlink(out, filepath.Join(root, n)); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Default()
	cfg.Lint.Taxonomy, cfg.Profiles.Dir, cfg.Profiles.MCPRegistry = "t/x", "p/x", "r/x"
	var first string
	for i := 0; i < 20; i++ {
		err := cfg.CheckPaths(root)
		if err == nil {
			t.Fatal("want an error")
		}
		if i == 0 {
			first = err.Error()
			if !(strings.Index(first, "lint.taxonomy") < strings.Index(first, "profiles.dir") && strings.Index(first, "profiles.dir") < strings.Index(first, "profiles.mcp_registry")) {
				t.Fatalf("keys out of order:\n%s", first)
			}
		} else if err.Error() != first {
			t.Fatal("error text changed between runs")
		}
	}
}

package marketplace

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `{
  "name": "acme-tools",
  "owner": {"name": "Acme Platform", "email": "platform@example.com", "url": "https://example.com"},
  "metadata": {"description": "Acme plugins", "pluginRoot": "./plugins", "future": 1},
  "renames": {"old-kit": "design-kit", "gone-kit": null},
  "forceRemoveDeletedPlugins": true,
  "allowCrossMarketplaceDependenciesOn": ["other-market"],
  "somethingNew": {"a": 1},
  "plugins": [
    {"name": "design-kit", "source": "design-kit", "description": "Design helpers", "category": "design",
     "tags": ["ui", "css"], "version": "1.2.0", "strict": false, "displayName": "Design Kit",
     "defaultEnabled": true, "author": "Web Team", "homepage": "https://example.com/dk",
     "repository": "https://example.com/dk.git", "license": "MIT", "keywords": ["k"],
     "dependencies": ["base-kit", {"name": "x", "version": "~1.0", "marketplace": "other-market"}, "y@other"],
     "relevance": {"topic": "ui", "signals": {"cwd": ["web"], "manifestDeps": [{"file": "package.json", "pattern": "react"}]}},
     "metadata": {"owner": "@acme/web"}, "hooks": {"PreToolUse": []}},
    {"name": "ext", "source": {"source": "github", "repo": "acme/ext"}, "author": {"name": "Ext", "email": "e@example.com"}},
    {"name": "bare", "source": "./bare"}
  ]
}`

func TestParse(t *testing.T) {
	m, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "acme-tools" || m.Owner.Name != "Acme Platform" || m.Description != "Acme plugins" || m.PluginRoot != "./plugins" {
		t.Errorf("top-level fields: %+v", m)
	}
	if !m.ForceRemoveDeletedPlugins || len(m.AllowCrossMarketplaceDependenciesOn) != 1 {
		t.Errorf("flags: %+v", m)
	}
	if v, ok := m.Renames["gone-kit"]; !ok || v != nil {
		t.Errorf("null rename lost: %v", m.Renames)
	}
	if got := *m.Renames["old-kit"]; got != "design-kit" {
		t.Errorf("rename = %q", got)
	}
	if _, ok := m.Raw["somethingNew"]; !ok {
		t.Error("unknown top-level key not recorded")
	}
	if _, ok := m.Raw["metadata.future"]; !ok {
		t.Error("unknown metadata key not recorded")
	}
	if len(m.Plugins) != 3 {
		t.Fatalf("plugins = %d", len(m.Plugins))
	}
	dk := m.Plugins[0]
	if dk.Source.Path != "plugins/design-kit" || !dk.Source.IsLocal() || dk.Source.LocalPath() != "plugins/design-kit" {
		t.Errorf("pluginRoot not applied: %+v", dk.Source)
	}
	if dk.Author.Name != "Web Team" || dk.Strict == nil || *dk.Strict || dk.DefaultEnabled == nil || !*dk.DefaultEnabled {
		t.Errorf("fields: %+v", dk)
	}
	if len(dk.Dependencies) != 3 || dk.Dependencies[0].Name != "base-kit" || dk.Dependencies[1].Marketplace != "other-market" ||
		dk.Dependencies[2].Name != "y" || dk.Dependencies[2].Marketplace != "other" {
		t.Errorf("dependencies = %+v", dk.Dependencies)
	}
	if dk.Relevance == nil || dk.Relevance.Signals.ManifestDeps[0].Pattern != "react" {
		t.Errorf("relevance = %+v", dk.Relevance)
	}
	if dk.Metadata["owner"] != "@acme/web" {
		t.Errorf("metadata = %v", dk.Metadata)
	}
	if _, ok := dk.Extra["hooks"]; !ok {
		t.Error("inline hooks key not kept in Extra")
	}
	ext := m.Plugins[1]
	if ext.Source.IsLocal() || ext.Source.Kind != "github" || ext.Source.LocalPath() != "" || ext.Source.Summary() != "github:acme/ext" {
		t.Errorf("external source = %+v", ext.Source)
	}
	if ext.Author.Email != "e@example.com" || ext.Author.String() != "Ext" {
		t.Errorf("author = %+v", ext.Author)
	}
	if m.Plugins[2].Source.Path != "bare" {
		t.Errorf("explicit ./ path = %q", m.Plugins[2].Source.Path)
	}
	if p, ok := m.Plugin("ext"); !ok || p.Name != "ext" {
		t.Error("Plugin lookup failed")
	}
	if _, ok := m.Plugin("nope"); ok {
		t.Error("Plugin lookup of missing name succeeded")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"not json", `nope`, "invalid JSON"},
		{"not object", `[]`, "invalid JSON"},
		{"name missing", `{"plugins":[]}`, "name: required"},
		{"name type", `{"name": 3}`, "name: expected string"},
		{"plugins type", `{"name":"a","plugins":{}}`, "plugins:"},
		{"plugin not object", `{"name":"a","plugins":[1]}`, "plugins[0]:"},
		{"plugin name missing", `{"name":"a","plugins":[{"source":"./x"}]}`, "plugins[0].name: required"},
		{"source missing", `{"name":"a","plugins":[{"name":"x"}]}`, "plugins[0].source: required"},
		{"source type", `{"name":"a","plugins":[{"name":"x","source":3}]}`, "plugins[0].source:"},
		{"tags type", `{"name":"a","plugins":[{"name":"x","source":"./x","tags":"ui"}]}`, "plugins[0].tags:"},
		{"author type", `{"name":"a","plugins":[{"name":"x","source":"./x","author":3}]}`, "plugins[0].author:"},
		{"dep type", `{"name":"a","plugins":[{"name":"x","source":"./x","dependencies":[1]}]}`, "plugins[0].dependencies: must be a string or an object"},
		{"strict type", `{"name":"a","plugins":[{"name":"x","source":"./x","strict":"yes"}]}`, "plugins[0].strict:"},
		{"owner type", `{"name":"a","owner":"me"}`, "owner:"},
		{"second plugin", `{"name":"a","plugins":[{"name":"x","source":"./x"},{"name":3,"source":"./y"}]}`, "plugins[1].name:"},
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

func TestSourcePaths(t *testing.T) {
	tests := []struct{ root, in, want string }{
		{"", "./a/b", "a/b"},
		{"", "./a/../b", "b"},
		{"", "./", ""},
		{"./plugins", "x", "plugins/x"},
		{"./plugins", "./x", "x"},
		{"", "a\\b", "a/b"},
		{"", "../up", "../up"},
	}
	for _, tt := range tests {
		m := &Marketplace{PluginRoot: tt.root}
		p := Plugin{Source: Source{Kind: "path", Declared: tt.in, Path: tt.in}}
		applyPluginRoot(m, &p)
		if p.Source.Path != tt.want {
			t.Errorf("root %q in %q: got %q want %q", tt.root, tt.in, p.Source.Path, tt.want)
		}
	}
	if (Source{Kind: "", Raw: map[string]any{}}).Summary() != "unknown" {
		t.Error("Summary of unknown kind")
	}
	if (Source{Kind: "npm", Raw: map[string]any{"package": "p"}}).Summary() != "npm:p" {
		t.Error("Summary of npm")
	}
}

func TestAuthorZero(t *testing.T) {
	if !(Author{}).IsZero() || (Author{URL: "u"}).IsZero() {
		t.Error("IsZero")
	}
	if (Author{Email: "e"}).String() != "e" || (Author{URL: "u"}).String() != "u" {
		t.Error("String fallbacks")
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v", err)
	}
	write(t, root, FilePath, `{"name":"a","plugins":[{"name":"x","source":3}]}`)
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "plugins[0].source") || !strings.Contains(err.Error(), FilePath) {
		t.Errorf("bad type: %v", err)
	}
	write(t, root, FilePath, sample)
	m, err := Load(root)
	if err != nil || m.Name != "acme-tools" {
		t.Fatalf("Load = %v, %v", m, err)
	}
	write(t, root, "other.json", `{"name":"o"}`)
	if m, err := LoadFile(root, "other.json"); err != nil || m.Name != "o" {
		t.Errorf("LoadFile = %v, %v", m, err)
	}
	if _, err := LoadFile(root, "../x.json"); err == nil {
		t.Error("LoadFile escape should fail")
	}
	if _, err := LoadFile(root, "bad.json"); err == nil {
		t.Error("LoadFile missing should fail")
	}
	write(t, root, "bad.json", "{")
	if _, err := LoadFile(root, "bad.json"); err == nil {
		t.Error("LoadFile invalid should fail")
	}
}

func TestLoadTooLarge(t *testing.T) {
	root := t.TempDir()
	write(t, root, FilePath, `{"name":"a","pad":"`+strings.Repeat("x", MaxFileSize)+`"}`)
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("err = %v", err)
	}
	if _, err := Parse(make([]byte, MaxFileSize+1)); err == nil {
		t.Error("Parse of oversized data should fail")
	}
}

func TestReservedNames(t *testing.T) {
	tests := []struct {
		name  string
		want  bool // reason expected
		isErr bool
	}{
		{"design-kit", false, false},
		{"claude-helper", true, true},
		{"Claude-Helper", true, true},
		{"anthropic-tools", true, true},
		{"cc-plugin-x", true, true},
		{"claude", true, false},
		{"my-claude", true, false},
		{"claude_x", true, false},
		{"claudette", false, false},
		{"claudex-kit", false, false},
		{"anthropic", false, false},
	}
	for _, tt := range tests {
		reason, isErr := ReservedNameCheck(tt.name)
		if (reason != "") != tt.want || isErr != tt.isErr {
			t.Errorf("%q: reason=%q isErr=%v", tt.name, reason, isErr)
		}
		if (ReservedNameReason(tt.name) != "") != tt.want {
			t.Errorf("%q: ReservedNameReason mismatch", tt.name)
		}
	}
}

func TestInspect(t *testing.T) {
	root := t.TempDir()
	write(t, root, "plugins/full/.claude-plugin/plugin.json", `{"name":"full","version":"2.0.0","description":"d","dependencies":["a",{"name":"b"}]}`)
	write(t, root, "plugins/full/hooks/hooks.json", `{}`)
	write(t, root, "plugins/full/.mcp.json", `{}`)
	write(t, root, "plugins/full/skills/one/SKILL.md", "x")
	write(t, root, "plugins/full/skills/two/SKILL.md", "x")
	write(t, root, "plugins/full/skills/empty/README.md", "x")
	write(t, root, "plugins/full/agents/a.md", "x")
	write(t, root, "plugins/full/agents/notes.txt", "x")
	write(t, root, "plugins/full/commands/c1.md", "x")
	write(t, root, "plugins/full/commands/c2.MD", "x")
	write(t, root, "plugins/inline/.claude-plugin/plugin.json", `{"name":"inline","hooks":{"a":[]},"mcpServers":{"s":{}}}`)
	write(t, root, "plugins/bare/README.md", "x")
	write(t, root, "plugins/broken/.claude-plugin/plugin.json", `{"name": 3}`)
	write(t, root, "plugins/afile", "x")

	local := func(path string) Plugin {
		return Plugin{Name: "p", Source: Source{Kind: "path", Path: path}}
	}
	info, err := Inspect(root, local("plugins/full"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "full" || info.Version != "2.0.0" || info.Description != "d" || len(info.Dependencies) != 2 ||
		!info.HasHooks || !info.HasMCP || info.Skills != 2 || info.Agents != 1 || info.Commands != 2 || info.ManifestMissing || info.External {
		t.Errorf("full = %+v", info)
	}
	info, err = Inspect(root, local("plugins/inline"))
	if err != nil || !info.HasHooks || !info.HasMCP {
		t.Errorf("inline = %+v, %v", info, err)
	}
	info, err = Inspect(root, local("plugins/bare"))
	if err != nil || !info.ManifestMissing || info.HasHooks || info.HasMCP {
		t.Errorf("bare = %+v, %v", info, err)
	}
	p := local("plugins/bare")
	p.Extra = map[string]json.RawMessage{"hooks": []byte(`{}`), "mcpServers": []byte(`{}`)}
	if info, _ = Inspect(root, p); !info.HasHooks || !info.HasMCP {
		t.Errorf("entry-level keys ignored: %+v", info)
	}
	if info, err = Inspect(root, Plugin{Source: Source{Kind: "github"}}); err != nil || !info.External {
		t.Errorf("external = %+v, %v", info, err)
	}
	if _, err = Inspect(root, local("plugins/broken")); err == nil || !strings.Contains(err.Error(), "plugin.json") {
		t.Errorf("broken manifest: %v", err)
	}
	if _, err = Inspect(root, local("plugins/missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing dir: %v", err)
	}
	if _, err = Inspect(root, local("plugins/afile")); err == nil {
		t.Error("file instead of dir should fail")
	}
	if _, err = Inspect(root, local("../outside")); err == nil {
		t.Error("escape should fail")
	}
	if info, err = Inspect(root, local("")); err != nil || info.Dir != "." {
		t.Errorf("root plugin = %+v, %v", info, err)
	}
}

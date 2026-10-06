package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/claude"
)

type result struct {
	code           int
	stdout, stderr string
}

func do(t *testing.T, env map[string]string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, func(k string) string { return env[k] }, &out, &errb)
	return result{code, out.String(), errb.String()}
}

func initOf(t *testing.T, env map[string]string, extra ...string) *claude.InitInfo {
	t.Helper()
	args := append([]string{"-p", "hi", "--output-format", "stream-json", "--verbose"}, extra...)
	r := do(t, env, args...)
	if r.code != 0 {
		t.Fatalf("exit %d: %s", r.code, r.stderr)
	}
	info, err := claude.ParseInit(strings.NewReader(r.stdout))
	if err != nil {
		t.Fatalf("%v\n%s", err, r.stdout)
	}
	return info
}

func write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func has(list []string, s string) bool { return slices.Contains(list, s) }

func serverNames(i *claude.InitInfo) []string {
	var out []string
	for _, s := range i.MCPServers {
		out = append(out, s.Name)
	}
	return out
}

func TestVersion(t *testing.T) {
	if r := do(t, nil, "--version"); r.stdout != "2.1.291 (Claude Code)\n" || r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := do(t, map[string]string{"FAKE_CLAUDE_VERSION": "3.0.1"}, "-v"); r.stdout != "3.0.1 (Claude Code)\n" {
		t.Fatalf("%+v", r)
	}
}

func TestPluginList(t *testing.T) {
	r := do(t, nil, "plugin", "list", "--json")
	var list []claude.Plugin
	if err := json.Unmarshal([]byte(r.stdout), &list); err != nil || len(list) != 5 || r.stderr != "" {
		t.Fatalf("%v %d %+v", err, len(list), r)
	}
	r = do(t, nil, "plugin", "list", "--json", "--available")
	var obj struct {
		Installed []claude.Plugin
		Available []claude.AvailablePlugin
	}
	if err := json.Unmarshal([]byte(r.stdout), &obj); err != nil || len(obj.Installed) != 5 || len(obj.Available) != 2 {
		t.Fatalf("%v %+v", err, r)
	}
	if r := do(t, nil, "plugin", "list"); !strings.Contains(r.stdout, "sre-kit@acme") {
		t.Fatalf("%+v", r)
	}
	f := write(t, "p.json", `[{"id":"only@one","scope":"user","enabled":true}]`)
	r = do(t, map[string]string{"FAKE_CLAUDE_PLUGINS": f}, "plugin", "list", "--json")
	if !strings.Contains(r.stdout, "only@one") || strings.Contains(r.stdout, "sre-kit") {
		t.Fatalf("%+v", r)
	}
	av := write(t, "a.json", `[]`)
	r = do(t, map[string]string{"FAKE_CLAUDE_AVAILABLE": av}, "plugin", "list", "--json", "--available")
	if !strings.Contains(r.stdout, `"available": []`) {
		t.Fatalf("%+v", r)
	}
	r = do(t, map[string]string{"FAKE_CLAUDE_PLUGIN_LIST_FAIL": "1"}, "plugin", "list", "--json")
	if r.code != 1 || r.stdout != "" || r.stderr == "" {
		t.Fatalf("%+v", r)
	}
	r = do(t, map[string]string{"FAKE_CLAUDE_PLUGINS": "/nonexistent"}, "plugin", "list", "--json")
	if r.code != 1 {
		t.Fatalf("%+v", r)
	}
	bad := write(t, "bad.json", `nope`)
	if r := do(t, map[string]string{"FAKE_CLAUDE_PLUGINS": bad}, "plugin", "list", "--json"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
	if r := do(t, map[string]string{"FAKE_CLAUDE_AVAILABLE": "/nonexistent"}, "plugin", "list", "--json", "--available"); r.code != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestAgents(t *testing.T) {
	if r := do(t, nil, "agents", "--json", "--all"); r.stdout != "[]\n" {
		t.Fatalf("%+v", r)
	}
	if r := do(t, map[string]string{"FAKE_CLAUDE_AGENTS_JSON": `[{"p":"/x"}]`}, "agents", "--json", "--all"); r.stdout != "[{\"p\":\"/x\"}]\n" {
		t.Fatalf("%+v", r)
	}
}

func TestGenericInvocation(t *testing.T) {
	r := do(t, nil)
	if r.code != 0 || r.stdout != "fake claude: ok\n" {
		t.Fatalf("%+v", r)
	}
	if r := do(t, map[string]string{"FAKE_CLAUDE_EXIT": "7"}, "--resume"); r.code != 7 {
		t.Fatalf("%+v", r)
	}
	if r := do(t, map[string]string{"FAKE_CLAUDE_SLEEP": "5"}, "x"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if r := do(t, nil, "-p", "hi"); r.stdout != "fake claude: ok\n" {
		t.Fatalf("-p without stream-json: %+v", r)
	}
}

func TestLog(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log.jsonl")
	env := map[string]string{"FAKE_CLAUDE_LOG": log, "CCSHELF_PROFILE": "sre"}
	do(t, env, "--settings", "x.json", "--resume")
	do(t, env, "--version")
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines: %q", b)
	}
	var inv invocation
	if err := json.Unmarshal([]byte(lines[0]), &inv); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(inv.Argv, []string{"--settings", "x.json", "--resume"}) || inv.Env["CCSHELF_PROFILE"] != "sre" || inv.Cwd == "" {
		t.Fatalf("%+v", inv)
	}
	// An unwritable log path must not break the run.
	if r := do(t, map[string]string{"FAKE_CLAUDE_LOG": filepath.Join(log, "nope", "x")}, "--version"); r.code != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestInitBaseline(t *testing.T) {
	info := initOf(t, nil)
	if len(info.Plugins) != 5 || !has(info.Plugins, "sre-kit") {
		t.Fatalf("plugins %v", info.Plugins)
	}
	if info.PermissionMode != "default" || info.Model != "fake-model" {
		t.Fatalf("%+v", info)
	}
	if !has(info.Skills, "pdf") || !has(info.Skills, "sre-kit:runbook") || !has(info.Skills, "design-kit:main") {
		t.Fatalf("skills %v", info.Skills)
	}
	want := []string{"claude.ai Shopify", "claude.ai Slack", "plugin:context7:context7"}
	if got := serverNames(info); !slices.Equal(got, want) {
		t.Fatalf("servers %v", got)
	}
	if !has(info.Tools, "mcp__claude.ai Slack") || len(info.Agents) == 0 {
		t.Fatalf("%+v", info)
	}
	if m := initOf(t, nil, "--model", "haiku"); m.Model != "haiku" {
		t.Fatalf("model %q", m.Model)
	}
}

func TestInitMasking(t *testing.T) {
	file := write(t, "s.json", `{"enabledPlugins":{"sre-kit@acme":false,"design-kit@acme":false}}`)
	info := initOf(t, nil, "--settings", file)
	if has(info.Plugins, "sre-kit") || has(info.Plugins, "design-kit") || !has(info.Plugins, "seo-tools") {
		t.Fatalf("per-key merge broken: %v", info.Plugins)
	}
	if has(info.Skills, "sre-kit:runbook") {
		t.Fatalf("skills of masked plugin remain: %v", info.Skills)
	}
	// Inline JSON works the same.
	info = initOf(t, nil, "--settings", `{"enabledPlugins":{"sre-kit@acme":false}}`)
	if has(info.Plugins, "sre-kit") {
		t.Fatalf("inline: %v", info.Plugins)
	}
	// Masking the MCP plugin removes its server.
	info = initOf(t, nil, "--settings", `{"enabledPlugins":{"context7@claude-plugins-official":false}}`)
	if has(serverNames(info), "plugin:context7:context7") {
		t.Fatalf("servers %v", serverNames(info))
	}
	// Unknown ids are silently ignored.
	info = initOf(t, nil, "--settings", `{"enabledPlugins":{"ghost@x":true}}`)
	if len(info.Plugins) != 5 {
		t.Fatalf("%v", info.Plugins)
	}
}

func TestInitLayers(t *testing.T) {
	user := write(t, "user.json", `{"enabledPlugins":{"seo-tools@acme":false},"permissions":{"defaultMode":"plan"}}`)
	project := write(t, "project.json", `{"enabledPlugins":{"design-kit@acme":false}}`)
	env := map[string]string{"FAKE_CLAUDE_USER_ENABLED": user, "FAKE_CLAUDE_PROJECT_SETTINGS": project}
	info := initOf(t, env)
	if has(info.Plugins, "seo-tools") || has(info.Plugins, "design-kit") || info.PermissionMode != "plan" {
		t.Fatalf("%+v", info)
	}
	// --settings beats the project layer.
	info = initOf(t, env, "--settings", `{"enabledPlugins":{"design-kit@acme":true}}`)
	if !has(info.Plugins, "design-kit") {
		t.Fatalf("command line should win: %v", info.Plugins)
	}
	// Project-enabled: a plugin disabled at user scope can be enabled by project.
	f := write(t, "p.json", `[{"id":"a@m","scope":"user","enabled":false},{"id":"b@m","scope":"user","enabled":true}]`)
	env = map[string]string{"FAKE_CLAUDE_PLUGINS": f, "FAKE_CLAUDE_PROJECT_SETTINGS": `{"enabledPlugins":{"a@m":true}}`}
	if info := initOf(t, env); !slices.Equal(info.Plugins, []string{"a", "b"}) {
		t.Fatalf("%v", info.Plugins)
	}
	// A plain map is accepted as the user layer.
	env = map[string]string{"FAKE_CLAUDE_USER_ENABLED": `{"b@m":false}`, "FAKE_CLAUDE_PLUGINS": f}
	if info := initOf(t, env); len(info.Plugins) != 0 {
		t.Fatalf("%v", info.Plugins)
	}
}

func TestSettingSources(t *testing.T) {
	project := `{"enabledPlugins":{"design-kit@acme":true}}`
	env := map[string]string{"FAKE_CLAUDE_PROJECT_SETTINGS": project}
	for _, src := range []string{"project,local", ""} {
		info := initOf(t, env, "--setting-sources", src)
		if src == "" && len(info.Plugins) != 0 {
			t.Fatalf("empty sources should drop everything: %v", info.Plugins)
		}
		if src != "" && !slices.Equal(info.Plugins, []string{"design-kit"}) {
			t.Fatalf("project,local: %v", info.Plugins)
		}
	}
	info := initOf(t, env, "--setting-sources=user")
	if len(info.Plugins) != 5 {
		t.Fatalf("user only: %v", info.Plugins)
	}
}

func TestSilentIgnoreAndMissing(t *testing.T) {
	bad := []string{
		`not json`,
		`{"enabledPlugins":{"sre-kit@acme":"false"}}`,
		`{"skillOverrides":{"pdf":"banana"}}`,
		`{"enabledPlugins":[1]}`,
		`[]`,
	}
	for _, b := range bad {
		file := write(t, "bad.json", b)
		var out, errb bytes.Buffer
		code := run([]string{"-p", "x", "--output-format", "stream-json", "--settings", file}, func(string) string { return "" }, &out, &errb)
		if code != 0 || errb.Len() != 0 {
			t.Fatalf("%q: code %d stderr %q", b, code, errb.String())
		}
		info, err := claude.ParseInit(&out)
		if err != nil || len(info.Plugins) != 5 {
			t.Fatalf("%q: mask applied from invalid file: %v %v", b, err, info)
		}
	}
	r := do(t, nil, "-p", "x", "--settings", filepath.Join(t.TempDir(), "missing.json"))
	if r.code != 1 || !strings.Contains(r.stderr, "Settings file not found") {
		t.Fatalf("%+v", r)
	}
}

func TestConnectorsAndMCP(t *testing.T) {
	info := initOf(t, nil, "--settings", `{"disableClaudeAiConnectors":true}`)
	if got := serverNames(info); !slices.Equal(got, []string{"plugin:context7:context7"}) {
		t.Fatalf("%v", got)
	}
	// Exact full-name matching only.
	info = initOf(t, nil, "--settings", `{"deniedMcpServers":[{"serverName":"context7"},{"serverName":"claude.ai Shopify"}]}`)
	got := serverNames(info)
	if !has(got, "plugin:context7:context7") || has(got, "claude.ai Shopify") || !has(got, "claude.ai Slack") {
		t.Fatalf("%v", got)
	}
	info = initOf(t, nil, "--settings", `{"deniedMcpServers":[{"serverName":"plugin:context7:context7"}]}`)
	if has(serverNames(info), "plugin:context7:context7") {
		t.Fatalf("%v", serverNames(info))
	}
	// --strict-mcp-config alone: zero servers; plugins unchanged.
	info = initOf(t, nil, "--strict-mcp-config")
	if len(info.MCPServers) != 0 || len(info.Plugins) != 5 {
		t.Fatalf("%+v", info)
	}
	cfg := write(t, "mcp.json", `{"mcpServers":{"mine":{"command":"x"}}}`)
	info = initOf(t, nil, "--strict-mcp-config", "--mcp-config", cfg)
	if got := serverNames(info); !slices.Equal(got, []string{"mine"}) {
		t.Fatalf("%v", got)
	}
	info = initOf(t, map[string]string{"FAKE_CLAUDE_CONNECTORS": `["claude.ai Only"]`}, "--mcp-config", `{"mcpServers":{"zz":{}}}`)
	if got := serverNames(info); !slices.Equal(got, []string{"claude.ai Only", "plugin:context7:context7", "zz"}) {
		t.Fatalf("%v", got)
	}
}

func TestSkillOverrides(t *testing.T) {
	info := initOf(t, nil, "--settings", `{"skillOverrides":{"pdf":"off","legacy-helper":"name-only","sre-kit:runbook":"off"}}`)
	if has(info.Skills, "pdf") || !has(info.Skills, "legacy-helper") || !has(info.Skills, "sre-kit:runbook") {
		t.Fatalf("%v", info.Skills)
	}
	// The long key works for standalone skills too.
	info = initOf(t, nil, "--settings", `{"skillOverrides":{"anthropic-skills:pdf":"off","legacy-helper":"user-invocable-only"}}`)
	if has(info.Skills, "pdf") || has(info.Skills, "legacy-helper") || !has(info.SlashCommands, "legacy-helper") || has(info.SlashCommands, "pdf") {
		t.Fatalf("%v %v", info.Skills, info.SlashCommands)
	}
	info = initOf(t, map[string]string{"FAKE_CLAUDE_SKILLS": `["one"]`})
	if !has(info.Skills, "one") || has(info.Skills, "pdf") {
		t.Fatalf("%v", info.Skills)
	}
}

func TestPermissionMode(t *testing.T) {
	info := initOf(t, nil, "--settings", `{"permissions":{"defaultMode":"bypassPermissions"}}`)
	if info.PermissionMode != "bypassPermissions" {
		t.Fatalf("%q", info.PermissionMode)
	}
	info = initOf(t, nil, "--settings", `{"model":"opus"}`)
	if info.Model != "opus" {
		t.Fatalf("%q", info.Model)
	}
}

func TestManaged(t *testing.T) {
	env := map[string]string{"FAKE_CLAUDE_MANAGED": `{"enabledPlugins":{"sre-kit@acme":true,"seo-tools@acme":false}}`}
	info := initOf(t, env, "--settings", `{"enabledPlugins":{"sre-kit@acme":false,"seo-tools@acme":true}}`)
	if !has(info.Plugins, "sre-kit") || has(info.Plugins, "seo-tools") {
		t.Fatalf("managed must win: %v", info.Plugins)
	}
	env = map[string]string{"FAKE_CLAUDE_MANAGED": `{"disableSideloadFlags":true}`}
	for _, flag := range [][]string{{"--plugin-dir", "x"}, {"--plugin-url", "u"}, {"--agents", "{}"}, {"--mcp-config", "{}"}} {
		r := do(t, env, append([]string{"-p", "x"}, flag...)...)
		if r.code != 1 || !strings.Contains(r.stderr, flag[0]) {
			t.Fatalf("%v: %+v", flag, r)
		}
	}
	if r := do(t, env, "-p", "x", "--settings", "{}"); r.code != 0 {
		t.Fatalf("settings flag should stay allowed: %+v", r)
	}
	if r := do(t, env, "--plugin-dir=x"); r.code != 1 {
		t.Fatalf("=form: %+v", r)
	}
}

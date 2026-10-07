package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/claude"
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
	if len(userPlugins(info)) != 5 || !has(userPlugins(info), "sre-kit") {
		t.Fatalf("plugins %v", userPlugins(info))
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
	if has(userPlugins(info), "sre-kit") || has(userPlugins(info), "design-kit") || !has(userPlugins(info), "seo-tools") {
		t.Fatalf("per-key merge broken: %v", userPlugins(info))
	}
	if has(info.Skills, "sre-kit:runbook") {
		t.Fatalf("skills of masked plugin remain: %v", info.Skills)
	}
	// Inline JSON works the same.
	info = initOf(t, nil, "--settings", `{"enabledPlugins":{"sre-kit@acme":false}}`)
	if has(userPlugins(info), "sre-kit") {
		t.Fatalf("inline: %v", userPlugins(info))
	}
	// Masking the MCP plugin removes its server.
	info = initOf(t, nil, "--settings", `{"enabledPlugins":{"context7@claude-plugins-official":false}}`)
	if has(serverNames(info), "plugin:context7:context7") {
		t.Fatalf("servers %v", serverNames(info))
	}
	// Unknown ids are silently ignored.
	info = initOf(t, nil, "--settings", `{"enabledPlugins":{"ghost@x":true}}`)
	if len(userPlugins(info)) != 5 {
		t.Fatalf("%v", userPlugins(info))
	}
}

func TestInitLayers(t *testing.T) {
	user := write(t, "user.json", `{"enabledPlugins":{"seo-tools@acme":false},"permissions":{"defaultMode":"plan"}}`)
	project := write(t, "project.json", `{"enabledPlugins":{"design-kit@acme":false}}`)
	env := map[string]string{"FAKE_CLAUDE_USER_ENABLED": user, "FAKE_CLAUDE_PROJECT_SETTINGS": project}
	info := initOf(t, env)
	if has(userPlugins(info), "seo-tools") || has(userPlugins(info), "design-kit") || info.PermissionMode != "plan" {
		t.Fatalf("%+v", info)
	}
	// --settings beats the project layer.
	info = initOf(t, env, "--settings", `{"enabledPlugins":{"design-kit@acme":true}}`)
	if !has(userPlugins(info), "design-kit") {
		t.Fatalf("command line should win: %v", userPlugins(info))
	}
	// Project-enabled: a plugin disabled at user scope can be enabled by project.
	f := write(t, "p.json", `[{"id":"a@m","scope":"user","enabled":false},{"id":"b@m","scope":"user","enabled":true}]`)
	env = map[string]string{"FAKE_CLAUDE_PLUGINS": f, "FAKE_CLAUDE_PROJECT_SETTINGS": `{"enabledPlugins":{"a@m":true}}`}
	if info := initOf(t, env); !slices.Equal(userPlugins(info), []string{"a", "b"}) {
		t.Fatalf("%v", userPlugins(info))
	}
	// A plain map is accepted as the user layer.
	env = map[string]string{"FAKE_CLAUDE_USER_ENABLED": `{"b@m":false}`, "FAKE_CLAUDE_PLUGINS": f}
	if info := initOf(t, env); len(userPlugins(info)) != 0 {
		t.Fatalf("%v", userPlugins(info))
	}
}

func TestSettingSources(t *testing.T) {
	project := `{"enabledPlugins":{"design-kit@acme":true}}`
	env := map[string]string{"FAKE_CLAUDE_PROJECT_SETTINGS": project}
	for _, src := range []string{"project,local", ""} {
		info := initOf(t, env, "--setting-sources", src)
		if src == "" && len(userPlugins(info)) != 0 {
			t.Fatalf("empty sources should drop everything: %v", userPlugins(info))
		}
		if src != "" && !slices.Equal(userPlugins(info), []string{"design-kit"}) {
			t.Fatalf("project,local: %v", userPlugins(info))
		}
	}
	info := initOf(t, env, "--setting-sources=user")
	if len(userPlugins(info)) != 5 {
		t.Fatalf("user only: %v", userPlugins(info))
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
		if err != nil || len(userPlugins(info)) != 5 {
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
	if len(info.MCPServers) != 0 || len(userPlugins(info)) != 5 {
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
	if !has(userPlugins(info), "sre-kit") || has(userPlugins(info), "seo-tools") {
		t.Fatalf("managed must win: %v", userPlugins(info))
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

// userPlugins drops the always-present harness entries.
func userPlugins(i *claude.InitInfo) []string {
	var out []string
	for _, p := range i.Plugins {
		if !strings.HasPrefix(p, "cc-plugin-") {
			out = append(out, p)
		}
	}
	return out
}

func TestHarnessPluginsAlwaysListed(t *testing.T) {
	for _, extra := range [][]string{nil, {"--setting-sources", ""}, {"--settings", `{"enabledPlugins":{"sre-kit@acme":false}}`}} {
		info := initOf(t, nil, extra...)
		for _, h := range []string{"cc-plugin-agents-md", "cc-plugin-plugin-authoring", "cc-plugin-telemetry"} {
			if !has(info.Plugins, h) {
				t.Errorf("%v: missing harness plugin %s in %v", extra, h, info.Plugins)
			}
		}
	}
}

func TestBareMapOnlyInUserEnabled(t *testing.T) {
	f := write(t, "p.json", `[{"id":"a@m","scope":"user","enabled":true},{"id":"b@m","scope":"user","enabled":true}]`)
	// Through its own variable the bare map works.
	env := map[string]string{"FAKE_CLAUDE_PLUGINS": f, "FAKE_CLAUDE_USER_ENABLED": `{"b@m":false}`}
	if got := userPlugins(initOf(t, env)); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("user layer: %v", got)
	}
	// Inside --settings, a project layer or the managed layer it is just
	// unknown keys, which real Claude Code ignores.
	env = map[string]string{"FAKE_CLAUDE_PLUGINS": f}
	if got := userPlugins(initOf(t, env, "--settings", `{"b@m":false}`)); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("--settings bare map applied: %v", got)
	}
	env["FAKE_CLAUDE_PROJECT_SETTINGS"] = `{"b@m":false}`
	if got := userPlugins(initOf(t, env)); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("project bare map applied: %v", got)
	}
	env = map[string]string{"FAKE_CLAUDE_PLUGINS": f, "FAKE_CLAUDE_MANAGED": `{"b@m":true}`}
	if got := userPlugins(initOf(t, env, "--settings", `{"enabledPlugins":{"b@m":false}}`)); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("managed bare map applied: %v", got)
	}
}

func TestUnknownOptionsRejected(t *testing.T) {
	for _, args := range [][]string{
		{"--bogus"}, {"-p", "x", "--setings", "{}"}, {"--model=x", "--nope=1"}, {"-z"}, {"plugin", "list", "--json", "--bogus"},
	} {
		r := do(t, nil, args...)
		bad := ""
		for _, a := range args {
			if strings.HasPrefix(a, "--bogus") || a == "--setings" || strings.HasPrefix(a, "--nope") || a == "-z" {
				bad = a
			}
		}
		if r.code != 1 || r.stdout != "" || !strings.Contains(r.stderr, "error: unknown option '"+bad+"'") {
			t.Errorf("%v: %+v", args, r)
		}
	}
	// Every allowlisted flag the launcher uses is accepted.
	ok := [][]string{
		{"--settings", "{}"},
		{"--setting-sources", "user"},
		{"--mcp-config", `{"mcpServers":{}}`},
		{"--strict-mcp-config"},
		{"--model", "m"},
		{"--effort", "high"},
		{"--append-system-prompt", "x"},
		{"--resume"},
		{"--resume", "id"},
		{"-r", "id"},
		{"--continue"},
		{"-c"},
		{"-p", "x"},
		{"--print", "x"},
		{"--output-format", "json"},
		{"--verbose"},
		{"--max-turns", "2"},
		{"--add-dir", "d"},
		{"--plugin-dir", "d"},
		{"--agents", "{}"},
		{"--permission-mode", "plan"},
		{"--session-id", "s"},
		{"--name", "n"},
		{"-n", "n"},
		{"--debug"},
		{"--json"},
		{"--available"},
	}
	for _, args := range ok {
		if r := do(t, nil, args...); r.code != 0 {
			t.Errorf("%v rejected: %+v", args, r)
		}
	}
	if r := do(t, nil, "--help"); r.code != 0 {
		t.Errorf("--help: %+v", r)
	}
	// A missing value for a value option is an error.
	if r := do(t, nil, "--model"); r.code != 1 || !strings.Contains(r.stderr, "argument missing") {
		t.Errorf("%+v", r)
	}
}

func TestMissingFilesExit1(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	for _, args := range [][]string{
		{"-p", "x", "--mcp-config", missing},
		{"-p", "x", "--append-system-prompt-file", missing},
		{"-p", "x", "--settings", missing},
		{"-p", "x", "--mcp-config", `{"mcpServers": 5}`},
		{"-p", "x", "--mcp-config", `{bad`},
	} {
		if r := do(t, nil, args...); r.code != 1 || r.stderr == "" {
			t.Errorf("%v: %+v", args, r)
		}
	}
	good := write(t, "prompt.txt", "hello")
	if r := do(t, nil, "-p", "x", "--append-system-prompt-file", good); r.code != 0 {
		t.Errorf("existing prompt file rejected: %+v", r)
	}
}

func TestSkillAliasOnlyAnthropicSkills(t *testing.T) {
	// Another namespace never matches a standalone skill.
	info := initOf(t, nil, "--settings", `{"skillOverrides":{"evil:pdf":"off","zzz:pdf":"off"}}`)
	if !has(info.Skills, "pdf") {
		t.Fatalf("a foreign namespace matched: %v", info.Skills)
	}
	info = initOf(t, nil, "--settings", `{"skillOverrides":{"anthropic-skills:pdf":"off"}}`)
	if has(info.Skills, "pdf") {
		t.Fatalf("alias ignored: %v", info.Skills)
	}
	// Deterministic: the exact name wins over the alias, every time.
	for i := 0; i < 20; i++ {
		info = initOf(t, nil, "--settings", `{"skillOverrides":{"anthropic-skills:pdf":"off","pdf":"on","evil:pdf":"off"}}`)
		if !has(info.Skills, "pdf") {
			t.Fatalf("round %d: %v", i, info.Skills)
		}
	}
	if got := overrideFor(map[string]string{"a:x": "off", "b:x": "name-only"}, "x"); got != "on" {
		t.Fatalf("%q", got)
	}
}

func TestPluginListManagedAndCwd(t *testing.T) {
	type entry struct {
		ID             string `json:"id"`
		Enabled        bool   `json:"enabled"`
		ProjectEnabled bool   `json:"projectEnabled"`
		RequiredByOrg  bool   `json:"requiredByOrg"`
	}
	list := func(env map[string]string) map[string]entry {
		t.Helper()
		r := do(t, env, "plugin", "list", "--json")
		var es []entry
		if err := json.Unmarshal([]byte(r.stdout), &es); err != nil {
			t.Fatalf("%v: %s", err, r.stdout)
		}
		m := map[string]entry{}
		for _, e := range es {
			m[e.ID] = e
		}
		return m
	}
	// Managed: only plugins forced true are required by org.
	got := list(map[string]string{"FAKE_CLAUDE_MANAGED": `{"enabledPlugins":{"sre-kit@acme":true,"seo-tools@acme":false}}`})
	if !got["sre-kit@acme"].RequiredByOrg || got["seo-tools@acme"].RequiredByOrg || got["design-kit@acme"].RequiredByOrg {
		t.Fatalf("%+v", got)
	}
	// cwd dependence.
	dir := t.TempDir()
	cwdNow, _ := os.Getwd()
	env := map[string]string{
		"FAKE_CLAUDE_PROJECT_SETTINGS": `{"enabledPlugins":{"design-kit@acme":false,"seo-tools@acme":true}}`,
		"FAKE_CLAUDE_PROJECT_DIR":      dir,
	}
	got = list(env)
	if !got["design-kit@acme"].Enabled || got["design-kit@acme"].ProjectEnabled {
		t.Fatalf("applied outside the project dir (cwd %s): %+v", cwdNow, got["design-kit@acme"])
	}
	env["FAKE_CLAUDE_PROJECT_DIR"] = cwdNow
	got = list(env)
	if got["design-kit@acme"].Enabled || got["design-kit@acme"].ProjectEnabled || !got["seo-tools@acme"].Enabled || !got["seo-tools@acme"].ProjectEnabled {
		t.Fatalf("not applied in the project dir: %+v", got)
	}
	// Init follows the same rule.
	env["FAKE_CLAUDE_PROJECT_DIR"] = dir
	if info := initOf(t, env); !has(info.Plugins, "design-kit") {
		t.Fatalf("init applied the project layer outside its directory: %v", info.Plugins)
	}
	env["FAKE_CLAUDE_PROJECT_DIR"] = cwdNow
	if info := initOf(t, env); has(info.Plugins, "design-kit") {
		t.Fatalf("init ignored the project layer inside its directory: %v", info.Plugins)
	}
}

func TestPluginListBadShapesVerbatim(t *testing.T) {
	for _, shape := range []string{`null`, `{}`, `{"installed":null}`, `{"plugins":[]}`, `"oops"`, `5`} {
		f := write(t, "p.json", shape)
		for _, args := range [][]string{{"plugin", "list", "--json"}, {"plugin", "list", "--json", "--available"}} {
			r := do(t, map[string]string{"FAKE_CLAUDE_PLUGINS": f}, args...)
			if r.code != 0 || strings.TrimSpace(r.stdout) != shape {
				t.Errorf("%s %v: %+v", shape, args, r)
			}
		}
	}
}

func TestMarketplaceList(t *testing.T) {
	var out, errb strings.Builder
	get := func(string) string { return "" }
	if code := run([]string{"plugin", "marketplace", "list", "--json"}, get, &out, &errb); code != 0 {
		t.Fatalf("code %d: %s", code, errb.String())
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &list); err != nil || len(list) != 2 || list[0]["name"] != "acme" || list[0]["source"] != "github" || list[0]["repo"] == "" {
		t.Fatalf("default list: %v %s", err, out.String())
	}
	out.Reset()
	if code := run([]string{"plugin", "marketplace", "list"}, get, &out, &errb); code != 0 || !strings.Contains(out.String(), "acme") || strings.Contains(out.String(), "{") {
		t.Errorf("plain list: %d %q", code, out.String())
	}
	if code := run([]string{"plugin", "marketplace", "list", "--bogus"}, get, &out, &errb); code != 1 {
		t.Errorf("unknown option: code %d", code)
	}
	if code := run([]string{"plugin", "marketplace", "list", "--json"}, func(k string) string {
		if k == "FAKE_CLAUDE_MARKETPLACES_FAIL" {
			return "1"
		}
		return ""
	}, &out, &errb); code != 1 {
		t.Errorf("fail switch: code %d", code)
	}
	path := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(path, []byte("null"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	run([]string{"plugin", "marketplace", "list", "--json"}, func(k string) string {
		if k == "FAKE_CLAUDE_MARKETPLACES" {
			return path
		}
		return ""
	}, &out, &errb)
	if strings.TrimSpace(out.String()) != "null" {
		t.Errorf("a non-array file must be printed verbatim: %q", out.String())
	}
}

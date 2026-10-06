package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var update = os.Getenv("CCSHELF_UPDATE_GOLDEN") != ""

// golden compares got with testdata/<name>.golden (rewritten when
// CCSHELF_UPDATE_GOLDEN is set).
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if update {
		write(t, path, got)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with CCSHELF_UPDATE_GOLDEN=1 to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

var hexRun = regexp.MustCompile(`[0-9a-f]{32}`)

// normalize replaces the machine-specific parts of dry-run output.
func (s *sandbox) normalize(out string) string {
	out = strings.ReplaceAll(out, s.CacheDir(), "<CACHE>")
	out = strings.ReplaceAll(out, filepath.Join(binDir, exe("claude")), "<CLAUDE>")
	out = strings.ReplaceAll(out, s.root, "<ROOT>")
	out = strings.ReplaceAll(out, string(filepath.Separator), "/")
	if runtime.GOOS == "windows" {
		// dry-run prints the PowerShell form there: `& 'cmd' 'arg'`. Compare it with the POSIX golden.
		out = strings.ReplaceAll(strings.TrimPrefix(out, "& "), "'", "")
	}
	return hexRun.ReplaceAllString(out, "<HASH>")
}

const personalMine = `name = "mine"
description = "My day-to-day profile"

[plugins]
include = ["design-kit@acme"]

[session]
effort = "high"
`

func TestProfileLifecycle(t *testing.T) {
	s := newSandbox(t)

	r := s.mustRun("--no-interactive", "init")
	contains(t, "init", r.Stdout+r.Stderr, "config.toml")
	if _, err := os.Stat(filepath.Join(s.ConfigDir(), "config.toml")); err != nil {
		t.Fatalf("init wrote no config: %v", err)
	}
	if r := s.run("--no-interactive", "init"); r.Code == 0 {
		t.Error("init over an existing config must fail without --force")
	}

	s.mustRun("new", "--no-interactive", "mine", "--description", "My day-to-day profile",
		"--plugin", "design-kit@acme", "--effort", "high")
	s.mustRun("new", "--no-interactive", "child", "--from", "mine", "--plugin", "sre-kit@acme")
	if r := s.run("new", "--no-interactive", "mine", "--plugin", "x@y"); r.Code == 0 {
		t.Error("new over an existing profile must fail")
	}
	if r := s.run("new", "--no-interactive"); r.Code != 2 || !strings.Contains(r.Stderr, "name") {
		t.Errorf("new without a name: exit %d, stderr %q (want 2 naming the argument)", r.Code, r.Stderr)
	}

	r = s.mustRun("ls")
	contains(t, "ls", r.Stdout, "mine", "child", "My day-to-day profile")

	r = s.mustRun("ls", "--json")
	var ls struct {
		Version int `json:"version"`
		Kind    string
		Data    json.RawMessage
	}
	if err := json.Unmarshal([]byte(r.Stdout), &ls); err != nil || ls.Version != 1 {
		t.Fatalf("ls --json: %v %s", err, r.Stdout)
	}

	r = s.mustRun("show", "mine")
	contains(t, "show", r.Stdout, "Profile: mine", "design-kit@acme", "effort: high")
	if strings.Contains(r.Stdout, "Trust: new") {
		t.Error("a personal profile needs no trust")
	}

	r = s.mustRun("diff", "mine", "child")
	contains(t, "diff", r.Stdout, "sre-kit@acme")
	if r.Code != 0 {
		t.Error("diff exits 0 whether or not profiles differ")
	}
	r = s.mustRun("diff", "--json", "mine", "mine")
	contains(t, "diff --json", r.Stdout, `"identical": true`)

	r = s.mustRun("dry-run", "mine")
	golden(t, "dry-run-mine", s.normalize(r.Stdout))
	if n := len(s.anyStart()); n != 0 {
		t.Errorf("dry-run started claude %d times", n)
	}
	if r.Stdout == "" || len(s.launches()) != 0 {
		t.Error("dry-run prints the command and starts nothing")
	}
}

func TestRunPersonalProfile(t *testing.T) {
	s := newSandbox(t)
	s.writeProfile("mine", personalMine)

	r := s.mustRun("run", "mine", "--resume")
	if !strings.Contains(r.Stdout, "fake claude: ok") {
		t.Errorf("claude's output must reach stdout: %q", r.Stdout)
	}
	ls := s.launches()
	if len(ls) != 1 {
		t.Fatalf("launches = %d, want 1", len(ls))
	}
	inv := ls[0]
	if inv.Argv[len(inv.Argv)-1] != "--resume" {
		t.Errorf("passthrough must come last: %v", inv.Argv)
	}
	if argAfter(inv.Argv, "--effort") != "high" {
		t.Errorf("effort not passed: %v", inv.Argv)
	}
	if inv.Cwd != s.Work {
		t.Errorf("cwd = %q, want %q", inv.Cwd, s.Work)
	}
	if v := inv.Env["CLAUDE_CONFIG_DIR"]; v != "" {
		t.Errorf("CLAUDE_CONFIG_DIR must not be set without an account: %q", v)
	}

	st := settingsOf(t, inv)
	ep := enabledPlugins(t, st)
	want := map[string]bool{
		"design-kit@acme":                         true,
		"sre-kit@acme":                            false,
		"seo-tools@acme":                          false,
		"context7@claude-plugins-official":        false,
		"frontend-design@claude-plugins-official": false,
	}
	for id, w := range want {
		if got, ok := ep[id]; !ok || got != w {
			t.Errorf("enabledPlugins[%s] = %v (present %v), want %v", id, got, ok, w)
		}
	}
	env, _ := st["env"].(map[string]any)
	if env["CCSHELF_PROFILE"] != "mine" {
		t.Errorf("settings env = %v, want CCSHELF_PROFILE=mine", env)
	}
	// The generated file is private and content-addressed.
	p := argAfter(inv.Argv, "--settings")
	if !strings.HasPrefix(p, s.CacheDir()) {
		t.Errorf("settings file %s outside the cache %s", p, s.CacheDir())
	}
	if fi, err := os.Stat(p); err == nil && filepath.Separator == '/' {
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("settings file mode %v, want private", fi.Mode().Perm())
		}
	}
	// A second run reuses the same file (same content, same name).
	s.mustRun("run", "mine")
	if ls = s.launches(); len(ls) != 2 || argAfter(ls[1].Argv, "--settings") != p {
		t.Errorf("settings file not content-addressed: %v", ls)
	}
}

func TestRunPassthroughAndExitCode(t *testing.T) {
	s := newSandbox(t)
	s.writeProfile("mine", personalMine)

	s.mustRun("run", "mine", "--", "-p", "summarize this", "--output-format", "stream-json", "--verbose")
	inv := s.launches()[0]
	if tail := strings.Join(inv.Argv, " "); !strings.HasSuffix(tail, "-p summarize this --output-format stream-json --verbose") {
		t.Errorf("argv = %v", inv.Argv)
	}

	s.Setenv("FAKE_CLAUDE_EXIT", "7")
	r := s.run("run", "mine")
	if r.Code != 7 {
		t.Errorf("exit = %d, want the child's 7", r.Code)
	}
	if strings.Contains(r.Stderr, "exit status") {
		t.Errorf("a child's exit status must not be reported as an error: %s", r.Stderr)
	}
}

func TestRunInitEventMasking(t *testing.T) {
	s := newSandbox(t)
	s.writeProfile("mine", personalMine)
	r := s.mustRun("run", "mine", "--", "-p", "hi", "--output-format", "stream-json", "--verbose")
	var init map[string]any
	for _, line := range strings.Split(r.Stdout, "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil && m["subtype"] == "init" {
			init = m
		}
	}
	if init == nil {
		t.Fatalf("no init event in %s", r.Stdout)
	}
	var names []string
	for _, p := range init["plugins"].([]any) {
		pm := p.(map[string]any)
		if pm["source"] != "harness" {
			names = append(names, pm["name"].(string))
		}
	}
	if strings.Join(names, ",") != "design-kit" {
		t.Errorf("plugins that loaded = %v, want only design-kit", names)
	}
}

func TestRunMissingProfile(t *testing.T) {
	s := newSandbox(t)
	s.writeProfile("mine", personalMine)
	for _, args := range [][]string{
		{"--no-interactive", "run"},
		{"run", "--", "-p", "x"},
		{"run", "nope"},
	} {
		r := s.run(args...)
		if r.Code != 2 {
			t.Errorf("ccshelf %v: exit %d, want 2\n%s", args, r.Code, r.Stderr)
		}
	}
	if r := s.run("run"); !strings.Contains(r.Stderr, "<profile>") {
		t.Errorf("a missing profile name should be named: %s", r.Stderr)
	}
	if len(s.anyStart()) != 0 {
		t.Error("nothing may start")
	}
}

func TestRunExplicitClaudeFlagAndMissingClaude(t *testing.T) {
	s := newSandbox(t)
	s.writeProfile("mine", personalMine)
	// An unusable PATH and --claude pointing at the fake: works.
	s.Setenv("PATH", t.TempDir())
	r := s.run("--claude", filepath.Join(binDir, exe("claude")), "run", "mine")
	if r.Code != 0 {
		t.Fatalf("exit %d: %s", r.Code, r.Stderr)
	}
	if len(s.launches()) != 1 {
		t.Error("the --claude binary was not started")
	}
	// No claude anywhere: a clear failure, nothing started.
	r = s.run("run", "mine")
	if r.Code == 0 || !strings.Contains(strings.ToLower(r.Stderr), "claude") {
		t.Errorf("exit %d, stderr %q: want a failure naming claude", r.Code, r.Stderr)
	}
}

func TestRunPromptFile(t *testing.T) {
	s := newSandbox(t)
	org := exampleOrg(t)
	s.addDirSource(filepath.Join(org, "profiles"))
	h := s.closureHash("frontend")
	s.mustRun("trust", "frontend", "--accept", h)
	s.mustRun("run", "frontend")
	ls := s.launches()
	if len(ls) != 1 {
		t.Fatalf("launches = %d", len(ls))
	}
	argv := ls[0].Argv
	pf := argAfter(argv, "--append-system-prompt-file")
	if pf == "" {
		t.Fatalf("no prompt file in %v", argv)
	}
	b, err := os.ReadFile(pf)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(org, "prompts", "frontend.md"))
	if string(b) != string(want) {
		t.Errorf("prompt file content differs from the profile's")
	}
	if argAfter(argv, "--model") != "" && argAfter(argv, "--model") != "opus" {
		t.Errorf("model = %q", argAfter(argv, "--model"))
	}
	if !hasArg(argv, "--strict-mcp-config") || argAfter(argv, "--mcp-config") == "" {
		t.Errorf("mcp args missing: %v", argv)
	}
	// Environment values of a profile reach claude through settings, not argv.
	if strings.Contains(strings.Join(argv, " "), "op://") {
		t.Error("an env value leaked into argv")
	}
}

func TestRunProtectedPluginsUntouched(t *testing.T) {
	s := newSandbox(t)
	org := exampleOrg(t)
	s.Setenv("FAKE_CLAUDE_PLUGINS", pluginsFile(t, "audit-logger@acme", "sre-kit@acme", "design-kit@acme", "seo-tools@acme"))
	s.addDirSource(filepath.Join(org, "profiles"))
	s.mustRun("trust", "seo", "--accept", s.closureHash("seo"))
	r := s.mustRun("run", "seo")
	contains(t, "stderr", r.Stderr, "audit-logger@acme is protected")
	ep := enabledPlugins(t, settingsOf(t, s.launches()[0]))
	if _, ok := ep["audit-logger@acme"]; ok {
		t.Errorf("a protected plugin must not appear in enabledPlugins at all: %v", ep)
	}
	if ep["sre-kit@acme"] || ep["design-kit@acme"] || !ep["seo-tools@acme"] {
		t.Errorf("enabledPlugins = %v", ep)
	}
}

// With inherit_user_settings = false the user layer, which enables plugins, is
// dropped (--setting-sources project,local): a protected plugin must then be
// written true, never omitted, or it would silently be off.
func TestRunProtectedPluginsEnabledWhenUserLayerDropped(t *testing.T) {
	s := newSandbox(t)
	org := exampleOrg(t)
	s.Setenv("FAKE_CLAUDE_PLUGINS", pluginsFile(t, "audit-logger@acme", "sre-kit@acme", "design-kit@acme"))
	s.addDirSource(filepath.Join(org, "profiles"))
	s.writeProfile("isolated", "name = \"isolated\"\n[plugins]\ninclude = [\"design-kit@acme\"]\n[session]\ninherit_user_settings = false\n")
	r := s.mustRun("run", "isolated")
	argv := s.launches()[0].Argv
	if argAfter(argv, "--setting-sources") != "project,local" {
		t.Fatalf("user layer not dropped: %v", argv)
	}
	ep := enabledPlugins(t, settingsOf(t, s.launches()[0]))
	if v, ok := ep["audit-logger@acme"]; !ok || !v {
		t.Errorf("protected plugin must be written true when the user layer is dropped: %v", ep)
	}
	if !ep["design-kit@acme"] || ep["sre-kit@acme"] {
		t.Errorf("enabledPlugins = %v", ep)
	}
	if n := strings.Count(r.Stderr, "inherit_user_settings = false"); n != 1 {
		t.Errorf("the inherit warning appears %d times:\n%s", n, r.Stderr)
	}
}

func pluginsFile(t *testing.T, ids ...string) string {
	t.Helper()
	var list []map[string]any
	for _, id := range ids {
		list = append(list, map[string]any{"id": id, "version": "1.0.0", "scope": "user", "enabled": true, "installPath": "/fake/" + id})
	}
	b, _ := json.Marshal(list)
	p := filepath.Join(t.TempDir(), "plugins.json")
	write(t, p, string(b))
	return p
}

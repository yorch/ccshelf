package launcher

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/ui"
)

const personalMine = `name = "mine"
description = "My profile"

[plugins]
include = ["design-kit@acme"]
`

func TestRunPersonalProfile(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	if code := h.run("run", "mine", "--resume"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if h.started != 1 {
		t.Fatalf("started %d times", h.started)
	}
	if h.startBin != h.claude {
		t.Errorf("bin %q", h.startBin)
	}
	if got := h.startArgs[len(h.startArgs)-1]; got != "--resume" {
		t.Errorf("passthrough not last: %v", h.startArgs)
	}
	st := h.settingsOf(h.startArgs)
	ep := st["enabledPlugins"].(map[string]any)
	if ep["design-kit@acme"] != true || ep["sre-kit@acme"] != false || ep["seo-tools@acme"] != false {
		t.Errorf("enabledPlugins = %v", ep)
	}
	if env := st["env"].(map[string]any); env["CCSHELF_PROFILE"] != "mine" {
		t.Errorf("env = %v", env)
	}
	if hasArg(h.startArgs, "--mcp-config") || hasArg(h.startArgs, "--setting-sources") {
		t.Errorf("unexpected args %v", h.startArgs)
	}
	for _, e := range h.startEnv {
		if strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") && e != "CLAUDE_CONFIG_DIR=" {
			t.Errorf("config dir must not be set: %s", e)
		}
	}
}

func TestRunDashPassthrough(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.mustRun("run", "mine", "--", "-p", "summarize this")
	n := len(h.startArgs)
	if h.startArgs[n-2] != "-p" || h.startArgs[n-1] != "summarize this" {
		t.Errorf("args %v", h.startArgs)
	}
	h.mustRun("run", "--no-interactive", "mine", "--verbose")
	if h.startArgs[len(h.startArgs)-1] != "--verbose" {
		t.Errorf("flags after the profile belong to claude: %v", h.startArgs)
	}
}

func TestRunMissingProfileNonInteractive(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	if code := h.run("run"); code != ui.ExitUsage {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(h.errb.String(), "<profile>") {
		t.Errorf("stderr should name the missing argument: %s", h.errb)
	}
	if code := h.run("run", "--", "-p", "x"); code != ui.ExitUsage {
		t.Errorf("-- without profile: code %d", code)
	}
	if h.started != 0 {
		t.Error("must not start")
	}
}

func TestRunPicker(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("alpha", strings.ReplaceAll(personalMine, "mine", "alpha"))
	h.writeProfile("mine", personalMine)
	sc := ui.NewScripted(1)
	h.prompt = sc
	h.g.Account = ""
	if code := h.run("run"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	if eq := h.errb.String(); !strings.Contains(eq, "Equivalent: ccshelf run ") || !strings.Contains(eq, " mine\n") {
		t.Errorf("no equivalent line: %s", h.errb)
	}
	if st := h.settingsOf(h.startArgs); st["env"].(map[string]any)["CCSHELF_PROFILE"] != "mine" {
		t.Error("picked the wrong profile")
	}
}

func TestRunPickerNoInteractiveFlag(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.prompt = ui.NewScripted(0)
	if code := h.run("--no-interactive", "run"); code != ui.ExitUsage {
		t.Fatalf("code %d", code)
	}
}

func TestRunUnknownProfile(t *testing.T) {
	h := newHarness(t)
	if code := h.run("run", "nope"); code != ui.ExitUsage {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(h.errb.String(), "not found") {
		t.Error(h.errb.String())
	}
}

func TestRunExitCodePropagates(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.startCode = 7
	if code := h.run("run", "mine"); code != 7 {
		t.Fatalf("code %d", code)
	}
	if strings.Contains(h.errb.String(), "exit status") {
		t.Errorf("child exit must not be reported: %s", h.errb)
	}
	h.startCode, h.startErr = 0, errors.New("boom")
	if code := h.run("run", "mine"); code != ui.ExitFailure {
		t.Fatalf("start error code %d", code)
	}
}

func TestRunMissingPluginWarns(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", strings.ReplaceAll(personalMine, "design-kit@acme", "ghost@acme"))
	h.mustRun("run", "mine")
	if !strings.Contains(h.errb.String(), "/plugin install ghost@acme") {
		t.Errorf("stderr: %s", h.errb)
	}
	// An interactive run asks; --yes skips the question, refusing stops.
	h.prompt = ui.NewScripted(false)
	h.started = 0
	if code := h.run("run", "mine"); code != ui.ExitFailure || h.started != 0 {
		t.Errorf("declined: code %d started %d", code, h.started)
	}
	h.prompt = ui.NewScripted()
	if code := h.run("run", "--yes", "mine"); code != 0 {
		t.Errorf("--yes: code %d", code)
	}
}

func TestRunInheritFalseAndEffort(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine+"\n[session]\ninherit_user_settings = false\neffort = \"high\"\nmodel = \"opus\"\n")
	h.mustRun("run", "mine")
	if argAfter(h.startArgs, "--setting-sources") != "project,local" {
		t.Errorf("args %v", h.startArgs)
	}
	if argAfter(h.startArgs, "--effort") != "high" {
		t.Errorf("args %v", h.startArgs)
	}
	if h.settingsOf(h.startArgs)["model"] != "opus" {
		t.Error("model missing in settings")
	}
	if !strings.Contains(h.errb.String(), "inherit_user_settings = false") {
		t.Errorf("no warning: %s", h.errb)
	}
}

func TestRunOverridingArgWarns(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.mustRun("run", "mine", "--settings", "x.json")
	if !strings.Contains(h.errb.String(), "can override") {
		t.Errorf("stderr: %s", h.errb)
	}
}

func TestRunAccountEnv(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	acct := t.TempDir() + "/acct-work"
	h.writeConfig("[accounts.work]\nconfig_dir = " + tomlString(acct) + "\n")

	// Flag sets the variable.
	h.mustRun("run", "--account", "work", "mine")
	if !hasEnv(h.startEnv, "CLAUDE_CONFIG_DIR="+acct) {
		t.Errorf("env %v", h.startEnv)
	}
	// An existing variable is honored and never overridden implicitly.
	existing := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", existing)
	h.writeConfig("default_account = \"work\"\n[accounts.work]\nconfig_dir = " + tomlString(acct) + "\n")
	h.mustRun("run", "mine")
	if !hasEnv(h.startEnv, "CLAUDE_CONFIG_DIR="+existing) {
		t.Errorf("existing dir must win: %v", h.startEnv)
	}
	// An explicit --account overrides it, with a warning.
	h.mustRun("run", "--account", "work", "mine")
	if !hasEnv(h.startEnv, "CLAUDE_CONFIG_DIR="+acct) || !strings.Contains(h.errb.String(), "warn:") {
		t.Errorf("env %v stderr %s", h.startEnv, h.errb)
	}
	// Unknown account is a usage error.
	if code := h.run("run", "--account", "nope", "mine"); code != ui.ExitUsage {
		t.Errorf("code %d", code)
	}
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestRunProfileAccountField(t *testing.T) {
	h := newHarness(t)
	acct := t.TempDir() + "/acct-p"
	h.writeConfig("[accounts.work]\nconfig_dir = " + tomlString(acct) + "\n")
	h.writeProfile("mine", "account = \"work\"\n"+personalMine)
	h.mustRun("run", "mine")
	if !hasEnv(h.startEnv, "CLAUDE_CONFIG_DIR="+acct) {
		t.Errorf("env %v", h.startEnv)
	}
}

func TestRunBrokenConfigFails(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("bogus = 1\n")
	if code := h.run("run", "mine"); code != ui.ExitFailure {
		t.Fatalf("code %d", code)
	}
}

func TestRunClaudeNotFound(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.g.ClaudePath = ""
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CCSHELF_CLAUDE", "")
	if code := h.run("run", "mine"); code != ui.ExitFailure {
		t.Fatalf("code %d", code)
	}
	if h.started != 0 {
		t.Error("started")
	}
}

func TestRunListFailureFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	t.Setenv("FAKE_CLAUDE_PLUGIN_LIST_FAIL", "1")
	if code := h.run("run", "mine"); code != ui.ExitFailure || h.started != 0 {
		t.Fatalf("code %d started %d", code, h.started)
	}
}

// ---- trust ----

func orgHash(t *testing.T, h *harness, name string) string {
	t.Helper()
	h.mustRun("--json", "show", name)
	var env struct {
		Data struct {
			ClosureHash string `json:"closure_hash"`
			Trust       string `json:"trust"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	return env.Data.ClosureHash
}

func TestRunTrustFailsClosed(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.exampleOrg())
	if code := h.run("run", "frontend"); code != ui.ExitTrust {
		t.Fatalf("untrusted: code %d\n%s", code, h.errb)
	}
	if h.started != 0 {
		t.Fatal("started untrusted profile")
	}
	hash := orgHash(t, h, "frontend")
	if hash == "" {
		t.Fatal("no hash")
	}
	// --yes never accepts trust.
	if code := h.run("run", "--yes", "frontend"); code != ui.ExitTrust {
		t.Fatalf("--yes: code %d", code)
	}
	if !strings.Contains(h.errb.String(), "ccshelf trust frontend --accept "+hash) {
		t.Errorf("hint missing: %s", h.errb)
	}
	// A scripted prompter answering no, and --no-interactive, stay at 4.
	h.prompt = ui.NewScripted("no")
	if code := h.run("run", "frontend"); code != ui.ExitTrust {
		t.Fatalf("declined: code %d", code)
	}
	h.prompt = ui.NewScripted("yes")
	if code := h.run("--no-interactive", "run", "frontend"); code != ui.ExitTrust {
		t.Fatalf("--no-interactive: code %d", code)
	}
	if h.started != 0 {
		t.Fatal("started")
	}
}

func TestTrustAcceptThenRun(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.exampleOrg())
	hash := orgHash(t, h, "frontend")

	if code := h.run("trust", "frontend", "--accept", "deadbeef"); code != ui.ExitTrust {
		t.Fatalf("wrong hash: code %d\n%s", code, h.errb)
	}
	if code := h.run("trust", "frontend"); code != ui.ExitUsage {
		t.Fatalf("no --accept, no tty: code %d", code)
	}
	if !strings.Contains(h.errb.String(), "--accept "+hash) {
		t.Errorf("must name the flag: %s", h.errb)
	}
	h.mustRun("trust", "frontend", "--accept", hash)
	h.mustRun("trust", "frontend") // already trusted

	h.mustRun("run", "frontend")
	args := h.startArgs
	if !hasArg(args, "--strict-mcp-config") || argAfter(args, "--mcp-config") == "" || argAfter(args, "--append-system-prompt-file") == "" {
		t.Errorf("args %v", args)
	}
	if argAfter(args, "--effort") != "high" {
		t.Errorf("args %v", args)
	}
	mcp, err := os.ReadFile(argAfter(args, "--mcp-config"))
	if err != nil || !strings.Contains(string(mcp), "figma") {
		t.Errorf("mcp config: %v %s", err, mcp)
	}
	st := h.settingsOf(args)
	if st["disableClaudeAiConnectors"] != true {
		t.Errorf("settings %v", st)
	}

	// Revoking removes trust again.
	h.mustRun("trust", "frontend", "--revoke")
	if code := h.run("run", "frontend"); code != ui.ExitTrust {
		t.Fatalf("revoked: code %d", code)
	}
}

func TestRunInteractiveTrustConfirm(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.exampleOrg())
	sc := ui.NewScripted("yes", true) // trust, then "start anyway" (docs-writer is not installed)
	h.prompt = sc
	if code := h.run("run", "seo"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	if !strings.Contains(h.errb.String(), "Equivalent: ccshelf trust seo --accept ") {
		t.Errorf("stderr: %s", h.errb)
	}
	// Now trusted: no prompt needed.
	h.prompt = ui.NewScripted()
	if code := h.run("run", "--yes", "seo"); code != 0 {
		t.Fatalf("second run %d", code)
	}
}

func TestTrustOnChangeFail(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	h.writeConfig("[trust]\non_change = \"fail\"\n\n[[sources]]\ntype = \"dir\"\npath = " + tomlString(org+"/profiles") + "\n")
	hash := orgHash(t, h, "seo")
	h.mustRun("trust", "seo", "--accept", hash)
	p := org + "/profiles/seo.toml"
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), "description = \"", "description = \"changed ", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	h.prompt = ui.NewScripted("yes")
	if code := h.run("run", "seo"); code != ui.ExitTrust {
		t.Fatalf("changed with on_change=fail must not prompt: code %d\n%s", code, h.errb)
	}
}

// ---- policy ----

func TestRunPolicyBlocksFail(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.exampleOrg())
	hash := orgHash(t, h, "sre")
	h.mustRun("trust", "sre", "--accept", hash)
	h.writeManaged(`{"disableSideloadFlags": true}`)
	if code := h.run("run", "sre"); code != ui.ExitPolicy {
		t.Fatalf("on_blocked=fail under policy: code %d\n%s", code, h.errb)
	}
	if h.started != 0 {
		t.Error("started")
	}
}

func TestRunPolicyBlocksWarn(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	h.useOrg(org)
	hash := orgHash(t, h, "frontend")
	h.mustRun("trust", "frontend", "--accept", hash)
	h.writeManaged(`{"disableSideloadFlags": true}`)
	if code := h.run("run", "frontend"); code != 0 {
		t.Fatalf("on_blocked=warn: code %d\n%s", code, h.errb)
	}
	if hasArg(h.startArgs, "--mcp-config") {
		t.Errorf("blocked flag passed: %v", h.startArgs)
	}
	if !strings.Contains(h.errb.String(), "policy") && !strings.Contains(h.errb.String(), "blocked") {
		t.Errorf("no policy warning: %s", h.errb)
	}
}

func TestRunProtectedPluginNeverMasked(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	// Protect an installed plugin that no profile includes.
	if err := os.WriteFile(org+"/ccshelf.toml", []byte("[protect]\nplugins = [\"seo-tools@acme\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.useOrg(org)
	hash := orgHash(t, h, "sre")
	h.mustRun("trust", "sre", "--accept", hash)
	h.mustRun("run", "sre")
	ep := h.settingsOf(h.startArgs)["enabledPlugins"].(map[string]any)
	if _, ok := ep["seo-tools@acme"]; ok {
		t.Errorf("protected plugin was written: %v", ep)
	}
}

func TestRunBrokenOrgConfigFailsClosed(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	if err := os.WriteFile(org+"/ccshelf.toml", []byte("[protect]\nbogus = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.useOrg(org)
	if code := h.run("run", "seo"); code != ui.ExitFailure || h.started != 0 {
		t.Fatalf("code %d", code)
	}
}

// ---- dry-run ----

func TestDryRun(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	if code := h.run("dry-run", "mine", "--", "-p", "hello world"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if h.started != 0 {
		t.Fatal("dry-run started claude")
	}
	out := h.out.String()
	if !strings.HasPrefix(out, h.claude) && !strings.HasPrefix(out, "'"+h.claude) {
		t.Errorf("out: %s", out)
	}
	if !strings.Contains(out, "--settings") || !strings.Contains(out, "'hello world'") {
		t.Errorf("out: %s", out)
	}
	// JSON.
	h.mustRun("--json", "dry-run", "mine")
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Command  []string `json:"command"`
			Settings string   `json:"settings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "dry-run" || len(env.Data.Command) < 3 || env.Data.Settings == "" {
		t.Errorf("json %s", h.out)
	}
}

func TestDryRunWindowsAndAccount(t *testing.T) {
	h := newHarness(t)
	h.goos = "windows"
	h.writeProfile("mine", personalMine)
	acct := t.TempDir() + "/acct w"
	h.writeConfig("[accounts.work]\nconfig_dir = " + tomlString(acct) + "\n")
	h.mustRun("dry-run", "--account", "work", "mine")
	out := h.out.String()
	if !strings.HasPrefix(out, "$env:CLAUDE_CONFIG_DIR = '") || !strings.Contains(out, "; & ") {
		t.Errorf("PowerShell form expected: %s", out)
	}
	h.goos = "linux"
	h.mustRun("dry-run", "--account", "work", "mine")
	if !strings.HasPrefix(h.out.String(), "CLAUDE_CONFIG_DIR='") {
		t.Errorf("POSIX form expected: %s", h.out)
	}
}

func TestDryRunRedactsSecretArgs(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.mustRun("dry-run", "mine", "--", "--token", "s3cr3t")
	if strings.Contains(h.out.String(), "s3cr3t") {
		t.Errorf("secret printed: %s", h.out)
	}
}

func TestDryRunTrustStillEnforced(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.exampleOrg())
	if code := h.run("dry-run", "frontend"); code != ui.ExitTrust {
		t.Fatalf("code %d", code)
	}
}

// ---- git source seam ----

func TestRunGitSourceViaFactory(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	var got []string
	h.newGit = func(o gitOpts) (PreparedSource, error) {
		got = append(got, o.URL, o.Ref, o.Subpath)
		return preparedDir{dirSourceFor(org)}, nil
	}
	h.writeConfig("[[sources]]\ntype = \"git\"\nurl = \"https://example.com/acme/data.git\"\nref = \"v1.0.0\"\npath = \"profiles\"\n")
	hash := orgHash(t, h, "seo")
	if len(got) != 3 || got[1] != "v1.0.0" {
		t.Errorf("factory options %v", got)
	}
	h.mustRun("trust", "seo", "--accept", hash)
	h.mustRun("run", "seo")
}

func TestRunGitPrepareFailure(t *testing.T) {
	h := newHarness(t)
	h.newGit = func(gitOpts) (PreparedSource, error) { return nil, errors.New("offline") }
	h.writeConfig("[[sources]]\ntype = \"git\"\nurl = \"https://example.com/acme/data.git\"\nref = \"v1.0.0\"\n")
	if code := h.run("run", "seo"); code != ui.ExitFailure {
		t.Fatalf("code %d", code)
	}
}

func TestConfigRelativeDirSourceRejected(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("[[sources]]\ntype = \"dir\"\npath = \"relative/p\"\n")
	if code := h.run("ls"); code != ui.ExitFailure {
		t.Fatalf("code %d", code)
	}
}

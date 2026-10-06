package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/claude"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/config"
	"github.com/ccshelf/ccshelf/internal/policy"
	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/testutil"
	"github.com/ccshelf/ccshelf/internal/trust"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// ---- L1: the editor is never canceled by the program's context ----

func TestEditRunsEditorOnUncancelableContext(t *testing.T) {
	h := newHarness(t)
	h.prompt = ui.NewScripted(0)
	h.writeProfile("mine", personalMine)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myedit")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the program's context is already canceled (Ctrl+C in the editor)
	h.ctx = ctx
	h.mustRun("edit", "mine")
	if h.spawnCtx == nil {
		t.Fatal("editor not started")
	}
	if err := h.spawnCtx.Err(); err != nil {
		t.Fatalf("the editor's context was canceled (%v): unsaved edits would be lost", err)
	}
}

// ---- L3: new records and counts every flag ----

func TestNewEquivalentRecordsEveryFlag(t *testing.T) {
	h := newHarness(t)
	sc := ui.NewScripted("night") // only the name is asked: the flags say the rest
	h.prompt = sc
	if code := h.run("new", "--owner", "team-a", "--model", "opus", "--effort", "low"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Errorf("the wizard opened although flags were given: %v", err)
	}
	eq := h.errb.String()
	for _, want := range []string{"Equivalent: ccshelf new night", "--owner team-a", "--model opus", "--effort low"} {
		if !strings.Contains(eq, want) {
			t.Errorf("missing %q in %s", want, eq)
		}
	}
	b, err := os.ReadFile(filepath.Join(h.configDir(), "profiles", "night.toml"))
	if err != nil || !strings.Contains(string(b), "team-a") || !strings.Contains(string(b), "opus") {
		t.Errorf("profile %v\n%s", err, b)
	}
}

func TestNewFlagsGiven(t *testing.T) {
	for name, f := range map[string]newFlags{
		"from": {from: []string{"a"}}, "plugin": {plugins: []string{"a@b"}}, "exclude": {exclude: []string{"a@b"}},
		"skill": {skillsOff: []string{"s"}}, "mcp": {mcp: []string{"m"}}, "description": {description: "d"},
		"owner": {owner: "o"}, "model": {model: "m"}, "effort": {effort: "low"},
	} {
		if !f.given() {
			t.Errorf("%s not counted as given", name)
		}
	}
	if (&newFlags{}).given() {
		t.Error("empty flags counted as given")
	}
}

// ---- L4: account add records --marketplace and --plugin ----

func TestAccountAddEquivalentRecordsMarketplaceAndPlugin(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "claude-work")
	sc := ui.NewScripted("work", dir)
	h.prompt = sc
	h.mustRun("account", "add", "--marketplace", "acme/claude-plugins", "--plugin", "design-kit@acme")
	eq := h.errb.String()
	for _, want := range []string{"Equivalent: ccshelf account add work", "--marketplace acme/claude-plugins", "--plugin design-kit@acme"} {
		if !strings.Contains(eq, want) {
			t.Errorf("missing %q in %s", want, eq)
		}
	}
}

// ---- L6: the org protect lists are part of the trust closure ----

func TestProtectListsArePinnedInTheClosure(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	h.useOrg(org)
	toml := filepath.Join(org, "ccshelf.toml")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(toml, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[protect]\nplugins = [\"seo-tools@acme\"]\n")
	hash := orgHash(t, h, "sre")
	h.mustRun("trust", "sre", "--accept", hash)
	h.mustRun("run", "sre")

	// An unreviewed edit that drops the protection must need a new review.
	write("[protect]\nplugins = []\n")
	if code := h.run("run", "sre"); code != ui.ExitTrust || h.started != 1 {
		t.Fatalf("removed protection: code %d, started %d\n%s", code, h.started, h.errb)
	}
	if h2 := orgHash(t, h, "sre"); h2 == hash {
		t.Error("the closure hash ignores the protect list")
	}
	// So does adding a protected MCP label.
	write("[protect]\nplugins = [\"seo-tools@acme\"]\nmcp = [\"audit\"]\n")
	if code := h.run("run", "sre"); code != ui.ExitTrust {
		t.Fatalf("added mcp protection: code %d", code)
	}
	// Back to the reviewed state: trusted again without a new accept.
	write("[protect]\nplugins = [\"seo-tools@acme\"]\n")
	if code := h.run("run", "sre"); code != 0 {
		t.Fatalf("restored: code %d\n%s", code, h.errb)
	}
}

func TestPinProtectedLeavesPlainClosureAlone(t *testing.T) {
	r := &profile.Resolved{Closure: profile.Closure{Hash: "x", Items: []profile.ClosureItem{{Kind: "profile", Name: "a"}}}}
	(&session{}).pinProtected(r)
	if r.Closure.Hash != "x" || len(r.Closure.Items) != 1 {
		t.Errorf("closure changed without protected controls: %+v", r.Closure)
	}
	s := &session{pinPlugins: []string{"a@b"}, pinMCP: []string{"m"}}
	s.pinProtected(r)
	if len(r.Closure.Items) != 3 || r.Closure.Hash != profile.HashItems(r.Closure.Items) {
		t.Errorf("closure %+v", r.Closure)
	}
}

// ---- L7: interruption is exit 130, not a failure ----

func TestRunInterruptedWhileListingIsNotAFailure(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.ctx = ctx
	code := h.run("run", "mine")
	if code != ui.ExitInterrupted || h.started != 0 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	var ee *ui.ExitError
	if !errors.Is(h.lastErr, context.Canceled) || errors.As(h.lastErr, &ee) {
		t.Errorf("error %v must be the bare cancellation", h.lastErr)
	}
	if strings.Contains(h.errb.String(), "hint:") {
		t.Errorf("a hint about claude plugin list on Ctrl+C: %s", h.errb)
	}
}

// cancelOnConfirm cancels a context when it is asked a yes/no question.
type cancelOnConfirm struct {
	ui.Prompter
	cancel func()
}

func (c cancelOnConfirm) Confirm(ctx context.Context, q string, def bool) (bool, error) {
	c.cancel()
	return true, nil
}

func TestRunChecksContextRightBeforeStart(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", strings.ReplaceAll(personalMine, "design-kit@acme", "ghost@acme"))
	ctx, cancel := context.WithCancel(context.Background())
	h.ctx = ctx
	h.prompt = cancelOnConfirm{Prompter: ui.NewScripted(), cancel: cancel}
	code := h.run("run", "mine")
	if code != ui.ExitInterrupted || h.started != 0 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if !errors.Is(h.lastErr, context.Canceled) {
		t.Errorf("error %v", h.lastErr)
	}
}

// ---- L8: the editor command line ----

func TestParseEditor(t *testing.T) {
	dir := t.TempDir()
	spaced := filepath.Join(dir, "Program Files", "Notepad++")
	if err := os.MkdirAll(spaced, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(spaced, "notepad++.exe")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in   string
		want []string
		bad  bool
	}{
		{in: "", want: nil},
		{in: "   ", want: nil},
		{in: "vim", want: []string{"vim"}},
		{in: "code --wait  -n", want: []string{"code", "--wait", "-n"}},
		{in: exe, want: []string{exe}},
		{in: `"` + exe + `" -multiInst`, want: []string{exe, "-multiInst"}},
		{in: `'C:\Program Files\x.exe' -f`, want: []string{`C:\Program Files\x.exe`, "-f"}},
		{in: `ed -c "set x"`, want: []string{"ed", "-c", "set x"}},
		{in: `a "" b`, want: []string{"a", "", "b"}},
		{in: `vim "unterminated`, bad: true},
		{in: `vim 'x`, bad: true},
	}
	for _, c := range cases {
		got, err := parseEditor(c.in)
		if (err != nil) != c.bad {
			t.Errorf("%q: err %v", c.in, err)
			continue
		}
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") || len(got) != len(c.want) {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEditEditorPathWithSpaces(t *testing.T) {
	h := newHarness(t)
	h.prompt = ui.NewScripted(0)
	h.writeProfile("mine", personalMine)
	dir := filepath.Join(t.TempDir(), "My Editor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "ed.exe")
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", exe)
	h.mustRun("edit", "mine")
	if len(h.spawned) != 1 || h.spawned[0][0] != exe || len(h.spawned[0]) != 2 {
		t.Errorf("spawned %q", h.spawned)
	}
	t.Setenv("VISUAL", `"unterminated`)
	if code := h.run("edit", "mine"); code != ui.ExitUsage {
		t.Errorf("unterminated quote: code %d", code)
	}
}

// ---- L9: --flag=value forms ----

func TestRunOverridingArgForms(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		warn bool
	}{
		{"--settings=/x", true},
		{"--mcp-config=/y.json", true},
		{"--setting-sources=user", true},
		{"--strict-mcp-config", true},
		{"--append-system-prompt-file=/p", true},
		{"--settingsx", false},
		{"--model=opus", false},
		{"--effort", false},
	} {
		h := newHarness(t)
		h.writeProfile("mine", personalMine)
		h.mustRun("run", "mine", tc.arg)
		got := strings.Contains(h.errb.String(), "can override what the profile generated")
		if got != tc.warn {
			t.Errorf("%s: warned %v, want %v\n%s", tc.arg, got, tc.warn, h.errb)
		}
	}
}

func TestRunResumeWarnsAboutProfiles(t *testing.T) {
	for _, a := range []string{"--resume", "-r", "--continue", "-c", "--resume=abc"} {
		h := newHarness(t)
		h.writeProfile("mine", personalMine)
		h.mustRun("run", "mine", a)
		if !strings.Contains(h.errb.String(), "another profile") {
			t.Errorf("%s: no warning\n%s", a, h.errb)
		}
	}
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.mustRun("run", "mine", "-p", "x")
	if strings.Contains(h.errb.String(), "another profile") {
		t.Errorf("warned without --resume\n%s", h.errb)
	}
}

// ---- L10: case-insensitive paths on Windows and macOS ----

func TestSamePath(t *testing.T) {
	cases := []struct {
		a, b, goos string
		want       bool
	}{
		{"/Users/Me", "/users/me", "darwin", true},
		{"/C/Users/Me", "/c/users/me/", "windows", true},
		{"/home/Me", "/home/me", "linux", false},
		{"/home/me", "/home/me/", "linux", true},
		{"", "", "linux", false},
		{"/a", "", "darwin", false},
	}
	for _, c := range cases {
		if got := samePath(c.a, c.b, c.goos); got != c.want {
			t.Errorf("samePath(%q, %q, %s) = %v", c.a, c.b, c.goos, got)
		}
	}
}

func TestSameDirHonorsOS(t *testing.T) {
	d := t.TempDir()
	upper := filepath.Join(filepath.Dir(d), strings.ToUpper(filepath.Base(d)))
	if upper == d {
		t.Skip("directory name has no letters to change")
	}
	if !sameDir(d, upper, "windows") || !sameDir(d, upper, "darwin") {
		t.Error("case must not matter on windows and darwin")
	}
	if _, err := os.Stat(upper); err != nil && sameDir(d, upper, "linux") {
		t.Error("case must matter on linux")
	}
}

func TestFindProjectSkipsHomeIgnoringCase(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Home")
	if err := os.MkdirAll(filepath.Join(home, trust.ProjectFolder), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(home, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", strings.ToUpper(home))
	t.Setenv("USERPROFILE", strings.ToUpper(home))
	if runtime.GOOS == "windows" {
		t.Skip("os.UserHomeDir on windows reads USERPROFILE only after normalisation; covered by TestSamePath")
	}
	if got := findProject(sub, "darwin"); got != "" {
		t.Errorf("the home directory is not a project (case-insensitive): %q", got)
	}
	if got := findProject(sub, "linux"); got != home {
		t.Errorf("on a case-sensitive OS %q is a different directory: %q", strings.ToUpper(home), got)
	}
}

// ---- L11: init --ref counts as given ----

func TestInitRefWithoutURLDoesNotOpenTheWizard(t *testing.T) {
	h := newHarness(t)
	sc := ui.NewScripted() // any question would fail the script
	h.prompt = sc
	if code := h.run("init", "--ref", "v1"); code != ui.ExitUsage {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if !strings.Contains(h.errb.String(), "--ref needs --git-url") {
		t.Errorf("stderr: %s", h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
}

// ---- L12: edit resolves the result ----

func TestEditWarnsWhenTheEditedProfileDoesNotResolve(t *testing.T) {
	h := newHarness(t)
	h.prompt = ui.NewScripted(0)
	h.writeProfile("mine", personalMine)
	p := filepath.Join(h.configDir(), "profiles", "mine.toml")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myedit")
	h.spawnHook = func([]string) {
		_ = os.WriteFile(p, []byte("name = \"mine\"\nextends = [\"ghost\"]\n"), 0o600)
	}
	if code := h.run("edit", "mine"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if !strings.Contains(h.errb.String(), "does not resolve") {
		t.Errorf("no warning: %s", h.errb)
	}
	// A good edit stays quiet.
	h.spawnHook = nil
	h.errb.Reset()
	h.mustRun("edit", "mine")
}

func TestEditRefusesNonRegularFile(t *testing.T) {
	h := newHarness(t)
	t.Setenv("EDITOR", "myedit")
	dir := filepath.Join(h.configDir(), "profiles", "odd.toml")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if code := h.run("edit", "odd"); code != ui.ExitFailure || len(h.spawned) != 0 {
		t.Fatalf("code %d spawned %v\n%s", code, h.spawned, h.errb)
	}
	if !strings.Contains(h.errb.String(), "not a regular file") {
		t.Errorf("stderr: %s", h.errb)
	}
}

// ---- mutants: the run pipeline ----

func TestRunValidatesSettingsBeforeWriting(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	calls := 0
	h.validate = func(raw []byte) error {
		calls++
		if calls == 1 {
			return errors.New("closed schema says no")
		}
		return nil
	}
	if code := h.run("run", "mine"); code != ui.ExitFailure || h.started != 0 {
		t.Fatalf("code %d started %d", code, h.started)
	}
	if !strings.Contains(h.errb.String(), "generated settings are invalid") {
		t.Errorf("stderr: %s", h.errb)
	}
	if files := settingsFilesInCache(t); len(files) != 0 {
		t.Errorf("invalid settings were written: %v", files)
	}
}

func TestRunValidatesSettingsOnDisk(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	calls := 0
	h.validate = func(raw []byte) error {
		calls++
		if calls == 2 {
			return errors.New("on disk it is bad")
		}
		return nil
	}
	if code := h.run("run", "mine"); code != ui.ExitFailure || h.started != 0 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if calls != 2 || !strings.Contains(h.errb.String(), "settings file on disk is invalid") {
		t.Errorf("calls %d stderr: %s", calls, h.errb)
	}
}

func TestRunRejectsSettingsChangedAfterWrite(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.afterWrite = func(path string) {
		if err := os.WriteFile(path, []byte(`{"env":{}}`), 0o600); err != nil {
			t.Error(err)
		}
	}
	if code := h.run("run", "mine"); code != ui.ExitFailure || h.started != 0 {
		t.Fatalf("code %d started %d", code, h.started)
	}
	if !strings.Contains(h.errb.String(), "changed after it was written") {
		t.Errorf("stderr: %s", h.errb)
	}
}

func settingsFilesInCache(t *testing.T) []string {
	t.Helper()
	var found []string
	cache := os.Getenv("XDG_CACHE_HOME")
	_ = filepath.WalkDir(cache, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "settings") {
			found = append(found, p)
		}
		return nil
	})
	return found
}

func TestRunPluginSourceAccountMismatch(t *testing.T) {
	h := newHarness(t)
	acct := filepath.Join(t.TempDir(), "acct-work")
	install := filepath.Join(t.TempDir(), "orgplugin")
	testutil.WriteFile(t, filepath.Join(install, "profiles", "pp.toml"), "name = \"pp\"\naccount = \"work\"\n"+"[plugins]\ninclude = [\"design-kit@acme\"]\n")
	list := []map[string]any{{
		"id": "orgprofiles@acme", "version": "1.0.0", "scope": "user", "enabled": true, "installPath": filepath.ToSlash(install),
		"installedAt": "2026-01-01T00:00:00.000Z", "lastUpdated": "2026-01-01T00:00:00.000Z", "projectEnabled": false,
	}}
	b, _ := json.Marshal(list)
	pl := filepath.Join(t.TempDir(), "plugins.json")
	testutil.WriteFile(t, pl, string(b))
	t.Setenv("FAKE_CLAUDE_PLUGINS", pl)
	h.writeConfig("[accounts.work]\nconfig_dir = " + tomlString(acct) + "\n\n[[sources]]\ntype = \"plugin\"\nplugin = \"orgprofiles@acme\"\n")
	code := h.run("run", "pp")
	if code != ui.ExitUsage || h.started != 0 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if !strings.Contains(h.errb.String(), "plugin profile source was already read under another account") {
		t.Errorf("stderr: %s", h.errb)
	}
}

// pluginsWithMCP writes a plugin list where each id maps to its MCP server names.
func pluginsWithMCP(t *testing.T, plugins map[string][]string) string {
	t.Helper()
	var list []map[string]any
	for id, servers := range plugins {
		m := map[string]any{}
		for _, n := range servers {
			m[n] = map[string]any{"command": "x"}
		}
		e := map[string]any{"id": id, "version": "1.0.0", "scope": "user", "enabled": true, "installPath": "/fake/" + id}
		if len(m) > 0 {
			e["mcpServers"] = m
		}
		list = append(list, e)
	}
	b, _ := json.Marshal(list)
	pl := filepath.Join(t.TempDir(), "plugins.json")
	testutil.WriteFile(t, pl, string(b))
	return pl
}

func TestRunStrictWithProtectedMCPDeniesKnownServers(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	if err := os.WriteFile(filepath.Join(org, "ccshelf.toml"), []byte("[protect]\nmcp = [\"audit\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.useOrg(org)
	t.Setenv("FAKE_CLAUDE_PLUGINS", pluginsWithMCP(t, map[string][]string{
		"sre-kit@acme": {"runbooks"}, "design-kit@acme": {"figma", "tokens"}, "seo-tools@acme": {"crawler"},
	}))
	hash := orgHash(t, h, "sre") // sre sets mcp.strict = true and includes sre-kit
	h.mustRun("trust", "sre", "--accept", hash)
	if code := h.run("run", "sre"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if hasArg(h.startArgs, "--strict-mcp-config") {
		t.Errorf("--strict-mcp-config would remove the protected server: %v", h.startArgs)
	}
	if !strings.Contains(h.errb.String(), "protected MCP server audit") {
		t.Errorf("stderr: %s", h.errb)
	}
	got := fmt.Sprint(h.settingsOf(h.startArgs)["deniedMcpServers"])
	for _, want := range []string{"plugin:design-kit:figma", "plugin:design-kit:tokens", "plugin:seo-tools:crawler"} {
		if !strings.Contains(got, want) {
			t.Errorf("%s not denied: %s", want, got)
		}
	}
	if strings.Contains(got, "plugin:sre-kit") || strings.Contains(got, "audit") {
		t.Errorf("an included or protected server is denied: %s", got)
	}
}

func TestRunStrictDeniesInsteadWhenPolicyBlocksTheFlag(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("strict", "name = \"strict\"\n[plugins]\ninclude = [\"sre-kit@acme\"]\n[mcp]\nstrict = true\n")
	h.writeManaged(`{"disableSideloadFlags": true}`)
	t.Setenv("FAKE_CLAUDE_PLUGINS", pluginsWithMCP(t, map[string][]string{
		"sre-kit@acme": {"runbooks"}, "design-kit@acme": {"figma"},
	}))
	if code := h.run("run", "strict"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if hasArg(h.startArgs, "--strict-mcp-config") || hasArg(h.startArgs, "--mcp-config") {
		t.Errorf("blocked sideload flags used: %v", h.startArgs)
	}
	if got := fmt.Sprint(h.settingsOf(h.startArgs)["deniedMcpServers"]); !strings.Contains(got, "plugin:design-kit:figma") || strings.Contains(got, "sre-kit") {
		t.Errorf("deniedMcpServers = %s", got)
	}
	if !strings.Contains(h.errb.String(), "managed policy blocks it") {
		t.Errorf("stderr: %s", h.errb)
	}
	// on_blocked = "fail" keeps failing instead of degrading.
	h.writeProfile("strictfail", "name = \"strictfail\"\n[mcp]\nstrict = true\n[policy]\non_blocked = \"fail\"\n")
	h.started = 0
	if code := h.run("run", "strictfail"); code != ui.ExitPolicy || h.started != 0 {
		t.Errorf("on_blocked fail: code %d started %d\n%s", code, h.started, h.errb)
	}
}

func TestDeniableMCP(t *testing.T) {
	mk := func(id string, servers ...string) claude.Plugin {
		name, _ := claude.SplitID(id)
		p := claude.Plugin{ID: id, Name: name, MCPServers: map[string]json.RawMessage{}}
		for _, s := range servers {
			p.MCPServers[s] = json.RawMessage("{}")
		}
		return p
	}
	forced := mk("forced@m", "f")
	forced.RequiredByOrg = true
	got := deniableMCP(
		[]claude.Plugin{mk("in@m", "a"), mk("prot@m", "b"), mk("lock@m", "c"), mk("x@m", "s2", "s1"), forced, mk("keep@m", "k")},
		[]string{"in@m"}, []string{"prot@m"}, []string{"lock@m"}, []string{"plugin:keep:k", "other"})
	if want := []string{"plugin:x:s1", "plugin:x:s2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deniableMCP = %v, want %v", got, want)
	}
}

func TestRedactPass(t *testing.T) {
	for _, c := range []struct{ in, want []string }{
		{[]string{"-p", "hi", "--", "--api-key", "sk-abc"}, []string{"-p", "hi", "--", "--api-key", "<redacted>"}},
		{[]string{"--", "--token=zzz", "--", "--password", "p"}, []string{"--", "--token=<redacted>", "--", "--password", "<redacted>"}},
		{[]string{"--resume"}, []string{"--resume"}},
		{nil, []string{}},
	} {
		if got := redactPass(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("redactPass(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDryRunRedactsAfterDoubleDash(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.mustRun("dry-run", "mine", "-p", "x", "--", "--api-key", "sk-abc")
	if strings.Contains(h.out.String(), "sk-abc") {
		t.Errorf("secret printed: %s", h.out)
	}
}

func TestWarningsForGOOS(t *testing.T) {
	ws := []string{`MCP server "figma" runs "cmd", a shell or command launcher on windows`, `MCP server "x" runs "sh", a shell on linux`, "other"}
	if got := warningsForGOOS("darwin", ws); !reflect.DeepEqual(got, []string{"other"}) {
		t.Errorf("darwin: %v", got)
	}
	if got := warningsForGOOS("windows", ws); len(got) != 2 || !strings.Contains(got[0], "figma") {
		t.Errorf("windows: %v", got)
	}
}

func TestRunRejectsOwnFlagsAfterTheProfile(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	for _, args := range [][]string{
		{"run", "mine", "--account", "personal"},
		{"run", "mine", "--yes"},
		{"run", "mine", "--config=x"},
		{"dry-run", "mine", "--json"},
		{"run", "mine", "--no-interactive"},
	} {
		h.started = 0
		if code := h.run(args...); code != ui.ExitUsage || h.started != 0 {
			t.Errorf("%v: code %d started %d", args, code, h.started)
		}
		if !strings.Contains(h.errb.String(), "before the profile name") {
			t.Errorf("%v: stderr %s", args, h.errb)
		}
	}
	// After a literal "--" the arguments belong to claude.
	h.mustRun("run", "mine", "--", "--json")
}

func TestEditNeedsATerminal(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	t.Setenv("EDITOR", "myedit")
	if code := h.run("edit", "mine"); code != ui.ExitUsage || len(h.spawned) != 0 {
		t.Fatalf("code %d spawned %v", code, h.spawned)
	}
	if !strings.Contains(h.errb.String(), "needs a terminal") {
		t.Errorf("stderr: %s", h.errb)
	}
}

func TestInitGitURLLocalPath(t *testing.T) {
	h := newHarness(t)
	if code := h.run("init", "--git-url", "/some/local/dir"); code != ui.ExitUsage {
		t.Fatalf("code %d", code)
	}
	e := h.errb.String()
	if !strings.Contains(e, "--git-url") || strings.Contains(e, "needs --ref") || !strings.Contains(e, "--dir") {
		t.Errorf("stderr: %s", e)
	}
}

func TestNewRejectsFlagLikePluginID(t *testing.T) {
	h := newHarness(t)
	if code := h.run("new", "x1", "--plugin", "--x@y"); code != ui.ExitUsage {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if !strings.Contains(h.errb.String(), "not a plugin id") {
		t.Errorf("stderr: %s", h.errb)
	}
}

// ---- mutants: trust settlement ----

func sessionForTrust(t *testing.T, h *harness, allowed bool) *session {
	t.Helper()
	no := false
	env := &clicore.Env{
		Streams: ui.Streams{Out: h.out, Err: h.errb}, Getenv: os.Getenv, Environ: os.Environ,
		Getwd: func() (string, error) { return h.cwd, nil }, GOOS: "linux",
	}
	cc := env.Context(&clicore.Globals{}, "", "")
	return &session{
		l:  &launcher{opt: Options{Policy: policy.Options{GOOS: "linux", ManagedDir: h.managed, WSL: &no}}},
		cc: cc, cfg: config.Default(), proj: projectState{Allowed: allowed},
	}
}

func TestSettleTrustInconsistentClosureIsAFailure(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	h.useOrg(org)
	s0 := sessionForTrust(t, h, false)
	s, err := s0.l.open(context.Background(), s0.cc, true, false)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.resolve("seo")
	if err != nil {
		t.Fatal(err)
	}
	r.Closure.Hash = strings.Repeat("0", 64) // not the hash of the items
	err = s.settleTrust(context.Background(), r)
	var ee *ui.ExitError
	if !errors.As(err, &ee) || ee.Code != ui.ExitFailure || !errors.Is(err, trust.ErrInconsistentClosure) {
		t.Fatalf("err %v: want exit 1 wrapping ErrInconsistentClosure, not a trust request", err)
	}
}

func TestSettleTrustProjectUntrusted(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	prof := filepath.Join(repo, ".ccshelf", "profiles")
	testutil.WriteFile(t, filepath.Join(prof, "proj.toml"), strings.ReplaceAll(personalMine, "mine", "proj"))
	src := profile.DirSource(profile.KindProject, prof)
	r, err := profile.Resolve("proj", []profile.Source{src}, profile.ResolveOptions{AllowProject: true})
	if err != nil {
		t.Fatal(err)
	}
	s := sessionForTrust(t, h, false)
	err = s.settleTrust(context.Background(), r)
	var ee *ui.ExitError
	if !errors.As(err, &ee) || ee.Code != ui.ExitTrust {
		t.Fatalf("err %v", err)
	}
	if !strings.Contains(hintOf(err), "trust --project") {
		t.Errorf("hint %q must point at ccshelf trust --project", hintOf(err))
	}
}

func hintOf(err error) string {
	var h interface{ Hint() string }
	if errors.As(err, &h) {
		return h.Hint()
	}
	return ""
}

func TestResolveNeverAllowsProjectUnlessTrusted(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	prof := filepath.Join(repo, ".ccshelf", "profiles")
	testutil.WriteFile(t, filepath.Join(prof, "proj.toml"), strings.ReplaceAll(personalMine, "mine", "proj"))
	s := sessionForTrust(t, h, false)
	s.sources = []profile.Source{profile.DirSource(profile.KindProject, prof)}
	_, err := s.resolve("proj")
	var ee *ui.ExitError
	if !errors.As(err, &ee) || ee.Code != ui.ExitTrust || !errors.Is(err, profile.ErrProjectNotTrusted) {
		t.Fatalf("err %v: a project profile must not resolve while the folder is untrusted", err)
	}
	s.proj.Allowed = true
	if _, err := s.resolve("proj"); err != nil {
		t.Errorf("trusted project: %v", err)
	}
}

func TestRunProjectProfileEndToEnd(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	h.cwd = repo
	testutil.WriteFile(t, filepath.Join(repo, ".ccshelf", "profiles", "proj.toml"), strings.ReplaceAll(personalMine, "mine", "proj"))
	h.writeConfig("[trust]\ntrust_project_profiles = true\n")
	// Not trusted: not found, with the reason.
	if code := h.run("run", "proj"); code != ui.ExitUsage || h.started != 0 {
		t.Fatalf("untrusted folder: code %d", code)
	}
	h.run("trust", "--project", "--accept", "bad")
	_, rest, _ := strings.Cut(h.out.String(), "content hash ")
	folder, _, _ := strings.Cut(rest, ")")
	h.mustRun("trust", "--project", "--accept", folder)
	// Folder trusted, profile not yet: exit 4 with the closure to review.
	if code := h.run("run", "proj"); code != ui.ExitTrust || h.started != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	hash := orgHash(t, h, "proj")
	h.mustRun("trust", "proj", "--accept", hash)
	if code := h.run("run", "proj"); code != 0 || h.started != 1 {
		t.Fatalf("trusted project profile: code %d\n%s", code, h.errb)
	}
}

// ---- mutants: interaction ----

func TestPickerEquivalentKeepsGlobalFlags(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	acct := filepath.Join(t.TempDir(), "acct")
	cfgFile := filepath.Join(t.TempDir(), "alt.toml")
	testutil.WriteFile(t, cfgFile, "[accounts.work]\nconfig_dir = "+tomlString(acct)+"\n")
	h.g.ConfigPath = cfgFile
	h.prompt = ui.NewScripted(0)
	h.mustRun("run", "--account", "work")
	eq := h.errb.String()
	for _, want := range []string{"--config " + cfgFile, "--claude " + h.claude, "--account work", " mine"} {
		if !strings.Contains(eq, want) {
			t.Errorf("equivalent line lacks %q: %s", want, eq)
		}
	}
}

func TestJSONNeverPrompts(t *testing.T) {
	h := newHarness(t)
	sc := ui.NewScripted("night")
	h.prompt = sc
	if code := h.run("--json", "new"); code != ui.ExitUsage {
		t.Fatalf("--json must not prompt for the name: code %d\n%s", code, h.errb)
	}
	if err := sc.Done(); err == nil {
		t.Error("the scripted answer was consumed")
	}
}

func TestWriteNewFileNeverReplaces(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "p.toml")
	if err := writeNewFile(p, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := writeNewFile(p, []byte("second")); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second write: %v", err)
	}
	if b, _ := os.ReadFile(p); string(b) != "first" {
		t.Errorf("existing file replaced: %q", b)
	}
}

package launcher

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
)

const baseConfig = `# my notes
[[sources]]
type = "git"
url = "https://example.com/acme/data.git"
ref = "v1.0.0"
path = "profiles"
`

func (h *harness) configFile() string { return filepath.Join(h.configDir(), "config.toml") }

func (h *harness) readConfig() string {
	h.t.Helper()
	b, err := os.ReadFile(h.configFile())
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

func (h *harness) loadConfig() *config.Config {
	h.t.Helper()
	cfg, err := config.Load(h.configFile())
	if err != nil {
		h.t.Fatal(err)
	}
	return cfg
}

// wantCode runs the command and fails unless it exits with code.
func (h *harness) wantCode(code int, args ...string) {
	h.t.Helper()
	if got := h.run(args...); got != code {
		h.t.Fatalf("ccshelf %v exited %d, want %d\nstdout:\n%s\nstderr:\n%s", args, got, code, h.out, h.errb)
	}
}

func (h *harness) wantErr(sub ...string) {
	h.t.Helper()
	for _, s := range sub {
		if !strings.Contains(h.errb.String(), s) {
			h.t.Errorf("stderr lacks %q:\n%s", s, h.errb)
		}
	}
}

func TestConfigPathShowAndLs(t *testing.T) {
	h := newHarness(t)
	// A missing file: path works, show prints defaults and says so, ls is empty.
	h.mustRun("config", "path")
	if strings.TrimSpace(h.out.String()) != h.configFile() {
		t.Errorf("path = %q", h.out)
	}
	h.mustRun("config", "path", "--json")
	var p struct {
		Data struct {
			Path   string
			Exists bool
		}
	}
	if err := json.Unmarshal(h.out.Bytes(), &p); err != nil || p.Data.Exists || p.Data.Path != h.configFile() {
		t.Errorf("path json: %v %s", err, h.out)
	}
	h.mustRun("config", "show")
	for _, want := range []string{"does not exist", "require_pin: true (default)", "mode: off (default)", "(none configured"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("show lacks %q:\n%s", want, h.out)
		}
	}
	h.mustRun("config", "source", "ls")
	if !strings.Contains(h.out.String(), "no profile sources configured") {
		t.Errorf("ls: %s", h.out)
	}

	h.writeConfig(baseConfig + "\n[update]\nmode = \"notify\"\n")
	h.mustRun("config", "show")
	for _, want := range []string{"1. git https://example.com/acme/data.git", "ref: v1.0.0", "mode: notify", "interval: 24h0m0s (default)"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("show lacks %q:\n%s", want, h.out)
		}
	}
	h.mustRun("config", "show", "--json")
	var env struct {
		Kind string
		Data struct {
			Exists  bool
			Sources []struct {
				Number int
				Type   string
				Ref    string
			}
			Update struct{ Mode string }
		}
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "config" || !env.Data.Exists || len(env.Data.Sources) != 1 || env.Data.Sources[0].Number != 1 || env.Data.Update.Mode != "notify" {
		t.Errorf("show json: %+v", env)
	}
	h.mustRun("config", "source", "ls")
	if !strings.Contains(h.out.String(), "example.com/acme/data.git") || !strings.Contains(h.out.String(), "v1.0.0") {
		t.Errorf("ls: %s", h.out)
	}
	h.mustRun("config", "source", "ls", "--json")
	if !strings.Contains(h.out.String(), `"kind": "config-sources"`) {
		t.Errorf("ls json: %s", h.out)
	}
	// --config picks another file.
	other := filepath.Join(t.TempDir(), "other.toml")
	h.mustRun("--config", other, "config", "path")
	if strings.TrimSpace(h.out.String()) != other {
		t.Errorf("--config path = %q", h.out)
	}
}

func TestConfigNeedsExistingFile(t *testing.T) {
	h := newHarness(t)
	for _, args := range [][]string{
		{"config", "source", "add", "--dir", h.dirs["WORK"]},
		{"config", "source", "pin", "1", "--ref", "v1"},
		{"config", "source", "rm", "1"},
		{"config", "set", "ui.color", "never"},
		{"config", "unset", "ui.color"},
		{"config", "edit", "--path"},
	} {
		h.wantCode(ui.ExitFailure, args...)
		h.wantErr("ccshelf init")
		if _, err := os.Stat(h.configFile()); err == nil {
			t.Fatalf("%v created the file", args)
		}
	}
}

func TestConfigSourceAdd(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	dir := filepath.Join(h.dirs["WORK"], "team")
	h.mustRun("config", "source", "add", "--dir", dir)
	h.mustRun("config", "source", "add", "--plugin", "org-profiles@acme", "--marketplace", "acme/claude-marketplace", "--path", "profiles")
	h.mustRun("config", "source", "add", "--git-url", "git@example.com:acme/other.git", "--ref", "1111111111111111111111111111111111111111", "--path", "p")
	cfg := h.loadConfig()
	if len(cfg.Sources) != 4 {
		t.Fatalf("sources: %+v", cfg.Sources)
	}
	if s := cfg.Sources[1]; s.Type != "dir" || s.Path != dir {
		t.Errorf("dir source: %+v", s)
	}
	if s := cfg.Sources[2]; s.Type != "plugin" || s.Plugin != "org-profiles@acme" || s.Marketplace != "acme/claude-marketplace" {
		t.Errorf("plugin source: %+v", s)
	}
	if s := cfg.Sources[3]; s.Path != "p" || s.URL != "git@example.com:acme/other.git" {
		t.Errorf("git source: %+v", s)
	}
	if !strings.Contains(h.errb.String(), "wrote") {
		t.Errorf("stderr: %s", h.errb)
	}
	// Nothing is fetched or trusted.
	if _, err := os.Stat(filepath.Join(h.dirs["XDG_STATE_HOME"], "ccshelf")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(h.dirs["XDG_STATE_HOME"], "ccshelf"))
		if len(entries) > 0 {
			t.Errorf("state written: %v", entries)
		}
	}
}

func TestConfigSourceAddRefusals(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	before := h.readConfig()
	dir := filepath.Join(h.dirs["WORK"], "team")
	cases := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"duplicate git", []string{"--git-url", "https://example.com/acme/data.git", "--ref", "v2.0.0"}, 2, "config source pin 1"},
		{"branch ref", []string{"--git-url", "https://example.com/acme/x.git", "--ref", "main"}, 2, "moving reference"},
		{"no ref", []string{"--git-url", "https://example.com/acme/x.git"}, 2, "ref"},
		{"token in url", []string{"--git-url", "https://user:ghp_secret@example.com/acme/x.git", "--ref", "v1"}, 2, "user information"},
		{"token in path", []string{"--git-url", "https://example.com/ghp_abcdef/x.git", "--ref", "v1"}, 2, "credential"},
		{"local path as url", []string{"--git-url", "/tmp/repo", "--ref", "v1"}, 2, "--dir"},
		{"two kinds", []string{"--git-url", "https://example.com/a/b.git", "--dir", dir}, 2, "exactly one"},
		{"none", nil, 2, "--git-url"},
		{"relative dir", []string{"--dir", "relative/dir"}, 2, "absolute"},
		{"bad plugin", []string{"--plugin", "nomarketplace"}, 2, "name@marketplace"},
		{"ref with dir", []string{"--dir", dir, "--ref", "v1"}, 2, "--dir takes no"},
		{"ref with plugin", []string{"--plugin", "a@b", "--ref", "v1"}, 2, "--git-url"},
		{"path escapes", []string{"--git-url", "https://example.com/a/b.git", "--ref", "v1", "--path", "../x"}, 2, ".."},
	}
	for _, tc := range cases {
		h.wantCode(tc.code, append([]string{"config", "source", "add"}, tc.args...)...)
		h.wantErr(tc.want)
		if h.readConfig() != before {
			t.Fatalf("%s: file changed", tc.name)
		}
	}
	// A dir duplicate.
	h.mustRun("config", "source", "add", "--dir", dir)
	h.wantCode(2, "config", "source", "add", "--dir", dir)
	h.wantErr("config source rm")
}

func TestConfigSourcePinAndRm(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(h.dirs["WORK"], "team")
	h.writeConfig(baseConfig + "\n[[sources]]\ntype = \"dir\"\npath = " + tomlString(dir) + "\n")
	h.mustRun("config", "source", "pin", "1", "--ref", "v1.1.0")
	if got := h.loadConfig().Sources[0].Ref; got != "v1.1.0" {
		t.Errorf("ref = %q", got)
	}
	h.wantErr("trust")
	// Same ref again: no change, no write.
	bak, _ := os.ReadFile(h.configFile() + ".bak")
	h.mustRun("config", "source", "pin", "1", "--ref", "v1.1.0")
	if now, _ := os.ReadFile(h.configFile() + ".bak"); string(now) != string(bak) {
		t.Error("an unchanged pin rewrote the backup")
	}
	h.wantErr("no change")

	h.wantCode(2, "config", "source", "pin", "1", "--ref", "main")
	h.wantCode(2, "config", "source", "pin", "1", "--ref", "feature/x")
	h.wantCode(2, "config", "source", "pin", "2", "--ref", "v1")
	h.wantErr("only git sources")
	h.wantCode(2, "config", "source", "pin", "3", "--ref", "v1")
	h.wantCode(2, "config", "source", "pin", "x", "--ref", "v1")
	h.wantCode(2, "config", "source", "pin", "1")
	h.wantErr("--ref")
	h.wantCode(2, "config", "source", "pin")

	h.mustRun("config", "source", "rm", "1")
	cfg := h.loadConfig()
	if len(cfg.Sources) != 1 || cfg.Sources[0].Type != "dir" {
		t.Errorf("after rm: %+v", cfg.Sources)
	}
	h.wantCode(2, "config", "source", "rm", "5")
	h.wantCode(2, "config", "source", "rm")
	h.mustRun("config", "source", "rm", "1")
	if len(h.loadConfig().Sources) != 0 {
		t.Error("sources remain")
	}
}

func TestConfigSetUnset(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	h.mustRun("config", "set", "update.mode", "notify")
	h.mustRun("config", "set", "update.interval", "48h")
	h.mustRun("config", "set", "trust.on_change", "fail")
	h.mustRun("config", "set", "ui.color", "never")
	h.mustRun("config", "set", "ui.interactive", "never")
	h.mustRun("config", "set", "catalog.remote_url", "https://catalog.example.com/catalog.json")
	cfg := h.loadConfig()
	if cfg.Update.Mode != "notify" || cfg.Update.Interval != "48h" || cfg.Trust.OnChange != "fail" || cfg.UI.Color != "never" || cfg.UI.Interactive != "never" || cfg.Catalog.RemoteURL == "" {
		t.Errorf("config: %+v", cfg)
	}
	h.mustRun("config", "unset", "update.interval")
	h.mustRun("config", "unset", "ui.color")
	h.mustRun("config", "unset", "catalog.remote_url")
	cfg = h.loadConfig()
	if cfg.Update.Interval != "" || cfg.UI.Color != "auto" || cfg.Catalog.RemoteURL != "" {
		t.Errorf("after unset: %+v", cfg)
	}
	h.mustRun("config", "set", "update.mode", "notify") // unchanged
	h.wantErr("no change")

	for _, tc := range []struct{ key, val, want string }{
		{"update.mode", "weekly", "not one of"},
		{"update.interval", "5m", "shorter"},
		{"update.interval", "soon", "duration"},
		{"trust.require_pin", "maybe", "not one of"},
		{"trust.on_change", "allow", "not one of"},
		{"catalog.remote_url", "http://catalog.example.com/c.json", "HTTPS"},
		{"catalog.remote_url", "https://u:p@catalog.example.com/c.json", "credentials"},
		{"catalog.remote_url", "https://catalog.example.com/ghp_x/c.json", "credential"},
		{"default_account", "nosuch", "not a configured account"},
		{"ui.color", "", "empty"},
	} {
		h.wantCode(2, "config", "set", tc.key, tc.val)
		h.wantErr(tc.want)
	}
	for _, key := range []string{"claude.path", "update.base_url", "update.cosign_identity_repo", "update.asset_hosts", "accounts", "accounts.work", "sources", "nosuch.key"} {
		h.wantCode(2, "config", "set", key, "x")
		h.wantErr("cannot be changed with config set")
		h.wantCode(2, "config", "unset", key)
	}
	h.wantCode(2, "config", "set", "accounts.work", "x")
	h.wantErr("ccshelf account")
	h.wantCode(2, "config", "set", "claude.path", "x")
	h.wantErr("config edit")
	h.wantCode(2, "config", "set")
	h.wantCode(2, "config", "set", "ui.color")
	h.wantCode(2, "config", "unset")
}

func TestConfigWeakeningNeedsYes(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	before := h.readConfig()
	for _, args := range [][]string{
		{"trust.require_pin", "false"},
		{"trust.trust_project_profiles", "true"},
		{"update.mode", "install"},
	} {
		h.wantCode(2, "config", "set", args[0], args[1])
		h.wantErr("--yes")
		if h.readConfig() != before {
			t.Fatalf("%v changed the file", args)
		}
		h.mustRun("config", "set", args[0], args[1], "--yes")
		h.wantErr("weakens a security setting")
		before = h.readConfig()
	}
	cfg := h.loadConfig()
	if cfg.Trust.RequirePin || !cfg.Trust.TrustProjectProfiles || cfg.Update.Mode != "install" {
		t.Errorf("config: %+v", cfg)
	}
	// Going back to the safe default is not a weakening.
	h.mustRun("config", "set", "trust.require_pin", "true")
	if strings.Contains(h.errb.String(), "weakens") {
		t.Errorf("tightening warned: %s", h.errb)
	}
	h.mustRun("config", "unset", "update.mode")
	h.mustRun("config", "unset", "trust.trust_project_profiles")
	// An unpinned git source while pinning is off needs --yes too.
	h.mustRun("config", "set", "trust.require_pin", "false", "--yes")
	h.wantCode(2, "config", "source", "add", "--git-url", "https://example.com/acme/loose.git", "--ref", "main")
	h.wantErr("--yes")
	h.wantCode(2, "config", "source", "add", "--git-url", "https://example.com/acme/loose.git")
	h.mustRun("config", "source", "add", "--git-url", "https://example.com/acme/loose.git", "--ref", "main", "--yes")
	h.wantErr("not a tag or full commit")
	h.mustRun("config", "source", "add", "--git-url", "https://example.com/acme/pinned.git", "--ref", "v1.2.3")
	// A weakening change reports itself in --json.
	h.mustRun("config", "set", "update.mode", "install", "--yes", "--json")
	var env struct {
		Kind string
		Data struct {
			Changed   bool
			Weakening []string
		}
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil || env.Kind != "config-change" || !env.Data.Changed || len(env.Data.Weakening) != 1 {
		t.Errorf("json: %v %s", err, h.out)
	}
	// Trust is never touched.
	for _, name := range []string{"lock.json", "project-trust.json"} {
		if _, err := os.Stat(filepath.Join(h.dirs["XDG_STATE_HOME"], "ccshelf", name)); err == nil {
			t.Errorf("%s written", name)
		}
	}
}

func TestConfigBackupAndCommentWarning(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	h.mustRun("config", "set", "ui.color", "never")
	h.wantErr("comments", "config.toml.bak")
	bak := h.configFile() + ".bak"
	b, err := os.ReadFile(bak)
	if err != nil || string(b) != baseConfig {
		t.Fatalf("backup = %q, %v", b, err)
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{bak, h.configFile()} {
			if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
				t.Errorf("%s mode %v", p, fi.Mode().Perm())
			}
		}
	}
	// The second write has no comment left to warn about, and replaces the backup.
	prev := h.readConfig()
	h.mustRun("config", "set", "ui.color", "always")
	if strings.Contains(h.errb.String(), "comments") {
		t.Errorf("warned without comments: %s", h.errb)
	}
	if b, _ := os.ReadFile(bak); string(b) != prev {
		t.Errorf("backup not replaced: %q", b)
	}
}

func TestConfigRefusesSymlinkedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	h := newHarness(t)
	real := filepath.Join(t.TempDir(), "real.toml")
	if err := os.WriteFile(real, []byte(baseConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.configDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, h.configFile()); err != nil {
		t.Fatal(err)
	}
	h.wantCode(1, "config", "set", "ui.color", "never")
	h.wantErr("symbolic link")
	if b, _ := os.ReadFile(real); string(b) != baseConfig {
		t.Error("target changed")
	}
	// A symlinked backup name is refused too, and the config stays as it was.
	h = newHarness(t)
	h.writeConfig(baseConfig)
	if err := os.Symlink(real, h.configFile()+".bak"); err != nil {
		t.Fatal(err)
	}
	h.wantCode(1, "config", "set", "ui.color", "never")
	if h.readConfig() != baseConfig {
		t.Error("config changed")
	}
	if b, _ := os.ReadFile(real); string(b) != baseConfig {
		t.Error("backup target changed")
	}
}

// hookPrompt runs a function when the write confirmation is asked.
type hookPrompt struct {
	*ui.Scripted
	onConfirm func()
}

func (p *hookPrompt) Confirm(ctx context.Context, title string, def bool) (bool, error) {
	if p.onConfirm != nil && title == "Write this configuration?" {
		p.onConfirm()
	}
	return p.Scripted.Confirm(ctx, title, def)
}

func TestConfigConfirmationAndChangedWhileEditing(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	// Declined: exit 1, nothing written, default is no.
	sc := ui.NewScripted(false)
	h.prompt = sc
	h.wantCode(1, "config", "set", "ui.color", "never")
	h.wantErr("configuration not written", "- color = ", "+ color = ", "Change to")
	if h.readConfig() != baseConfig {
		t.Error("declined confirmation wrote")
	}
	if _, err := os.Stat(h.configFile() + ".bak"); err == nil {
		t.Error("declined confirmation wrote a backup")
	}
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	// Aborted.
	h.prompt = ui.NewScripted(ui.ErrAborted)
	h.wantCode(ui.ExitInterrupted, "config", "set", "ui.color", "never")
	// Confirmed.
	h.prompt = ui.NewScripted(true)
	h.mustRun("config", "set", "ui.color", "never")
	if h.loadConfig().UI.Color != "never" {
		t.Error("not written")
	}
	if strings.Contains(h.errb.String(), "Equivalent:") {
		t.Error("a flag run printed an equivalent command")
	}
	// --yes answers only the confirmation: no prompt is asked.
	h.prompt = ui.NewScripted()
	h.mustRun("config", "set", "ui.color", "always", "--yes")
	// The file changes while the confirmation is open.
	h.writeConfig(baseConfig)
	other := baseConfig + "\n[ui]\ncolor = \"always\"\n"
	h.prompt = &hookPrompt{Scripted: ui.NewScripted(true), onConfirm: func() { h.writeConfig(other) }}
	h.wantCode(1, "config", "set", "update.mode", "notify")
	h.wantErr("changed while editing, nothing written")
	if h.readConfig() != other {
		t.Error("the other change was overwritten")
	}
	if _, err := os.Stat(h.configFile() + ".bak"); err == nil {
		if b, _ := os.ReadFile(h.configFile() + ".bak"); string(b) == other {
			t.Error("backup holds the unseen file")
		}
	}
	// With --no-interactive a terminal is not used even if one exists.
	h.prompt = ui.NewScripted()
	h.mustRun("--no-interactive", "config", "set", "update.mode", "notify")
}

func TestConfigAloneWithoutTerminal(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	h.wantCode(2, "config")
	if !strings.Contains(h.errb.String(), "Usage:") && !strings.Contains(h.errb.String(), "Available Commands") {
		t.Errorf("no help on stderr: %s", h.errb)
	}
	h.prompt = ui.NewScripted()
	h.wantCode(2, "--no-interactive", "config")
}

func TestConfigMenu(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	dir := filepath.Join(h.dirs["WORK"], "team")
	url := "https://example.com/acme/second.git"
	// Build the answers by name for the settings picker.
	keys := config.SettingKeys()
	colorIdx := -1
	for i, k := range keys {
		if k == "ui.color" {
			colorIdx = i
		}
	}
	sc := ui.NewScripted(
		0, 0, url, "v2.0.0", "profiles", true,
		1, 0, "v2.1.0", true,
		3, colorIdx, 2, true, // ui.color values: auto, always, never -> never
		0, 1, dir, true,
		2, 2, true,
		5,
	)
	h.prompt = sc
	h.mustRun("config")
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	cfg := h.loadConfig()
	if len(cfg.Sources) != 2 || cfg.Sources[0].Ref != "v2.1.0" || cfg.Sources[1].URL != url || cfg.UI.Color != "never" {
		t.Fatalf("config: %+v", cfg)
	}
	// The equivalent lines are printed per action; run one action alone to
	// check them against stderr (the harness resets buffers per run).
	h = newHarness(t)
	h.writeConfig(baseConfig)
	h.prompt = ui.NewScripted(0, 0, url, "v2.0.0", "profiles", true, 5)
	h.mustRun("config")
	h.wantErr("Equivalent: ccshelf config source add --git-url "+url+" --ref v2.0.0 --yes", "Nothing is fetched or trusted", "+ url = ", "wrote")

	// The replayed command gives the same file.
	want := h.readConfig()
	h2 := newHarness(t)
	h2.writeConfig(baseConfig)
	h2.mustRun("config", "source", "add", "--git-url", url, "--ref", "v2.0.0", "--yes")
	if h2.readConfig() != want {
		t.Errorf("replay differs:\n%s\n---\n%s", h2.readConfig(), want)
	}

	// Pin / set / rm equivalents.
	h = newHarness(t)
	h.writeConfig(baseConfig)
	h.prompt = ui.NewScripted(1, 0, "v3.0.0", true, 5)
	h.mustRun("config")
	h.wantErr("Equivalent: ccshelf config source pin 1 --ref v3.0.0 --yes")
	h.prompt = ui.NewScripted(3, colorIdx, 2, true, 5)
	h.mustRun("config")
	h.wantErr("Equivalent: ccshelf config set ui.color never --yes")
	h.prompt = ui.NewScripted(2, 0, true, 5)
	h.mustRun("config")
	h.wantErr("Equivalent: ccshelf config source rm 1 --yes")
	// Declining in the menu exits 1 with nothing written.
	h.writeConfig(baseConfig)
	h.prompt = ui.NewScripted(3, colorIdx, 2, false)
	h.wantCode(1, "config")
	if h.readConfig() != baseConfig {
		t.Error("declined menu change wrote")
	}
	// Done right away writes nothing and exits 0; Ctrl+C exits 130.
	h.prompt = ui.NewScripted(5)
	h.mustRun("config")
	h.prompt = ui.NewScripted(ui.ErrAborted)
	h.wantCode(ui.ExitInterrupted, "config")
	// A missing file fails before the first question.
	h = newHarness(t)
	h.prompt = ui.NewScripted()
	h.wantCode(1, "config")
}

func TestConfigSetPrompted(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	// A key without a value asks for the value; an empty value unsets.
	h.prompt = ui.NewScripted("48h", true)
	h.mustRun("config", "set", "update.interval")
	h.wantErr("Equivalent: ccshelf config set update.interval 48h --yes")
	h.prompt = ui.NewScripted("", true)
	h.mustRun("config", "set", "update.interval")
	h.wantErr("Equivalent: ccshelf config unset update.interval --yes")
	if h.loadConfig().Update.Interval != "" {
		t.Error("not unset")
	}
}

// ---- config edit ----------------------------------------------------------

func editWith(h *harness, content func(path string) string) {
	h.spawnHook = func(args []string) {
		p := args[len(args)-1]
		if err := os.WriteFile(p, []byte(content(p)), 0o600); err != nil {
			h.t.Fatal(err)
		}
	}
}

func TestConfigEditValid(t *testing.T) {
	t.Setenv("EDITOR", "myedit")
	t.Setenv("VISUAL", "")
	h := newHarness(t)
	h.writeConfig(baseConfig)
	edited := baseConfig + "\n# added by hand\n[ui]\ncolor = \"never\"\n"
	editWith(h, func(string) string { return edited })
	h.prompt = ui.NewScripted(true)
	h.mustRun("config", "edit")
	if h.readConfig() != edited {
		t.Errorf("config = %q", h.readConfig())
	}
	if b, _ := os.ReadFile(h.configFile() + ".bak"); string(b) != baseConfig {
		t.Errorf("backup = %q", b)
	}
	if len(h.spawned) != 1 || h.spawned[0][0] != "myedit" {
		t.Fatalf("spawned %v", h.spawned)
	}
	copyPath := h.spawned[0][len(h.spawned[0])-1]
	if filepath.Dir(copyPath) != h.configDir() || copyPath == h.configFile() {
		t.Errorf("copy = %s", copyPath)
	}
	if _, err := os.Stat(copyPath); err == nil {
		t.Error("the copy was not removed")
	}
	h.wantErr("wrote")
	// The copy had mode 0600 while it existed.
	if runtime.GOOS != "windows" {
		var mode os.FileMode
		h2 := newHarness(t)
		h2.writeConfig(baseConfig)
		h2.spawnHook = func(args []string) {
			fi, err := os.Stat(args[len(args)-1])
			if err != nil {
				t.Fatal(err)
			}
			mode = fi.Mode().Perm()
		}
		h2.prompt = ui.NewScripted()
		h2.mustRun("config", "edit")
		if mode != 0o600 {
			t.Errorf("copy mode %v", mode)
		}
	}
}

func TestConfigEditUnchangedDeclinedAndInvalid(t *testing.T) {
	t.Setenv("EDITOR", "myedit")
	t.Setenv("VISUAL", "")
	h := newHarness(t)
	h.writeConfig(baseConfig)

	// Saved without changes.
	h.prompt = ui.NewScripted()
	h.mustRun("config", "edit")
	h.wantErr("no change")
	if _, err := os.Stat(h.configFile() + ".bak"); err == nil {
		t.Error("a no-op edit wrote a backup")
	}
	left, _ := filepath.Glob(filepath.Join(h.configDir(), "config-edit-*"))
	if len(left) != 0 {
		t.Errorf("copies left: %v", left)
	}

	// Invalid content: the original is untouched, errors name the line, the copy is kept.
	var copyPath string
	editWith(h, func(p string) string { copyPath = p; return baseConfig + "\nbogus_key = 1\n" })
	h.prompt = ui.NewScripted()
	h.wantCode(1, "config", "edit")
	h.wantErr("invalid", "unknown key", "line ", copyPath)
	if h.readConfig() != baseConfig {
		t.Error("an invalid edit replaced the file")
	}
	if _, err := os.Stat(copyPath); err != nil {
		t.Errorf("the copy was not kept: %v", err)
	}
	// A validation error (not a syntax one).
	editWith(h, func(p string) string { copyPath = p; return "[trust]\non_change = \"allow\"\n" })
	h.wantCode(1, "config", "edit")
	h.wantErr("trust.on_change")
	if h.readConfig() != baseConfig {
		t.Error("an invalid edit replaced the file")
	}

	// Valid but declined.
	editWith(h, func(string) string { return baseConfig + "\n[ui]\ncolor = \"never\"\n" })
	h.prompt = ui.NewScripted(false)
	h.wantCode(1, "config", "edit")
	h.wantErr("configuration not written", "kept in")
	if h.readConfig() != baseConfig {
		t.Error("declined edit wrote")
	}

	// The editor fails.
	h.spawnHook = nil
	h.spawnCode = 3
	h.prompt = ui.NewScripted()
	h.wantCode(1, "config", "edit")
	h.wantErr("status 3")
	h.spawnCode = 0

	// Weakening through the editor warns and needs the confirmation.
	editWith(h, func(string) string { return baseConfig + "\n[trust]\nrequire_pin = false\non_change = \"prompt\"\n" })
	h.prompt = ui.NewScripted(true)
	h.mustRun("config", "edit")
	h.wantErr("weakens")
	if h.loadConfig().Trust.RequirePin {
		t.Error("not written")
	}
}

func TestConfigEditOriginalChangedAndNoTerminal(t *testing.T) {
	t.Setenv("EDITOR", "myedit")
	t.Setenv("VISUAL", "")
	h := newHarness(t)
	h.writeConfig(baseConfig)
	other := baseConfig + "\n[ui]\ncolor = \"always\"\n"
	h.spawnHook = func(args []string) {
		h.writeConfig(other)
		_ = os.WriteFile(args[len(args)-1], []byte(baseConfig+"\n[ui]\ncolor = \"never\"\n"), 0o600)
	}
	h.prompt = ui.NewScripted(true)
	h.wantCode(1, "config", "edit")
	h.wantErr("changed while editing, nothing written")
	if h.readConfig() != other {
		t.Error("the other change was overwritten")
	}

	// Without a terminal edit refuses; --path still works.
	h = newHarness(t)
	h.writeConfig(baseConfig)
	h.wantCode(2, "config", "edit")
	h.wantErr("needs a terminal", "--path")
	h.mustRun("config", "edit", "--path")
	if strings.TrimSpace(h.out.String()) != h.configFile() {
		t.Errorf("--path = %q", h.out)
	}
	if len(h.spawned) != 0 {
		t.Error("an editor was started")
	}
	// No editor configured.
	t.Setenv("EDITOR", "")
	h.prompt = ui.NewScripted()
	h.wantCode(2, "config", "edit")
	h.wantErr("no editor")
}

func TestConfigEditRepairsInvalidFile(t *testing.T) {
	t.Setenv("EDITOR", "myedit")
	t.Setenv("VISUAL", "")
	h := newHarness(t)
	h.writeConfig("bogus = 1\n")
	h.wantCode(1, "config", "show")
	editWith(h, func(string) string { return baseConfig })
	h.prompt = ui.NewScripted(true)
	h.mustRun("config", "edit")
	h.wantErr("current file is invalid")
	if h.readConfig() != baseConfig {
		t.Errorf("config = %q", h.readConfig())
	}
}

func TestDiffLines(t *testing.T) {
	a := strings.Split("[[sources]]\ntype = \"git\"\nref = \"v1\"\n[ui]\ncolor = \"auto\"", "\n")
	b := strings.Split("[[sources]]\ntype = \"git\"\nref = \"v1\"\n[[sources]]\ntype = \"git\"\nref = \"v2\"\n[ui]\ncolor = \"auto\"", "\n")
	got := strings.Join(diffLines(a, b), "|")
	if got != "+ [[sources]]|+ type = \"git\"|+ ref = \"v2\"" {
		t.Errorf("diff = %q", got)
	}
	if d := diffLines(a, a); len(d) != 0 {
		t.Errorf("diff of equal = %v", d)
	}
}

package account

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.Cleanup()
	os.Exit(code)
}

func setup(t *testing.T) (home string, cfg *config.Config) {
	t.Helper()
	env := testutil.IsolatedEnv(t)
	return env["HOME"], config.Default()
}

func TestAddCreatesDirectoryAndPlan(t *testing.T) {
	home, cfg := setup(t)
	dir := filepath.Join(home, ".claude-work")
	plan, err := Add(context.Background(), cfg, "work", dir, Options{
		Marketplaces: []string{"acme/claude-plugins"},
		Plugins:      []string{"sre-kit@acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Created || plan.Persisted || plan.Dir != dir || plan.Name != "work" {
		t.Errorf("plan = %+v", plan)
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("directory not created: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700", fi.Mode().Perm())
	}
	if len(cfg.Accounts) != 0 {
		t.Error("config modified without Persist")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("Add wrote files into the account directory: %v", entries)
	}
	if len(plan.Steps) != 4 || plan.Steps[0].Env[0] != "CLAUDE_CONFIG_DIR="+dir || plan.Steps[1].Slash != "/login" ||
		plan.Steps[2].Slash != "/plugin marketplace add acme/claude-plugins" || plan.Steps[3].Slash != "/plugin install sre-kit@acme" {
		t.Errorf("steps = %+v", plan.Steps)
	}
}

func TestAddPersist(t *testing.T) {
	home, cfg := setup(t)
	path := filepath.Join(home, "cfg", "config.toml")
	dir := filepath.Join(home, ".claude-work")
	plan, err := Add(context.Background(), cfg, "work", dir, Options{Persist: true, ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Persisted || cfg.Accounts["work"].ConfigDir != dir {
		t.Errorf("plan %+v cfg %+v", plan, cfg.Accounts)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Accounts["work"].ConfigDir != dir {
		t.Errorf("saved config = %+v", loaded.Accounts)
	}
	// Adding the same account again is idempotent.
	if _, err = Add(context.Background(), cfg, "work", dir, Options{Persist: true, ConfigPath: path}); err != nil {
		t.Errorf("idempotent add: %v", err)
	}
}

func TestAddPersistDefaultConfigPath(t *testing.T) {
	home, cfg := setup(t)
	if _, err := Add(context.Background(), cfg, "work", filepath.Join(home, "w"), Options{Persist: true}); err != nil {
		t.Fatal(err)
	}
	p, _ := config.Path()
	if _, err := os.Stat(p); err != nil {
		t.Errorf("config not written at default path %s: %v", p, err)
	}
	// IsolatedEnv puts the config directory below the home directory.
	if !strings.HasPrefix(p, home) {
		t.Errorf("config path %s escaped the isolated home %s", p, home)
	}
}

func TestAddPersistFailureRollsBack(t *testing.T) {
	home, cfg := setup(t)
	blocker := filepath.Join(home, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "acct")
	_, err := Add(context.Background(), cfg, "work", dir, Options{Persist: true, ConfigPath: filepath.Join(blocker, "config.toml")})
	if err == nil {
		t.Fatal("expected save failure")
	}
	if _, serr := os.Stat(dir); !errors.Is(serr, os.ErrNotExist) {
		t.Errorf("directory created by the failed add was not removed: %v", serr)
	}
	if len(cfg.Accounts) != 0 {
		t.Error("config modified after failed save")
	}
}

func TestAddExpandsTilde(t *testing.T) {
	home, cfg := setup(t)
	plan, err := Add(context.Background(), cfg, "work", "~/.claude-work", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Dir != filepath.Join(home, ".claude-work") {
		t.Errorf("dir = %q", plan.Dir)
	}
}

func TestAddRejectsBadNames(t *testing.T) {
	home, cfg := setup(t)
	for _, n := range []string{"", "Work", "a b", "../x", "a/b", "-x", "a;b", strings.Repeat("a", 33), "work\n", "~/.claude-x"} {
		dir := filepath.Join(home, "d")
		if _, err := Add(context.Background(), cfg, n, dir, Options{}); err == nil {
			t.Errorf("name %q accepted", n)
		}
	}
	if _, serr := os.Stat(filepath.Join(home, "d")); serr == nil {
		t.Error("directory created for an invalid name")
	}
}

func TestAddRejectsBadDirs(t *testing.T) {
	home, cfg := setup(t)
	def := filepath.Join(home, ".claude")
	if err := os.MkdirAll(filepath.Join(def, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	nonEmpty := filepath.Join(home, "nonempty")
	if err := os.MkdirAll(nonEmpty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonEmpty, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	afile := filepath.Join(home, "afile")
	if err := os.WriteFile(afile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = map[string]config.Account{"other": {ConfigDir: filepath.Join(home, "other")}}
	if err := os.MkdirAll(filepath.Join(home, "other"), 0o700); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		dir  string
	}{
		{"empty", ""},
		{"relative", "relative/dir"},
		{"dot", "."},
		{"default dir", def},
		{"default dir with dots", filepath.Join(def, "..", ".claude")},
		{"inside default dir", filepath.Join(def, "sub", "x")},
		{"tilde default", "~/.claude"},
		{"ancestor of default", home},
		{"non-empty", nonEmpty},
		{"a file", afile},
		{"used by other account", filepath.Join(home, "other")},
		{"control character", filepath.Join(home, "a\nb")},
		{"unset variable", "$CCSHELF_SURELY_UNSET/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Add(context.Background(), cfg, "work", tt.dir, Options{}); err == nil {
				t.Errorf("dir %q accepted", tt.dir)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(def, "sub", "x")); err == nil {
		t.Error("created a directory inside the default Claude directory")
	}
}

func TestAddRejectsSymlinks(t *testing.T) {
	home, cfg := setup(t)
	real := filepath.Join(home, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Add(context.Background(), cfg, "work", link, Options{}); err == nil {
		t.Error("symlinked account directory accepted")
	}
	// A symlink to the default directory must be caught through resolution.
	def := filepath.Join(home, ".claude")
	if err := os.MkdirAll(def, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(def, alias); err != nil {
		t.Skip(err)
	}
	if _, err := Add(context.Background(), cfg, "work", filepath.Join(alias, "x"), Options{}); err == nil {
		t.Error("path through a symlink to ~/.claude accepted")
	}
}

func TestAddExistingDirectories(t *testing.T) {
	home, cfg := setup(t)
	empty := filepath.Join(home, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil { //nolint:gosec // testing permissive existing dir
		t.Fatal(err)
	}
	plan, err := Add(context.Background(), cfg, "work", empty, Options{})
	if err != nil || plan.Created {
		t.Fatalf("empty existing dir: %+v %v", plan, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(empty); fi.Mode().Perm() != 0o700 {
			t.Errorf("empty dir not tightened: %v", fi.Mode().Perm())
		}
	}
	// A non-empty directory that is already this account's directory is fine.
	acct := filepath.Join(home, "acct")
	if err := os.MkdirAll(acct, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acct, ".claude.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Accounts = map[string]config.Account{"mine": {ConfigDir: acct}}
	if _, err = Add(context.Background(), cfg, "mine", acct, Options{}); err != nil {
		t.Errorf("existing account dir: %v", err)
	}
	// Same name, different directory: error.
	if _, err = Add(context.Background(), cfg, "mine", filepath.Join(home, "elsewhere"), Options{}); err == nil {
		t.Error("changing an account's directory accepted")
	}
}

func TestAddArgumentErrors(t *testing.T) {
	home, cfg := setup(t)
	dir := filepath.Join(home, "d")
	ctx := context.Background()
	if _, err := Add(ctx, nil, "work", dir, Options{}); err == nil {
		t.Error("nil config accepted")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Add(cctx, cfg, "work", dir, Options{}); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	for _, o := range []Options{
		{Marketplaces: []string{""}},
		{Marketplaces: []string{"-x"}},
		{Marketplaces: []string{"a b"}},
		{Marketplaces: []string{"a\nb"}},
		{Plugins: []string{"nover"}},
		{Plugins: []string{"a@b; rm -rf /"}},
	} {
		if _, err := Add(ctx, cfg, "work", dir, o); err == nil {
			t.Errorf("options %+v accepted", o)
		}
	}
	if _, serr := os.Stat(dir); serr == nil {
		t.Error("directory created despite invalid options")
	}
	cfg.Trust.OnChange = "allow"
	if _, err := Add(ctx, cfg, "work", dir, Options{}); err == nil {
		t.Error("invalid resulting config accepted")
	}
}

func TestPlanLines(t *testing.T) {
	plan := &Plan{Name: "work", Dir: "/home/me/my dir/it's", Steps: []Step{
		{Description: "Start", Env: []string{"CLAUDE_CONFIG_DIR=/home/me/my dir/it's"}, Argv: []string{"claude"}},
		{Description: "Login", Slash: "/login"},
	}}
	tests := []struct {
		shell string
		want  string
	}{
		{"bash", `CLAUDE_CONFIG_DIR='/home/me/my dir/it'\''s' claude`},
		{"zsh", `CLAUDE_CONFIG_DIR='/home/me/my dir/it'\''s' claude`},
		{"fish", `env CLAUDE_CONFIG_DIR='/home/me/my dir/it\'s' claude`},
		{"pwsh", `$env:CLAUDE_CONFIG_DIR = '/home/me/my dir/it''s'; claude`},
	}
	for _, tt := range tests {
		lines, err := plan.Lines(tt.shell)
		if err != nil {
			t.Fatalf("%s: %v", tt.shell, err)
		}
		if lines[2] != "   "+tt.want {
			t.Errorf("%s: %q, want %q", tt.shell, lines[2], tt.want)
		}
		if lines[4] != "   /login" {
			t.Errorf("slash line: %q", lines[4])
		}
	}
	win := &Plan{Name: "w", Dir: `C:\Users\me\.claude-w`, Steps: []Step{
		{Description: "Start", Env: []string{`CLAUDE_CONFIG_DIR=C:\Users\me\.claude w`}, Argv: []string{"claude"}},
	}}
	lines, err := win.Lines("cmd")
	if err != nil || lines[2] != `   set "CLAUDE_CONFIG_DIR=C:\Users\me\.claude w" && claude` {
		t.Errorf("cmd: %v %v", lines, err)
	}
	if _, err := plan.Lines("tcsh"); err == nil {
		t.Error("unknown shell accepted")
	}
	bad := &Plan{Steps: []Step{{Env: []string{"CLAUDE_CONFIG_DIR=/a\nb"}, Argv: []string{"claude"}}}}
	for _, sh := range []string{"bash", "pwsh", "cmd"} {
		if _, err := bad.Lines(sh); err == nil {
			t.Errorf("%s: control character accepted", sh)
		}
	}
	bad = &Plan{Steps: []Step{{Argv: []string{"a\nb"}}}}
	if _, err := bad.Lines("bash"); err == nil {
		t.Error("control character in argv accepted")
	}
	noEnv := &Plan{Steps: []Step{{Description: "x", Argv: []string{"claude", "--version"}}}}
	if l, err := noEnv.Lines("bash"); err != nil || l[2] != "   claude --version" {
		t.Errorf("no env: %v %v", l, err)
	}
}

func TestListDescribeEnvRemove(t *testing.T) {
	home, _ := setup(t)
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{
		"work":     {ConfigDir: work},
		"personal": {ConfigDir: filepath.Join(home, "missing")},
	}
	cfg.DefaultAccount = "work"

	list := List(cfg)
	if len(list) != 2 || list[0].Name != "personal" || list[1].Name != "work" || !list[1].IsDefault || list[0].IsDefault {
		t.Errorf("List = %+v", list)
	}
	if List(nil) != nil {
		t.Error("List(nil)")
	}

	info, err := Describe(cfg, "work")
	if err != nil || !info.Exists || info.ConfigDir != work || !info.IsDefault {
		t.Errorf("Describe work = %+v %v", info, err)
	}
	if info, _ = Describe(cfg, "personal"); info.Exists {
		t.Error("missing dir reported as existing")
	}
	if _, err = Describe(cfg, "nope"); err == nil || !strings.Contains(err.Error(), "personal, work") {
		t.Errorf("unknown account: %v", err)
	}
	if _, err = Describe(nil, "x"); err == nil || !strings.Contains(err.Error(), "none configured") {
		t.Errorf("nil config: %v", err)
	}

	env, err := Env(cfg, "work")
	if err != nil || len(env) != 1 || env[0] != "CLAUDE_CONFIG_DIR="+work {
		t.Errorf("Env = %v %v", env, err)
	}
	if _, err = Env(cfg, "nope"); err == nil {
		t.Error("Env of unknown account")
	}

	rem, err := Remove(cfg, "work")
	if err != nil || !rem.ClearedDefault || rem.ConfigDir != work {
		t.Fatalf("Remove = %+v %v", rem, err)
	}
	if _, serr := os.Stat(work); serr != nil {
		t.Errorf("Remove deleted the directory: %v", serr)
	}
	if _, ok := cfg.Accounts["work"]; ok || cfg.DefaultAccount != "" {
		t.Errorf("cfg after remove: %+v", cfg)
	}
	if msg := rem.Message(); !strings.Contains(msg, "NOT deleted") || !strings.Contains(msg, "default_account") {
		t.Errorf("message = %q", msg)
	}
	rem, err = Remove(cfg, "personal")
	if err != nil || rem.ClearedDefault || strings.Contains(rem.Message(), "default_account") {
		t.Errorf("Remove personal = %+v %v", rem, err)
	}
	if _, err = Remove(cfg, "personal"); err == nil {
		t.Error("removing twice succeeded")
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("config invalid after remove: %v", err)
	}
}

package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "local"))
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigDirFor(t *testing.T) {
	abs := func(p string) string { return filepath.Join(t.TempDir(), p) }
	x, ap := abs("x"), abs("ap")
	tests := []struct {
		name, goos string
		env        map[string]string
		home       string
		want       string
		wantErr    bool
	}{
		{"xdg", "linux", map[string]string{"XDG_CONFIG_HOME": x}, "/h", filepath.Join(x, "ccshelf"), false},
		{"xdg relative ignored", "linux", map[string]string{"XDG_CONFIG_HOME": "rel"}, "/h", filepath.Join("/h", ".config", "ccshelf"), false},
		{"mac default", "darwin", nil, "/h", filepath.Join("/h", ".config", "ccshelf"), false},
		{"no home", "linux", nil, "", "", true},
		{"windows appdata", "windows", map[string]string{"APPDATA": ap}, "/h", filepath.Join(ap, "ccshelf"), false},
		{"windows fallback", "windows", nil, "/h", filepath.Join("/h", "AppData", "Roaming", "ccshelf"), false},
		{"windows none", "windows", nil, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configDirFor(tt.goos, func(k string) string { return tt.env[k] }, tt.home)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("got %q, %v; want %q (err %v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestPaths(t *testing.T) {
	isolate(t)
	d, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		fn   func() (string, error)
		want string
	}{{Path, "config.toml"}, {LockfilePath, "lock.json"}, {ProjectTrustPath, "project-trust.json"}} {
		want := c.want
		got, err := c.fn()
		if err != nil || got != filepath.Join(d, want) {
			t.Errorf("got %q, %v want %q", got, err, want)
		}
	}
	if def, err := DefaultClaudeDir(); err != nil || filepath.Base(def) != ".claude" {
		t.Errorf("DefaultClaudeDir = %q, %v", def, err)
	}
}

func TestExpandPath(t *testing.T) {
	get := func(k string) string { return map[string]string{"A": "/a", "B_1": "bee"}[k] }
	tests := []struct {
		in, want string
		wantErr  bool
	}{
		{"~", "/h", false},
		{"~/x", "/h/x", false},
		{`~\x`, `/h\x`, false},
		{"~user/x", "~user/x", false},
		{"$A/x", "/a/x", false},
		{"${A}x/${B_1}", "/ax/bee", false},
		{"plain", "plain", false},
		{"$UNSET/x", "", true},
		{"${A", "", true},
		{"$/x", "", true},
		{"${}", "", true},
		{"${A-B}", "", true},
	}
	for _, tt := range tests {
		got, err := expandPath(tt.in, get, "/h")
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("expandPath(%q) = %q, %v; want %q err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
	if _, err := expandPath("~/x", get, ""); err == nil {
		t.Error("expected error without home")
	}
	isolate(t)
	t.Setenv("CCSHELF_T", "v")
	if got, err := ExpandPath("$CCSHELF_T"); err != nil || got != "v" {
		t.Errorf("ExpandPath = %q, %v", got, err)
	}
}

func TestLoadDefaultsAndMissing(t *testing.T) {
	isolate(t)
	cfg, err := Load(filepath.Join(t.TempDir(), "none.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Trust.RequirePin || cfg.Trust.OnChange != "prompt" || cfg.UI.Color != "auto" || cfg.UI.Interactive != "auto" {
		t.Errorf("bad defaults: %+v", cfg)
	}
	cfg, err = Load(write(t, "[trust]\ntrust_project_profiles = true\n"))
	if err != nil || !cfg.Trust.RequirePin || !cfg.Trust.TrustProjectProfiles {
		t.Errorf("partial table lost defaults: %+v %v", cfg, err)
	}
}

func TestLoadValid(t *testing.T) {
	home := isolate(t)
	t.Setenv("WORKDIR", filepath.Join(home, "w"))
	sha := strings.Repeat("a", 40)
	body := `
default_account = "work"
[[sources]]
type = "dir"
path = "~/.config/ccshelf/profiles"
[[sources]]
type = "git"
name = "org"
url = "git@ghe.example.com:acme/data.git"
ref = "v2026.10.1"
path = "profiles"
[[sources]]
type = "git"
url = "https://example.com/a.git"
ref = "` + sha + `"
[[sources]]
type = "plugin"
plugin = "org-profiles@acme"
marketplace = "acme/plugins"
[trust]
on_change = "fail"
[accounts.work]
config_dir = "$WORKDIR/claude"
[accounts.personal]
config_dir = "~/.claude-personal"
[claude]
path = "~/bin/claude"
[ui]
color = "never"
interactive = "never"
`
	cfg, err := Load(write(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Accounts["personal"].ConfigDir != filepath.Join(home, ".claude-personal") && !strings.HasSuffix(cfg.Accounts["personal"].ConfigDir, ".claude-personal") {
		t.Errorf("not expanded: %v", cfg.Accounts)
	}
	if cfg.Sources[3].Marketplace != "acme/plugins" {
		t.Errorf("marketplace not read: %+v", cfg.Sources[3])
	}
	if !filepath.IsAbs(cfg.Accounts["work"].ConfigDir) || len(cfg.Sources) != 4 {
		t.Errorf("unexpected: %+v", cfg)
	}
	if p, err := cfg.Sources[0].ResolvedPath(); err != nil || !filepath.IsAbs(p) {
		t.Errorf("ResolvedPath = %q, %v", p, err)
	}
	if p, _ := cfg.Sources[1].ResolvedPath(); p != "profiles" {
		t.Errorf("git path changed: %q", p)
	}
}

func TestLoadInvalid(t *testing.T) {
	isolate(t)
	sha := strings.Repeat("b", 40)
	tests := []struct{ name, body, want string }{
		{"unknown key", "bogus = 1\n", "unknown key"},
		{"unknown nested", "[trust]\nfoo = 1\n", "line 2"},
		{"syntax", "[trust\n", "line 1"},
		{"allow", "[trust]\non_change = \"allow\"\n", "never auto-accepted"},
		{"bad on_change", "[trust]\non_change = \"x\"\n", "on_change"},
		{"bad color", "[ui]\ncolor = \"red\"\n", "ui.color"},
		{"bad interactive", "[ui]\ninteractive = \"always\"\n", "ui.interactive"},
		{"dir no path", "[[sources]]\ntype = \"dir\"\n", "need path"},
		{"dir extra", "[[sources]]\ntype = \"dir\"\npath = \"x\"\nurl = \"https://h.example/r.git\"\n", "only path"},
		{"bad type", "[[sources]]\ntype = \"ftp\"\n", "sources[0].type"},
		{"bad source name", "[[sources]]\ntype = \"dir\"\npath = \"/x\"\nname = \"Bad Name\"\n", "name"},
		{"git no ref", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\n", "pinned ref"},
		{"git main", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"main\"\n", "looks like a branch"},
		{"git MASTER", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"MASTER\"\n", "looks like a branch"},
		{"git HEAD", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"HEAD\"\n", "looks like a branch"},
		{"git origin/x", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"origin/x\"\n", "branch"},
		{"git dash ref", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"--upload-pack=x\"\n", "not allowed"},
		{"git dotdot ref", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"a..b\"\n", "not a valid git tag name"},
		{"git no url", "[[sources]]\ntype = \"git\"\nref = \"v1\"\n", "need url"},
		{"git dash url", "[[sources]]\ntype = \"git\"\nurl = \"-oProxyCommand=x\"\nref = \"v1\"\n", "must not start"},
		{"git ext url", "[[sources]]\ntype = \"git\"\nurl = \"ext::sh-c-x\"\nref = \"v1\"\n", "transport"},
		{"git file url", "[[sources]]\ntype = \"git\"\nurl = \"file:///x\"\nref = \"v1\"\n", "transport"},
		{"git password", "[[sources]]\ntype = \"git\"\nurl = \"https://u:p@h/x.git\"\nref = \"v1\"\n", "password"},
		{"git abs path", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"" + sha + "\"\npath = \"/etc\"\n", "relative"},
		{"git dotdot path", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"v1\"\npath = \"a/../../b\"\n", ".."},
		{"git backslash path", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"v1\"\npath = \"a\\\\b\"\n", "forward"},
		{"git drive path", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"v1\"\npath = \"C:x\"\n", "relative"},
		{"git plugin field", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"v1\"\nplugin = \"a@b\"\n", "do not take plugin"},
		{"plugin bad", "[[sources]]\ntype = \"plugin\"\nplugin = \"nomarket\"\n", "name@marketplace"},
		{"plugin extra", "[[sources]]\ntype = \"plugin\"\nplugin = \"a@b\"\nurl = \"x\"\n", "only plugin"},
		{"plugin marketplace bad", "[[sources]]\ntype = \"plugin\"\nplugin = \"a@b\"\nmarketplace = \"not a source\"\n", "marketplace"},
		{"plugin marketplace http", "[[sources]]\ntype = \"plugin\"\nplugin = \"a@b\"\nmarketplace = \"http://h/o/r\"\n", "marketplace"},
		{"plugin marketplace token", "[[sources]]\ntype = \"plugin\"\nplugin = \"a@b\"\nmarketplace = \"https://h.example/ghp_abcdef/r\"\n", "credential"},
		{"plugin marketplace userinfo", "[[sources]]\ntype = \"plugin\"\nplugin = \"a@b\"\nmarketplace = \"https://u:p@h.example/o/r\"\n", "user information"},
		{"git marketplace", "[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\nref = \"v1\"\nmarketplace = \"o/r\"\n", "do not take marketplace"},
		{"dir marketplace", "[[sources]]\ntype = \"dir\"\npath = \"/x\"\nmarketplace = \"o/r\"\n", "only path"},
		{"plugin bad path", "[[sources]]\ntype = \"plugin\"\nplugin = \"a@b\"\npath = \"../x\"\n", ".."},
		{"bad account name", "[accounts.Work]\nconfig_dir = \"/x\"\n", "name must match"},
		{"account no dir", "[accounts.w]\n", "required"},
		{"account relative", "[accounts.w]\nconfig_dir = \"rel/x\"\n", "absolute"},
		{"account unset var", "[accounts.w]\nconfig_dir = \"$NOPE_UNSET/x\"\n", "not set"},
		{"default unknown", "default_account = \"x\"\n", "not a configured account"},
		{"claude unset var", "[claude]\npath = \"$NOPE_UNSET\"\n", "claude.path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(write(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v; want containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadAccountDefaultDir(t *testing.T) {
	home := isolate(t)
	_, err := Load(write(t, "[accounts.w]\nconfig_dir = \"~/.claude\"\n"))
	if err == nil || !strings.Contains(err.Error(), "default directory") {
		t.Fatalf("err = %v", err)
	}
	_ = home
}

func TestLoadRelaxedPin(t *testing.T) {
	isolate(t)
	body := "[trust]\nrequire_pin = false\n[[sources]]\ntype = \"git\"\nurl = \"https://h.example/r.git\"\n"
	if _, err := Load(write(t, body)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(write(t, body+"ref = \"-x\"\n")); err == nil {
		t.Error("dash ref accepted")
	}
	if _, err := Load(write(t, body+"ref = \"main\"\n")); err != nil {
		t.Errorf("unpinned allowed when require_pin=false: %v", err)
	}
}

func TestLoadTooLarge(t *testing.T) {
	isolate(t)
	_, err := Load(write(t, "# "+strings.Repeat("x", MaxFileSize)+"\n"))
	if err == nil || !strings.Contains(err.Error(), "larger") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadUnreadable(t *testing.T) {
	isolate(t)
	if _, err := Load(t.TempDir()); err == nil {
		t.Error("loading a directory should fail")
	}
}

func TestSaveRoundTrip(t *testing.T) {
	isolate(t)
	abs := filepath.Join(t.TempDir(), "acct")
	cfg := Default()
	cfg.DefaultAccount = "work"
	cfg.Accounts = map[string]Account{"work": {ConfigDir: abs}}
	cfg.Sources = []SourceConfig{{Type: "dir", Path: "~/p"}, {Type: "git", URL: "https://h.example/r.git", Ref: "v1", Path: "profiles"}}
	cfg.Trust.TrustProjectProfiles = true
	path := filepath.Join(t.TempDir(), "sub", "ccshelf", "config.toml")
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		di, _ := os.Stat(filepath.Dir(path))
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Errorf("modes %v %v", fi.Mode().Perm(), di.Mode().Perm())
		}
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultAccount != "work" || got.Accounts["work"].ConfigDir != abs || len(got.Sources) != 2 || !got.Trust.TrustProjectProfiles || !got.Trust.RequirePin {
		t.Errorf("round trip mismatch: %+v", got)
	}
	// overwrite works and leaves no temp files
	if err := Save(path, got); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 {
		t.Errorf("leftover files: %v", ents)
	}
}

func TestSaveErrors(t *testing.T) {
	isolate(t)
	if err := Save(filepath.Join(t.TempDir(), "c.toml"), nil); err == nil {
		t.Error("nil config accepted")
	}
	bad := Default()
	bad.Trust.OnChange = "allow"
	if err := Save(filepath.Join(t.TempDir(), "c.toml"), bad); err == nil {
		t.Error("invalid config saved")
	}
	// parent is a file
	f := write(t, "")
	if err := Save(filepath.Join(f, "x", "c.toml"), Default()); err == nil {
		t.Error("expected mkdir failure")
	}
	// target is a directory: rename fails
	d := t.TempDir()
	target := filepath.Join(d, "c.toml")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Save(target, Default()); err == nil {
		t.Error("expected rename failure")
	}
}

func TestSaveRefusesSymlink(t *testing.T) {
	isolate(t)
	d := t.TempDir()
	real := filepath.Join(d, "real.toml")
	if err := os.WriteFile(real, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(d, "link.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable: " + err.Error())
	}
	if err := Save(link, Default()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidatePin(t *testing.T) {
	for _, ok := range []string{"v1.2.3", strings.Repeat("0", 40), strings.Repeat("f", 64), "2026-10-01"} {
		if err := ValidatePin(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "main", "Master", "head", "refs/heads/x", "refs/remotes/o/x", "a b", "-x", "a..b", "develop", "trunk"} {
		if err := ValidatePin(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestValidAccountName(t *testing.T) {
	if !ValidAccountName("work-1") || ValidAccountName("-x") || ValidAccountName("") || ValidAccountName(strings.Repeat("a", 33)) {
		t.Error("ValidAccountName wrong")
	}
}

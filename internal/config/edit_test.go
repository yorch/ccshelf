package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestSettingsRoundTrip(t *testing.T) {
	cfg := Default()
	for _, st := range Settings() {
		if _, ok := LookupSetting(st.Key); !ok {
			t.Fatalf("%s not found", st.Key)
		}
		// Every key unsets to the same value as an untouched Default().
		if err := cfg.UnsetSetting(st.Key); err != nil {
			t.Fatalf("unset %s: %v", st.Key, err)
		}
	}
	d := Default()
	if cfg.Trust != d.Trust || cfg.UI != d.UI || cfg.Update.Present() || cfg.Catalog.RemoteURL != "" || cfg.DefaultAccount != "" {
		t.Errorf("unset did not restore defaults: %+v", cfg)
	}
	for key, val := range map[string]string{
		"trust.on_change": "fail", "trust.require_pin": "false", "trust.trust_project_profiles": "true",
		"update.mode": "notify", "update.interval": "2h", "catalog.remote_url": "https://c.example.com/c.json",
		"ui.color": "never", "ui.interactive": "never", "trust.branch_check_interval": "12h",
	} {
		if err := cfg.SetSetting(key, val); err != nil {
			t.Errorf("set %s: %v", key, err)
		}
		if got, set := cfg.GetSetting(key); got != val || !set {
			t.Errorf("get %s = %q, %v", key, got, set)
		}
	}
	if err := cfg.Validate(); err != nil {
		t.Error(err)
	}
	if err := cfg.SetSetting("claude.path", "x"); err == nil {
		t.Error("claude.path must not be settable")
	}
	for _, v := range []string{"30m", "9999h", "daily"} {
		if err := cfg.SetSetting("trust.branch_check_interval", v); err == nil {
			t.Errorf("trust.branch_check_interval %q must be refused", v)
		}
	}
	if err := cfg.SetSetting("update.interval", "9999h"); err == nil {
		t.Error("an interval above the maximum must be refused")
	}
}

func TestWeakening(t *testing.T) {
	base := Default()
	mod := func(f func(c *Config)) *Config {
		c := Default()
		f(c)
		return c
	}
	for name, tc := range map[string]struct {
		after *Config
		want  int
	}{
		"same":            {Default(), 0},
		"require_pin off": {mod(func(c *Config) { c.Trust.RequirePin = false }), 1},
		"project on":      {mod(func(c *Config) { c.Trust.TrustProjectProfiles = true }), 1},
		"install":         {mod(func(c *Config) { c.Update.Mode = UpdateInstall }), 1},
		"notify":          {mod(func(c *Config) { c.Update.Mode = UpdateNotify }), 0},
		"on_change fail":  {mod(func(c *Config) { c.Trust.OnChange = OnChangeFail }), 0},
		"pinned source": {mod(func(c *Config) {
			c.Sources = []SourceConfig{{Type: SourceGit, URL: "https://e.com/a/b.git", Ref: "v1"}}
		}), 0},
		"loose source": {mod(func(c *Config) {
			c.Trust.RequirePin = false
			c.Sources = []SourceConfig{{Type: SourceGit, URL: "https://e.com/a/b.git", Ref: "main"}}
		}), 2},
		"dir source": {mod(func(c *Config) { c.Sources = []SourceConfig{{Type: SourceDir, Path: "/x"}} }), 0},
	} {
		if got := Weakening(base, tc.after); len(got) != tc.want {
			t.Errorf("%s: %v, want %d", name, got, tc.want)
		}
	}
	// An already loose source is not reported again when something else changes.
	loose := Default()
	loose.Trust.RequirePin = false
	loose.Sources = []SourceConfig{{Type: SourceGit, URL: "https://e.com/a/b.git", Ref: "main"}}
	next := *loose
	next.UI.Color = ColorNever
	if got := Weakening(loose, &next); len(got) != 0 {
		t.Errorf("unrelated change: %v", got)
	}
	// Tightening is not a weakening.
	if got := Weakening(loose, Default()); len(got) != 0 {
		t.Errorf("tightening: %v", got)
	}
}

func TestSameSourceAndComment(t *testing.T) {
	a := SourceConfig{Type: SourceGit, URL: "https://e.com/a.git", Ref: "v1", Path: "profiles"}
	if !SameSource(a, SourceConfig{Type: SourceGit, URL: "https://e.com/a.git", Ref: "v2", Path: "profiles"}) {
		t.Error("same URL and folder is the same source")
	}
	if SameSource(a, SourceConfig{Type: SourceGit, URL: "https://e.com/a.git", Path: "other"}) {
		t.Error("another folder of the repository is another source")
	}
	for _, u := range []string{"https://E.com/a", "https://e.com/a/", "https://e.com/a.git", "https://e.com/a.git/"} {
		if !SameSource(a, SourceConfig{Type: SourceGit, URL: u, Path: "profiles"}) {
			t.Errorf("%s should equal %s", u, a.URL)
		}
	}
	if !SameSource(SourceConfig{Type: SourceGit, URL: "git@e.com:o/r.git"}, SourceConfig{Type: SourceGit, URL: "ssh://git@E.com/o/r"}) {
		t.Error("scp-like and ssh:// spellings are one repository")
	}
	if SameSource(SourceConfig{Type: SourceGit, URL: "git@e.com:o/r.git"}, SourceConfig{Type: SourceGit, URL: "https://e.com/o/r"}) {
		t.Error("ssh and https stay different")
	}
	if SameSource(a, SourceConfig{Type: SourceGit, URL: "https://e.com/b.git"}) || SameSource(a, SourceConfig{Type: SourceDir, Path: a.URL}) {
		t.Error("different sources compared equal")
	}
	if !SameSource(SourceConfig{Type: SourcePlugin, Plugin: "a@b"}, SourceConfig{Type: SourcePlugin, Plugin: "a@b", Path: "x"}) {
		t.Error("same plugin")
	}
	for raw, want := range map[string]bool{
		"# c\n":                           true,
		"a = 1 # c\n":                     true,
		"a = \"x # y\"\n":                 false,
		"a = 'x # y'\n":                   false,
		"a = \"x \\\" # y\" # real\n":     true,
		"[ui]\ncolor = \"never\"\n":       false,
		"":                                false,
		"url = \"https://e.com/#frag\"\n": false,
	} {
		if got := HasComment([]byte(raw)); got != want {
			t.Errorf("HasComment(%q) = %v", raw, got)
		}
	}
}

func TestParseReportsLines(t *testing.T) {
	_, err := Parse([]byte("[ui]\ncolor = \"never\"\nbogus = 1\n"), "x.toml")
	if err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Errorf("err = %v", err)
	}
	_, err = Parse([]byte("[ui\n"), "x.toml")
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("err = %v", err)
	}
}

func TestSaveChecked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	orig := []byte("# keep me\n[ui]\ncolor = \"never\"\n")
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(orig, path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.UI.Color = ColorAlways

	// Changed since it was read: refused, nothing written, no backup.
	if _, err := SaveChecked(path, cfg, []byte("different")); !errors.Is(err, ErrChangedWhileEditing) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(orig) {
		t.Error("file written")
	}
	if _, err := os.Stat(BackupPath(path)); err == nil {
		t.Error("backup written")
	}

	bak, err := SaveChecked(path, cfg, orig)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bak); string(b) != string(orig) {
		t.Errorf("backup = %q", b)
	}
	if got, _ := Load(path); got.UI.Color != ColorAlways {
		t.Error("not saved")
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{path, bak} {
			if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
				t.Errorf("%s mode %v", p, fi.Mode().Perm())
			}
		}
	}
	// Raw: written as it is, but only when valid.
	cur, _ := os.ReadFile(path)
	if _, err := SaveRawChecked(path, []byte("bogus = 1\n"), cur); err == nil {
		t.Error("invalid raw text was saved")
	}
	raw := []byte("# hello\n[ui]\ncolor = \"auto\"\n")
	if _, err := SaveRawChecked(path, raw, cur); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(raw) {
		t.Errorf("raw = %q", b)
	}
	// A missing file cannot be "replaced".
	if _, err := SaveChecked(filepath.Join(dir, "none.toml"), cfg, nil); err == nil {
		t.Error("missing file accepted")
	}
}

func TestSaveCheckedRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("[ui]\ncolor = \"never\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	orig := []byte("[ui]\ncolor = \"never\"\n")
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, BackupPath(path)); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Parse(orig, path)
	cfg.UI.Color = ColorAlways
	if _, err := SaveChecked(path, cfg, orig); err == nil {
		t.Fatal("a symlinked backup name was accepted")
	}
	if b, _ := os.ReadFile(path); string(b) != string(orig) {
		t.Error("config written")
	}
	// The config itself being a link.
	os.Remove(BackupPath(path))
	link := filepath.Join(dir, "link.toml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveChecked(link, cfg, orig); err == nil {
		t.Fatal("a symlinked config was accepted")
	}
}

func TestCreateExclusive(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	a, err := CreateExclusive(p, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CreateExclusive(p, []byte("y"))
	if err != nil || a == b {
		t.Fatalf("copies must be distinct: %s %s %v", a, b, err)
	}
	if filepath.Dir(a) != dir || !strings.HasSuffix(a, ".toml") {
		t.Errorf("name = %s", a)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(a); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", fi.Mode().Perm())
		}
	}
}

// dirNames returns the sorted names in dir.
func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	return names
}

func TestCreateNew(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := CreateNew(p, []byte("x = 1\n")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "x = 1\n" {
		t.Fatalf("content = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", fi.Mode().Perm())
		}
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{"config.toml"}) {
		t.Errorf("files left in %s: %v", dir, names)
	}
}

func TestCreateNewCreatesParent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new", "config.toml")
	if err := CreateNew(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Dir(p))
	if err != nil || !fi.IsDir() {
		t.Fatalf("parent not created: %v", err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		t.Errorf("parent mode %v", fi.Mode().Perm())
	}
}

func TestCreateNewExistingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := CreateNew(p, []byte("new"))
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "old" {
		t.Errorf("existing file changed: %q", got)
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{"config.toml"}) {
		t.Errorf("files left in %s: %v", dir, names)
	}
}

func TestCreateNewExistingSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.toml")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, "missing.toml")
	links := map[string]string{
		"config.toml": target,
		"dangling":    dangling,
	}
	for name, dest := range links {
		if err := os.Symlink(dest, filepath.Join(dir, name)); err != nil {
			t.Skipf("symlinks not available: %v", err)
		}
	}
	for name := range links {
		t.Run(name, func(t *testing.T) {
			link := filepath.Join(dir, name)
			err := CreateNew(link, []byte("new"))
			if !errors.Is(err, fs.ErrExist) {
				t.Fatalf("err = %v, want fs.ErrExist", err)
			}
			if got, _ := os.ReadFile(target); string(got) != "keep" {
				t.Errorf("target changed: %q", got)
			}
			if _, err := os.Lstat(dangling); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("dangling target was created: %v", err)
			}
		})
	}
	want := []string{"config.toml", "dangling", "target.toml"}
	if names := dirNames(t, dir); !slices.Equal(names, want) {
		t.Errorf("files in %s = %v, want %v", dir, names, want)
	}
}

// errNoHardLinks stands for a filesystem without hard links.
var errNoHardLinks = errors.New("not supported")

// useNoHardLinks makes linkFile fail as on a filesystem without hard links
// and counts the calls. It restores linkFile when the test ends.
func useNoHardLinks(t *testing.T) *int {
	t.Helper()
	orig := linkFile
	calls := 0
	linkFile = func(oldname, newname string) error {
		calls++
		return &os.LinkError{Op: "link", Old: oldname, New: newname, Err: errNoHardLinks}
	}
	t.Cleanup(func() { linkFile = orig })
	return &calls
}

func TestCreateNewFallbackWithoutHardLinks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	calls := useNoHardLinks(t)
	if err := CreateNew(p, []byte("x = 1\n")); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("linkFile calls = %d, want 1", *calls)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "x = 1\n" {
		t.Fatalf("content = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
			t.Errorf("mode %v", fi.Mode().Perm())
		}
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{"config.toml"}) {
		t.Errorf("files left in %s: %v", dir, names)
	}
}

func TestCreateNewFallbackExistingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := useNoHardLinks(t)
	err := CreateNew(p, []byte("new"))
	if *calls != 1 {
		t.Fatalf("linkFile calls = %d, want 1", *calls)
	}
	if !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v, want fs.ErrExist", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "old" {
		t.Errorf("existing file changed: %q", got)
	}
	if names := dirNames(t, dir); !slices.Equal(names, []string{"config.toml"}) {
		t.Errorf("files left in %s: %v", dir, names)
	}
}

func TestWeakeningTrustWidening(t *testing.T) {
	base := Default()
	base.Sources = []SourceConfig{
		{Type: SourceGit, URL: "https://e.com/a/b.git", Ref: "v1", Path: "profiles"},
		{Type: SourcePlugin, Plugin: "p@m", Marketplace: "acme/m"},
	}
	mod := func(f func(c *Config)) *Config {
		c := *base
		c.Sources = append([]SourceConfig(nil), base.Sources...)
		f(&c)
		return &c
	}
	for name, after := range map[string]*Config{
		"claude.path":    mod(func(c *Config) { c.Claude.Path = "/opt/claude" }),
		"base_url":       mod(func(c *Config) { c.Update.BaseURL = "https://ghe.example.com" }),
		"cosign":         mod(func(c *Config) { c.Update.CosignIdentityRepo = "x/y" }),
		"asset host":     mod(func(c *Config) { c.Update.AssetHosts = []string{"a.example.com"} }),
		"marketplace":    mod(func(c *Config) { c.Sources[1].Marketplace = "evil/m" }),
		"no marketplace": mod(func(c *Config) { c.Sources[1].Marketplace = "" }),
		"git url":        mod(func(c *Config) { c.Sources[0].URL = "https://e.com/evil/b.git" }),
		"dollar dir":     mod(func(c *Config) { c.Sources = append(c.Sources, SourceConfig{Type: SourceDir, Path: "$HOME/p"}) }),
	} {
		if got := Weakening(base, after); len(got) != 1 {
			t.Errorf("%s: %v", name, got)
		}
	}
	for name, after := range map[string]*Config{
		"same URL spelled differently": mod(func(c *Config) { c.Sources[0].URL = "https://e.com/a/b" }),
		"remove first":                 mod(func(c *Config) { c.Sources = c.Sources[1:] }),
		"asset host removed":           mod(func(c *Config) { c.Update.AssetHosts = nil }),
		"tilde dir":                    mod(func(c *Config) { c.Sources = append(c.Sources, SourceConfig{Type: SourceDir, Path: "~/p"}) }),
	} {
		if got := Weakening(base, after); len(got) != 0 {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestDirSourceRefusesWorkingDirectoryVariables(t *testing.T) {
	for _, p := range []string{"$PWD/.ccshelf/profiles", "${PWD}/x", "${PWD:-/x}/y", "$OLDPWD/x", "${OLDPWD}", "$PWD"} {
		c := Default()
		c.Sources = []SourceConfig{{Type: SourceDir, Path: p}}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "SR2") {
			t.Errorf("%s: %v", p, err)
		}
		if _, err := Parse([]byte("[[sources]]\ntype = \"dir\"\npath = \""+p+"\"\n"), "x"); err == nil {
			t.Errorf("Load accepted %s", p)
		}
	}
	// An absolute path in the host's own form: "/abs" is not absolute on Windows.
	for _, p := range []string{"$HOME/profiles", "${HOME}/p", "$PWDX/p", "~/p", t.TempDir()} {
		c := Default()
		c.Sources = []SourceConfig{{Type: SourceDir, Path: p}}
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestParseUnexpandedKeepsWrittenPaths(t *testing.T) {
	t.Setenv("MYBIN", "/opt/a$x")
	raw := []byte("[claude]\npath = \"$MYBIN/claude\"\n[accounts.work]\nconfig_dir = \"~/.claude-work-test\"\n")
	cfg, err := ParseUnexpanded(raw, "x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Claude.Path != "$MYBIN/claude" || cfg.Accounts["work"].ConfigDir != "~/.claude-work-test" {
		t.Errorf("%+v", cfg)
	}
	out, err := EncodeUnexpanded(cfg)
	if err != nil || !strings.Contains(string(out), "$MYBIN/claude") || !strings.Contains(string(out), "~/.claude-work-test") {
		t.Errorf("%s %v", out, err)
	}
	if _, err := Parse(out, "x"); err != nil {
		t.Errorf("the re-encoded file does not load: %v", err)
	}
}

func TestFormatInterval(t *testing.T) {
	for in, want := range map[string]string{"24h": "24h", "90m": "1h30m", "36h": "36h", "30s": "30s"} {
		d, _ := ParseUpdateInterval(in)
		if got := FormatInterval(d); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestWeakeningBranchSource(t *testing.T) {
	git := func(mutate func(s *SourceConfig)) *Config {
		c := Default()
		s := SourceConfig{Type: SourceGit, URL: "https://ghe.example.com/acme/profiles.git", Ref: "v1"}
		mutate(&s)
		c.Sources = []SourceConfig{s}
		return c
	}
	pinned := git(func(*SourceConfig) {})
	branch := git(func(s *SourceConfig) { s.Ref, s.Branch = "", "main" })
	other := git(func(s *SourceConfig) { s.Ref, s.Branch = "", "release" })
	if got := Weakening(Default(), branch); len(got) != 1 || !strings.Contains(got[0], "branch main") {
		t.Errorf("adding a branch source: %v", got)
	}
	if got := Weakening(pinned, branch); len(got) != 1 {
		t.Errorf("switching a tag to a branch: %v", got)
	}
	if got := Weakening(branch, other); len(got) != 1 {
		t.Errorf("switching to another branch: %v", got)
	}
	if got := Weakening(branch, branch); len(got) != 0 {
		t.Errorf("unchanged branch source: %v", got)
	}
	if got := Weakening(branch, pinned); len(got) != 0 {
		t.Errorf("switching back to a tag: %v", got)
	}
	// A branch source is allowed with require_pin on, so it adds no second finding.
	if !branch.Trust.RequirePin {
		t.Fatal("require_pin should be on by default")
	}
}

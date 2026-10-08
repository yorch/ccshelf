package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
		"ui.color": "never", "ui.interactive": "never",
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
	if !SameSource(a, SourceConfig{Type: SourceGit, URL: "https://e.com/a.git", Ref: "v2", Path: "x"}) {
		t.Error("same URL is the same source")
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

package clicore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
)

func TestConfigPath(t *testing.T) {
	c := testEnv().Context(&Globals{ConfigPath: "/x/config.toml"}, "", "")
	if p, err := c.ConfigPath(); err != nil || p != "/x/config.toml" {
		t.Fatalf("--config: %q %v", p, err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	want, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	c = testEnv().Context(&Globals{}, "", "")
	if p, err := c.ConfigPath(); err != nil || p != want {
		t.Fatalf("default: %q %v, want %q", p, err, want)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.toml")
	c := testEnv().Context(&Globals{ConfigPath: missing}, "", "")
	cfg, p, err := c.LoadConfig()
	if err != nil || p != missing || cfg == nil {
		t.Fatalf("missing file: %v %q %v", cfg, p, err)
	}
	bad := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(bad, []byte("not = [valid"), 0o600); err != nil {
		t.Fatal(err)
	}
	c = testEnv().Context(&Globals{ConfigPath: bad}, "", "")
	if _, _, err := c.LoadConfig(); err == nil || !strings.Contains(err.Error(), bad) {
		t.Fatalf("invalid file: %v", err)
	}
}

func TestAccountEnv(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work")
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"work": {ConfigDir: work}}

	for _, tc := range []struct {
		name, flag, profile, def, env string
		want                          string
		notes                         int
		code                          int
	}{
		{name: "none"},
		{name: "default", def: "work", want: work},
		{name: "profile", profile: "work", want: work},
		{name: "flag", flag: "work", want: work},
		{name: "env keeps", def: "work", env: "/env", notes: 1},
		{name: "flag over env", flag: "work", env: "/env", want: work, notes: 1},
		{name: "unknown", flag: "nope", code: ui.CodeOf(ui.Usage(errors.New("x")))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := testEnv()
			e.Getenv = func(k string) string {
				if k == config.EnvConfigDir {
					return tc.env
				}
				return ""
			}
			c := e.Context(&Globals{Account: tc.flag}, "", "")
			cfg.DefaultAccount = tc.def
			choice, extra, err := c.AccountEnv(cfg, tc.profile)
			if tc.code != 0 {
				if err == nil || ui.CodeOf(err) != tc.code {
					t.Fatalf("err = %v (code %d), want code %d", err, ui.CodeOf(err), tc.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := extra[config.EnvConfigDir]; got != tc.want {
				t.Errorf("%s = %q, want %q", config.EnvConfigDir, got, tc.want)
			}
			if len(choice.Notes) != tc.notes {
				t.Errorf("notes = %q, want %d", choice.Notes, tc.notes)
			}
		})
	}
}

func TestPrintAccountNotes(t *testing.T) {
	e := testEnv()
	c := e.Context(&Globals{}, "", "")
	c.PrintAccountNotes(config.AccountChoice{Notes: []string{"first", "bad\x1b[31m"}})
	got := e.Streams.Err.(*bytes.Buffer).String()
	if !strings.Contains(got, "first") || strings.Count(got, "\n") != 2 || strings.Contains(got, "\x1b") {
		t.Fatalf("output = %q", got)
	}
}

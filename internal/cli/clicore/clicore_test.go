package clicore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/ui"
)

func testEnv() *Env {
	return &Env{
		Streams: ui.Streams{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}},
		Getenv:  func(string) string { return "" },
		Environ: func() []string { return nil },
		Getwd:   func() (string, error) { return "/work", nil },
		Now:     func() time.Time { return time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) },
		GOOS:    "linux",
	}
}

func TestContextNonInteractiveWithoutTerminal(t *testing.T) {
	c := testEnv().Context(&Globals{}, "", "")
	if c.Mode.Interactive {
		t.Fatal("no terminal files: mode must not be interactive")
	}
	if _, ok := c.Prompt.(ui.NonInteractive); !ok {
		t.Fatalf("prompter = %T, want ui.NonInteractive", c.Prompt)
	}
}

func TestContextPrefersInjectedPrompter(t *testing.T) {
	e := testEnv()
	e.Prompter = ui.NewScripted()
	c := e.Context(&Globals{}, "", "")
	if c.Prompt != e.Prompter {
		t.Fatal("injected prompter must be used")
	}
}

func TestContextCarriesFlags(t *testing.T) {
	c := testEnv().Context(&Globals{JSON: true, NoColor: true, Plain: true}, "never", "never")
	if !c.Mode.JSON || c.Mode.Color || !c.Mode.Plain {
		t.Fatalf("mode = %+v", c.Mode)
	}
}

func TestNewEnvHasProductionDefaults(t *testing.T) {
	e := NewEnv()
	if e.GOOS == "" || e.Getenv == nil || e.Now == nil || e.Getwd == nil {
		t.Fatalf("incomplete env: %+v", e)
	}
}

func TestUIPrefs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("good.toml", "[ui]\ncolor = \"never\"\ninteractive = \"never\"\n")
	if c, i := UIPrefs(&Globals{ConfigPath: good}); c != "never" || i != "never" {
		t.Errorf("good: %q %q", c, i)
	}
	if c, i := UIPrefs(&Globals{ConfigPath: filepath.Join(dir, "absent.toml")}); c != "auto" || i != "auto" {
		t.Errorf("absent file gives the defaults: %q %q", c, i)
	}
	bad := write("bad.toml", "bogus = 1\n")
	if c, i := UIPrefs(&Globals{ConfigPath: bad}); c != "" || i != "" {
		t.Errorf("invalid file must give empty prefs: %q %q", c, i)
	}
}

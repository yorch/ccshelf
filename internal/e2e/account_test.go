package e2e

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountLifecycle(t *testing.T) {
	s := newSandbox(t)
	s.mustRun("--no-interactive", "init")
	if r := s.run("account", "ls"); r.Code != 0 {
		t.Fatalf("account ls on an empty config: exit %d\n%s", r.Code, r.Stderr)
	}

	r := s.mustRun("account", "add", "work")
	contains(t, "account add", r.Stdout+r.Stderr, ".claude-work", "/login", "never")
	dir := filepath.Join(s.Home, ".claude-work")
	custom := filepath.Join(s.root, "elsewhere")
	s.mustRun("account", "add", "personal", "--dir", custom)

	r = s.mustRun("account", "ls", "--json")
	var env struct {
		Version int `json:"version"`
		Data    []struct {
			Name      string `json:"name"`
			ConfigDir string `json:"config_dir"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
		t.Fatalf("%v\n%s", err, r.Stdout)
	}
	got := map[string]string{}
	for _, a := range env.Data {
		got[a.Name] = a.ConfigDir
	}
	if got["work"] != dir || got["personal"] != custom {
		t.Errorf("accounts = %v, want work=%s personal=%s", got, dir, custom)
	}

	if r := s.run("account", "add", "Bad Name"); r.Code != 2 {
		t.Errorf("bad name: exit %d, want 2\n%s", r.Code, r.Stderr)
	}
	if r := s.run("--no-interactive", "account", "add"); r.Code != 2 {
		t.Errorf("missing name: exit %d, want 2\n%s", r.Code, r.Stderr)
	}

	r = s.mustRun("account", "rm", "work")
	contains(t, "account rm", r.Stdout+r.Stderr, "NOT deleted")
	if r := s.run("account", "rm", "work"); r.Code != 2 {
		t.Errorf("rm of an unknown account: exit %d, want 2", r.Code)
	}
}

func TestAccountFlagAndEnv(t *testing.T) {
	s := newSandbox(t)
	s.writeProfile("mine", personalMine)
	s.mustRun("--no-interactive", "init")
	s.mustRun("account", "add", "work")
	work := filepath.Join(s.Home, ".claude-work")

	// The flag selects the directory.
	s.mustRun("--account", "work", "run", "mine")
	if got := s.launches()[0].Env["CLAUDE_CONFIG_DIR"]; got != work {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want %q", got, work)
	}

	// With CLAUDE_CONFIG_DIR already set the flag still wins and says so.
	s.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(s.root, "from-env"))
	r := s.mustRun("--account", "work", "run", "mine")
	contains(t, "stderr", r.Stderr, "--account work overrides CLAUDE_CONFIG_DIR")
	if got := s.launches()[1].Env["CLAUDE_CONFIG_DIR"]; got != work {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, want the flag's %q", got, work)
	}

	// Without the flag the environment is left alone.
	s.mustRun("run", "mine")
	if got := s.launches()[2].Env["CLAUDE_CONFIG_DIR"]; got != filepath.Join(s.root, "from-env") {
		t.Errorf("CLAUDE_CONFIG_DIR = %q, the caller's value must pass through", got)
	}

	// An unknown account is a usage error and starts nothing.
	n := len(s.launches())
	r = s.run("--account", "nope", "run", "mine")
	if r.Code != 2 || !strings.Contains(r.Stderr, "nope") {
		t.Errorf("unknown account: exit %d\n%s", r.Code, r.Stderr)
	}
	if len(s.launches()) != n {
		t.Error("claude started for an unknown account")
	}
}

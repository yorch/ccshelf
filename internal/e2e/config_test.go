package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCommandLifecycle(t *testing.T) {
	s := newSandbox(t)
	cfgFile := filepath.Join(s.ConfigDir(), "config.toml")

	// A missing file: read-only commands work, writing ones point at init.
	if r := s.run("config", "set", "ui.color", "never"); r.Code != 1 || !strings.Contains(r.Stderr, "ccshelf init") {
		t.Fatalf("set without a file: exit %d\n%s", r.Code, r.Stderr)
	}
	contains(t, "config show", s.mustRun("config", "show").Stdout, "does not exist")

	s.mustRun("--no-interactive", "init", "--git-url", "https://example.com/acme/data.git", "--ref", "v1.0.0")
	s.mustRun("config", "source", "add", "--dir", filepath.Join(s.root, "team"))
	r := s.mustRun("config", "source", "ls")
	contains(t, "source ls", r.Stdout, "example.com/acme/data.git", "v1.0.0", "team")
	s.mustRun("config", "source", "pin", "1", "--ref", "v1.1.0")
	s.mustRun("config", "set", "update.mode", "notify")
	s.mustRun("config", "set", "update.interval", "48h")
	s.mustRun("config", "unset", "update.interval")

	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Sources []struct {
				Number int    `json:"number"`
				Type   string `json:"type"`
				Ref    string `json:"ref"`
			} `json:"sources"`
			Update struct {
				Mode string `json:"mode"`
			} `json:"update"`
		} `json:"data"`
	}
	r = s.mustRun("config", "show", "--json")
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
		t.Fatalf("%v\n%s", err, r.Stdout)
	}
	if env.Kind != "config" || len(env.Data.Sources) != 2 || env.Data.Sources[0].Ref != "v1.1.0" || env.Data.Update.Mode != "notify" {
		t.Errorf("show: %+v", env)
	}

	// The weakening refusal leaves the file alone.
	before, _ := os.ReadFile(cfgFile)
	if r := s.run("config", "set", "trust.require_pin", "false"); r.Code != 2 || !strings.Contains(r.Stderr, "--yes") {
		t.Fatalf("weakening without --yes: exit %d\n%s", r.Code, r.Stderr)
	}
	if after, _ := os.ReadFile(cfgFile); string(after) != string(before) {
		t.Error("the refused change wrote the file")
	}
	if r := s.run("config", "set", "claude.path", "/x"); r.Code != 2 {
		t.Errorf("disallowed key: exit %d", r.Code)
	}
	if r := s.run("config", "bogus"); r.Code != 2 {
		t.Errorf("unknown subcommand: exit %d", r.Code)
	}
	// Without a terminal, config alone prints help and exits 2.
	if r := s.run("config"); r.Code != 2 || !strings.Contains(r.Stderr, "Available Commands") {
		t.Errorf("config alone: exit %d\n%s", r.Code, r.Stderr)
	}

	s.mustRun("config", "source", "rm", "2")
	if r := s.run("config", "source", "rm", "2"); r.Code != 2 {
		t.Errorf("rm of a missing number: exit %d", r.Code)
	}
	if _, err := os.Stat(cfgFile + ".bak"); err != nil {
		t.Errorf("no backup: %v", err)
	}
	if r := s.run("config", "edit"); r.Code != 2 {
		t.Errorf("edit without a terminal: exit %d\n%s", r.Code, r.Stderr)
	}
	contains(t, "edit --path", s.mustRun("config", "edit", "--path").Stdout, cfgFile)
}

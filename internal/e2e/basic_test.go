package e2e

import (
	"strings"
	"testing"
)

func TestVersionHelpUnknown(t *testing.T) {
	s := newSandbox(t)

	r := s.mustRun("version")
	contains(t, "version stdout", r.Stdout, "ccshelf ")

	r = s.mustRun("--help")
	contains(t, "--help", r.Stdout, "Usage:", "run ", "lint", "catalog", "trust", "Available Commands")

	for _, cmd := range []string{"run", "trust", "doctor", "catalog", "account"} {
		r = s.mustRun(cmd, "--help")
		contains(t, cmd+" --help", r.Stdout, "Usage:")
	}

	for _, args := range [][]string{
		{"frobnicate"},
		{"--no-such-flag"},
		{"ls", "--no-such-flag"},
		{"lint", "--format"},
	} {
		r = s.run(args...)
		if r.Code != 2 {
			t.Errorf("ccshelf %v: exit %d, want 2\nstderr: %s", args, r.Code, r.Stderr)
		}
		if r.Stdout != "" {
			t.Errorf("ccshelf %v: usage errors must not write stdout: %q", args, r.Stdout)
		}
		if strings.TrimSpace(r.Stderr) == "" {
			t.Errorf("ccshelf %v: no error message", args)
		}
	}
	if len(s.anyStart()) != 0 {
		t.Error("a usage error must not start claude")
	}
}

func TestCompletion(t *testing.T) {
	s := newSandbox(t)
	for shell, marker := range map[string]string{
		"bash":       "complete",
		"zsh":        "#compdef",
		"fish":       "complete -c ccshelf",
		"powershell": "Register-ArgumentCompleter",
	} {
		r := s.mustRun("completion", shell)
		if !strings.Contains(r.Stdout, marker) || !strings.Contains(r.Stdout, "ccshelf") {
			t.Errorf("completion %s lacks %q:\n%.300s", shell, marker, r.Stdout)
		}
	}
	if r := s.run("completion", "tcsh"); r.Code != 2 {
		t.Errorf("completion tcsh: exit %d, want 2 (%s)", r.Code, r.Stderr)
	}
}

func TestShellInit(t *testing.T) {
	s := newSandbox(t)
	s.writeProfile("mine", "name = \"mine\"\n[plugins]\ninclude = [\"design-kit@acme\"]\n")
	r := s.mustRun("shell-init", "bash")
	contains(t, "shell-init bash", r.Stdout, "cs-mine", "ccshelf run")
	r = s.mustRun("shell-init", "pwsh")
	contains(t, "shell-init pwsh", r.Stdout, "cs-mine")
}

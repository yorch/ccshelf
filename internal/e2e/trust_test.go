package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// trustedOrg returns a sandbox with the example org as a dir source.
func trustedSetup(t *testing.T) (*sandbox, string) {
	t.Helper()
	s := newSandbox(t)
	org := exampleOrg(t)
	s.addDirSource(filepath.Join(org, "profiles"))
	return s, org
}

func TestTrustRequiredBeforeRun(t *testing.T) {
	s, _ := trustedSetup(t)

	for _, args := range [][]string{
		{"run", "frontend"},
		{"run", "--yes", "frontend"},
		{"--no-interactive", "run", "--yes", "frontend"},
		{"dry-run", "frontend"},
	} {
		r := s.run(args...)
		if r.Code != 4 {
			t.Errorf("ccshelf %v: exit %d, want 4\nstderr: %s", args, r.Code, r.Stderr)
		}
		contains(t, "stderr of "+strings.Join(args, " "), r.Stderr, "needs trust", "ccshelf trust frontend")
	}
	if n := len(s.anyStart()); n != 0 {
		t.Errorf("claude started %d times before trust", n)
	}
	// --yes does not exist on trust: trust is never answered by default.
	if r := s.run("trust", "frontend", "--yes"); r.Code != 2 {
		t.Errorf("trust --yes: exit %d, want 2 (unknown flag)", r.Code)
	}
}

func TestTrustNonInteractiveNeedsAccept(t *testing.T) {
	s, _ := trustedSetup(t)
	h := s.closureHash("frontend")

	r := s.run("--no-interactive", "trust", "frontend")
	if r.Code != 2 {
		t.Fatalf("exit %d, want 2\n%s", r.Code, r.Stderr)
	}
	contains(t, "stderr", r.Stderr, "--accept")
	// What is shown for review: the closure and what it runs.
	contains(t, "review", r.Stdout+r.Stderr, h, "design-kit@acme", "figma")
	// The same with stdin closed and CI set: never a prompt, never a default yes.
	s.Setenv("CI", "true")
	if r = s.runIn(s.Work, "y\n", "trust", "frontend"); r.Code != 2 {
		t.Errorf("trust with piped 'y' on stdin: exit %d, want 2 (non-interactive)", r.Code)
	}
	if r = s.run("run", "frontend"); r.Code != 4 {
		t.Errorf("run after refused trust: exit %d, want 4", r.Code)
	}
	// A wrong hash accepts nothing.
	r = s.run("trust", "frontend", "--accept", strings.Repeat("0", 64))
	if r.Code != 4 {
		t.Errorf("trust --accept <wrong hash>: exit %d, want 4\n%s", r.Code, r.Stderr)
	}
	if r = s.run("run", "frontend"); r.Code != 4 {
		t.Errorf("run after a wrong hash: exit %d, want 4", r.Code)
	}
}

func TestTrustAcceptRunChangeRevoke(t *testing.T) {
	s, org := trustedSetup(t)
	h := s.closureHash("frontend")

	s.mustRun("trust", "frontend", "--accept", h)
	if r := s.mustRun("show", "frontend"); !strings.Contains(r.Stdout, "Trust: trusted") && !strings.Contains(r.Stdout, "Trust: ok") {
		t.Errorf("show does not report the trust state:\n%s", r.Stdout)
	}
	r := s.mustRun("run", "frontend")
	contains(t, "stdout", r.Stdout, "fake claude: ok")
	if n := len(s.launches()); n != 1 {
		t.Fatalf("launches = %d, want 1", n)
	}
	// A trusted profile does not authorize the others of the same source.
	if r := s.run("run", "sre"); r.Code != 4 {
		t.Errorf("run sre: exit %d, want 4 (not reviewed)", r.Code)
	}

	// The lock file is private.
	if runtime.GOOS != "windows" {
		lock := filepath.Join(s.ConfigDir(), "lock.json")
		fi, err := os.Stat(lock)
		if err != nil {
			t.Fatalf("no lock file: %v", err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("lock.json mode %v, want 0600", fi.Mode().Perm())
		}
	}

	// Change one byte of the profile: trust is void again and the diff shows.
	path := filepath.Join(org, "profiles", "frontend.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), `effort = "high"`, `effort = "max"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	before := len(s.launches())
	r = s.run("run", "frontend")
	if r.Code != 4 {
		t.Fatalf("run after a change: exit %d, want 4\n%s", r.Code, r.Stderr)
	}
	if len(s.launches()) != before {
		t.Error("claude started although the profile changed")
	}
	r = s.run("--no-interactive", "trust", "frontend")
	if r.Code != 2 {
		t.Errorf("trust after a change: exit %d, want 2", r.Code)
	}
	out := r.Stdout + r.Stderr
	contains(t, "trust diff", out, "frontend", "changed")
	h2 := s.closureHash("frontend")
	if h2 == h {
		t.Fatal("the closure hash did not change with the profile")
	}
	// The old hash does not accept the new content.
	if r := s.run("trust", "frontend", "--accept", h); r.Code != 4 {
		t.Errorf("accepting the old hash: exit %d, want 4", r.Code)
	}
	s.mustRun("trust", "frontend", "--accept", h2)
	s.mustRun("run", "frontend")

	// Revoke.
	s.mustRun("trust", "frontend", "--revoke")
	if r := s.run("run", "frontend"); r.Code != 4 {
		t.Errorf("run after revoke: exit %d, want 4", r.Code)
	}
}

func TestTrustCoversChangedParent(t *testing.T) {
	s, org := trustedSetup(t)
	s.mustRun("trust", "frontend", "--accept", s.closureHash("frontend"))
	s.mustRun("run", "frontend")
	// base is a parent of frontend: editing it changes frontend's closure.
	path := filepath.Join(org, "profiles", "base.toml")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), `effort = "medium"`, `effort = "low"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := s.run("run", "frontend"); r.Code != 4 {
		t.Errorf("run after the parent changed: exit %d, want 4\n%s", r.Code, r.Stderr)
	}
}

func TestUnreadableOrInvalidConfigFails(t *testing.T) {
	s := newSandbox(t)
	write(t, filepath.Join(s.ConfigDir(), "config.toml"), "[trust]\non_change = \"allow\"\n")
	r := s.run("ls")
	if r.Code != 1 && r.Code != 2 {
		t.Errorf("exit %d, want 1 or 2 (on_change = allow is not accepted)\n%s", r.Code, r.Stderr)
	}
	write(t, filepath.Join(s.ConfigDir(), "config.toml"), "bogus_key = 1\n")
	if r := s.run("ls"); r.Code == 0 {
		t.Error("an unknown config key must be an error")
	}
}

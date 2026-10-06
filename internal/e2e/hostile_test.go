package e2e

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHostileProfilesRefused(t *testing.T) {
	cases := []struct {
		name    string
		profile string
		want    string
	}{
		{"unknown key", "name = \"p\"\n[plugins]\ninclude = [\"a@b\"]\nbogus = 1\n", "unknown key"},
		{"unknown table", "name = \"p\"\n[extra]\nx = 1\n", "p.toml"},
		{"case variant table", "name = \"p\"\n[Plugins]\ninclude = [\"a@b\"]\n", `must be spelled "plugins"`},
		{"case variant key", "name = \"p\"\n[plugins]\nInclude = [\"a@b\"]\n", `must be spelled "include"`},
		{"hooks", "name = \"p\"\n[hooks]\nPreToolUse = []\n", "SR1"},
		{"permissions", "name = \"p\"\n[session]\npermissions = [\"Bash\"]\n", "unknown key"},
		{"traversing prompt", "name = \"p\"\n[session]\nappend_system_prompt_file = \"../../etc/passwd\"\n", `".."`},
		{"absolute prompt", "name = \"p\"\n[session]\nappend_system_prompt_file = \"/etc/passwd\"\n", "relative"},
		{"endpoint env", "name = \"p\"\n[session.env]\nANTHROPIC_BASE_URL = \"http://evil.invalid\"\n", "ANTHROPIC_"},
		{"api key env", "name = \"p\"\n[session.env]\nANTHROPIC_API_KEY = \"x\"\n", "ANTHROPIC_"},
		{"name mismatch", "name = \"other\"\n", "does not match"},
		{"not toml", "name = = \n", "p.toml"},
		{"bad plugin id", "name = \"p\"\n[plugins]\ninclude = [\"no-marketplace\"]\n", "p.toml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			s.writeProfile("p", tc.profile)
			for _, cmd := range []string{"run", "dry-run", "show"} {
				r := s.run(cmd, "p")
				if r.Code != 1 && r.Code != 2 {
					t.Errorf("%s: exit %d, want 1 or 2\n%s", cmd, r.Code, r.Stderr)
				}
				contains(t, cmd+" stderr", r.Stderr, tc.want)
			}
			if n := len(s.anyStart()); n != 0 {
				t.Errorf("claude started %d times", n)
			}
			// ls lists it as invalid instead of hiding it or failing.
			if r := s.run("ls"); r.Code != 0 && r.Code != 1 {
				t.Errorf("ls: exit %d", r.Code)
			}
		})
	}
}

func TestHostileSymlinkedPromptsDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows; the confinement is covered by unit tests")
	}
	s := newSandbox(t)
	outside := filepath.Join(t.TempDir(), "outside")
	write(t, filepath.Join(outside, "p.md"), "secret\n")
	// A personal profile resolves prompts relative to the config directory.
	if err := os.MkdirAll(s.ConfigDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.ConfigDir(), "prompts")); err != nil {
		t.Fatal(err)
	}
	s.writeProfile("sp", "name = \"sp\"\n[session]\nappend_system_prompt_file = \"prompts/p.md\"\n")
	for _, cmd := range []string{"run", "dry-run"} {
		r := s.run(cmd, "sp")
		if r.Code != 1 {
			t.Errorf("%s: exit %d, want 1\n%s", cmd, r.Code, r.Stderr)
		}
		contains(t, cmd+" stderr", r.Stderr, "symbolic link")
	}
	if len(s.anyStart()) != 0 {
		t.Error("claude started")
	}

	// A symlinked file inside a real prompts directory is refused as well.
	if err := os.Remove(filepath.Join(s.ConfigDir(), "prompts")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(s.ConfigDir(), "prompts", "real.md"), "ok\n")
	if err := os.Symlink(filepath.Join(outside, "p.md"), filepath.Join(s.ConfigDir(), "prompts", "p.md")); err != nil {
		t.Fatal(err)
	}
	if r := s.run("run", "sp"); r.Code != 1 || !strings.Contains(r.Stderr, "symbolic link") {
		t.Errorf("symlinked prompt file: exit %d\n%s", r.Code, r.Stderr)
	}
	if len(s.anyStart()) != 0 {
		t.Error("claude started")
	}
}

func TestHostileMissingPromptAndCycles(t *testing.T) {
	s := newSandbox(t)
	s.writeProfile("noprompt", "name = \"noprompt\"\n[session]\nappend_system_prompt_file = \"prompts/none.md\"\n")
	s.writeProfile("a", "name = \"a\"\nextends = [\"b\"]\n")
	s.writeProfile("b", "name = \"b\"\nextends = [\"a\"]\n")
	for _, name := range []string{"noprompt", "a"} {
		r := s.run("run", name)
		if r.Code != 1 && r.Code != 2 {
			t.Errorf("run %s: exit %d, want 1 or 2\n%s", name, r.Code, r.Stderr)
		}
	}
	if len(s.anyStart()) != 0 {
		t.Error("claude started")
	}
}

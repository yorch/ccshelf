package profile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	ents, _ := os.ReadDir("testdata/valid")
	if len(ents) == 0 {
		t.Fatal("no fixtures")
	}
	for _, e := range ents {
		t.Run(e.Name(), func(t *testing.T) {
			m, err := Parse(readFixture(t, "valid", e.Name()), e.Name())
			if err != nil {
				t.Fatal(err)
			}
			if m.Name+".toml" != e.Name() {
				t.Errorf("name %q", m.Name)
			}
		})
	}
	m, _ := Parse(readFixture(t, "valid", "full.toml"), "full.toml")
	if m.Plugins.Mode != "additive" || m.Session.Env["FIGMA_TOKEN_REF"] == "" || m.MCP.Strict == nil || !*m.MCP.Strict ||
		m.Session.InheritUserSettings == nil || *m.Session.InheritUserSettings || m.Policy.OnBlocked != "fail" || m.Account != "work" {
		t.Errorf("fields not decoded: %+v", m)
	}
}

func TestWithDefaults(t *testing.T) {
	m, _ := Parse([]byte(`name = "a"`), "a.toml")
	d := m.WithDefaults()
	if d.Status != "active" || d.Plugins.Mode != "allow-only" || d.Policy.OnBlocked != "warn" || !d.InheritsUserSettings() {
		t.Errorf("defaults: %+v", d)
	}
	if m.Status != "" || m.Session.InheritUserSettings != nil {
		t.Error("WithDefaults mutated the receiver")
	}
}

var invalidWant = map[string]string{
	"top-permissions":      "line 2: permissions: not allowed in a profile: SR1",
	"top-hooks":            "not allowed in a profile: SR1",
	"top-apikeyhelper":     "not allowed in a profile: SR1",
	"top-allowedmcp":       "not allowed in a profile: SR1",
	"top-deniedmcp":        "not allowed in a profile: SR1",
	"top-disablehooks":     "not allowed in a profile: SR1",
	"top-statusline":       "not allowed in a profile: SR1",
	"top-env":              "not allowed in a profile: SR1",
	"mcp-command":          "SR1",
	"mcp-definition":       "MCP definitions are not allowed in a profile: SR1",
	"unknown-key":          "colour",
	"unknown-nested":       "plugins.mod",
	"syntax":               "line",
	"no-name":              "name: required",
	"bad-name":             "name:",
	"deprecated-no-super":  "superseded_by: required when status is deprecated",
	"super-not-deprecated": "only allowed when status is deprecated",
	"bad-status":           "status:",
	"account-path":         "never a path",
	"account-tilde":        "never a path",
	"bad-plugin-id":        "plugins.include[0]",
	"dup-plugin":           "duplicate entry",
	"include-exclude":      "in both plugins.include and plugins.exclude",
	"bad-mode":             "plugins.mode",
	"skill-conflict":       "in both skills.off and skills.name_only",
	"bad-skill":            "skills.off[0]",
	"bad-effort":           "session.effort",
	"bad-model":            "session.model",
	"prompt-abs":           "relative",
	"prompt-dotdot":        "..",
	"prompt-backslash":     "forward slashes",
	"prompt-drive":         "relative",
	"env-denied":           "not allowed in a profile",
	"env-anthropic":        "not allowed in a profile",
	"env-newline-value":    "single line",
	"bad-connectors":       "mcp.claudeai_connectors",
	"bad-server-name":      "mcp.servers[0]",
	"bad-onblocked":        "policy.on_blocked",
	"control-chars":        "control characters",
	"empty-hint":           "when_to_use[0]",
	"extends-bad":          "extends[0]",
	"wrong-type":           "name",
}

func TestParseInvalidFixtures(t *testing.T) {
	ents, _ := os.ReadDir("testdata/invalid")
	if len(ents) != len(invalidWant) {
		t.Fatalf("%d fixtures but %d expectations", len(ents), len(invalidWant))
	}
	for _, e := range ents {
		base := strings.TrimSuffix(e.Name(), ".toml")
		t.Run(base, func(t *testing.T) {
			_, err := Parse(readFixture(t, "invalid", e.Name()), "")
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want *ValidationError, got %T %v", err, err)
			}
			if !strings.Contains(err.Error(), invalidWant[base]) {
				t.Errorf("error %q does not contain %q", err.Error(), invalidWant[base])
			}
		})
	}
}

func TestParseLineNumbers(t *testing.T) {
	_, err := Parse([]byte("name = \"x\"\n\n[session.env]\nFOO = \"1\"\n"), "")
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 1 || ve.Problems[0].Line != 4 {
		t.Fatalf("got %v", err)
	}
	_, err = Parse([]byte("name = \"x\"\nbad = 1\n"), "")
	if !errors.As(err, &ve) || ve.Problems[0].Line != 2 {
		t.Fatalf("got %v", err)
	}
}

func TestParseMultipleProblems(t *testing.T) {
	_, err := Parse([]byte("name = \"x\"\nstatus = \"zzz\"\naccount = \"/p\"\n"), "x.toml")
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Problems) != 2 {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(err.Error(), "2 problems") || !strings.Contains(err.Error(), "invalid x.toml") {
		t.Errorf("message: %s", err)
	}
}

func TestParseFileNameMismatch(t *testing.T) {
	_, err := Parse([]byte(`name = "a"`), "b.toml")
	mustErrContain(t, err, "does not match the file name")
	if _, err := Parse([]byte(`name = "a"`), ""); err != nil {
		t.Errorf("empty filename should skip the check: %v", err)
	}
}

func TestParseTooLarge(t *testing.T) {
	_, err := Parse([]byte("name = \"a\"\n# "+strings.Repeat("x", MaxManifestSize)), "a.toml")
	mustErrContain(t, err, "larger than")
}

func TestParseDeprecatedSelfAndBadSuper(t *testing.T) {
	_, err := Parse([]byte("name = \"a\"\nstatus = \"deprecated\"\nsuperseded_by = \"a\"\n"), "")
	mustErrContain(t, err, "itself")
	_, err = Parse([]byte("name = \"a\"\nstatus = \"deprecated\"\nsuperseded_by = \"B B\"\n"), "")
	mustErrContain(t, err, "superseded_by")
}

func TestCheckRelPath(t *testing.T) {
	for _, ok := range []string{"a.md", "prompts/a.md", "a/b/c.md"} {
		if err := CheckRelPath(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "/a", "a/../b", "..", "a//b", "a\\b", "C:/a", "a\x00b", "a/"} {
		if err := CheckRelPath(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLineOf(t *testing.T) {
	raw := []byte("name = \"x\"\n# comment = 1\n[plugins]\ninclude = [\"a\"]\n[session.env]\n\"FOO\" = \"1\"\n")
	for field, want := range map[string]int{"name": 1, "plugins.include[0]": 4, "session.env.FOO": 6, "plugins": 3, "nope": 0} {
		if got := lineOf(raw, field); got != want {
			t.Errorf("lineOf(%q) = %d want %d", field, got, want)
		}
	}
}

func TestValidName(t *testing.T) {
	if !ValidName("a-b1") || ValidName("A") || ValidName("-a") || ValidName("") || ValidName(strings.Repeat("a", 64)) {
		t.Error("ValidName wrong")
	}
	_ = filepath.Separator
}

package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/testutil"
)

const instrProfile = personalMine + `
[instructions]
files = ["prompts/a.md", "prompts/b.md"]
`

func (h *harness) writePrompt(name, content string) {
	h.t.Helper()
	testutil.WriteFile(h.t, filepath.Join(h.configDir(), "prompts", name), content)
}

func envValue(env []string, name string) (string, bool) {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

func TestRunInstructions(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", instrProfile)
	h.writePrompt("a.md", "Alpha\r\n")
	h.writePrompt("b.md", "Beta\n")
	h.mustRun("run", "mine", "--resume")

	dir := argAfter(h.startArgs, "--add-dir")
	if dir == "" {
		t.Fatalf("no --add-dir in %v", h.startArgs)
	}
	b, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || string(b) != profile.InstructionsHeader("mine")+"Alpha\n\nBeta\n" {
		t.Fatalf("CLAUDE.md = %q, %v", b, err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("directory holds %d entries", len(es))
	}
	if !strings.HasPrefix(filepath.Base(dir), "instructions-") {
		t.Errorf("directory name %s", dir)
	}
	if v, ok := envValue(h.startEnv, "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"); !ok || v != "1" {
		t.Errorf("env var missing: %v", h.startEnv)
	}
	if got := h.startArgs[len(h.startArgs)-1]; got != "--resume" {
		t.Errorf("passthrough not last: %v", h.startArgs)
	}
	if hasArg(h.startArgs, "--append-system-prompt-file") {
		t.Errorf("instructions must not use the system prompt channel: %v", h.startArgs)
	}
	if strings.Contains(h.errb.String(), "--add-dir") {
		t.Errorf("unexpected warning: %s", h.errb)
	}
}

func TestRunWithoutInstructionsHasNoAddDir(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.mustRun("run", "mine")
	if hasArg(h.startArgs, "--add-dir") {
		t.Errorf("args %v", h.startArgs)
	}
	if _, ok := envValue(h.startEnv, "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"); ok {
		t.Error("env var must not be set")
	}
	// Without instructions a user --add-dir is not a concern of the profile.
	h.mustRun("run", "mine", "--add-dir", "/x")
	if strings.Contains(h.errb.String(), "CLAUDE.md files") {
		t.Errorf("unexpected warning: %s", h.errb)
	}
}

func TestRunInstructionsWithAccountKeepsAccountEnv(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", instrProfile)
	h.writePrompt("a.md", "A\n")
	h.writePrompt("b.md", "B\n")
	acct := t.TempDir() + "/acct-work"
	h.writeConfig("[accounts.work]\nconfig_dir = " + tomlString(acct) + "\n")
	h.mustRun("run", "--account", "work", "mine")
	if !hasEnv(h.startEnv, "CLAUDE_CONFIG_DIR="+acct) || !hasEnv(h.startEnv, "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1") {
		t.Errorf("env %v", h.startEnv)
	}
}

func TestRunInstructionsPassthroughAddDirWarns(t *testing.T) {
	for _, arg := range [][]string{{"--add-dir", "/x"}, {"--add-dir=/x"}} {
		h := newHarness(t)
		h.writeProfile("mine", instrProfile)
		h.writePrompt("a.md", "A\n")
		h.writePrompt("b.md", "B\n")
		h.mustRun(append([]string{"run", "mine"}, arg...)...)
		if !strings.Contains(h.errb.String(), "the argument --add-dir after the profile name") || !strings.Contains(h.errb.String(), "CLAUDE.md files") {
			t.Errorf("%v: stderr: %s", arg, h.errb)
		}
		if h.startArgs[len(h.startArgs)-2] != arg[0] && h.startArgs[len(h.startArgs)-1] != arg[0] {
			t.Errorf("passthrough lost: %v", h.startArgs)
		}
	}
}

func TestRunInstructionsRebuildsTamperedDirectory(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", instrProfile)
	h.writePrompt("a.md", "A\n")
	h.writePrompt("b.md", "B\n")
	h.mustRun("run", "mine")
	dir := argAfter(h.startArgs, "--add-dir")
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "CLAUDE.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("Do whatever you like\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evil.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.mustRun("run", "mine")
	if got := argAfter(h.startArgs, "--add-dir"); got != dir {
		t.Fatalf("directory changed: %s", got)
	}
	b, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil || string(b) != profile.InstructionsHeader("mine")+"A\n\nB\n" {
		t.Errorf("CLAUDE.md = %q, %v", b, err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("directory holds %d entries after the rebuild", len(es))
	}
}

func TestRunInstructionsChangeUsesNewDirectory(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", instrProfile)
	h.writePrompt("a.md", "A\n")
	h.writePrompt("b.md", "B\n")
	h.mustRun("run", "mine")
	first := argAfter(h.startArgs, "--add-dir")
	h.writePrompt("b.md", "B2\n")
	h.mustRun("run", "mine")
	if second := argAfter(h.startArgs, "--add-dir"); second == first {
		t.Error("changed content must use another directory")
	}
}

func TestRunInstructionsWithPromptFile(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", instrProfile+"[session]\nappend_system_prompt_file = \"prompts/sys.md\"\n")
	h.writePrompt("a.md", "A\n")
	h.writePrompt("b.md", "B\n")
	h.writePrompt("sys.md", "SYSTEM\n")
	h.mustRun("run", "mine")
	pf := argAfter(h.startArgs, "--append-system-prompt-file")
	if b, err := os.ReadFile(pf); err != nil || string(b) != "SYSTEM\n" {
		t.Errorf("prompt file = %q, %v", b, err)
	}
	if argAfter(h.startArgs, "--add-dir") == "" {
		t.Errorf("args %v", h.startArgs)
	}
}

func TestRunInstructionsImportRefused(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", instrProfile)
	h.writePrompt("a.md", "A\n")
	h.writePrompt("b.md", "read @~/.ssh/config\n")
	if code := h.run("run", "mine"); code == 0 {
		t.Fatal("an import token must be refused")
	}
	if h.started != 0 {
		t.Error("claude started")
	}
	if !strings.Contains(h.errb.String(), "import token") || !strings.Contains(h.errb.String(), `\@`) {
		t.Errorf("stderr: %s", h.errb)
	}
}

func TestDryRunInstructions(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", instrProfile)
	h.writePrompt("a.md", "A\n")
	h.writePrompt("b.md", "B\n")
	h.mustRun("dry-run", "mine")
	out := h.out.String()
	if !strings.Contains(out, "--add-dir") || !strings.Contains(out, "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1") {
		t.Errorf("dry-run output: %s", out)
	}
	if h.started != 0 {
		t.Fatal("dry-run started claude")
	}

	h.out.Reset()
	h.mustRun("--json", "dry-run", "mine")
	var env struct {
		Data struct {
			Command         []string          `json:"command"`
			Env             map[string]string `json:"env"`
			InstructionsDir string            `json:"instructions_dir"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.InstructionsDir == "" || env.Data.Env["CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"] != "1" {
		t.Errorf("json: %+v", env.Data)
	}
	if b, err := os.ReadFile(filepath.Join(env.Data.InstructionsDir, "CLAUDE.md")); err != nil || string(b) != profile.InstructionsHeader("mine")+"A\n\nB\n" {
		t.Errorf("CLAUDE.md = %q, %v", b, err)
	}
}

func TestShowDiffInstructions(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.copyTree("testdata/org-instructions"))

	h.mustRun("show", "guided")
	golden(t, "show-guided.golden.txt", h.out.String())
	if strings.Contains(h.out.String(), "company style guide") {
		t.Error("show must not print instructions text")
	}
	h.out.Reset()
	h.mustRun("show", "fresh")
	golden(t, "show-fresh.golden.txt", h.out.String())

	h.out.Reset()
	h.mustRun("--json", "show", "fresh")
	var env struct {
		Data struct {
			Instructions struct {
				Files []struct {
					Profile, Source, Path, Digest string
					Bytes                         int
				} `json:"files"`
				CutBy        string `json:"cut_by"`
				JoinedBytes  int    `json:"joined_bytes"`
				JoinedDigest string `json:"joined_digest"`
			} `json:"instructions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	in := env.Data.Instructions
	if len(in.Files) != 1 || in.Files[0].Path != "prompts/team.md" || in.Files[0].Profile != "fresh" || in.Files[0].Source != "dir:org" ||
		in.CutBy != "fresh" || in.JoinedBytes != len(profile.InstructionsHeader("fresh")+"Use the team checklist.\n") || len(in.JoinedDigest) != 64 {
		t.Errorf("json: %+v", in)
	}
	if strings.Contains(h.out.String(), "team checklist") {
		t.Error("JSON must not contain the text")
	}

	h.out.Reset()
	h.mustRun("diff", "guided", "fresh")
	golden(t, "diff-guided-fresh.golden.txt", h.out.String())
}

func TestDiffInstructionsReorder(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("one", "name = \"one\"\n[instructions]\nfiles = [\"prompts/a.md\", \"prompts/b.md\"]\n")
	h.writeProfile("two", "name = \"two\"\n[instructions]\nfiles = [\"prompts/b.md\", \"prompts/a.md\"]\n")
	h.writePrompt("a.md", "A\n")
	h.writePrompt("b.md", "B\n")
	h.mustRun("diff", "one", "two")
	out := h.out.String()
	if !strings.Contains(out, "instructions.order: prompts/a.md [dir:personal], prompts/b.md [dir:personal] -> prompts/b.md [dir:personal], prompts/a.md [dir:personal]") || !strings.Contains(out, "instructions (sha256)") {
		t.Errorf("diff: %s", out)
	}
	if strings.Contains(out, "instructions.files") {
		t.Errorf("a reorder is not a set change: %s", out)
	}
}

func TestRunInstructionsEnvOverride(t *testing.T) {
	const name = "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"
	// A user value of 0 is replaced with 1 only when instructions are active.
	h := newHarness(t)
	t.Setenv(name, "0")
	h.writeProfile("mine", instrProfile)
	h.writeProfile("plain", strings.Replace(personalMine, "mine", "plain", 1))
	h.writePrompt("a.md", "A\n")
	h.writePrompt("b.md", "B\n")
	h.mustRun("run", "mine")
	if v, ok := envValue(h.startEnv, name); !ok || v != "1" {
		t.Errorf("with instructions: %q %v", v, ok)
	}
	n := 0
	for _, e := range h.startEnv {
		if strings.HasPrefix(e, name+"=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("the variable appears %d times", n)
	}
	h.mustRun("run", "plain")
	if v, ok := envValue(h.startEnv, name); !ok || v != "0" {
		t.Errorf("without instructions the user value must stay: %q %v", v, ok)
	}
}

func TestDiffInstructionsSameFileOtherSource(t *testing.T) {
	org := t.TempDir()
	testutil.WriteFile(t, filepath.Join(org, "profiles", "o.toml"), "name = \"o\"\n[instructions]\nfiles = [\"prompts/a.md\"]\n")
	testutil.WriteFile(t, filepath.Join(org, "prompts", "a.md"), "A\n")
	h := newHarness(t)
	h.writeConfig("[[sources]]\ntype = \"dir\"\npath = " + tomlString(filepath.Join(org, "profiles")) + "\n")
	h.writeProfile("p", "name = \"p\"\n[instructions]\nfiles = [\"prompts/a.md\"]\n")
	h.writePrompt("a.md", "A\n")
	h.mustRun("diff", "o", "p")
	out := h.out.String()
	if !strings.Contains(out, "instructions.files") || !strings.Contains(out, "prompts/a.md [dir:org]") || !strings.Contains(out, "prompts/a.md [dir:personal]") {
		t.Errorf("diff: %s", out)
	}
}

// settingsEnv returns the env map of the generated settings file.
func settingsEnv(t *testing.T, h *harness) map[string]any {
	t.Helper()
	env, _ := h.settingsOf(h.startArgs)["env"].(map[string]any)
	return env
}

func TestRunInstructionsPutsVariableInSettingsEnv(t *testing.T) {
	const name = "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"
	h := newHarness(t)
	h.writeProfile("mine", instrProfile)
	h.writeProfile("plain", strings.Replace(personalMine, "mine", "plain", 1))
	h.writePrompt("a.md", "A\n")
	h.writePrompt("b.md", "B\n")
	h.mustRun("run", "mine")
	if settingsEnv(t, h)[name] != "1" {
		t.Errorf("settings env: %v", settingsEnv(t, h))
	}
	if v, ok := envValue(h.startEnv, name); !ok || v != "1" {
		t.Errorf("process env must keep the variable: %q %v", v, ok)
	}
	h.mustRun("run", "plain")
	if _, ok := settingsEnv(t, h)[name]; ok {
		t.Errorf("a profile without instructions must not get the key: %v", settingsEnv(t, h))
	}
}

func TestRunInstructionsManagedOffWarns(t *testing.T) {
	const name = "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"
	h := newHarness(t)
	h.writeProfile("mine", instrProfile)
	h.writeProfile("plain", strings.Replace(personalMine, "mine", "plain", 1))
	h.writePrompt("a.md", "A\n")
	h.writePrompt("b.md", "B\n")
	h.writeManaged(`{"env":{"` + name + `":"0"}}`)
	h.mustRun("run", "mine")
	if !strings.Contains(h.errb.String(), "managed policy sets "+name) {
		t.Errorf("no warning: %s", h.errb)
	}
	if h.started != 1 {
		t.Error("the launcher must still start claude")
	}
	h.errb.Reset()
	h.mustRun("run", "plain")
	if strings.Contains(h.errb.String(), name) {
		t.Errorf("a profile without instructions must not warn: %s", h.errb)
	}
	h.errb.Reset()
	h.writeManaged(`{"env":{"` + name + `":"1"}}`)
	h.mustRun("run", "mine")
	if strings.Contains(h.errb.String(), name) {
		t.Errorf("a managed value of 1 must not warn: %s", h.errb)
	}
}

package launcher

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
)

const branchURL = "https://ghe.example.com/acme/profiles.git"

func TestConfigSourceAddBranch(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	before := h.readConfig()
	// Adding a branch source is a weakening change: --yes without a terminal.
	h.wantCode(2, "config", "source", "add", "--git-url", branchURL, "--branch", "main")
	h.wantErr("--yes", "tracks branch main")
	if h.readConfig() != before {
		t.Fatal("the file changed without --yes")
	}
	// It is allowed with trust.require_pin on (the default).
	h.mustRun("config", "source", "add", "--git-url", branchURL, "--branch", "release/2026", "--yes")
	h.wantErr("weakens a security setting", "tracks branch release/2026")
	cfg := h.loadConfig()
	if !cfg.Trust.RequirePin || len(cfg.Sources) != 2 {
		t.Fatalf("config: %+v", cfg)
	}
	if s := cfg.Sources[1]; s.Branch != "release/2026" || s.Ref != "" || s.URL != branchURL {
		t.Errorf("source: %+v", s)
	}
	h.wantNoTrustFiles()
}

func TestConfigSourceAddBranchRefusals(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	before := h.readConfig()
	dir := filepath.Join(h.dirs["WORK"], "team")
	other := "https://ghe.example.com/acme/other.git"
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"both":        {[]string{"--git-url", other, "--ref", "v1", "--branch", "main"}, "not both"},
		"dash":        {[]string{"--git-url", other, "--branch", "-x"}, "must not start"},
		"dotdot":      {[]string{"--git-url", other, "--branch", "a..b"}, ".."},
		"full ref":    {[]string{"--git-url", other, "--branch", "refs/heads/main"}, "full ref"},
		"with dir":    {[]string{"--dir", dir, "--branch", "main"}, "--dir takes no"},
		"with plugin": {[]string{"--plugin", "a@b", "--branch", "main"}, "--git-url"},
		"duplicate":   {[]string{"--git-url", "https://example.com/acme/data.git", "--branch", "main", "--yes"}, "config source pin 1"},
	} {
		h.wantCode(2, append([]string{"config", "source", "add"}, tc.args...)...)
		h.wantErr(tc.want)
		if h.readConfig() != before {
			t.Fatalf("%s: the file changed", name)
		}
	}
	// A ref of main keeps failing, and the hint names branch.
	h.wantCode(2, "config", "source", "add", "--git-url", other, "--ref", "main")
	h.wantErr(`branch = "main"`)
}

func TestConfigSourcePinBranch(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	before := h.readConfig()
	h.wantCode(2, "config", "source", "pin", "1", "--branch", "main")
	h.wantErr("--yes")
	if h.readConfig() != before {
		t.Fatal("the file changed without --yes")
	}
	h.wantCode(2, "config", "source", "pin", "1", "--ref", "v2", "--branch", "main", "--yes")
	h.wantErr("not both")
	h.wantCode(2, "config", "source", "pin", "1", "--branch", "--bad", "--yes")
	h.mustRun("config", "source", "pin", "1", "--branch", "main", "--yes")
	if s := h.loadConfig().Sources[0]; s.Branch != "main" || s.Ref != "" {
		t.Fatalf("source: %+v", s)
	}
	// Switching to another branch is a weakening again, back to a tag is not.
	h.wantCode(2, "config", "source", "pin", "1", "--branch", "release")
	h.mustRun("config", "source", "pin", "1", "--ref", "v2.0.0")
	if strings.Contains(h.errb.String(), "weakens") {
		t.Errorf("pinning a tag warned:\n%s", h.errb)
	}
	if s := h.loadConfig().Sources[0]; s.Branch != "" || s.Ref != "v2.0.0" {
		t.Fatalf("source: %+v", s)
	}
	h.wantCode(2, "config", "source", "pin", "1")
	h.wantErr("--ref|--branch")
	h.wantNoTrustFiles()
}

func TestConfigSetBranchCheckInterval(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	h.mustRun("config", "set", "trust.branch_check_interval", "12h")
	if got := h.loadConfig().Trust.BranchCheckInterval; got != "12h" {
		t.Errorf("interval = %q", got)
	}
	if strings.Contains(h.errb.String(), "weakens") {
		t.Errorf("a longer or shorter check interval is not a weakening:\n%s", h.errb)
	}
	for _, v := range []string{"30m", "9000h", "often"} {
		h.wantCode(2, "config", "set", "trust.branch_check_interval", v)
	}
	h.mustRun("config", "unset", "trust.branch_check_interval")
	if got := h.loadConfig().Trust.BranchCheckInterval; got != "" {
		t.Errorf("interval after unset = %q", got)
	}
	h.mustRun("config", "show")
	if !strings.Contains(h.out.String(), "branch_check_interval: 24h (default)") {
		t.Errorf("show:\n%s", h.out)
	}
}

func TestConfigMenuAddBranchSource(t *testing.T) {
	h := newHarness(t)
	h.writeConfig(baseConfig)
	sc := ui.NewScripted(0, 0, branchURL, "branch:main", "profiles", true, 5)
	h.prompt = sc
	h.mustRun("config")
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	if s := h.loadConfig().Sources[1]; s.Branch != "main" || s.Ref != "" {
		t.Fatalf("source: %+v", s)
	}
	h.wantErr("Equivalent: ccshelf config source add --git-url "+branchURL+" --branch main --yes", "tracks branch main")
	// The replayed command gives the same file.
	want := h.readConfig()
	h2 := newHarness(t)
	h2.writeConfig(baseConfig)
	h2.mustRun("config", "source", "add", "--git-url", branchURL, "--branch", "main", "--yes")
	if h2.readConfig() != want {
		t.Errorf("replay differs:\n%s\n---\n%s", h2.readConfig(), want)
	}
	// Pin picks a branch the same way, and prints --branch.
	h = newHarness(t)
	h.writeConfig(baseConfig)
	h.prompt = ui.NewScripted(1, 0, "branch:main", true, 5)
	h.mustRun("config")
	h.wantErr("Equivalent: ccshelf config source pin 1 --branch main --yes")
	// A bad branch name is refused by the prompt's validator or the command.
	if err := validatePinOrBranch(config.ValidatePin)("branch:-x"); err == nil {
		t.Error("branch:-x accepted")
	}
	if err := validatePinOrBranch(config.ValidatePin)("branch:"); err == nil {
		t.Error("branch: accepted")
	}
	if err := validatePinOrBranch(config.ValidatePin)("v1.0.0"); err != nil {
		t.Errorf("v1.0.0: %v", err)
	}
}

func TestInitBranch(t *testing.T) {
	h := newHarness(t)
	h.mustRun("init", "--git-url", branchURL, "--branch", "main")
	cfg := h.loadConfig()
	if len(cfg.Sources) != 1 || cfg.Sources[0].Branch != "main" || cfg.Sources[0].Ref != "" {
		t.Fatalf("config: %+v", cfg.Sources)
	}
	for _, args := range [][]string{
		{"--git-url", branchURL, "--ref", "v1", "--branch", "main"},
		{"--branch", "main"},
		{"--git-url", branchURL, "--branch", "refs/heads/main"},
		{"--git-url", branchURL, "--branch", "-x"},
	} {
		h = newHarness(t)
		h.wantCode(2, append([]string{"init"}, args...)...)
	}
}

func TestInitWizardBranch(t *testing.T) {
	h := newHarness(t)
	sc := ui.NewScripted(branchURL, "branch:main", "profiles", "", "", false, true)
	h.prompt = &initWritePrompt{Scripted: sc, h: h, t: t}
	h.mustRun("init")
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Tracked branch: main", "every new commit needs your trust", "--git-url " + branchURL + " --branch main", "--update-mode off --yes"} {
		if !strings.Contains(h.errb.String(), want) {
			t.Errorf("missing %q in %s", want, h.errb)
		}
	}
	if strings.Contains(h.errb.String(), "--ref") {
		t.Errorf("the equivalent command has --ref:\n%s", h.errb)
	}
	cfg := h.loadConfig()
	if cfg.Sources[0].Branch != "main" {
		t.Errorf("config: %+v", cfg.Sources)
	}
}

package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/ui"
)

func TestLsGoldenAndJSON(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.fixtureOrg())
	h.writeProfile("mine", personalMine)
	h.mustRun("ls")
	golden(t, "ls.golden.txt", h.out.String())
	h.writeProfile("broken", "name = \"broken\"\nbogus = 1\n")
	h.mustRun("ls")
	if !strings.Contains(h.out.String(), "invalid") || !strings.Contains(h.out.String(), "unknown key") {
		t.Errorf("invalid profile not listed with its error: %s", h.out)
	}

	h.mustRun("--json", "ls")
	var env struct {
		Version int     `json:"version"`
		Kind    string  `json:"kind"`
		Data    []lsRow `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Version != 1 || env.Kind != "profiles" || len(env.Data) != 4 {
		t.Errorf("json: %+v", env)
	}
	for _, r := range env.Data {
		if strings.Contains(r.Source, string(filepath.Separator)) {
			t.Errorf("machine path leaked: %s", r.Source)
		}
	}
}

func TestLsEmpty(t *testing.T) {
	h := newHarness(t)
	h.mustRun("ls")
	if !strings.Contains(h.out.String(), "no profiles found\nhint:") || !strings.Contains(h.out.String(), "ccshelf new") || h.errb.Len() != 0 {
		t.Errorf("out %q err %q", h.out, h.errb)
	}
}

func TestShowGolden(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.fixtureOrg())
	h.mustRun("show", "web")
	golden(t, "show-web.golden.txt", h.out.String())
	h.mustRun("--json", "show", "web")
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Name  string   `json:"name"`
			Chain []string `json:"chain"`
			Env   []string `json:"env_names"`
			Trust string   `json:"trust"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Kind != "profile" || env.Data.Name != "web" || len(env.Data.Chain) != 2 || env.Data.Trust != "new" {
		t.Errorf("json: %s", h.out)
	}
	if strings.Contains(h.out.String(), "op://") {
		t.Errorf("env values must not be printed: %s", h.out)
	}
	if code := h.run("show"); code != ui.ExitUsage {
		t.Errorf("show without name: %d", code)
	}
	if code := h.run("show", "nope"); code != ui.ExitUsage {
		t.Errorf("show nope: %d", code)
	}
}

func TestShowPicker(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.prompt = ui.NewScripted(0)
	h.mustRun("show")
	if !strings.Contains(h.errb.String(), "Equivalent: ccshelf show mine") {
		t.Errorf("stderr %s", h.errb)
	}
}

func TestDiff(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.fixtureOrg())
	h.mustRun("diff", "base", "web")
	golden(t, "diff-base-web.golden.txt", h.out.String())
	h.mustRun("diff", "web", "web")
	if !strings.Contains(h.out.String(), "same settings") {
		t.Errorf("out %s", h.out)
	}
	h.mustRun("--json", "diff", "base", "web")
	var env struct {
		Data diffDoc `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Identical || len(env.Data.Lists) == 0 {
		t.Errorf("json %s", h.out)
	}
	if code := h.run("diff"); code != ui.ExitUsage {
		t.Errorf("diff without args: %d", code)
	}
}

func TestNewWithFlags(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.exampleOrg())
	h.mustRun("new", "night", "--from", "base", "--plugin", "sre-kit@acme", "--skill-off", "legacy-helper", "--description", "Night shift", "--effort", "low")
	p := filepath.Join(h.configDir(), "profiles", "night.toml")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`name = 'night'`, "base", "sre-kit@acme", "legacy-helper", "Night shift"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing %q in\n%s", want, b)
		}
	}
	if strings.Contains(h.errb.String(), "Equivalent") {
		t.Error("no equivalent line without prompts")
	}
	// Duplicate.
	if code := h.run("new", "night"); code != ui.ExitFailure {
		t.Errorf("duplicate: %d", code)
	}
	// Missing name, bad name, missing parent (file must not stay).
	if code := h.run("new"); code != ui.ExitUsage {
		t.Errorf("no name: %d", code)
	}
	if code := h.run("new", "Bad Name"); code != ui.ExitUsage {
		t.Errorf("bad name: %d", code)
	}
	if code := h.run("new", "orphan", "--from", "ghost"); code == 0 {
		t.Errorf("missing parent accepted")
	}
	if _, err := os.Stat(filepath.Join(h.configDir(), "profiles", "orphan.toml")); err == nil {
		t.Error("invalid profile was kept")
	}
	if fi, err := os.Stat(p); err == nil && runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
}

func TestNewWizard(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.exampleOrg())
	// name, description, parents (base = index 0), plugins, skills; there is no personal MCP registry to pick from.
	sc := ui.NewScripted("wiz", "Wizard profile", []int{0}, []int{0}, "legacy-helper, other")
	h.prompt = sc
	if code := h.run("new"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	eq := h.errb.String()
	for _, want := range []string{"Equivalent: ccshelf new wiz", "--from base", "--skill-off legacy-helper", "--skill-off other", "--description"} {
		if !strings.Contains(eq, want) {
			t.Errorf("missing %q in %s", want, eq)
		}
	}
	h.mustRun("show", "wiz")
}

func TestEdit(t *testing.T) {
	h := newHarness(t)
	h.prompt = ui.NewScripted(0) // a terminal: edit starts an editor only on one
	h.writeProfile("mine", personalMine)
	p := filepath.Join(h.configDir(), "profiles", "mine.toml")
	h.mustRun("edit", "mine", "--path")
	if strings.TrimSpace(h.out.String()) != p {
		t.Errorf("path %q", h.out)
	}
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	if code := h.run("edit", "mine"); code != ui.ExitUsage {
		t.Errorf("no editor: %d", code)
	}
	t.Setenv("EDITOR", "myedit --wait")
	h.mustRun("edit", "mine")
	if len(h.spawned) != 1 || h.spawned[0][0] != "myedit" || h.spawned[0][1] != "--wait" || h.spawned[0][2] != p {
		t.Errorf("spawned %v", h.spawned)
	}
	// VISUAL wins; an editor that breaks the file is reported.
	t.Setenv("VISUAL", "vis")
	h.spawnHook = func([]string) { _ = os.WriteFile(p, []byte("name = \"mine\"\nbogus = 1\n"), 0o600) }
	if code := h.run("edit", "mine"); code != ui.ExitFailure {
		t.Errorf("invalid edit: %d", code)
	}
	if h.spawned[len(h.spawned)-1][0] != "vis" {
		t.Errorf("VISUAL ignored: %v", h.spawned)
	}
	h.spawnHook = nil
	h.spawnCode = 3
	if code := h.run("edit", "mine"); code != ui.ExitFailure {
		t.Errorf("editor failure: %d", code)
	}
	// Not personal / unknown / bad name.
	if code := h.run("edit", "ghost"); code != ui.ExitUsage {
		t.Errorf("ghost: %d", code)
	}
	if code := h.run("edit", "../x"); code != ui.ExitUsage {
		t.Errorf("traversal: %d", code)
	}
	h.prompt = nil
	if code := h.run("edit"); code != ui.ExitUsage {
		t.Errorf("missing: %d", code)
	}
}

func TestInit(t *testing.T) {
	h := newHarness(t)
	h.mustRun("init")
	cfg := filepath.Join(h.configDir(), "config.toml")
	if _, err := os.Stat(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.configDir(), "profiles")); err != nil {
		t.Error("profiles dir missing")
	}
	if code := h.run("init"); code != ui.ExitFailure {
		t.Errorf("existing config: %d", code)
	}
	h.mustRun("init", "--force", "--git-url", "https://example.com/acme/data.git", "--ref", "v1.2.3")
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), "https://example.com/acme/data.git") || !strings.Contains(string(b), "v1.2.3") {
		t.Errorf("config:\n%s", b)
	}
	// Unpinned ref, ref without url, bad url.
	if code := h.run("init", "--force", "--git-url", "https://example.com/a.git", "--ref", "main"); code != ui.ExitUsage {
		t.Errorf("branch pin: %d", code)
	}
	if code := h.run("init", "--force", "--ref", "v1"); code != ui.ExitUsage {
		t.Errorf("ref alone: %d", code)
	}
	if code := h.run("init", "--force", "--git-url", "file:///tmp/x", "--ref", "v1"); code != ui.ExitUsage {
		t.Errorf("file url: %d", code)
	}
}

func TestInitWizardAndAccount(t *testing.T) {
	h := newHarness(t)
	sc := ui.NewScripted("https://example.com/acme/data.git", "v1.0.0", "profiles", "", "work", true)
	h.prompt = sc
	if code := h.run("init"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	if !strings.Contains(h.errb.String(), "Equivalent: ccshelf init --git-url") || !strings.Contains(h.errb.String(), "--account-name work") || !strings.Contains(h.errb.String(), "--update-mode notify") {
		t.Errorf("stderr %s", h.errb)
	}
	h.prompt = nil
	h.mustRun("account", "ls")
	if !strings.Contains(h.out.String(), "work") {
		t.Errorf("ls: %s", h.out)
	}
}

func TestAccountCommands(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "claude-work")
	h.mustRun("account", "add", "work", "--dir", dir, "--default", "--plugin", "design-kit@acme")
	if !strings.Contains(h.out.String(), "CLAUDE_CONFIG_DIR") || !strings.Contains(h.out.String(), "/login") {
		t.Errorf("steps: %s", h.out)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Error("directory not created")
	}
	h.mustRun("--json", "account", "ls")
	if !strings.Contains(h.out.String(), `"default": true`) {
		t.Errorf("json %s", h.out)
	}
	h.mustRun("account", "ls")
	if !strings.Contains(h.out.String(), "work") {
		t.Errorf("table %s", h.out)
	}
	if code := h.run("account", "add", "work", "--dir", dir+"-2"); code == 0 {
		t.Error("duplicate account accepted")
	}
	if code := h.run("account", "add"); code != ui.ExitUsage {
		t.Errorf("no name: %d", code)
	}
	if code := h.run("account", "add", "Bad"); code != ui.ExitUsage {
		t.Errorf("bad name: %d", code)
	}
	if code := h.run("account", "add", "x", "--dir", filepath.Join(h.dirs["HOME"], ".claude")); code == 0 {
		t.Error("default claude dir accepted")
	}
	h.mustRun("account", "rm", "work")
	if !strings.Contains(h.errb.String(), "nothing was deleted") && !strings.Contains(strings.ToLower(h.errb.String()), "not delete") {
		t.Logf("message: %s", h.errb)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("directory must be left alone")
	}
	if code := h.run("account", "rm", "work"); code != ui.ExitUsage {
		t.Errorf("rm twice: %d", code)
	}
	if code := h.run("account", "rm"); code != ui.ExitUsage {
		t.Errorf("rm no name: %d", code)
	}
	h.mustRun("account", "ls")
	if !strings.Contains(h.out.String(), "no accounts") || h.errb.Len() != 0 {
		t.Errorf("empty list: out %s err %s", h.out, h.errb)
	}
}

func TestAccountAddWizard(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "claude-personal")
	h.prompt = ui.NewScripted("personal", dir)
	h.mustRun("account", "add")
	if !strings.Contains(h.errb.String(), "Equivalent: ccshelf account add personal --dir") {
		t.Errorf("stderr %s", h.errb)
	}
}

func TestShellInit(t *testing.T) {
	h := newHarness(t)
	h.useOrg(h.exampleOrg())
	h.writeProfile("mine", personalMine)
	h.mustRun("shell-init", "bash", "--profile", "extra")
	out := h.out.String()
	for _, want := range []string{"cs-mine", "cs-frontend", "cs-extra", "run mine"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	h.mustRun("shell-init", "fish")
	if !strings.Contains(h.out.String(), "cs-mine") {
		t.Errorf("fish: %s", h.out)
	}
	h.goos = "windows"
	h.mustRun("shell-init", "pwsh")
	if !strings.Contains(h.out.String(), "function cs-mine") {
		t.Errorf("pwsh: %s", h.out)
	}
	h.mustRun("shell-init", "cmd")
	if !strings.Contains(h.out.String(), "cs-mine") {
		t.Errorf("cmd: %s", h.out)
	}
	h.goos = "linux"
	if code := h.run("shell-init", "tcsh"); code != ui.ExitUsage {
		t.Errorf("unknown shell: %d", code)
	}
	t.Setenv("SHELL", "/bin/zsh")
	h.mustRun("shell-init")
	if !strings.Contains(h.out.String(), "zsh") {
		t.Errorf("zsh detect: %s", h.out)
	}
	t.Setenv("SHELL", "")
	if code := h.run("shell-init"); code != ui.ExitUsage {
		t.Errorf("no shell: %d", code)
	}
	// A hostile profile name is skipped with a warning.
	h.writeProfile("ok-name", strings.ReplaceAll(personalMine, "mine", "ok-name"))
	h.mustRun("shell-init", "bash", "--profile", "x;rm -rf")
	if strings.Contains(h.out.String(), "rm -rf") || !strings.Contains(h.errb.String(), "skipping") {
		t.Errorf("hostile name: %s / %s", h.out, h.errb)
	}
	// cmd shims.
	shims := filepath.Join(t.TempDir(), "shims")
	h.goos = "windows"
	h.mustRun("shell-init", "--write-cmd-shims", shims)
	if _, err := os.Stat(filepath.Join(shims, "cs-mine.cmd")); err != nil {
		t.Error(err)
	}
}

func TestVersion(t *testing.T) {
	h := newHarness(t)
	h.mustRun("version")
	if !strings.HasPrefix(h.out.String(), "ccshelf ") {
		t.Errorf("out %s", h.out)
	}
	h.mustRun("--json", "version")
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Version string `json:"version"`
			OS      string `json:"os"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil || env.Kind != "version" || env.Data.OS == "" {
		t.Errorf("json %v %s", err, h.out)
	}
}

func TestCompletion(t *testing.T) {
	h := newHarness(t)
	for _, sh := range []string{"bash", "zsh", "fish", "powershell"} {
		h.mustRun("completion", sh)
		if h.out.Len() < 100 || !strings.Contains(h.out.String(), "ccshelf") {
			t.Errorf("%s: short output", sh)
		}
	}
	if code := h.run("completion"); code != ui.ExitUsage {
		t.Errorf("no shell: %d", code)
	}
	if code := h.run("completion", "tcsh"); code != ui.ExitUsage {
		t.Errorf("bad shell: %d", code)
	}
}

func TestTrustProject(t *testing.T) {
	h := newHarness(t)
	repo := filepath.Join(t.TempDir(), "repo")
	h.cwd = repo
	prof := filepath.Join(repo, ".ccshelf", "profiles", "proj.toml")
	if err := os.MkdirAll(filepath.Dir(prof), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prof, []byte(strings.ReplaceAll(personalMine, "mine", "proj")), 0o644); err != nil {
		t.Fatal(err)
	}
	// Off by default: not listed, run says why.
	h.mustRun("ls")
	if strings.Contains(h.out.String(), "proj") || !strings.Contains(h.errb.String(), ".ccshelf") {
		t.Errorf("project profile loaded by default: %s / %s", h.out, h.errb)
	}
	if code := h.run("run", "proj"); code != ui.ExitUsage || h.started != 0 {
		t.Errorf("run proj: %d", code)
	}
	h.writeConfig("[trust]\ntrust_project_profiles = true\n")
	if code := h.run("trust", "--project"); code != ui.ExitUsage {
		t.Errorf("no --accept, no tty: %d", code)
	}
	if code := h.run("trust", "--project", "--accept", "bad"); code != ui.ExitTrust {
		t.Errorf("bad hash: %d", code)
	}
	h.run("trust", "--project", "--accept", "bad")
	hash := ""
	if _, rest, ok := strings.Cut(h.out.String(), "content hash "); ok {
		hash, _, _ = strings.Cut(rest, ")")
	}
	if len(hash) != 64 {
		t.Fatalf("folder hash not printed: %q", h.out)
	}
	h.mustRun("trust", "--project", "--accept", hash)
	// A trusted folder is not enough: the closure needs its own lockfile entry.
	if code := h.run("run", "proj"); code != ui.ExitTrust {
		t.Fatalf("run proj before trusting the profile: %d\n%s", code, h.errb)
	}
	h.mustRun("--json", "show", "proj")
	var env struct {
		Data struct {
			ClosureHash string `json:"closure_hash"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	h.mustRun("trust", "proj", "--accept", env.Data.ClosureHash)
	if code := h.run("run", "proj"); code != 0 {
		t.Fatalf("run proj: %d\n%s", code, h.errb)
	}
	// Editing the folder revokes folder trust.
	if err := os.WriteFile(prof, []byte(strings.ReplaceAll(personalMine, "mine", "proj")+"\n# edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("run", "proj"); code == 0 {
		t.Error("edited project folder still trusted")
	}
	h.mustRun("trust", "--project", "--revoke")
}

func TestTrustProjectNoFolder(t *testing.T) {
	h := newHarness(t)
	if code := h.run("trust", "--project"); code != ui.ExitUsage {
		t.Errorf("code %d", code)
	}
}

func TestTrustRevokeUnknown(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	if code := h.run("trust", "mine", "--revoke"); code != ui.ExitFailure {
		t.Errorf("code %d", code)
	}
}

func TestInitUpdateMode(t *testing.T) {
	read := func(h *harness) string {
		b, err := os.ReadFile(filepath.Join(h.configDir(), "config.toml"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// Flags-only and non-interactive: [update] is never written, so the
	// default (off) stands.
	h := newHarness(t)
	h.mustRun("init")
	if strings.Contains(read(h), "update") {
		t.Errorf("init must not write [update] by itself:\n%s", read(h))
	}

	// The flag sets it, with no question asked even on a terminal.
	h = newHarness(t)
	sc := ui.NewScripted()
	h.prompt = sc
	h.mustRun("init", "--update-mode", "notify")
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	if got := read(h); !strings.Contains(got, "[update]") || !strings.Contains(got, "notify") {
		t.Errorf("config:\n%s", got)
	}
	if strings.Contains(h.errb.String(), "Equivalent") {
		t.Error("a flags-only run prints no equivalent command")
	}

	// A bad value is a usage error and writes nothing.
	h = newHarness(t)
	for _, bad := range []string{"always", "Notify", "on", "auto"} {
		if code := h.run("init", "--update-mode", bad); code != ui.ExitUsage {
			t.Errorf("--update-mode %s: exit %d, want 2", bad, code)
		}
	}
	if _, err := os.Stat(filepath.Join(h.configDir(), "config.toml")); err == nil {
		t.Error("an invalid --update-mode wrote a config")
	}
	h.mustRun("init", "--update-mode", "off")
	if got := read(h); !strings.Contains(got, "off") {
		t.Errorf("an explicit off is written:\n%s", got)
	}

	// The wizard asks once; the default is no, and the answer is recorded.
	for answer, want := range map[bool]string{true: "notify", false: "off"} {
		h = newHarness(t)
		sc = ui.NewScripted("", "", "", answer) // no repo, no dir, no account, then the update question
		h.prompt = sc
		h.mustRun("init")
		if err := sc.Done(); err != nil {
			t.Error(err)
		}
		if !strings.Contains(sc.Asked[len(sc.Asked)-1], "Check for updates once a day") {
			t.Errorf("asked %v", sc.Asked)
		}
		if got := read(h); !strings.Contains(got, want) {
			t.Errorf("answer %v: config:\n%s", answer, got)
		}
		if !strings.Contains(h.errb.String(), "--update-mode "+want) {
			t.Errorf("answer %v: the equivalent command lacks --update-mode %s:\n%s", answer, want, h.errb)
		}
	}

	// --force keeps an existing [update] section and does not ask again.
	h = newHarness(t)
	h.mustRun("init", "--update-mode", "install")
	sc = ui.NewScripted("", "", "") // only the three profile questions
	h.prompt = sc
	h.mustRun("init", "--force")
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	if got := read(h); !strings.Contains(got, "install") {
		t.Errorf("--force dropped the update mode:\n%s", got)
	}
	if strings.Contains(h.errb.String(), "--update-mode") {
		t.Errorf("nothing about the update mode was chosen interactively:\n%s", h.errb)
	}
}

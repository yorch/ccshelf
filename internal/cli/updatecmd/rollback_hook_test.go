package updatecmd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yorch/ccshelf/internal/ui"
	"github.com/yorch/ccshelf/internal/update"
)

func TestRollback(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	if code := h.run("update", "--rollback", "--yes"); code != ui.ExitFailure {
		t.Fatalf("no backup: exit %d", code)
	}
	has(t, h.errb.String(), "no previous version")
	if h.hits() != 0 {
		t.Error("a rollback never touches the network")
	}

	h.mustRun("update", "--yes")
	h.cur = "0.2.0"
	hits := h.hits()

	// Without a terminal and without --yes: exit 2 naming the flag.
	if code := h.run("update", "--rollback"); code != ui.ExitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	has(t, h.errb.String(), "--yes")
	if h.exeContent() != string(fakeBinary("0.2.0")) {
		t.Error("changed without consent")
	}

	// --dry-run.
	h.mustRun("update", "--rollback", "--dry-run")
	has(t, h.out.String(), "would restore ccshelf.old (0.1.0)")
	h.mustRun("--json", "update", "--rollback", "--dry-run")
	if _, data := h.envelope(); data["action"] != "dry-run" || data["latest"] != "0.1.0" {
		t.Errorf("envelope = %v", data)
	}
	if h.exeContent() != string(fakeBinary("0.2.0")) {
		t.Error("--dry-run changed the binary")
	}

	// Interactive: declined, then confirmed.
	h.prompt = ui.NewScripted(false)
	h.mustRun("update", "--rollback")
	has(t, h.errb.String(), "not rolled back")
	sc := ui.NewScripted(true)
	h.prompt = sc
	h.mustRun("update", "--rollback")
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	has(t, h.errb.String(), "Equivalent: ccshelf update --rollback --yes")
	if h.exeContent() != string(fakeBinary("0.1.0")) {
		t.Error("not rolled back")
	}
	h.prompt = nil
	b, _ := os.ReadFile(h.exe + ".old")
	if string(b) != string(fakeBinary("0.2.0")) {
		t.Errorf("the replaced version must become the new backup, got %q", b)
	}
	has(t, h.errb.String(), "restored 0.1.0")

	// --yes, and JSON, roll forward again.
	h.mustRun("--json", "update", "--rollback", "--yes")
	if _, data := h.envelope(); data["action"] != "rolled-back" || h.exeContent() != string(fakeBinary("0.2.0")) {
		t.Errorf("envelope = %v, exe %q", data, h.exeContent())
	}
	if h.hits() != hits {
		t.Error("a rollback touched the network")
	}
}

func TestRollbackOfAnUnverifiableBackup(t *testing.T) {
	h := newHarness(t, "0.2.0")
	if err := os.WriteFile(h.exe+".old", []byte("from another platform"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.opt.RunVersion = func(_ context.Context, path string, _ []string) (string, error) {
		if strings.HasSuffix(path, ".old") {
			return "", errors.New("exec format error")
		}
		return "0.2.0", nil
	}
	// --yes is required even in a terminal flow's flag form.
	if code := h.run("update", "--rollback"); code != ui.ExitUsage {
		t.Errorf("exit = %d, want 2", code)
	}
	has(t, h.errb.String(), "--yes")
	// A terminal must type "yes".
	h.prompt = ui.NewScripted("no")
	h.mustRun("update", "--rollback")
	has(t, h.errb.String(), "could not be checked")
	has(t, h.errb.String(), "not rolled back")
	if h.exeContent() != string(fakeBinary("0.2.0")) {
		t.Error("changed without consent")
	}
	h.prompt = ui.NewScripted("yes")
	h.mustRun("update", "--rollback")
	if h.exeContent() != "from another platform" {
		t.Errorf("exe = %q", h.exeContent())
	}
	// A backup that runs but is not ccshelf is refused whatever the flags.
	h2 := newHarness(t, "0.2.0")
	if err := os.WriteFile(h2.exe+".old", []byte("i am a shell script"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := h2.run("update", "--rollback", "--yes", "--force"); code != ui.ExitFailure {
		t.Errorf("not ccshelf: exit %d", code)
	}
	has(t, h2.errb.String(), "not a ccshelf binary")
}

func TestRollbackPackageManaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Homebrew rule is for darwin and linux")
	}
	h := newHarness(t, "0.2.0")
	cellar := t.TempDir() + "/Cellar/ccshelf/0.2.0"
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatal(err)
	}
	h.exe = cellar + "/ccshelf"
	if err := os.WriteFile(h.exe, fakeBinary("0.2.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.exe+".old", fakeBinary("0.1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code := h.run("update", "--rollback", "--yes"); code != ui.ExitFailure {
		t.Fatalf("exit = %d", code)
	}
	has(t, h.errb.String(), "brew upgrade ccshelf")
	h.mustRun("update", "--rollback", "--yes", "--force")
	if h.exeContent() != string(fakeBinary("0.1.0")) {
		t.Error("--force did not roll back")
	}
}

// ---- the automatic hooks -------------------------------------------------

func (h *harness) tty() { h.prompt = ui.NewScripted() }

func TestHookOffByDefault(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	h.tty()
	for _, cmd := range []string{"ls", "run", "dry-run", "show"} {
		h.mustRun(cmd)
	}
	if h.hits() != 0 {
		t.Errorf("%d requests with [update] absent: nothing may contact the network", h.hits())
	}
	h.writeConfig("mode = \"off\"\n")
	h.mustRun("ls")
	if h.hits() != 0 {
		t.Error("mode off contacted the network")
	}
	hasNot(t, h.errb.String(), "available")
}

func TestHookNotify(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	h.writeConfig("mode = \"notify\"\n")
	h.tty()

	h.mustRun("ls")
	has(t, h.out.String(), "ran ls")
	has(t, h.errb.String(), "ccshelf 0.2.0 is available (you have 0.1.0). Run: ccshelf update")
	hits := h.hits()
	if hits != 1 {
		t.Errorf("requests = %d, want the single metadata request", hits)
	}

	// Within a day: not again, no new request.
	h.now = h.now.Add(3 * time.Hour)
	h.mustRun("ls")
	hasNot(t, h.errb.String(), "available")
	if h.hits() != hits {
		t.Error("asked the network again within the interval")
	}

	// run and dry-run: cached notice only, never the network, even when due.
	h.now = h.now.Add(48 * time.Hour)
	h.mustRun("run", "web")
	has(t, h.errb.String(), "is available")
	h.mustRun("dry-run", "web")
	hasNot(t, h.errb.String(), "available") // shown once already today
	if h.hits() != hits {
		t.Errorf("run and dry-run made %d request(s)", h.hits()-hits)
	}
	// ... and the next list command asks again, because the interval is over.
	h.now = h.now.Add(time.Minute)
	h.mustRun("ls")
	if h.hits() != hits+1 {
		t.Errorf("requests = %d, want one more after the interval", h.hits()-hits)
	}
}

func TestHookIsQuietWithoutATerminalCIAndKillSwitch(t *testing.T) {
	type setupFn func(h *harness) []string
	for name, setup := range map[string]setupFn{
		"no terminal": func(h *harness) []string { h.prompt = nil; return nil },
		"no-interact": func(h *harness) []string { h.tty(); return []string{"--no-interactive"} },
		"json":        func(h *harness) []string { h.tty(); return []string{"--json"} },
		"CI":          func(h *harness) []string { h.tty(); h.env["CI"] = "true"; return nil },
		"CI false":    func(h *harness) []string { h.tty(); h.env["CI"] = "false"; return nil },
		"kill switch": func(h *harness) []string { h.tty(); h.env[update.KillSwitchEnv] = "1"; return nil },
	} {
		for _, mode := range []string{"notify", "install"} {
			h := newHarness(t, "0.1.0")
			h.publish("v0.2.0")
			h.writeConfig("mode = \"" + mode + "\"\n")
			args := append(setup(h), "ls")
			h.mustRun(args...)
			hasNot(t, h.errb.String(), "available")
			switch {
			case name == "CI" || name == "CI false" || name == "kill switch":
				if h.hits() != 0 {
					t.Errorf("%s/%s: %d requests", name, mode, h.hits())
				}
			case mode == "notify":
				if h.hits() != 0 {
					t.Errorf("%s/%s: notify with nobody to tell contacted the network (%d)", name, mode, h.hits())
				}
			}
			if h.exeContent() != string(fakeBinary("0.1.0")) {
				t.Errorf("%s/%s: the binary was replaced although v0.2.0 is a new 0.x minor (and CI/kill switch forbid it)", name, mode)
			}
		}
	}
}

func TestHookSkipsTheseCommands(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	h.writeConfig("mode = \"install\"\n")
	h.tty()
	for _, cmd := range []string{"version", "completion", "shell-init"} {
		h.mustRun(cmd)
	}
	h.mustRun("--help")
	if h.hits() != 0 {
		t.Errorf("%d requests around version, completion, shell-init and --help", h.hits())
	}
	// `update` runs its own flow and the hook must not run it a second time.
	h.mustRun("update", "--check")
	if h.hits() != 1 {
		t.Errorf("update --check made %d requests, want 1", h.hits())
	}
	// A command that failed is not followed by a check.
	h2 := newHarness(t, "0.1.0")
	h2.publish("v0.2.0")
	h2.writeConfig("mode = \"notify\"\n")
	h2.tty()
	if code := h2.run("fail"); code != ui.ExitFailure {
		t.Fatalf("exit = %d", code)
	}
	if h2.hits() != 0 {
		t.Error("checked for updates after a failed command")
	}
}

func TestHookInstall(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.1.1")
	h.writeConfig("mode = \"install\"\ninterval = \"6h\"\n")
	h.tty()
	h.mustRun("ls")
	if h.exeContent() != string(fakeBinary("0.1.1")) {
		t.Fatalf("exe = %q", h.exeContent())
	}
	has(t, h.errb.String(), "updating 0.1.0 -> 0.1.1")
	has(t, h.errb.String(), "ccshelf updated to 0.1.1")
	has(t, h.out.String(), "ran ls") // the command's own output is untouched
	// A new minor of a 0.x line is only announced.
	h2 := newHarness(t, "0.1.0")
	h2.publish("v0.2.0")
	h2.writeConfig("mode = \"install\"\n")
	h2.tty()
	h2.mustRun("ls")
	if h2.exeContent() != string(fakeBinary("0.1.0")) {
		t.Error("installed a new 0.x minor without being asked")
	}
	has(t, h2.errb.String(), "ccshelf 0.2.0 is available")
	// run never installs: no network at all.
	h3 := newHarness(t, "0.1.0")
	h3.publish("v0.1.1")
	h3.writeConfig("mode = \"install\"\n")
	h3.tty()
	h3.mustRun("run", "web")
	if h3.hits() != 0 || h3.exeContent() != string(fakeBinary("0.1.0")) {
		t.Errorf("run in install mode: %d requests, exe %q", h3.hits(), h3.exeContent())
	}
}

func TestHookNeverFailsTheCommand(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	h.writeConfig("mode = \"notify\"\n")
	h.tty()
	h.srv.Handler = func(w http.ResponseWriter, _ *http.Request) bool {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	}
	h.mustRun("ls")
	has(t, h.errb.String(), "could not check for updates")
	// A broken config is not the hook's business: silently nothing.
	if err := os.WriteFile(h.cfgPath, []byte("[update\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.mustRun("show")
	hasNot(t, h.errb.String(), "update")
	// An unusable state directory does not matter either.
	h.writeConfig("mode = \"notify\"\n")
	h.opt.StateDir = "/proc/does/not/exist/\x00"
	h.mustRun("ls")
}

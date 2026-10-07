package updatecmd

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
	"github.com/yorch/ccshelf/internal/update"
	"github.com/yorch/ccshelf/internal/update/updatetest"
)

func has(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("output lacks %q:\n%s", want, got)
	}
}

func hasNot(t *testing.T, got, bad string) {
	t.Helper()
	if strings.Contains(got, bad) {
		t.Errorf("output must not contain %q:\n%s", bad, got)
	}
}

func downloads(h *harness) int {
	n := 0
	for _, p := range h.srv.Hits() {
		if strings.Contains(p, "/releases/download/") {
			n++
		}
	}
	return n
}

func TestCheck(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")

	h.mustRun("update", "--check")
	has(t, h.out.String(), "current: 0.1.0")
	has(t, h.out.String(), "latest:  0.2.0")
	has(t, h.out.String(), "an update is available: ")
	has(t, h.out.String(), "/releases/tag/v0.2.0")
	has(t, h.out.String(), "run: ccshelf update")
	if downloads(h) != 0 {
		t.Error("--check must not download anything")
	}
	if h.exeContent() != string(fakeBinary("0.1.0")) {
		t.Error("--check changed the binary")
	}

	h.mustRun("--json", "update", "--check")
	kind, data := h.envelope()
	if kind != "update" || data["action"] != "check" || data["current"] != "0.1.0" || data["latest"] != "0.2.0" ||
		data["updateAvailable"] != true || data["installMethod"] != "manual" || !strings.HasSuffix(data["releaseUrl"].(string), "/releases/tag/v0.2.0") {
		t.Errorf("envelope = %v %v", kind, data)
	}

	// Up to date: exit 0 as well.
	h.cur = "0.2.0"
	h.mustRun("update", "--check")
	has(t, h.out.String(), "ccshelf is up to date")
	h.mustRun("--json", "update", "--check")
	if _, data := h.envelope(); data["updateAvailable"] != false {
		t.Errorf("up to date envelope = %v", data)
	}
	// A development build shows the latest for reference.
	h.cur = "dev"
	h.mustRun("update", "--check")
	has(t, h.out.String(), "development build")
}

func TestCheckFailureIsExitOne(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.srv.Handler = nil
	// Nothing published: the server answers 404 for "latest".
	code := h.run("update", "--check")
	if code != ui.ExitFailure {
		t.Fatalf("exit = %d, want 1", code)
	}
	has(t, h.errb.String(), "hint: ")
}

func TestUpdateYes(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	h.mustRun("update", "--yes")
	if h.exeContent() != string(fakeBinary("0.2.0")) {
		t.Fatalf("exe = %q", h.exeContent())
	}
	b, _ := os.ReadFile(h.exe + ".old")
	if string(b) != string(fakeBinary("0.1.0")) {
		t.Errorf("backup = %q", b)
	}
	e := h.errb.String()
	has(t, e, "updated ccshelf 0.1.0 -> 0.2.0")
	has(t, e, "SHA-256 (cosign was not found")
	has(t, e, "ccshelf update --rollback")
	if h.out.Len() != 0 {
		t.Errorf("stdout must stay empty for a human run, got %q", h.out)
	}

	// JSON: only the envelope on stdout.
	h2 := newHarness(t, "0.1.0")
	h2.publish("v0.2.0")
	h2.mustRun("--json", "update", "--yes")
	kind, data := h2.envelope()
	if kind != "update" || data["action"] != "updated" || data["signature"] != "none" || data["backup"] != h2.exe+".old" || data["executable"] != h2.exe {
		t.Errorf("envelope = %v %v", kind, data)
	}
	hasNot(t, h2.errb.String(), "downloading")
}

func TestUpdateUpToDate(t *testing.T) {
	h := newHarness(t, "0.2.0")
	h.publish("v0.2.0")
	h.mustRun("update")
	has(t, h.errb.String(), "up to date")
	if downloads(h) != 0 {
		t.Error("nothing to download")
	}
	h.mustRun("--json", "update")
	if _, data := h.envelope(); data["action"] != "up-to-date" {
		t.Errorf("envelope = %v", data)
	}
	// Even without --yes and without a terminal: nothing to confirm.
	h.mustRun("--no-interactive", "update")
}

func TestNonInteractiveNeedsYes(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	code := h.run("update")
	if code != ui.ExitUsage {
		t.Fatalf("exit = %d, want 2", code)
	}
	has(t, h.errb.String(), "--yes")
	if h.exeContent() != string(fakeBinary("0.1.0")) || downloads(h) != 0 {
		t.Error("nothing may be downloaded or replaced without --yes")
	}
	if code := h.run("--no-interactive", "update"); code != ui.ExitUsage {
		t.Errorf("--no-interactive: exit %d", code)
	}
	if code := h.run("--json", "update"); code != ui.ExitUsage {
		t.Errorf("--json without --yes: exit %d", code)
	}
}

func TestInteractiveConfirm(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")

	h.prompt = ui.NewScripted(false)
	h.mustRun("update")
	has(t, h.errb.String(), "ccshelf 0.1.0 -> 0.2.0")
	has(t, h.errb.String(), "release notes: ")
	has(t, h.errb.String(), "not updated")
	if h.exeContent() != string(fakeBinary("0.1.0")) || downloads(h) != 0 {
		t.Error("a declined update changed something")
	}

	sc := ui.NewScripted(true)
	h.prompt = sc
	h.mustRun("update")
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	if h.exeContent() != string(fakeBinary("0.2.0")) {
		t.Error("not updated")
	}
	// R6: the equivalent flag command.
	has(t, h.errb.String(), "Equivalent: ccshelf update --version v0.2.0 --yes")
	if len(sc.Asked) != 1 || !strings.Contains(sc.Asked[0], "0.2.0") {
		t.Errorf("asked %v", sc.Asked)
	}
}

func TestInteractiveAbort(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	h.prompt = ui.NewScripted(ui.ErrAborted)
	if code := h.run("update"); code != ui.ExitInterrupted {
		t.Errorf("exit = %d, want 130", code)
	}
}

func TestDryRun(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	h.mustRun("update", "--dry-run")
	o := h.out.String()
	has(t, o, "dry run: ccshelf 0.1.0 -> 0.2.0")
	has(t, o, "/releases/download/v0.2.0/"+updatetest.ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH))
	has(t, o, "SHA-256 against checksums.txt")
	has(t, o, "cosign was not found")
	has(t, o, h.exe)
	if downloads(h) != 0 || h.exeContent() != string(fakeBinary("0.1.0")) {
		t.Error("--dry-run changed or downloaded something")
	}
	h.mustRun("--json", "update", "--dry-run")
	if _, data := h.envelope(); data["action"] != "dry-run" || data["signature"] != "none" || data["executable"] != h.exe {
		t.Errorf("envelope = %v", data)
	}
	// With cosign available the plan says so.
	h.opt.LookCosign = func() (string, bool) { return "/usr/bin/cosign", true }
	h.mustRun("update", "--dry-run")
	has(t, h.out.String(), "cosign signature of checksums.txt (/usr/bin/cosign)")
}

func TestVersionFlag(t *testing.T) {
	h := newHarness(t, "0.2.0")
	h.publish("v0.1.0")
	h.publish("v0.2.0")
	h.publish("v0.3.0")
	h.srv.SetLatest("v0.3.0")

	h.mustRun("update", "--version", "v0.3.0", "--yes")
	if h.exeContent() != string(fakeBinary("0.3.0")) {
		t.Fatal("not installed")
	}
	// Downgrade: refused, with the flag named, and nothing changes.
	h.cur = "0.3.0"
	code := h.run("update", "--version", "v0.1.0", "--yes")
	if code != ui.ExitUsage {
		t.Fatalf("downgrade exit = %d, want 2", code)
	}
	has(t, h.errb.String(), "--allow-downgrade")
	if h.exeContent() != string(fakeBinary("0.3.0")) {
		t.Error("a refused downgrade changed the binary")
	}
	h.mustRun("update", "--version", "v0.1.0", "--allow-downgrade", "--yes")
	if h.exeContent() != string(fakeBinary("0.1.0")) {
		t.Error("allowed downgrade not installed")
	}
	// Bad versions and impossible combinations are usage errors. (The running
	// version is 0.1.0 now, so v0.2.0 would be a plain upgrade without them.)
	h.cur = "0.1.0"
	for _, args := range [][]string{
		{"update", "--version", "banana"},
		{"update", "--version", "v1"},
		{"update", "--version", "v0.2.0", "--prerelease", "--yes"},
		{"update", "--rollback", "--check", "--yes"},
		{"update", "--rollback", "--version", "v0.2.0", "--yes"},
		{"update", "--rollback", "--prerelease", "--yes"},
		{"update", "--rollback", "--allow-downgrade", "--yes"},
		{"update", "--rollback", "--require-signature", "--yes"},
	} {
		if code := h.run(args...); code != ui.ExitUsage {
			t.Errorf("%v: exit %d, want 2 (%s)", args, code, h.errb)
		}
	}
	if code := h.run("update", "extra"); code == 0 {
		t.Error("a stray argument must be rejected")
	}
	// A version that does not exist is a failure with a hint.
	if code := h.run("update", "--version", "v9.9.9", "--yes"); code != ui.ExitFailure {
		t.Errorf("unknown version: exit %d", code)
	}
	has(t, h.errb.String(), "v9.9.9")
}

func TestPrerelease(t *testing.T) {
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	h.publish("v0.3.0-rc.1")
	h.srv.SetLatest("v0.2.0")
	h.mustRun("update", "--check")
	has(t, h.out.String(), "latest:  0.2.0")
	h.mustRun("update", "--check", "--prerelease")
	has(t, h.out.String(), "latest:  0.3.0-rc.1")
}

func TestFailuresChangeNothing(t *testing.T) {
	t.Run("server error", func(t *testing.T) {
		h := newHarness(t, "0.1.0")
		h.publish("v0.2.0")
		h.srv.Handler = func(w http.ResponseWriter, _ *http.Request) bool {
			w.WriteHeader(http.StatusBadGateway)
			return true
		}
		if code := h.run("update", "--yes"); code != ui.ExitFailure {
			t.Errorf("exit = %d, want 1", code)
		}
		has(t, h.errb.String(), "hint: check your network")
		if h.exeContent() != string(fakeBinary("0.1.0")) {
			t.Error("the binary changed")
		}
	})
	t.Run("wrong checksum", func(t *testing.T) {
		h := newHarness(t, "0.1.0")
		r := h.publish("v0.2.0")
		name := updatetest.ArchiveName("v0.2.0", runtime.GOOS, runtime.GOARCH)
		r.Assets["checksums.txt"] = []byte(strings.Repeat("0", 64) + "  " + name + "\n")
		if code := h.run("update", "--yes"); code != ui.ExitFailure {
			t.Errorf("exit = %d, want 1", code)
		}
		has(t, h.errb.String(), "checksum mismatch")
		has(t, h.errb.String(), "nothing was changed")
		if h.exeContent() != string(fakeBinary("0.1.0")) {
			t.Error("the binary changed")
		}
		if _, err := os.Stat(h.exe + ".old"); err == nil {
			t.Error("a backup exists although nothing was replaced")
		}
	})
	t.Run("require-signature without cosign", func(t *testing.T) {
		h := newHarness(t, "0.1.0")
		h.publish("v0.2.0")
		if code := h.run("update", "--yes", "--require-signature"); code != ui.ExitFailure {
			t.Errorf("exit = %d, want 1", code)
		}
		has(t, h.errb.String(), "cosign")
		if downloads(h) != 0 {
			t.Error("downloaded before the signature requirement was met")
		}
	})
	t.Run("a cosign that rejects the signature", func(t *testing.T) {
		h := newHarness(t, "0.1.0")
		r := h.publish("v0.2.0")
		r.Assets["checksums.txt.sigstore.json"] = []byte("{}")
		h.opt.LookCosign = func() (string, bool) { return "/fake/cosign", true }
		h.opt.VerifyCosign = func(context.Context, string, []string, string, string, string, string) error {
			return update.ErrSignature
		}
		if code := h.run("update", "--yes"); code != ui.ExitFailure {
			t.Errorf("exit = %d, want 1", code)
		}
		has(t, h.errb.String(), "signature")
		if h.exeContent() != string(fakeBinary("0.1.0")) {
			t.Error("the binary changed")
		}
	})
	t.Run("the real network is never reached", func(t *testing.T) {
		h := newHarness(t, "0.1.0")
		// A broken config falls back to the defaults, which name github.com;
		// the test client refuses it.
		if err := os.WriteFile(h.cfgPath, []byte("[update]\nbase_url = \"http://not-https.example.com\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if code := h.run("update", "--check"); code != ui.ExitFailure {
			t.Errorf("exit = %d", code)
		}
		has(t, h.errb.String(), "ignoring the configuration file")
		has(t, h.errb.String(), "test network guard")
	})
}

func TestPackageManagedAndDev(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Homebrew rule is for darwin and linux")
	}
	t.Run("homebrew", func(t *testing.T) {
		h := newHarness(t, "0.1.0")
		h.publish("v0.2.0")
		cellar := filepath.Join(t.TempDir(), "Cellar", "ccshelf", "0.1.0", "bin")
		if err := os.MkdirAll(cellar, 0o755); err != nil {
			t.Fatal(err)
		}
		exe := filepath.Join(cellar, "ccshelf")
		if err := os.WriteFile(exe, fakeBinary("0.1.0"), 0o755); err != nil {
			t.Fatal(err)
		}
		h.exe = exe
		code := h.run("update", "--yes")
		if code != ui.ExitFailure {
			t.Fatalf("exit = %d, want 1", code)
		}
		has(t, h.errb.String(), "hint: run: brew upgrade ccshelf")
		has(t, h.errb.String(), "--force")
		if h.exeContent() != string(fakeBinary("0.1.0")) {
			t.Error("a package-managed binary was replaced")
		}
		// --check says the right command.
		h.mustRun("update", "--check")
		has(t, h.out.String(), "installed with homebrew; run: brew upgrade ccshelf")
		h.mustRun("update", "--yes", "--force")
		if h.exeContent() != string(fakeBinary("0.2.0")) {
			t.Error("--force did not replace it")
		}
	})
	t.Run("dev build", func(t *testing.T) {
		h := newHarness(t, "dev")
		h.publish("v0.2.0")
		if code := h.run("update", "--yes"); code != ui.ExitFailure {
			t.Fatalf("exit = %d, want 1", code)
		}
		has(t, h.errb.String(), "development build")
		h.mustRun("update", "--yes", "--force")
		if h.exeContent() != string(fakeBinary("0.2.0")) {
			t.Error("--force did not replace the development build")
		}
	})
}

func TestNotWritableGivesTheExactCommand(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions and a non-root user")
	}
	h := newHarness(t, "0.1.0")
	h.publish("v0.2.0")
	if err := os.Chmod(h.binDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.binDir, 0o700) })
	if code := h.run("update", "--yes"); code != ui.ExitFailure {
		t.Fatalf("exit = %d, want 1\n%s", code, h.errb)
	}
	has(t, h.errb.String(), "hint: "+h.binDir+" is not writable by you; run: sudo "+h.exe+" update --yes --version v0.2.0")
	if downloads(h) != 0 {
		t.Error("downloaded into an unwritable directory")
	}
}

func TestElevatedAdvice(t *testing.T) {
	for _, tc := range []struct {
		name  string
		goos  string
		dir   string
		flags flags
		plan  *update.Plan
		want  string
	}{
		{
			"unix latest", "linux", "/usr/local/bin",
			flags{},
			&update.Plan{Exe: "/usr/local/bin/ccshelf", Target: update.Release{Tag: "v0.2.0"}},
			"/usr/local/bin is not writable by you; run: sudo /usr/local/bin/ccshelf update --yes --version v0.2.0",
		},
		{
			"unix pinned", "darwin", "/opt/x y",
			flags{version: "v0.5.0"},
			&update.Plan{Exe: "/opt/x y/ccshelf", Target: update.Release{Tag: "v0.5.0"}},
			"sudo '/opt/x y/ccshelf' update --yes --version v0.5.0",
		},
		{
			"unix rollback", "linux", "/usr/local/bin",
			flags{rollback: true},
			nil,
			"run: sudo /usr/local/bin/ccshelf update --rollback --yes",
		},
		{
			"windows", "windows", `C:\Program Files\ccshelf`,
			flags{},
			&update.Plan{Exe: `C:\Program Files\ccshelf\ccshelf.exe`, Target: update.Release{Tag: "v0.2.0"}},
			`run this in a terminal started as Administrator: `,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cc := &clicore.Context{Env: &clicore.Env{GOOS: tc.goos}}
			got := elevatedAdvice(cc, tc.dir, &tc.flags, tc.plan)
			if !strings.Contains(got, tc.want) {
				t.Errorf("advice = %q, want it to contain %q", got, tc.want)
			}
			if tc.goos == "windows" && (strings.Contains(got, "sudo") || !strings.Contains(got, "ccshelf.exe") || !strings.Contains(got, "update --yes --version v0.2.0")) {
				t.Errorf("windows advice = %q", got)
			}
		})
	}
}

func TestDowngradeIsRefusedBeforeAsking(t *testing.T) {
	h := newHarness(t, "0.3.0")
	h.publish("v0.1.0")
	h.publish("v0.3.0")
	sc := ui.NewScripted() // no answer: any question is a failure
	h.prompt = sc
	if code := h.run("update", "--version", "v0.1.0"); code != ui.ExitUsage {
		t.Fatalf("exit = %d, want 2\n%s", code, h.errb)
	}
	if len(sc.Asked) != 0 {
		t.Errorf("asked %v: a downgrade must be refused before the confirmation", sc.Asked)
	}
	has(t, h.errb.String(), "--allow-downgrade")
	if h.hits() > 2 || downloads(h) != 0 {
		t.Errorf("nothing may be downloaded: %v", h.srv.Hits())
	}
}

func TestPlainHTTPBaseURLIsRefusedWhateverTheConfigSays(t *testing.T) {
	h := newHarness(t, "0.1.0")
	c := &command{opt: h.opt}
	c.opt.AllowLoopbackHTTP = false
	cfg := config.Default()
	cfg.Update.BaseURL = "http://127.0.0.1:9" // built in memory, past the config check
	env := &clicore.Env{Getenv: func(string) string { return "" }, Environ: func() []string { return nil }, Now: time.Now, GOOS: "linux"}
	if _, err := c.newUpdater(&clicore.Context{Env: env, G: &h.g}, cfg); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("newUpdater = %v, want a refusal of plain http", err)
	}
	cfg.Update.BaseURL = h.srv.URL()
	if _, err := c.newUpdater(&clicore.Context{Env: env, G: &h.g}, cfg); err != nil {
		t.Errorf("https base URL refused: %v", err)
	}
}

package update

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type said struct {
	mu    sync.Mutex
	lines []string
}

func (s *said) say(l string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, l)
}

func (s *said) String() string { return strings.Join(s.lines, "\n") }

func notify(tty bool) AutoOptions {
	return AutoOptions{Mode: "notify", Interval: 24 * time.Hour, TTY: tty}
}

func install(tty bool) AutoOptions {
	return AutoOptions{Mode: "install", Interval: 24 * time.Hour, TTY: tty}
}

func TestAutoOffDoesNothing(t *testing.T) {
	for name, o := range map[string]AutoOptions{
		"mode off":     {Mode: "off", TTY: true},
		"mode empty":   {TTY: true},
		"mode unknown": {Mode: "always", TTY: true},
		"CI":           {Mode: "notify", TTY: true, CI: true},
		"CI install":   {Mode: "install", TTY: true, CI: true},
		"kill switch":  {Mode: "notify", TTY: true, KillSwitch: true},
		"kill install": {Mode: "install", TTY: true, KillSwitch: true},
	} {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.2.0", false)
		var s said
		fx.u.Auto(context.Background(), o, s.say)
		fx.u.CachedNotice(o, s.say)
		if fx.srv.hitCount() != 0 || len(s.lines) != 0 {
			t.Errorf("%s: %d requests, output %q", name, fx.srv.hitCount(), s.String())
		}
		if _, err := os.Stat(filepath.Join(fx.state, StateName)); err == nil {
			t.Errorf("%s: a state file was written", name)
		}
	}
}

func TestAutoNotify(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	ctx := context.Background()
	var s said
	fx.u.Auto(ctx, notify(true), s.say)
	want := "ccshelf 0.2.0 is available (you have 0.1.0). Run: ccshelf update"
	if s.String() != want {
		t.Fatalf("output = %q, want %q", s.String(), want)
	}
	hits := fx.srv.hitCount()
	if hits != 1 {
		t.Errorf("requests = %d, want one metadata request", hits)
	}
	st := LoadState(fx.state, fx.now)
	if st.Latest != "0.2.0" || !st.LastCheck.Equal(fx.now) || !st.NotifiedAt.Equal(fx.now) {
		t.Errorf("state = %+v", st)
	}

	// Within the interval: no network, and the line is not repeated.
	fx.now = fx.now.Add(2 * time.Hour)
	s = said{}
	fx.u.Auto(ctx, notify(true), s.say)
	if fx.srv.hitCount() != hits || len(s.lines) != 0 {
		t.Errorf("within the interval: %d new requests, output %q", fx.srv.hitCount()-hits, s.String())
	}

	// Past the interval: asks again and says it again (a day has passed).
	fx.now = fx.now.Add(23 * time.Hour)
	s = said{}
	fx.u.Auto(ctx, notify(true), s.say)
	if fx.srv.hitCount() != hits+1 || s.String() != want {
		t.Errorf("after the interval: %d new requests, output %q", fx.srv.hitCount()-hits, s.String())
	}

	// Up to date: asks, says nothing.
	fx.u.Current = "0.2.0"
	fx.now = fx.now.Add(48 * time.Hour)
	s = said{}
	fx.u.Auto(ctx, notify(true), s.say)
	if len(s.lines) != 0 {
		t.Errorf("up to date but said %q", s.String())
	}
}

func TestAutoNotifyWithoutATerminalIsSilentAndOffline(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	var s said
	fx.u.Auto(context.Background(), notify(false), s.say)
	fx.u.CachedNotice(notify(false), s.say)
	if fx.srv.hitCount() != 0 || len(s.lines) != 0 {
		t.Errorf("%d requests, output %q: with nobody to tell, notify does nothing", fx.srv.hitCount(), s.String())
	}
}

func TestAutoNoticeNamesThePackageManager(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Homebrew rule is for darwin and linux")
	}
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	cellar := filepath.Join(t.TempDir(), "Cellar", "ccshelf", "0.1.0")
	if err := os.MkdirAll(cellar, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := writeExe(t, cellar, "ccshelf", string(fakeBinary("0.1.0")), 0o755)
	fx.u.Executable = func() (string, error) { return exe, nil }
	var s said
	fx.u.Auto(context.Background(), notify(true), s.say)
	if !strings.HasSuffix(s.String(), "Run: brew upgrade ccshelf") {
		t.Errorf("output = %q", s.String())
	}
}

func TestAutoIntervalAndStateRobustness(t *testing.T) {
	ctx := context.Background()
	t.Run("interval honored exactly", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.2.0", false)
		o := notify(true)
		o.Interval = 6 * time.Hour
		var s said
		fx.u.Auto(ctx, o, s.say)
		n := fx.srv.hitCount()
		fx.now = fx.now.Add(6*time.Hour - time.Second)
		fx.u.Auto(ctx, o, s.say)
		if fx.srv.hitCount() != n {
			t.Error("asked again before the interval elapsed")
		}
		fx.now = fx.now.Add(time.Second)
		fx.u.Auto(ctx, o, s.say)
		if fx.srv.hitCount() != n+1 {
			t.Error("did not ask once the interval elapsed")
		}
	})
	t.Run("default interval is a day", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.2.0", false)
		o := AutoOptions{Mode: "notify", TTY: true}
		fx.u.Auto(ctx, o, func(string) {})
		fx.now = fx.now.Add(23 * time.Hour)
		fx.u.Auto(ctx, o, func(string) {})
		if fx.srv.hitCount() != 1 {
			t.Errorf("requests = %d after 23h, want 1", fx.srv.hitCount())
		}
		fx.now = fx.now.Add(2 * time.Hour)
		fx.u.Auto(ctx, o, func(string) {})
		if fx.srv.hitCount() != 2 {
			t.Errorf("requests = %d after 25h, want 2", fx.srv.hitCount())
		}
	})
	for name, content := range map[string]string{
		"corrupt":   "{{{",
		"unknown":   `{"last_check":"2026-10-06T11:59:00Z","latest":"0.2.0","uid":"x"}`,
		"future":    `{"last_check":"2099-01-01T00:00:00Z"}`,
		"bad ver":   `{"latest":"nope"}`,
		"empty":     "",
		"directory": "<dir>",
	} {
		t.Run("state "+name, func(t *testing.T) {
			fx := newFixture(t, "0.1.0")
			fx.release("v0.2.0", false)
			p := filepath.Join(fx.state, StateName)
			if content == "<dir>" {
				if err := os.Mkdir(p, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			var s said
			fx.u.Auto(ctx, notify(true), s.say)
			if !strings.Contains(s.String(), "0.2.0 is available") {
				t.Errorf("a bad state file must act like a first run, got %q", s.String())
			}
		})
	}
	t.Run("an unusable state directory never breaks the command", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.2.0", false)
		fx.u.StateDir = filepath.Join(t.TempDir(), "does", "not", "exist")
		var s said
		fx.u.Auto(ctx, notify(true), s.say) // must not panic or hang
		fx.u.CachedNotice(notify(true), s.say)
	})
}

func TestAutoFailuresAreQuietAndThrottled(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	fx.srv.handler = func(w http.ResponseWriter, _ *http.Request) bool {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	}
	var s said
	fx.u.Auto(ctx, notify(true), s.say)
	if len(s.lines) != 1 || !strings.HasPrefix(s.lines[0], "ccshelf: could not check for updates") {
		t.Fatalf("output = %q: one warning line at most", s.String())
	}
	if strings.Contains(s.String(), "\n") {
		t.Error("the warning must be one line")
	}
	n := fx.srv.hitCount()
	s = said{}
	fx.now = fx.now.Add(time.Hour)
	fx.u.Auto(ctx, notify(true), s.say)
	if fx.srv.hitCount() != n || len(s.lines) != 0 {
		t.Errorf("a failure must be retried only after the interval: %d requests, %q", fx.srv.hitCount()-n, s.String())
	}
	// A failed automatic install (a release without the asset) says so once
	// on a terminal and still advances LastCheck.
	fx2 := newFixture(t, "0.1.0")
	r := fx2.release("v0.1.1", false)
	delete(r.assets, ChecksumsName)
	var s2 said
	fx2.u.Auto(ctx, install(true), s2.say)
	if st := LoadState(fx2.state, fx2.now); st.LastCheck.IsZero() {
		t.Error("a failed check must still advance LastCheck so that it is not retried on every command")
	}
	if readFile(t, fx2.exe) != string(fakeBinary("0.1.0")) {
		t.Error("a failed install changed the binary")
	}
}

func TestAutoHardTimeout(t *testing.T) {
	if AutoCheckTimeout != 3*time.Second {
		t.Errorf("AutoCheckTimeout = %s, want 3s", AutoCheckTimeout)
	}
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	release := make(chan struct{})
	defer close(release)
	fx.srv.handler = func(w http.ResponseWriter, r *http.Request) bool {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		return true
	}
	o := notify(true)
	o.CheckTimeout = 150 * time.Millisecond
	var s said
	start := time.Now()
	fx.u.Auto(context.Background(), o, s.say)
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %s: the hard timeout did not hold", d)
	}
	if len(s.lines) != 1 || !strings.Contains(s.lines[0], "timed out") {
		t.Errorf("output = %q", s.String())
	}
}

func TestAutoInstall(t *testing.T) {
	ctx := context.Background()
	t.Run("same minor: installs, takes effect next time", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.1.1", false)
		var s said
		fx.u.Auto(ctx, install(true), s.say)
		if readFile(t, fx.exe) != string(fakeBinary("0.1.1")) || readFile(t, fx.exe+".old") != string(fakeBinary("0.1.0")) {
			t.Fatalf("exe=%q backup=%q", readFile(t, fx.exe), readFile(t, fx.exe+".old"))
		}
		if !strings.Contains(s.String(), "updating 0.1.0 -> 0.1.1") || !strings.Contains(s.String(), "ccshelf updated to 0.1.1") || !strings.Contains(s.String(), "next time") {
			t.Errorf("output = %q", s.String())
		}
		if fx.u.Current != "0.1.0" {
			t.Error("the running process keeps its own version")
		}
		fx.noLeftovers()
		// And nothing more to do on the next command.
		s = said{}
		fx.u.Current = "0.1.1"
		fx.now = fx.now.Add(48 * time.Hour)
		fx.u.Auto(ctx, install(true), s.say)
		if len(s.lines) != 0 {
			t.Errorf("up to date but said %q", s.String())
		}
	})
	t.Run("does nothing without a terminal: no network, no swap, no output", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.1.1", false)
		var s said
		fx.u.Auto(ctx, install(false), s.say)
		if fx.srv.hitCount() != 0 {
			t.Errorf("%d requests without a terminal", fx.srv.hitCount())
		}
		if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
			t.Error("a script, cron job or pipeline had its binary swapped")
		}
		if _, err := os.Stat(fx.exe + ".old"); err == nil {
			t.Error("a backup exists: something was replaced")
		}
		if len(s.lines) != 0 {
			t.Errorf("output = %q", s.String())
		}
		if st := LoadState(fx.state, fx.now); !st.LastCheck.IsZero() {
			t.Error("nothing was checked, so no state may be recorded")
		}
	})
	for name, tc := range map[string]struct{ cur, latest string }{
		"0.x new minor":     {"0.1.0", "0.2.0"},
		"new major from 0":  {"0.1.0", "1.0.0"},
		"new major":         {"1.2.3", "2.0.0"},
		"older than us":     {"1.2.3", "1.2.2"},
		"same version":      {"1.2.3", "1.2.3"},
		"prerelease latest": {"1.2.3", "1.3.0-rc.1"},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			fx := newFixture(t, tc.cur)
			fx.release("v"+tc.latest, false)
			var s said
			fx.u.Auto(ctx, install(true), s.say)
			if readFile(t, fx.exe) != string(fakeBinary(tc.cur)) {
				t.Errorf("the binary changed to %q", readFile(t, fx.exe))
			}
			if _, err := os.Stat(fx.exe + ".old"); err == nil {
				t.Error("a backup exists: something was replaced")
			}
			if strings.Contains(s.String(), "updated") {
				t.Errorf("output = %q", s.String())
			}
			lv, _ := ParseVersion(tc.latest)
			cv, _ := ParseVersion(tc.cur)
			if lv.Compare(cv) > 0 && !lv.IsPrerelease() && !strings.Contains(s.String(), "is available") {
				t.Errorf("a release that is not auto-installable should still be announced: %q", s.String())
			}
		})
	}
	t.Run("a release the publisher flags as pre-release is never installed", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		r := fx.release("v0.1.1", false) // a stable-looking tag
		r.prerelease = true
		var s said
		fx.u.Auto(ctx, install(true), s.say)
		if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
			t.Error("a pre-release was installed automatically")
		}
	})
	t.Run("package managed: notice only", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the Homebrew rule is for darwin and linux")
		}
		fx := newFixture(t, "0.1.0")
		fx.release("v0.1.1", false)
		cellar := filepath.Join(t.TempDir(), "Cellar", "ccshelf", "0.1.0")
		if err := os.MkdirAll(cellar, 0o755); err != nil {
			t.Fatal(err)
		}
		exe := writeExe(t, cellar, "ccshelf", string(fakeBinary("0.1.0")), 0o755)
		fx.u.Executable = func() (string, error) { return exe, nil }
		var s said
		fx.u.Auto(ctx, install(true), s.say)
		if readFile(t, exe) != string(fakeBinary("0.1.0")) {
			t.Error("a package-managed binary was replaced")
		}
		if !strings.Contains(s.String(), "brew upgrade ccshelf") {
			t.Errorf("output = %q", s.String())
		}
	})
	t.Run("dev build: nothing at all", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		fx.u.Current = "dev"
		fx.release("v0.1.1", false)
		var s said
		fx.u.Auto(ctx, install(true), s.say)
		if fx.srv.hitCount() != 0 || len(s.lines) != 0 {
			t.Errorf("%d requests, %q", fx.srv.hitCount(), s.String())
		}
	})
	t.Run("verification failure: warning, nothing replaced, not retried at once", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		r := fx.release("v0.1.1", false)
		ar, _ := ArchiveFor(Version{Minor: 1, Patch: 1}, runtime.GOOS, runtime.GOARCH)
		r.assets[ChecksumsName] = []byte(strings.Repeat("0", 64) + "  " + ar.Name + "\n")
		var s said
		fx.u.Auto(ctx, install(true), s.say)
		if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
			t.Error("the binary changed")
		}
		if !strings.Contains(s.String(), "automatic update failed") || !strings.Contains(s.String(), "ccshelf update") {
			t.Errorf("output = %q", s.String())
		}
		n := fx.srv.hitCount()
		fx.now = fx.now.Add(time.Hour)
		fx.u.Auto(ctx, install(true), func(string) {})
		if fx.srv.hitCount() != n {
			t.Error("retried within the interval")
		}
		fx.noLeftovers()
	})
	t.Run("another update running: silent", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.1.1", false)
		rel, _ := AcquireLock(fx.state)
		defer rel()
		var s said
		fx.u.Auto(ctx, install(true), s.say)
		if strings.Contains(s.String(), "failed") || readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
			t.Errorf("output = %q", s.String())
		}
	})
	t.Run("unwritable directory: warning", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 {
			t.Skip("needs Unix permissions and a non-root user")
		}
		fx := newFixture(t, "0.1.0")
		fx.release("v0.1.1", false)
		if err := os.Chmod(fx.binDir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(fx.binDir, 0o700) })
		var s said
		fx.u.Auto(ctx, install(true), s.say)
		if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
			t.Error("the binary changed")
		}
		if !strings.Contains(s.String(), "run: ccshelf update") && !strings.Contains(s.String(), "is available") {
			t.Errorf("output = %q", s.String())
		}
	})
	t.Run("install timeout is its own, longer budget", func(t *testing.T) {
		if AutoInstallTimeout <= AutoCheckTimeout {
			t.Error("an install downloads an archive and needs more than the check budget")
		}
	})
}

func TestCachedNotice(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	if err := SaveState(fx.state, State{LastCheck: fx.now.Add(-time.Hour), Latest: "0.2.0"}); err != nil {
		t.Fatal(err)
	}
	var s said
	fx.u.CachedNotice(notify(true), s.say)
	want := "ccshelf 0.2.0 is available (you have 0.1.0). Run: ccshelf update"
	if s.String() != want {
		t.Errorf("output = %q, want %q", s.String(), want)
	}
	if fx.srv.hitCount() != 0 {
		t.Error("run and dry-run must do no network I/O")
	}
	// Not again within 24 hours.
	s = said{}
	fx.now = fx.now.Add(23*time.Hour + 59*time.Minute)
	fx.u.CachedNotice(notify(true), s.say)
	if len(s.lines) != 0 {
		t.Errorf("repeated within 24h: %q", s.String())
	}
	fx.now = fx.now.Add(2 * time.Minute)
	fx.u.CachedNotice(notify(true), s.say)
	if len(s.lines) != 1 {
		t.Errorf("not repeated after 24h: %q", s.String())
	}
	if fx.srv.hitCount() != 0 {
		t.Error("network I/O in CachedNotice")
	}
}

func TestCachedNoticeConditions(t *testing.T) {
	for name, tc := range map[string]struct {
		cur   string
		state State
		opt   AutoOptions
		want  bool
	}{
		"newer cached":     {"0.1.0", State{Latest: "0.2.0"}, notify(true), true},
		"equal":            {"0.2.0", State{Latest: "0.2.0"}, notify(true), false},
		"older cached":     {"0.3.0", State{Latest: "0.2.0"}, notify(true), false},
		"no cache":         {"0.1.0", State{}, notify(true), false},
		"install mode":     {"0.1.0", State{Latest: "0.2.0"}, install(true), false},
		"no terminal":      {"0.1.0", State{Latest: "0.2.0"}, notify(false), false},
		"CI":               {"0.1.0", State{Latest: "0.2.0"}, AutoOptions{Mode: "notify", TTY: true, CI: true}, false},
		"kill switch":      {"0.1.0", State{Latest: "0.2.0"}, AutoOptions{Mode: "notify", TTY: true, KillSwitch: true}, false},
		"dev build":        {"dev", State{Latest: "0.2.0"}, notify(true), false},
		"cached prerelase": {"0.1.0", State{Latest: "0.2.0-rc.1"}, notify(true), false},
	} {
		fx := newFixture(t, "0.1.0")
		fx.u.Current = tc.cur
		if err := SaveState(fx.state, tc.state); err != nil {
			t.Fatal(err)
		}
		var s said
		fx.u.CachedNotice(tc.opt, s.say)
		if got := len(s.lines) > 0; got != tc.want {
			t.Errorf("%s: printed=%v (%q), want %v", name, got, s.String(), tc.want)
		}
		if fx.srv.hitCount() != 0 {
			t.Errorf("%s: network I/O", name)
		}
	}
}

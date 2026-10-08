package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
)

type initWritePrompt struct {
	*ui.Scripted
	h      *harness
	t      *testing.T
	cancel context.CancelFunc
}

func (p *initWritePrompt) Confirm(ctx context.Context, title string, def bool) (bool, error) {
	if title == "Write this configuration?" {
		if def {
			p.t.Fatal("write confirmation default must be no")
		}
		if !strings.Contains(p.h.errb.String(), "Configuration summary (nothing has been written):") {
			p.t.Fatal("confirmation preceded summary")
		}
		if _, err := os.Stat(filepath.Join(p.h.configDir(), "config.toml")); !errors.Is(err, os.ErrNotExist) {
			p.t.Fatalf("config exists before confirmation: %v", err)
		}
		if _, err := os.Stat(filepath.Join(p.h.configDir(), "profiles")); !errors.Is(err, os.ErrNotExist) {
			p.t.Fatalf("profiles exist before confirmation: %v", err)
		}
		if _, err := os.Stat(filepath.Join(p.h.dirs["HOME"], ".claude-work")); !errors.Is(err, os.ErrNotExist) {
			p.t.Fatalf("account exists before confirmation: %v", err)
		}
		if p.cancel != nil {
			p.cancel()
			return true, nil
		}
	}
	return p.Scripted.Confirm(ctx, title, def)
}

func assertInitAbsent(t *testing.T, h *harness) {
	t.Helper()
	for _, path := range []string{filepath.Join(h.configDir(), "config.toml"), filepath.Join(h.configDir(), "profiles"), filepath.Join(h.dirs["HOME"], ".claude-work")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("unexpected write at %s: %v", path, err)
		}
	}
}

func TestInitPromptSummaryAndReplay(t *testing.T) {
	h := newHarness(t)
	url := "https://example.com/acme/data.git"
	sc := ui.NewScripted(url, "v1.0.0", "team-profiles", "", "work", false, true)
	h.prompt = &initWritePrompt{Scripted: sc, h: h, t: t}
	h.mustRun("init")
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Config file:", "Personal profiles:", "Org data repo: " + url, "Pinned ref: v1.0.0", "Profiles folder: team-profiles", "New account: work", "Account directory:", "Automatic updates: off", "No sources are fetched or trusted", "--update-mode off --yes"} {
		if !strings.Contains(h.errb.String(), want) {
			t.Errorf("missing %q in %s", want, h.errb)
		}
	}
	if strings.Contains(h.out.String(), "Configuration summary") {
		t.Fatal("summary leaked to stdout")
	}
	if sc.Asked[len(sc.Asked)-1] != "Write this configuration?" {
		t.Fatalf("asked %v", sc.Asked)
	}
	cfg, err := config.Load(filepath.Join(h.configDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sources) != 1 || cfg.Update.Mode != config.UpdateOff {
		t.Fatalf("config: %+v", cfg)
	}
	// Replay the printed explicit values in another isolated home: no prompt,
	// same choices, no trust or fetch. Account paths are home-relative by design.
	replay := []string{"init", "--git-url", url, "--ref", "v1.0.0", "--path", "team-profiles", "--account-name", "work", "--update-mode", "off", "--yes"}
	if !strings.Contains(h.errb.String(), "Equivalent: ccshelf "+strings.Join(replay, " ")) {
		t.Fatalf("equivalent: %s", h.errb)
	}
	h = newHarness(t)
	sc = ui.NewScripted()
	h.prompt = sc
	h.mustRun(replay...)
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
}

func TestInitPromptDeclineAbortAndCancellationWriteNothing(t *testing.T) {
	for _, answer := range []any{false, ui.ErrAborted, context.Canceled} {
		h := newHarness(t)
		sc := ui.NewScripted("", "", "work", false, answer)
		h.prompt = &initWritePrompt{Scripted: sc, h: h, t: t}
		code := h.run("init")
		want := ui.ExitInterrupted
		if answer == false {
			want = ui.ExitFailure
		}
		if code != want {
			t.Errorf("answer %v: code %d want %d: %s", answer, code, want, h.errb)
		}
		if err := sc.Done(); err != nil {
			t.Fatal(err)
		}
		assertInitAbsent(t, h)
		if strings.Contains(h.errb.String(), "Equivalent:") {
			t.Fatal("unsuccessful write printed equivalent")
		}
	}
	// An ill-behaved prompter returning true after cancellation must not write.
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.ctx = ctx
	h.prompt = &initWritePrompt{Scripted: ui.NewScripted("", "", "work", false), h: h, t: t, cancel: cancel}
	if code := h.run("init"); code != ui.ExitInterrupted {
		t.Fatalf("code %d: %s", code, h.errb)
	}
	assertInitAbsent(t, h)
}

func TestInitPromptEmptyChoicesReplayWithoutWizard(t *testing.T) {
	h := newHarness(t)
	sc := ui.NewScripted("", "", "", false, true)
	h.prompt = sc
	h.mustRun("init")
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errb.String(), "Equivalent: ccshelf init --update-mode off --yes\n") {
		t.Fatalf("equivalent: %s", h.errb)
	}
	h = newHarness(t)
	sc = ui.NewScripted()
	h.prompt = sc
	h.mustRun("init", "--update-mode", "off", "--yes")
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
}

func TestInitPromptYesSkipsOnlyWriteConfirmation(t *testing.T) {
	h := newHarness(t)
	sc := ui.NewScripted("", "", "", false)
	h.prompt = sc
	h.mustRun("init", "--yes")
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	if len(sc.Asked) != 4 || !strings.Contains(h.errb.String(), "Configuration summary") {
		t.Fatalf("asked %v, output %s", sc.Asked, h.errb)
	}
	// Even --yes cannot supply a missing wizard value or accept trust.
	h = newHarness(t)
	sc = ui.NewScripted(ui.ErrAborted)
	h.prompt = sc
	if code := h.run("init", "--yes"); code != ui.ExitInterrupted {
		t.Fatalf("code %d", code)
	}
	assertInitAbsent(t, h)
}

func TestInitPromptPartialFlagsStayPromptFree(t *testing.T) {
	for _, flags := range [][]string{
		{"--update-mode", "off"},
		{"--dir", filepath.Join(t.TempDir(), "mock-profiles")},
		{"--account-name", "work"},
		{"--git-url", "https://example.com/acme/data.git", "--ref", "v1.0.0"},
		{"--update-mode", "notify", "--yes"},
	} {
		h := newHarness(t)
		sc := ui.NewScripted()
		h.prompt = sc
		h.mustRun(append([]string{"init"}, flags...)...)
		if err := sc.Done(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(h.errb.String(), "Configuration summary") || strings.Contains(h.errb.String(), "Equivalent:") {
			t.Fatalf("partial flags prompted: %s", h.errb)
		}
	}
}

func TestInitPromptForceDeclinePreservesConfig(t *testing.T) {
	h := newHarness(t)
	h.mustRun("init", "--update-mode", "install")
	path := filepath.Join(h.configDir(), "config.toml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sc := ui.NewScripted("", "", "", false)
	h.prompt = sc
	if code := h.run("init", "--force"); code != ui.ExitFailure {
		t.Fatalf("code %d: %s", code, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("decline changed existing config")
	}
	sc = ui.NewScripted("", "", "", true)
	h.prompt = sc
	h.mustRun("init", "--force")
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errb.String(), "--update-mode install --yes --force") {
		t.Fatalf("kept update mode missing: %s", h.errb)
	}
}

func TestInitPromptSummarySanitizesValues(t *testing.T) {
	h := newHarness(t)
	cc := &clicore.Context{Env: &clicore.Env{Streams: ui.Streams{Err: h.errb}}}
	cfg := config.Default()
	cfg.Sources = []config.SourceConfig{{Type: config.SourceGit, URL: "u\n\x1b[2J", Ref: "r\r\u202e", Path: "p\t\a"}, {Type: config.SourceDir, Path: "d\n"}}
	printInitSummary(cc, "config\n", "personal\x1b", "account\n", cfg, &initFlags{force: true, accountName: "work\n"})
	for _, line := range strings.Split(strings.TrimSuffix(h.errb.String(), "\n"), "\n") {
		if ui.HasControl(line) {
			t.Errorf("unsafe line %q", line)
		}
	}
	if strings.Count(h.errb.String(), "\n") != 12 {
		t.Fatalf("forged summary lines: %q", h.errb.String())
	}
}

func TestInitPromptCancellationAndInvalidAccountBeforeWrites(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.ctx = ctx
	if code := h.run("init", "--update-mode", "off"); code != ui.ExitInterrupted {
		t.Fatalf("code %d", code)
	}
	assertInitAbsent(t, h)
	h = newHarness(t)
	if code := h.run("init", "--account-name", "Bad Name"); code != ui.ExitUsage {
		t.Fatalf("code %d", code)
	}
	assertInitAbsent(t, h)
}

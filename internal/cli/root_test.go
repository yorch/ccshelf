package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yorch/ccshelf/internal/claude"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/testutil"
	"github.com/yorch/ccshelf/internal/ui"
)

func testEnv(out, errb *bytes.Buffer) *clicore.Env {
	return &clicore.Env{
		Streams: ui.Streams{Out: out, Err: errb},
		Getenv:  func(string) string { return "" },
		Environ: func() []string { return nil },
		Getwd:   func() (string, error) { return ".", nil },
		Now:     time.Now,
		GOOS:    "linux",
	}
}

func TestExecuteUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"nope"}, {"--bogus"}, {"lint", "--bogus"}} {
		var o, e bytes.Buffer
		if got := Execute(context.Background(), testEnv(&o, &e), args); got != ui.ExitUsage {
			t.Errorf("%v: exit %d, want %d (stderr %q)", args, got, ui.ExitUsage, e.String())
		}
	}
}

func TestExecuteVersion(t *testing.T) {
	var o, e bytes.Buffer
	if got := Execute(context.Background(), testEnv(&o, &e), []string{"version"}); got != ui.ExitOK {
		t.Fatalf("exit %d, stderr %q", got, e.String())
	}
	if o.Len() == 0 {
		t.Fatal("version printed nothing")
	}
}

func TestCommandSet(t *testing.T) {
	var o, e bytes.Buffer
	root := NewRoot(testEnv(&o, &e))
	have := map[string]bool{}
	for _, c := range root.Commands() {
		have[c.Name()] = true
	}
	for _, want := range []string{"run", "dry-run", "show", "ls", "diff", "new", "edit", "init", "trust", "account", "shell-init", "version", "update", "lint", "compile", "catalog", "search", "recommend", "doctor"} {
		if !have[want] {
			t.Errorf("missing command %q", want)
		}
	}
}

func TestExitCodeOf(t *testing.T) {
	wrapped := ui.Failure(fmt.Errorf("listing installed plugins: %w", context.Canceled))
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"child status":        {&ui.ExitError{Code: 7}, 7},
		"cancel in failure":   {wrapped, ui.ExitInterrupted},
		"bare cancel":         {fmt.Errorf("x: %w", context.Canceled), ui.ExitInterrupted},
		"aborted prompt":      {ui.Failure(ui.ErrAborted), ui.ExitInterrupted},
		"trust":               {ui.TrustRequired(errors.New("t")), ui.ExitTrust},
		"plain failure":       {ui.Failure(errors.New("f")), ui.ExitFailure},
		"unknown command":     {errors.New(`unknown command "x" for "ccshelf"`), ui.ExitUsage},
		"missing flag":        {ui.MissingFlags("h", "--x"), ui.ExitUsage},
		"unclassified":        {errors.New("boom"), ui.ExitFailure},
		"policy with message": {ui.Policy(errors.New("p")), ui.ExitPolicy},
	} {
		if got := exitCodeOf(tc.err); got != tc.want {
			t.Errorf("%s: %d, want %d", name, got, tc.want)
		}
	}
}

func isolated(t *testing.T) (cfg string) {
	t.Helper()
	d := testutil.IsolatedEnv(t)
	return filepath.Join(d["XDG_CONFIG_HOME"], "ccshelf", "config.toml")
}

func TestExecuteAppliesUIConfigAndFlagsToErrors(t *testing.T) {
	cfg := isolated(t)
	testutil.WriteFile(t, cfg, "[ui]\ncolor = \"always\"\n")
	run := func(extra ...string) (int, string) {
		var o, e bytes.Buffer
		args := append([]string{"--config", cfg}, extra...)
		args = append(args, "show", "ghost")
		code := Execute(context.Background(), testEnv(&o, &e), args)
		return code, e.String()
	}
	code, got := run()
	if code != ui.ExitUsage || !strings.Contains(got, "\x1b[") {
		t.Errorf("config color=always not applied to the error line: %d %q", code, got)
	}
	if _, got = run("--no-color"); strings.Contains(got, "\x1b[") {
		t.Errorf("--no-color ignored on the error line: %q", got)
	}
	code, got = run("--json")
	if code != ui.ExitUsage {
		t.Fatalf("code %d", code)
	}
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Message string `json:"message"`
			Hint    string `json:"hint"`
			Code    int    `json:"code"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &env); err != nil {
		t.Fatalf("--json error is not JSON: %v\n%s", err, got)
	}
	if env.Kind != "error" || env.Data.Code != ui.ExitUsage || !strings.Contains(env.Data.Message, "not found") || env.Data.Hint == "" {
		t.Errorf("json error %+v", env)
	}
}

func TestExecuteBareCommand(t *testing.T) {
	d := testutil.IsolatedEnv(t)
	testutil.WriteFile(t, filepath.Join(d["XDG_CONFIG_HOME"], "ccshelf", "profiles", "mine.toml"), "name = \"mine\"\n")
	claude := testutil.BuildFakeClaude(t)
	t.Setenv("CCSHELF_CLAUDE", claude)

	// Without a terminal: the help, exit 0, nothing started.
	var o, e bytes.Buffer
	if code := Execute(context.Background(), testEnv(&o, &e), nil); code != ui.ExitOK || !strings.Contains(o.String(), "Available Commands") {
		t.Fatalf("no tty: %d\n%s%s", code, o.String(), e.String())
	}

	// With a prompter (a terminal): the run picker.
	var started []string
	claudeStart := &started
	restore := setStartHook(func(bin string, args, env []string) (int, error) {
		*claudeStart = append(*claudeStart, bin)
		return 0, nil
	})
	defer restore()
	o.Reset()
	e.Reset()
	env := testEnv(&o, &e)
	env.Getenv = os.Getenv
	env.Environ = os.Environ
	env.Prompter = ui.NewScripted(0)
	if code := Execute(context.Background(), env, nil); code != ui.ExitOK || len(started) != 1 {
		t.Fatalf("picker: %d started %v\n%s%s", code, started, o.String(), e.String())
	}

	// --no-interactive and --json keep it a help screen even with a prompter.
	for _, flag := range []string{"--no-interactive", "--json"} {
		o.Reset()
		started = nil
		env.Prompter = ui.NewScripted(0)
		if code := Execute(context.Background(), env, []string{flag}); code != ui.ExitOK || len(started) != 0 {
			t.Errorf("%s: %d started %v", flag, code, started)
		}
	}
}

func setStartHook(f func(bin string, args, env []string) (int, error)) func() {
	claude.StartHook = f
	return func() { claude.StartHook = nil }
}

package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/ui"
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
	for _, want := range []string{"run", "dry-run", "show", "ls", "diff", "new", "edit", "init", "trust", "account", "shell-init", "version", "lint", "compile", "catalog", "search", "recommend", "doctor"} {
		if !have[want] {
			t.Errorf("missing command %q", want)
		}
	}
}

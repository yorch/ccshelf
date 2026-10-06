// Package cli assembles the ccshelf command tree: the global flags, the
// launcher commands and the org and catalog commands.
package cli

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/cli/launcher"
	"github.com/ccshelf/ccshelf/internal/cli/orgcmd"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// NewRoot builds the root command. The globals are bound as persistent flags
// and every subcommand receives its Context from the provider once cobra has
// parsed the flags.
func NewRoot(env *clicore.Env) *cobra.Command {
	g := &clicore.Globals{}
	get := func() (*clicore.Context, error) { return env.Context(g, "", ""), nil }

	root := &cobra.Command{
		Use:           "ccshelf",
		Short:         "Run Claude Code with a named profile; lint and publish an org's plugin catalog",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(env.Streams.Out)
	root.SetErr(env.Streams.Err)
	root.CompletionOptions.DisableDefaultCmd = true

	f := root.PersistentFlags()
	f.StringVar(&g.ConfigPath, "config", "", "path to the user config file")
	f.StringVar(&g.ClaudePath, "claude", "", "path to the claude binary")
	f.StringVar(&g.Account, "account", "", "account to use for this invocation")
	f.StringVar(&g.Root, "root", "", "org data repo root (default: current directory)")
	f.BoolVar(&g.NoInteractive, "no-interactive", false, "never prompt; fail naming the missing flag")
	f.BoolVar(&g.NoColor, "no-color", false, "disable color")
	f.BoolVar(&g.Plain, "plain", false, "line-oriented output and prompts")
	f.BoolVar(&g.JSON, "json", false, "machine-readable output where supported")

	root.AddCommand(launcher.Commands(get)...)
	root.AddCommand(orgcmd.Commands(get)...)
	return root
}

// Execute runs the command tree with args and returns the process exit code.
// Errors are reported on the error stream; a child's exit status (an ExitError
// without a message) is returned silently.
func Execute(ctx context.Context, env *clicore.Env, args []string) int {
	root := NewRoot(env)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ui.ExitOK
	}
	var ee *ui.ExitError
	if !(errors.As(err, &ee) && ee.Err == nil) {
		mode := env.Context(&clicore.Globals{}, "", "").Mode
		reportTo(env.Streams.Err, err, mode)
	}
	if isUsage(err) {
		return ui.ExitUsage
	}
	return ui.CodeOf(err)
}

func reportTo(w io.Writer, err error, mode ui.Mode) { ui.Report(w, err, mode) }

// isUsage reports cobra's own parse errors (unknown command or flag), which
// do not carry an ExitError.
func isUsage(err error) bool {
	var ee *ui.ExitError
	var mf *ui.MissingFlagError
	if errors.As(err, &ee) || errors.As(err, &mf) || errors.Is(err, context.Canceled) || errors.Is(err, ui.ErrAborted) {
		return false
	}
	msg := err.Error()
	for _, p := range []string{"unknown command", "unknown flag", "unknown shorthand flag", "flag needs an argument", "invalid argument", "accepts ", "requires at least", "required flag"} {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

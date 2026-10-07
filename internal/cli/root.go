// Package cli assembles the ccshelf command tree: the global flags, the
// launcher commands and the org and catalog commands.
package cli

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/cli/launcher"
	"github.com/yorch/ccshelf/internal/cli/orgcmd"
	"github.com/yorch/ccshelf/internal/cli/updatecmd"
	"github.com/yorch/ccshelf/internal/ui"
	"github.com/yorch/ccshelf/internal/version"
)

// NewRoot builds the root command. The globals are bound as persistent flags
// and every subcommand receives its Context from the provider once cobra has
// parsed the flags.
func NewRoot(env *clicore.Env) *cobra.Command {
	root, _ := newRoot(env)
	return root
}

// newRoot is NewRoot that also returns the globals the flags are bound to, so
// that Execute can report an error in the mode the flags asked for.
func newRoot(env *clicore.Env) (*cobra.Command, *clicore.Globals) {
	g := &clicore.Globals{}
	get := func() (*clicore.Context, error) { //nolint:unparam // the Provider signature allows an error
		color, interactive := clicore.UIPrefs(g)
		return env.Context(g, color, interactive), nil
	}

	root := &cobra.Command{
		Use:   "ccshelf",
		Short: "Run Claude Code with a named profile; lint and publish an org's plugin catalog",
		Long: `Choose which plugins, skills and MCP servers are active for a Claude Code
session, or maintain your organization's plugin catalog.

Getting started: init creates your config; new creates a profile; run launches it.
Run ccshelf without a command in a terminal to pick a profile.
Flags and arguments work without prompts; use --no-interactive in scripts.`,
		Example: `  ccshelf init
  ccshelf new my-profile
  ccshelf ls
  ccshelf run my-profile
  ccshelf show my-profile --json`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	root.SetVersionTemplate("{{.Version}}\n")
	// Bare "ccshelf": the profile picker on a terminal, the help otherwise.
	var run *cobra.Command
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		cc, err := get()
		if err != nil {
			return err
		}
		if _, non := cc.Prompt.(ui.NonInteractive); non || cc.G.NoInteractive || cc.Mode.JSON || run == nil {
			return cmd.Help()
		}
		run.SetContext(cmd.Context())
		return run.RunE(run, nil)
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

	lc := launcher.Commands(get)
	for _, c := range lc {
		if c.Name() == "run" {
			run = c
		}
	}
	root.AddCommand(lc...)
	root.AddCommand(orgcmd.CommandsWith(get, orgcmd.Options{Catalog: launcher.CatalogProvider(launcher.Options{})})...)
	root.AddCommand(updatecmd.Commands(get, updatecmd.Options{})...)
	updatecmd.Hook(root, get, updatecmd.Options{})
	configureHelp(root)
	return root, g
}

// Execute runs the command tree with args and returns the process exit code.
// Errors are reported on the error stream; a child's exit status (an ExitError
// without a message) is returned silently.
func Execute(ctx context.Context, env *clicore.Env, args []string) int {
	root, g := newRoot(env)
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return ui.ExitOK
	}
	code := exitCodeOf(err)
	if code == ui.ExitUsage {
		err = withUsageHint(err, cmd, root)
	}
	var ee *ui.ExitError
	if !(errors.As(err, &ee) && ee.Err == nil) {
		color, interactive := clicore.UIPrefs(g)
		mode := env.Context(g, color, interactive).Mode
		// Cobra can reject a command before parsing any flags. Still honor
		// an explicit --json for these early usage errors.
		if code == ui.ExitUsage && requestsJSON(args, cmd, root) {
			mode.JSON = true
		}
		if mode.JSON {
			reportJSON(env.Streams.Err, err, code)
		} else {
			reportTo(env.Streams.Err, err, mode)
		}
	}
	return code
}

// exitCodeOf maps err to the process exit code. A child's own status (an
// ExitError without a message) is kept as it is; an interruption that arrives
// wrapped in a failure ("listing installed plugins: context canceled") is 130,
// not 1; cobra's own parse errors are usage errors.
func exitCodeOf(err error) int {
	var ee *ui.ExitError
	if errors.As(err, &ee) && ee.Err == nil {
		return ee.Code
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, ui.ErrAborted) {
		return ui.ExitInterrupted
	}
	if isUsage(err) {
		return ui.ExitUsage
	}
	return ui.CodeOf(err)
}

// reportJSON writes the error as one JSON object on the error stream, so a
// script that asked for --json can parse failures as well as results.
func reportJSON(w io.Writer, err error, code int) {
	type out struct {
		Message string `json:"message"`
		Hint    string `json:"hint,omitempty"`
		Code    int    `json:"code"`
		// Data carries what a command knows about a partial failure, such as
		// the files it had written; see errorData.
		Data map[string]any `json:"data,omitempty"`
	}
	o := out{Message: ui.SanitizeLine(err.Error()), Code: code}
	var h interface{ Hint() string }
	if errors.As(err, &h) {
		o.Hint = ui.SanitizeLine(h.Hint())
	}
	var ed interface{ ErrorData() map[string]any }
	if errors.As(err, &ed) {
		o.Data = ed.ErrorData()
	}
	var mf *ui.MissingFlagError
	if o.Hint == "" && errors.As(err, &mf) && mf.Flag != "" {
		o.Hint = "pass " + ui.SanitizeLine(mf.Flag) + ", or run in a terminal without --no-interactive to be asked"
	}
	if werr := ui.WriteJSON(w, "error", o); werr != nil {
		// Fall back to the plain line; there is nothing else to report to.
		ui.Report(w, err, ui.Mode{})
	}
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

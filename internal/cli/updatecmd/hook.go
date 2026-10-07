package updatecmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
	"github.com/yorch/ccshelf/internal/update"
)

// skipAll are the commands around which the automatic update never acts: the
// update command itself, and the ones that print scripts or versions that other
// programs parse or that run in every new shell.
var skipAll = map[string]bool{
	"update": true, "shell-init": true, "completion": true, "version": true, "help": true,
	"__complete": true, "__completeNoDesc": true,
}

// startsClaude are the commands that start Claude Code and must stay fast:
// they never touch the network, they only print the cached notice.
var startsClaude = map[string]bool{"run": true, "dry-run": true}

// Hook installs the automatic-update behavior around every command of root:
// a cached notice before "run" and "dry-run" (and the bare picker), and the
// opt-in periodic check after any other command that succeeded. It does
// nothing at all unless [update] mode is notify or install in config.toml.
func Hook(root *cobra.Command, get clicore.Provider, opt Options) {
	c := &command{get: get, opt: opt}
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if name := topName(cmd); name == "" || startsClaude[name] {
			c.auto(cmd, true)
		}
		return nil
	}
	root.PersistentPostRunE = func(cmd *cobra.Command, _ []string) error {
		if name := topName(cmd); name != "" && !skipAll[name] && !startsClaude[name] {
			c.auto(cmd, false)
		}
		return nil
	}
}

// topName is the first word of the command path after the program name; ""
// for the root command itself.
func topName(cmd *cobra.Command) string {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// auto runs the automatic behavior for one invocation. cachedOnly selects the
// no-network notice. It never returns an error and never prints anything but
// the update lines.
func (c *command) auto(cmd *cobra.Command, cachedOnly bool) {
	cc, err := c.get()
	if err != nil {
		return
	}
	cfg, warn := loadConfig(cc)
	if warn != "" || cfg.Update.EffectiveMode() == config.UpdateOff {
		return
	}
	o := update.AutoOptions{
		Mode:       cfg.Update.EffectiveMode(),
		Interval:   cfg.Update.EffectiveInterval(),
		CI:         cc.Getenv("CI") != "",
		KillSwitch: cc.Getenv(update.KillSwitchEnv) != "",
		TTY:        canPrompt(cc) && c.stderrIsTerminal(cc),
	}
	u, err := c.newUpdater(cc, cfg)
	if err != nil {
		return
	}
	say := func(l string) { fmt.Fprintln(cc.Streams.Err, ui.Sanitize(l)) }
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	if cachedOnly {
		u.CachedNotice(o, say)
		return
	}
	u.Auto(ctx, o, say)
}

// stderrIsTerminal reports whether the error stream is a terminal: the
// automatic update prints there, and a binary must never be swapped (or a
// network call made) when stderr goes to a log or a pipe.
func (c *command) stderrIsTerminal(cc *clicore.Context) bool {
	if c.opt.StderrIsTerminal != nil {
		return c.opt.StderrIsTerminal(cc)
	}
	f, ok := cc.Streams.Err.(*os.File)
	return ok && term.IsTerminal(int(f.Fd())) //nolint:gosec // fd fits in int
}

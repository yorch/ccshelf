package launcher

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/account"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
)

func (l *launcher) accountCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "account",
		Short: "Manage named Claude Code accounts (separate CLAUDE_CONFIG_DIR)",
		Long: `An account is a name for a separate Claude Code configuration directory, so
work and personal logins, plugins and history stay apart. ccshelf never reads,
copies or moves credentials and never deletes a directory.`,
	}
	c.AddCommand(l.accountAddCmd(), l.accountLsCmd(), l.accountRmCmd())
	return c
}

func (l *launcher) accountAddCmd() *cobra.Command {
	var dir string
	var marketplaces, plugins []string
	var makeDefault bool
	c := &cobra.Command{
		Use:   "add [name]",
		Short: "Add an account and print the one-time steps to log in",
		Long: `Create the account's configuration directory (mode 0700), save it in the
configuration and print the one-time steps you run yourself: start claude with
the variable set, /login, then marketplace adds and plugin installs inside it.
The directory defaults to ~/.claude-<name>.`,
		Example: `  ccshelf account add work
  ccshelf account add personal --dir ~/.claude-personal --default`,
		Args: cobra.MaximumNArgs(1),
	}
	c.Flags().StringVar(&dir, "dir", "", "configuration directory (default ~/.claude-<name>)")
	c.Flags().StringArrayVar(&marketplaces, "marketplace", nil, "marketplace to add inside the account, such as acme/claude-plugins (repeatable)")
	c.Flags().StringArrayVar(&plugins, "plugin", nil, "plugin to install inside the account, name@marketplace (repeatable)")
	c.Flags().BoolVar(&makeDefault, "default", false, "make this the default account")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		name := first(args)
		asked := false
		if name == "" {
			if !canPrompt(cc) {
				return ui.Usage(withHint(errors.New("missing argument <name>"), "run: ccshelf account add <name> [--dir <path>]"))
			}
			n, err := cc.Prompt.Input(ctx, "Account name", "", func(s string) error {
				if !config.ValidAccountName(s) {
					return errors.New("use lower-case letters, digits and hyphens, at most 32 characters")
				}
				return nil
			})
			if err != nil {
				return err
			}
			name, asked = n, true
		}
		if !config.ValidAccountName(name) {
			return ui.Usage(fmt.Errorf("invalid account name %q: use lower-case letters, digits and hyphens, at most 32 characters", ui.Sanitize(name)))
		}
		// An explicit directory is stored as the user wrote it. The default
		// directory is stored as an absolute path.
		keepText := dir != ""
		if dir == "" {
			dir = "~/.claude-" + name
			if canPrompt(cc) && asked {
				d, err := cc.Prompt.Input(ctx, "Configuration directory", dir, nil)
				if err != nil {
					return err
				}
				keepText = d != dir
				dir = d
			}
		}
		wc, err := loadConfigForWrite(cc)
		if err != nil {
			return err
		}
		cfg, path := wc.cfg, wc.path
		expanded, err := config.ExpandPath(dir)
		if err != nil {
			return ui.Usage(fmt.Errorf("--dir: %w", err))
		}
		stored := ""
		if keepText {
			stored = dir
		}
		plan, err := account.Add(ctx, cfg, name, expanded, account.Options{
			StoredDir: stored, Persist: true, ConfigPath: path, Marketplaces: marketplaces, Plugins: plugins,
			Save: func(updated *config.Config) error {
				if makeDefault {
					updated.DefaultAccount = name
				}
				return wc.save(updated)
			},
		})
		wc.warnCommentsDropped(cc)
		if err != nil {
			return ui.Failure(fmt.Errorf("adding account %s: %w", name, err))
		}
		lines, err := plan.Lines(shell(cc))
		if err != nil {
			return ui.Failure(fmt.Errorf("rendering the steps: %w", err))
		}
		if asked {
			rec := ui.NewRecorder("account", "add", name)
			rec.Flag("--dir", dir)
			for _, m := range marketplaces {
				rec.Flag("--marketplace", m)
			}
			for _, p := range plugins {
				rec.Flag("--plugin", p)
			}
			if makeDefault {
				rec.Bool("--default")
			}
			printEquivalent(cc, rec)
		}
		if cc.Mode.JSON {
			type out struct {
				Name         string   `json:"name"`
				Dir          string   `json:"dir"`
				Created      bool     `json:"created"`
				Persisted    bool     `json:"persisted"`
				Default      bool     `json:"default"`
				CommentsLost bool     `json:"comments_dropped"`
				Steps        []string `json:"steps"`
			}
			return ui.WriteJSON(cc.Streams.Out, "account-add", out{
				Name: plan.Name, Dir: plan.Dir, Created: plan.Created,
				Persisted: plan.Persisted, Default: makeDefault, CommentsLost: wc.commentsDropped(), Steps: lines,
			})
		}
		okf(cc, "account %s uses %s", name, plan.Dir)
		fmt.Fprintln(cc.Streams.Out, "One-time steps for you to run (ccshelf never touches credentials):")
		for _, ln := range lines {
			fmt.Fprintf(cc.Streams.Out, "  %s\n", ui.Sanitize(ln))
		}
		return nil
	})
	return c
}

func (l *launcher) accountLsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the configured accounts",
		Args:    cobra.NoArgs,
	}
	c.RunE = l.do(func(_ context.Context, cc *clicore.Context, _ *cobra.Command, _ []string) error {
		cfg, _, err := loadConfig(cc)
		if err != nil {
			return err
		}
		infos := account.List(cfg)
		for i := range infos {
			if d, err := account.Describe(cfg, infos[i].Name); err == nil {
				infos[i] = d
			}
		}
		if cc.Mode.JSON {
			type row struct {
				Name      string `json:"name"`
				ConfigDir string `json:"config_dir"`
				Default   bool   `json:"default"`
				Exists    bool   `json:"exists"`
			}
			rows := make([]row, 0, len(infos))
			for _, i := range infos {
				rows = append(rows, row{Name: i.Name, ConfigDir: i.ConfigDir, Default: i.IsDefault, Exists: i.Exists})
			}
			return ui.WriteJSON(cc.Streams.Out, "accounts", rows)
		}
		if len(infos) == 0 {
			return ui.EmptyState(cc.Streams.Out, "no accounts configured", "accounts are optional. To add one: ccshelf account add <name>")
		}
		rows := make([][]string, 0, len(infos))
		for _, i := range infos {
			def, ex := "", "no"
			if i.IsDefault {
				def = "yes"
			}
			if i.Exists {
				ex = "yes"
			}
			rows = append(rows, []string{i.Name, i.ConfigDir, def, ex})
		}
		return ui.Table(cc.Streams.Out, []string{"NAME", "CONFIG DIR", "DEFAULT", "EXISTS"}, rows, cc.Mode)
	})
	return c
}

func (l *launcher) accountRmCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove"},
		Short:   "Forget an account (its directory is left alone)",
		Long:    "Remove the account from the configuration. The directory, with its login and history, is never deleted.",
		Args:    cobra.MaximumNArgs(1),
	}
	c.RunE = l.do(func(_ context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return ui.Usage(withHint(errors.New("missing argument <name>"), "run: ccshelf account rm <name>  (ccshelf account ls lists them)"))
		}
		wc, err := loadConfigForWrite(cc)
		if err != nil {
			return err
		}
		rem, err := account.Remove(wc.cfg, args[0])
		if err != nil {
			return ui.Usage(fmt.Errorf("removing account: %w", err))
		}
		if err := wc.save(wc.cfg); err != nil {
			return ui.Failure(fmt.Errorf("saving the configuration: %w", err))
		}
		wc.warnCommentsDropped(cc)
		if cc.Mode.JSON {
			return ui.WriteJSON(cc.Streams.Out, "account-rm", struct {
				Name             string `json:"name"`
				ConfigDir        string `json:"config_dir"`
				WasDefault       bool   `json:"was_default"`
				DirectoryDeleted bool   `json:"directory_deleted"`
				CommentsLost     bool   `json:"comments_dropped"`
			}{
				Name: rem.Name, ConfigDir: rem.ConfigDir, WasDefault: rem.ClearedDefault,
				DirectoryDeleted: false, CommentsLost: wc.commentsDropped(),
			})
		}
		okf(cc, "%s", rem.Message())
		return nil
	})
	return c
}

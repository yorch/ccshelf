package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/account"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/config"
	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/ui"
)

type initFlags struct {
	gitURL, ref, path, dir  string
	accountName, accountDir string
	force                   bool
}

func (l *launcher) initCmd() *cobra.Command {
	var f initFlags
	c := &cobra.Command{
		Use:   "init",
		Short: "Create the configuration file, optionally from an org data repo",
		Long: `Create config.toml and your personal profiles directory. With --git-url the org
data repo becomes a profile source (pin it with --ref: a tag or a full commit
id). With --dir another local profiles directory becomes a source. In a
terminal, anything you did not pass as a flag is asked for, and the equivalent
flag command is printed at the end. Nothing is fetched here: profiles from a
shared source are fetched, and need your trust, when you first use them.`,
		Example: `  ccshelf init
  ccshelf init --git-url git@ghe.example.com:acme/claude-marketplace.git --ref v2026.10.1
  ccshelf init --account-name work`,
		Args: cobra.NoArgs,
	}
	c.Flags().StringVar(&f.gitURL, "git-url", "", "org data repo URL to add as a git source")
	c.Flags().StringVar(&f.ref, "ref", "", "tag or full commit id the git source is pinned to")
	c.Flags().StringVar(&f.path, "path", "profiles", "folder inside the repo that holds the profiles")
	c.Flags().StringVar(&f.dir, "dir", "", "absolute directory of profiles to add as a dir source")
	c.Flags().StringVar(&f.accountName, "account-name", "", "also create an account with this name (see: ccshelf account add)")
	c.Flags().StringVar(&f.accountDir, "account-dir", "", "directory of that account (default ~/.claude-<name>)")
	c.Flags().BoolVar(&f.force, "force", false, "replace an existing configuration file")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, cmd *cobra.Command, _ []string) error {
		return l.initConfig(ctx, cc, &f, cmd.Flags().Changed("path"))
	})
	return c
}

func (l *launcher) initConfig(ctx context.Context, cc *clicore.Context, f *initFlags, pathGiven bool) error {
	path, err := configPath(cc)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil && !f.force {
		return ui.Failure(withHint(fmt.Errorf("%s already exists", path), "edit it, or replace it with: ccshelf init --force"))
	}
	asked := false
	if canPrompt(cc) && f.gitURL == "" && f.ref == "" && f.dir == "" && f.accountName == "" {
		var err error
		if asked, err = askInit(ctx, cc, f, pathGiven); err != nil {
			return err
		}
	}
	if f.ref != "" && f.gitURL == "" {
		return ui.Usage(errors.New("--ref needs --git-url"))
	}
	if f.gitURL != "" {
		if err := config.ValidateGitURL(f.gitURL); err != nil {
			return ui.Usage(withHint(fmt.Errorf("--git-url: %w", err),
				"use a remote URL (https://, ssh:// or git@host:path); for a local folder of profiles use --dir <absolute path>"))
		}
	}
	cfg := config.Default()
	if f.gitURL != "" {
		cfg.Sources = append(cfg.Sources, config.SourceConfig{Type: config.SourceGit, URL: f.gitURL, Ref: f.ref, Path: f.path})
	}
	if f.dir != "" {
		cfg.Sources = append(cfg.Sources, config.SourceConfig{Type: config.SourceDir, Path: f.dir})
	}
	if err := cfg.Validate(); err != nil {
		return ui.Usage(withHint(fmt.Errorf("configuration: %w", err), "a git source needs --ref (a tag or full commit id) unless trust.require_pin is off"))
	}
	if err := config.Save(path, cfg); err != nil {
		return ui.Failure(fmt.Errorf("writing the configuration: %w", err))
	}
	dir, err := profile.PersonalDir()
	if err != nil {
		return fmt.Errorf("personal profiles directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ui.Failure(fmt.Errorf("creating %s: %w", dir, err))
	}
	okf(cc, "wrote %s", path)
	okf(cc, "personal profiles go in %s", dir)

	if f.accountName != "" {
		if !config.ValidAccountName(f.accountName) {
			return ui.Usage(fmt.Errorf("invalid --account-name %q", ui.Sanitize(f.accountName)))
		}
		d := f.accountDir
		if d == "" {
			d = "~/.claude-" + f.accountName
		}
		expanded, err := config.ExpandPath(d)
		if err != nil {
			return ui.Usage(fmt.Errorf("--account-dir: %w", err))
		}
		plan, err := account.Add(ctx, cfg, f.accountName, expanded, account.Options{Persist: true, ConfigPath: path})
		if err != nil {
			return ui.Failure(fmt.Errorf("adding account %s: %w", f.accountName, err))
		}
		lines, err := plan.Lines(shell(cc))
		if err != nil {
			return ui.Failure(fmt.Errorf("rendering the account steps: %w", err))
		}
		okf(cc, "account %s uses %s", f.accountName, plan.Dir)
		for _, ln := range lines {
			fmt.Fprintf(cc.Streams.Out, "  %s\n", ui.Sanitize(ln))
		}
	}
	if asked {
		rec := ui.NewRecorder("init")
		if f.gitURL != "" {
			rec.Flag("--git-url", f.gitURL)
			rec.Flag("--ref", f.ref)
			if f.path != "profiles" {
				rec.Flag("--path", f.path)
			}
		}
		if f.dir != "" {
			rec.Flag("--dir", f.dir)
		}
		if f.accountName != "" {
			rec.Flag("--account-name", f.accountName)
			if f.accountDir != "" {
				rec.Flag("--account-dir", f.accountDir)
			}
		}
		if f.force {
			rec.Bool("--force")
		}
		printEquivalent(cc, rec)
	}
	return nil
}

// askInit is the init wizard. It reports whether it asked anything.
func askInit(ctx context.Context, cc *clicore.Context, f *initFlags, pathGiven bool) (bool, error) {
	url, err := cc.Prompt.Input(ctx, "Org data repo URL (empty to skip)", "", func(s string) error {
		if s == "" {
			return nil
		}
		return config.ValidateGitURL(s)
	})
	if err != nil {
		return false, err
	}
	if url = strings.TrimSpace(url); url != "" {
		f.gitURL = url
		ref, err := cc.Prompt.Input(ctx, "Tag or full commit id to pin it to", "", config.ValidatePin)
		if err != nil {
			return false, err
		}
		f.ref = ref
		if !pathGiven {
			p, err := cc.Prompt.Input(ctx, "Folder inside the repo that holds the profiles", f.path, nil)
			if err != nil {
				return false, err
			}
			f.path = p
		}
	}
	dir, err := cc.Prompt.Input(ctx, "Another local profiles directory (absolute, empty to skip)", "", nil)
	if err != nil {
		return false, err
	}
	f.dir = strings.TrimSpace(dir)
	name, err := cc.Prompt.Input(ctx, "Account name to create (empty to skip)", "", func(s string) error {
		if s == "" || config.ValidAccountName(s) {
			return nil
		}
		return errors.New("use lower-case letters, digits and hyphens, at most 32 characters")
	})
	if err != nil {
		return false, err
	}
	f.accountName = strings.TrimSpace(name)
	return true, nil
}

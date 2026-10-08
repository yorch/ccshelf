package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/account"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
)

type initFlags struct {
	gitURL, ref, branch, path, dir string
	accountName, accountDir        string
	updateMode                     string
	force, yes                     bool
}

func (l *launcher) initCmd() *cobra.Command {
	var f initFlags
	c := &cobra.Command{
		Use:   "init",
		Short: "Create the configuration file, optionally from an org data repo",
		Long: `Create config.toml and your personal profiles directory. With --git-url the org
data repo becomes a profile source. Pin it with --ref (a tag or a full commit
id), or track a branch with --branch. A branch can move: ccshelf runs the
commit you trusted, and every new commit on the branch needs your trust again.
With --dir another local profiles directory becomes a source. In a
terminal, running without source, account or update values starts the full
setup wizard. It shows a summary and asks before writing (default no). Partial
flag runs do not prompt. --yes confirms writing only, never trust. The wizard
prints an equivalent flag command at the end. Nothing is fetched here. ccshelf
fetches profiles from a shared source when you first use them, and they need
your trust then.

Automatic updates are off unless you turn them on:
  - --update-mode notify checks for a newer release once a day and prints one
    line when there is one.
  - --update-mode install also installs it (same major version only).
The full wizard asks once.`,
		Example: `  ccshelf init
  ccshelf init --git-url git@ghe.example.com:acme/claude-marketplace.git --ref v2026.10.1
  ccshelf init --git-url git@ghe.example.com:acme/claude-marketplace.git --branch main
  ccshelf init --account-name work`,
		Args: cobra.NoArgs,
	}
	c.Flags().StringVar(&f.gitURL, "git-url", "", "org data repo URL to add as a git source")
	c.Flags().StringVar(&f.ref, "ref", "", "tag or full commit id the git source is pinned to")
	c.Flags().StringVar(&f.branch, "branch", "", "branch the git source tracks, instead of --ref (every new commit needs trust again)")
	c.Flags().StringVar(&f.path, "path", "profiles", "folder inside the repo that holds the profiles")
	c.Flags().StringVar(&f.dir, "dir", "", "absolute directory of profiles to add as a dir source")
	c.Flags().StringVar(&f.accountName, "account-name", "", "also create an account with this name (see: ccshelf account add)")
	c.Flags().StringVar(&f.accountDir, "account-dir", "", "directory of that account (default ~/.claude-<name>)")
	c.Flags().StringVar(&f.updateMode, "update-mode", "", "automatic update mode: off, notify or install (default: asked in the full wizard, otherwise off)")
	c.Flags().BoolVar(&f.force, "force", false, "replace an existing configuration file")
	c.Flags().BoolVar(&f.yes, "yes", false, "confirm writing in the full wizard (never accepts trust)")
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
	if f.updateMode != "" && !validUpdateMode(f.updateMode) {
		return ui.Usage(withHint(fmt.Errorf("--update-mode %q is not one of %s", ui.Sanitize(f.updateMode), strings.Join(config.UpdateModes(), ", ")),
			"off never checks, notify prints one line when a release exists, and install also installs it"))
	}
	// An [update] section of the file being replaced is kept, and is never asked
	// about again: init asks once.
	var keptUpdate config.Update
	if f.force {
		if old, err := config.Load(path); err == nil {
			keptUpdate = old.Update
		}
	}
	asked := false
	if canPrompt(cc) && f.gitURL == "" && f.ref == "" && f.branch == "" && f.dir == "" && f.accountName == "" && f.updateMode == "" {
		var err error
		if asked, err = askInit(ctx, cc, f, pathGiven, !keptUpdate.Present()); err != nil {
			return err
		}
	}
	if f.ref != "" && f.branch != "" {
		return ui.Usage(errors.New("give --ref or --branch, not both"))
	}
	if f.ref != "" && f.gitURL == "" {
		return ui.Usage(errors.New("--ref needs --git-url"))
	}
	if f.branch != "" && f.gitURL == "" {
		return ui.Usage(errors.New("--branch needs --git-url"))
	}
	if f.gitURL != "" {
		if err := config.ValidateGitURL(f.gitURL); err != nil {
			return ui.Usage(withHint(fmt.Errorf("--git-url: %w", err),
				"use a remote URL (https://, ssh:// or git@host:path). For a local folder of profiles, use --dir <absolute path>"))
		}
	}
	cfg := config.Default()
	cfg.Update = keptUpdate
	if f.updateMode != "" {
		cfg.Update.Mode = f.updateMode
	}
	if f.gitURL != "" {
		cfg.Sources = append(cfg.Sources, config.SourceConfig{Type: config.SourceGit, URL: f.gitURL, Ref: f.ref, Branch: f.branch, Path: f.path})
	}
	if f.dir != "" {
		cfg.Sources = append(cfg.Sources, config.SourceConfig{Type: config.SourceDir, Path: f.dir})
	}
	if err := cfg.Validate(); err != nil {
		return ui.Usage(withHint(fmt.Errorf("configuration: %w", err), "a git source needs --ref (a tag or full commit id) or --branch"))
	}
	// A branch can move. init only warns: its --yes keeps its meaning (it
	// confirms the write in the full wizard), and trust is still per commit.
	for _, src := range cfg.Sources {
		if src.Type == config.SourceGit && src.Branch != "" {
			warnf(cc, "this configuration weakens a security setting: %s", config.BranchWarning(src))
		}
	}
	dir, err := profile.PersonalDir()
	if err != nil {
		return fmt.Errorf("personal profiles directory: %w", err)
	}
	accountDir := ""
	if f.accountName != "" {
		if !config.ValidAccountName(f.accountName) {
			return ui.Usage(fmt.Errorf("invalid --account-name %q", ui.SanitizeLine(f.accountName)))
		}
		d := f.accountDir
		if d == "" {
			d = "~/.claude-" + f.accountName
		}
		accountDir, err = config.ExpandPath(d)
		if err != nil {
			return ui.Usage(fmt.Errorf("--account-dir: %w", err))
		}
	}
	if asked {
		printInitSummary(cc, path, dir, accountDir, cfg, f)
		if !f.yes {
			write, err := cc.Prompt.Confirm(ctx, "Write this configuration?", false)
			if err != nil {
				return err
			}
			if !write {
				return ui.Failure(errors.New("configuration not written"))
			}
		}
	}
	// Cancellation must be checked after the final prompt and before any writes,
	// even if a custom prompter returns success after its context was canceled.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.Save(path, cfg); err != nil {
		return ui.Failure(fmt.Errorf("writing the configuration: %w", err))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ui.Failure(fmt.Errorf("creating %s: %w", dir, err))
	}
	okf(cc, "wrote %s", path)
	okf(cc, "personal profiles go in %s", dir)

	if f.accountName != "" {
		plan, err := account.Add(ctx, cfg, f.accountName, accountDir, account.Options{Persist: true, ConfigPath: path})
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
		if cc.G.ConfigPath != "" {
			rec.Flag("--config", cc.G.ConfigPath)
		}
		if f.gitURL != "" {
			rec.Flag("--git-url", f.gitURL)
			if f.branch != "" {
				rec.Flag("--branch", f.branch)
			} else {
				rec.Flag("--ref", f.ref)
			}
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
		// Record the effective mode even when kept or off: this makes a wizard
		// with no optional source/account values replay without starting it again.
		rec.Flag("--update-mode", cfg.Update.EffectiveMode())
		rec.Bool("--yes")
		if f.force {
			rec.Bool("--force")
		}
		printEquivalent(cc, rec)
	}
	return nil
}

// printInitSummary writes only single-line sanitized values to the prompt stream.
func printInitSummary(cc *clicore.Context, path, personalDir, accountDir string, cfg *config.Config, f *initFlags) {
	line := func(label, value string) { fmt.Fprintf(cc.Streams.Err, "  %s: %s\n", label, ui.SanitizeLine(value)) }
	fmt.Fprintln(cc.Streams.Err, "Configuration summary (nothing has been written):")
	line("Config file", path)
	if f.force {
		line("Existing config", "replace (--force)")
	}
	line("Personal profiles", personalDir)
	if len(cfg.Sources) == 0 {
		line("Additional profile sources", "none")
	}
	for _, src := range cfg.Sources {
		if src.Type == config.SourceGit {
			line("Org data repo", src.URL)
			if src.Branch != "" {
				line("Tracked branch", src.Branch+" (the branch can move, and every new commit needs your trust before it runs)")
			} else {
				line("Pinned ref", src.Ref)
			}
			line("Profiles folder", src.Path)
		} else {
			line("Local profiles", src.Path)
		}
	}
	if f.accountName != "" {
		line("New account", f.accountName)
		line("Account directory", accountDir)
	} else {
		line("New account", "none (existing Claude account unchanged)")
	}
	line("Automatic updates", cfg.Update.EffectiveMode())
	fmt.Fprintln(cc.Streams.Err, "  No sources are fetched or trusted. Shared profiles need separate trust.")
}

// askInit is the init wizard. It reports whether it asked anything.
func askInit(ctx context.Context, cc *clicore.Context, f *initFlags, pathGiven, askUpdate bool) (bool, error) {
	url, err := cc.Prompt.Input(ctx, "Org data repo URL (optional, Enter to skip, nothing is fetched or trusted here)", "", func(s string) error {
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
		pin, err := cc.Prompt.Input(ctx, pinPrompt("Tag or full commit id to pin it to"), "", validatePinOrBranch(config.ValidatePin))
		if err != nil {
			return false, err
		}
		f.ref, f.branch = splitPinInput(pin)
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
	name, err := cc.Prompt.Input(ctx, "Separate Claude account name (optional, Enter to keep your existing account)", "", func(s string) error {
		if s == "" || config.ValidAccountName(s) {
			return nil
		}
		return errors.New("use lower-case letters, digits and hyphens, at most 32 characters")
	})
	if err != nil {
		return false, err
	}
	f.accountName = strings.TrimSpace(name)
	if askUpdate {
		// Default no; the answer is written either way so that it is asked once.
		yes, err := cc.Prompt.Confirm(ctx, "Check for updates once a day and tell me when one exists?", false)
		if err != nil {
			return false, err
		}
		f.updateMode = config.UpdateOff
		if yes {
			f.updateMode = config.UpdateNotify
		}
	}
	return true, nil
}

// validUpdateMode reports whether m is an accepted --update-mode value.
func validUpdateMode(m string) bool {
	for _, x := range config.UpdateModes() {
		if m == x {
			return true
		}
	}
	return false
}

// pinPrompt adds the way to track a branch to a prompt for a tag or commit.
func pinPrompt(base string) string {
	return base + " (or " + config.BranchRefPrefix + "NAME to track a branch)"
}

// splitPinInput turns the answer of a pin prompt into a ref or a branch: the
// text "branch:NAME" names a branch (no tag name holds a colon).
func splitPinInput(s string) (ref, branch string) {
	if name, ok := strings.CutPrefix(strings.TrimSpace(s), config.BranchRefPrefix); ok {
		return "", name
	}
	return strings.TrimSpace(s), ""
}

// validatePinOrBranch validates the answer of a pin prompt: a ref by validate,
// or "branch:NAME" by config.ValidateBranch.
func validatePinOrBranch(validate func(string) error) func(string) error {
	return func(s string) error {
		ref, branch := splitPinInput(s)
		if branch != "" || strings.HasPrefix(strings.TrimSpace(s), config.BranchRefPrefix) {
			return config.ValidateBranch(branch)
		}
		return validate(ref)
	}
}

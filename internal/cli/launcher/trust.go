package launcher

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/trust"
	"github.com/yorch/ccshelf/internal/ui"
)

func (l *launcher) trustCmd() *cobra.Command {
	var accept string
	var revoke, project bool
	c := &cobra.Command{
		Use:   "trust [profile]",
		Short: "Review and trust a shared profile (or a project folder)",
		Long: `Show what a profile's closure runs (MCP servers, prompts, plugins, environment
names) and record it as trusted. In a terminal, the command asks you to confirm
what it shows. For scripts, name exactly what you accept:

  ccshelf trust <profile> --accept <closure-hash>

"ccshelf trust <profile>" and "ccshelf show <profile>" print the hash.
--yes does not exist here: trust is never accepted by default.

--project reviews the repository's .ccshelf folder (loaded only when
trust.trust_project_profiles is true). With --project, --accept takes the
folder hash.
--revoke removes the records of a profile (or of the project folder).`,
		Args: cobra.MaximumNArgs(1),
	}
	c.Flags().StringVar(&accept, "accept", "", "closure hash (or, with --project, folder hash) you accept")
	c.Flags().BoolVar(&revoke, "revoke", false, "remove the trust records instead")
	c.Flags().BoolVar(&project, "project", false, "act on the repository's .ccshelf folder")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		if project {
			return l.trustProject(ctx, cc, accept, revoke)
		}
		return l.trustProfile(ctx, cc, first(args), accept, revoke)
	})
	return c
}

func (l *launcher) trustProfile(ctx context.Context, cc *clicore.Context, name, accept string, revoke bool) error {
	// Reviewing re-resolves the git tags on the remote, so a tag that moved
	// since the last acceptance is seen (a run uses the pinned commit).
	s, err := l.openWith(ctx, cc, openOpts{prepare: true, refresh: true})
	if err != nil {
		return err
	}
	name, picked, err := pickProfile(ctx, cc, name, "trust", s.profileOptions)
	if err != nil {
		return err
	}
	lock, err := config.LockfilePath()
	if err != nil {
		return ui.Failure(fmt.Errorf("trust lockfile location: %w", err))
	}
	store, err := trust.Open(lock)
	if err != nil {
		return ui.Failure(fmt.Errorf("trust lockfile: %w", err))
	}
	if revoke {
		if err := store.Revoke(name, ""); err != nil {
			if errors.Is(err, trust.ErrNotFound) {
				return ui.Failure(fmt.Errorf("no trust record for %q", ui.Sanitize(name)))
			}
			return ui.Failure(err)
		}
		okf(cc, "trust for %s removed", name)
		return nil
	}
	r, err := s.resolve(name)
	if err != nil {
		return err
	}
	v := store.CheckWithProject(r, s.proj.Allowed)
	if v.State == trust.Trusted {
		okf(cc, "%s is already trusted (closure %s)", name, v.Hash)
		return nil
	}
	if v.Problem != "" {
		// An inconsistent closure is a bug or tampering: exit 1, not exit 4.
		return ui.Failure(withHint(fmt.Errorf("profile %s: %w: %s", ui.SanitizeLine(r.Name), trust.ErrInconsistentClosure, ui.SanitizeLine(v.Problem)),
			"the closure cannot be trusted. This is a bug or a tampered file"))
	}
	if v.State == trust.ProjectUntrusted {
		return ui.TrustRequired(withHint(&trust.NeedsTrustError{Profile: r.Name, Verdict: v},
			"project profiles need: ccshelf trust --project (then trust the profile)"))
	}
	fmt.Fprintf(cc.Streams.Out, "Profile %s needs trust (%s). Closure: %s\n", ui.Sanitize(r.Name), v.State, v.Hash)
	v.Describe(cc.Streams.Out)
	if accept != "" {
		if err := store.Accept(r, accept); err != nil {
			if errors.Is(err, trust.ErrHashMismatch) {
				return ui.TrustRequired(withHint(err, "the closure is now %s. Review it above, and pass that hash if you accept it", v.Hash))
			}
			return ui.Failure(fmt.Errorf("recording trust: %w", err))
		}
		okf(cc, "trusted %s (closure %s)", name, v.Hash)
		return nil
	}
	if !canPrompt(cc) {
		return ui.Usage(ui.MissingFlags("review the closure above, then name what you accept", "--accept "+v.Hash))
	}
	ok, err := ui.ConfirmRisky(ctx, cc.Prompt, fmt.Sprintf("Trust profile %s as shown?", ui.Sanitize(name)))
	if err != nil {
		return err
	}
	if !ok {
		return ui.TrustRequired(errors.New("not trusted"))
	}
	// Accept what was shown (v.Hash), not whatever the closure is by now.
	if err := store.Accept(r, v.Hash); err != nil {
		return ui.Failure(fmt.Errorf("recording trust: %w", err))
	}
	okf(cc, "trusted %s (closure %s)", name, v.Hash)
	if picked {
		rec := ui.NewRecorder("trust", name)
		rec.Flag("--accept", v.Hash)
		printEquivalent(cc, rec)
	}
	return nil
}

func (l *launcher) trustProject(ctx context.Context, cc *clicore.Context, accept string, revoke bool) error {
	cfg, _, err := loadConfig(cc)
	if err != nil {
		return err
	}
	cwd, err := cc.Getwd()
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}
	root := findProject(cwd, cc.GOOS)
	if root == "" {
		return ui.Usage(errors.New("no .ccshelf folder in this directory or its parents"))
	}
	pp, err := config.ProjectTrustPath()
	if err != nil {
		return ui.Failure(err)
	}
	ps, err := trust.OpenProjects(pp)
	if err != nil {
		return ui.Failure(fmt.Errorf("project trust file: %w", err))
	}
	if real, err := evalSymlinks(root); err == nil {
		root = real
	}
	if revoke {
		if err := ps.Revoke(root); err != nil {
			return ui.Failure(err)
		}
		okf(cc, "project trust removed")
		return nil
	}
	hash, err := trust.HashProjectFolder(root)
	if err != nil {
		return ui.Failure(fmt.Errorf("reading the .ccshelf folder: %w", err))
	}
	if !cfg.Trust.TrustProjectProfiles {
		warnf(cc, "trust.trust_project_profiles is false: project profiles stay off even when the folder is trusted")
	}
	fmt.Fprintf(cc.Streams.Out, "Project folder %s (content hash %s)\n", ui.Sanitize(root), hash)
	if accept != "" {
		if accept != hash {
			return ui.TrustRequired(fmt.Errorf("the folder hash is %s, not the one you accepted", hash))
		}
	} else {
		if !canPrompt(cc) {
			return ui.Usage(ui.MissingFlags("review the folder, then name its hash", "--accept "+hash))
		}
		ok, err := ui.ConfirmRisky(ctx, cc.Prompt, "Trust this project's .ccshelf folder as it is now?")
		if err != nil {
			return err
		}
		if !ok {
			return ui.TrustRequired(errors.New("not trusted"))
		}
	}
	if err := ps.Trust(root); err != nil {
		return ui.Failure(fmt.Errorf("recording project trust: %w", err))
	}
	okf(cc, "trusted the project folder (hash %s)", hash)
	if accept == "" {
		rec := ui.NewRecorder("trust")
		rec.Bool("--project")
		rec.Flag("--accept", hash)
		printEquivalent(cc, rec)
	}
	return nil
}

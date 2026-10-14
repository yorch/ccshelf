package launcher

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yorch/ccshelf/internal/cache"
	"github.com/yorch/ccshelf/internal/claude"
	"github.com/yorch/ccshelf/internal/ui"
)

// installAll is the value of a bare --install-missing. A plugin id always has
// an "@", so the value cannot be an id.
const installAll = "*"

// installRequest is the --install-missing flag of "run".
type installRequest struct {
	// On is true when the flag is present.
	On bool
	// All is a bare flag: every missing plugin of the profile.
	All bool
	// IDs are the plugins that the user named.
	IDs []string
}

// newInstallRequest checks the flag values. A bare flag together with --yes is
// an error, so that --yes cannot widen the set of plugins to install.
func newInstallRequest(present bool, values []string, yes bool) (installRequest, error) {
	if !present {
		return installRequest{}, nil
	}
	req := installRequest{On: true}
	for _, v := range values {
		if v == installAll {
			req.All = true
			continue
		}
		if !validPluginID(v) {
			return installRequest{}, ui.Usage(withHint(fmt.Errorf("--install-missing: %q is not a plugin id", ui.SanitizeLine(v)),
				"write name@marketplace, for example --install-missing=sre-kit@acme"))
		}
		if !slices.Contains(req.IDs, v) {
			req.IDs = append(req.IDs, v)
		}
	}
	switch {
	case req.All && len(req.IDs) > 0:
		return installRequest{}, ui.Usage(errors.New("--install-missing: give plugin ids or no value, not both"))
	case req.All && yes:
		return installRequest{}, ui.Usage(withHint(errors.New("--install-missing with --yes needs the plugins by name, so that --yes cannot add plugins that you did not read"),
			"write --install-missing=name@marketplace,name@marketplace"))
	}
	return req, nil
}

// flag returns the form of the flag for the equivalent command.
func (r installRequest) flag() string {
	if r.All {
		return "--install-missing"
	}
	return "--install-missing=" + strings.Join(r.IDs, ",")
}

// installTimeout bounds one "claude plugin install" call. A plugin install
// clones a repository, so it can take longer than a listing call.
const installTimeout = 10 * time.Minute

// installMissing installs the plugins of the profile that claude does not
// list, for "run --install-missing". It never adds a marketplace, never passes
// -y to claude (so claude still asks before it runs a command that a
// marketplace declares) and never runs when the user did not confirm. Managed
// policy applies, because claude itself does the install.
//
// It lists marketplaces and runs the installs in the cache directory, not the
// project, so a project cannot add or shadow a marketplace (SR3). A plugin that
// claude does not install stays missing: installMissing warns, and the caller
// reports it as it does without the flag.
//
// It returns the fresh list of installed plugins when claude installed at
// least one plugin, and nil when nothing changed.
func (s *session) installMissing(ctx context.Context, bin string, missing []string, yes bool) ([]claude.Plugin, error) {
	cc := s.cc
	var want []string
	for _, id := range missing {
		if validPluginID(id) {
			want = append(want, id)
		}
	}
	if len(want) == 0 {
		return nil, nil
	}
	dir, err := cache.Dir()
	if err != nil {
		return nil, ui.Failure(fmt.Errorf("cache directory: %w", err))
	}

	// A plugin installs only from a marketplace that the user already added.
	// Adding a marketplace is a separate trust step that ccshelf does not take.
	added, err := claude.ListMarketplaces(ctx, bin, dir, s.env)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("listing marketplaces: %w", cerr)
		}
		return nil, ui.Failure(withHint(fmt.Errorf("listing marketplaces: %w", err),
			"claude plugin marketplace list --json must work under the chosen account"))
	}
	count := map[string]int{}
	for _, m := range added {
		count[m.Name]++
	}
	var plan []string
	for _, id := range want {
		_, mkt := claude.SplitID(id)
		switch {
		case mkt == "npm":
			// claude reads <package>@npm as an npm install, not as a marketplace.
			warnf(cc, "plugin %s is not installed. ccshelf does not install an npm package", id)
		case count[mkt] == 0:
			warnf(cc, "plugin %s is not installed and marketplace %s is not added. Add it inside Claude Code with: /plugin marketplace add <source>", id, mkt)
		case count[mkt] > 1:
			warnf(cc, "plugin %s is not installed and marketplace %s is listed more than once. ccshelf does not install from it", id, mkt)
		default:
			plan = append(plan, id)
		}
	}
	if len(plan) == 0 {
		return nil, nil
	}

	fmt.Fprintf(cc.Streams.Err, "ccshelf will run this command for each missing plugin of the profile:\n")
	for _, id := range plan {
		fmt.Fprintf(cc.Streams.Err, "  claude plugin install --scope user %s\n", id)
	}
	if !yes {
		if !canPrompt(cc) {
			return nil, ui.Usage(errors.New("--install-missing needs a terminal to confirm. Add --yes to confirm in advance"))
		}
		ok, err := cc.Prompt.Confirm(ctx, "Install these plugins?", false)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
	}

	var done, failed []string
	for _, id := range plan {
		ictx, cancel := context.WithTimeout(ctx, installTimeout)
		code, err := s.l.opt.SpawnIn(ictx, dir, bin, []string{"plugin", "install", "--scope", "user", id}, s.env, cc.Streams.In, cc.Streams.Err, cc.Streams.Err)
		cancel()
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("installing plugin %s: %w", id, cerr)
		}
		if err != nil || code != 0 {
			failed = append(failed, id)
			continue
		}
		done = append(done, id)
	}
	if len(failed) > 0 {
		warnf(cc, "claude did not install: %s", strings.Join(failed, ", "))
	}
	if len(done) == 0 {
		return nil, nil
	}
	okf(cc, "installed: %s", strings.Join(done, ", "))
	fresh, err := claude.ListInstalled(ctx, bin, s.cwd, s.env)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("listing installed plugins: %w", cerr)
		}
		return nil, ui.Failure(withHint(fmt.Errorf("listing installed plugins: %w", err),
			"claude plugin list --json must work in this directory (and under the chosen account)"))
	}
	return fresh, nil
}

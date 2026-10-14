package launcher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yorch/ccshelf/internal/claude"
	"github.com/yorch/ccshelf/internal/ui"
)

// installTimeout bounds one "claude plugin install" call. A plugin install
// clones a repository, so it can take longer than a listing call.
const installTimeout = 10 * time.Minute

// installMissing installs the plugins of the profile that claude does not
// list, for "run --install-missing". It never adds a marketplace, never
// passes -y to claude (so claude still asks before it runs a command that a
// marketplace declares) and never runs when the user did not confirm. Managed
// policy applies, because claude itself does the install: when claude refuses,
// the plugin stays missing and the error goes to the user.
//
// It returns the fresh list of installed plugins when claude installed at
// least one plugin, and nil when nothing changed. The caller reports the
// plugins that are still missing.
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

	// A plugin installs only from a marketplace that the user already added.
	// Adding a marketplace is a separate trust step that ccshelf does not take.
	added, err := claude.ListMarketplaces(ctx, bin, s.cwd, s.env)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("listing marketplaces: %w", cerr)
		}
		return nil, ui.Failure(withHint(fmt.Errorf("listing marketplaces: %w", err),
			"claude plugin marketplace list --json must work under the chosen account"))
	}
	known := map[string]bool{}
	for _, m := range added {
		known[m.Name] = true
	}
	var plan []string
	for _, id := range want {
		_, mkt := claude.SplitID(id)
		if !known[mkt] {
			warnf(cc, "plugin %s is not installed and marketplace %s is not added. Add it inside Claude Code with: /plugin marketplace add <source>", id, mkt)
			continue
		}
		plan = append(plan, id)
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
			return nil, ui.Failure(errors.New("not started: you declined to install the plugins of the profile"))
		}
	}

	var failed []string
	for _, id := range plan {
		ictx, cancel := context.WithTimeout(ctx, installTimeout)
		code, err := s.l.opt.Spawn(ictx, bin, []string{"plugin", "install", "--scope", "user", id}, s.env, cc.Streams.In, cc.Streams.Err, cc.Streams.Err)
		cancel()
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("installing plugin %s: %w", id, cerr)
		}
		if err != nil || code != 0 {
			failed = append(failed, id)
			warnf(cc, "claude could not install plugin %s", id)
		}
	}
	if len(failed) > 0 {
		return nil, ui.Failure(fmt.Errorf("not started: claude could not install: %s", strings.Join(failed, ", ")))
	}
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

package launcher

import (
	"context"
	"fmt"
	"strings"

	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/config"
	"github.com/ccshelf/ccshelf/internal/marketplace"
	"github.com/ccshelf/ccshelf/internal/orgconfig"
	"github.com/ccshelf/ccshelf/internal/profile/gitsource"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// CatalogProvider returns the launcher's implementation of
// clicore.CatalogProvider: it finds the catalog data of the org data repo that
// the user's configured sources point at, for search and recommend run outside
// an org data repo checkout. Only opt.NewGit is used.
//
// It never touches the network. A dir source is used as it is. A git source is
// used from its verified cache, and only at a commit that was accepted: the
// one a full-SHA ref names, else a commit the trust lockfile recorded for its
// tag (never merely the newest cached checkout). Sources are tried in the
// order of the configuration and the first one whose org config and first
// marketplace file read cleanly wins; a source with nothing usable is skipped.
// With none left it returns clicore.ErrNoCatalog.
func CatalogProvider(opt Options) clicore.CatalogProvider {
	l := &launcher{opt: opt}
	return l.catalogData
}

func (l *launcher) catalogData(ctx context.Context, cc *clicore.Context) (*clicore.CatalogData, error) {
	cfg, _, err := loadConfig(cc)
	if err != nil {
		return nil, err
	}
	cwd, err := cc.Getwd()
	if err != nil {
		return nil, fmt.Errorf("working directory: %w", err)
	}
	s := &session{l: l, cc: cc, cfg: cfg, cwd: cwd, gitIDs: map[string]bool{}}
	newGit := l.opt.NewGit
	if newGit == nil {
		newGit = defaultGit
	}
	untrusted := false
	for i, sc := range cfg.Sources {
		var root, label string
		switch sc.Type {
		case config.SourceDir:
			p, err := sc.ResolvedPath()
			if err != nil {
				continue
			}
			root, label = s.orgDirSource(i, p).Root(), "dir "+ui.SanitizeLine(p)
		case config.SourceGit:
			g, err := newGit(gitsource.Options{URL: sc.URL, Ref: sc.Ref, Subpath: sc.Path, RequirePin: cfg.Trust.RequirePin})
			if err != nil {
				continue
			}
			commit := s.cachedCheckout(ctx, g, sc)
			if commit == "" {
				untrusted = untrusted || len(s.catalogCommits(sc)) == 0
				continue
			}
			root, label = g.Root(), fmt.Sprintf("git %s at %s", ui.SanitizeLine(sc.URL), shortSHA(commit))
		default:
			continue // plugin sources have no cached copy of the org data repo
		}
		if root != "" && hasCatalogData(root) {
			return &clicore.CatalogData{Root: root, Source: label}, nil
		}
	}
	if untrusted {
		return nil, fmt.Errorf("%w: a git source has no accepted commit yet (run `ccshelf ls --refresh`, then `ccshelf trust`)", clicore.ErrNoCatalog)
	}
	return nil, clicore.ErrNoCatalog
}

// cachedCheckout prepares g from a verified checkout already in the cache,
// without network access, and returns the commit it used ("" when none). Only
// a commit the user accepted is ever read: the one a full-SHA ref names, else
// the commits the trust lockfile recorded for the tag, the most recent first.
// The cache is never searched for "the newest checkout": that commit may never
// have been reviewed (the offline fallback of run feeds the trust check, a
// catalog read has none).
func (s *session) cachedCheckout(ctx context.Context, g PreparedSource, sc config.SourceConfig) string {
	cp, ok := g.(cachedPreparer)
	if !ok {
		return ""
	}
	for _, commit := range s.catalogCommits(sc) {
		if cp.PrepareCached(ctx, commit) == nil {
			return commit
		}
	}
	return ""
}

// catalogCommits lists the commits a catalog read may use for a git source.
func (s *session) catalogCommits(sc config.SourceConfig) []string {
	if ref := strings.ToLower(sc.Ref); fullSHA.MatchString(ref) {
		return []string{ref}
	}
	return s.lockedCommits(sc)
}

// hasCatalogData reports whether root holds an org config that parses and a
// readable first marketplace file, which is what search and recommend need
// (and what makes a directory an org data repo).
func hasCatalogData(root string) bool {
	cfg, _, err := orgconfig.Find(root)
	if err != nil || len(cfg.Catalog.Marketplaces) == 0 {
		return false
	}
	_, err = marketplace.LoadFile(root, cfg.Catalog.Marketplaces[0])
	return err == nil
}

package launcher

import (
	"context"
	"fmt"
	"strings"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/marketplace"
	"github.com/yorch/ccshelf/internal/orgconfig"
	"github.com/yorch/ccshelf/internal/ui"
)

// CatalogProvider returns the launcher's implementation of
// clicore.CatalogProvider: it finds catalog data for search and recommend
// outside an org data repo checkout. It fetches an explicit catalog.remote_url
// first. Otherwise it reads the configured sources from disk. It uses only
// opt.NewGit for profile sources.
//
// Remote catalogs require HTTPS and are size and format checked. Without that
// setting, a dir source is used as it is and a git source is used from its
// verified cache, at a commit that the trust model accepted. It tries the
// sources in order. When none is usable, it returns clicore.ErrNoCatalog.
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
	if cfg.Catalog.RemoteURL != "" {
		return l.fetchCatalog(ctx, cfg.Catalog.RemoteURL)
	}
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
			g, err := newGit(gitOptions(sc, cfg.Trust.RequirePin))
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
	if err != nil || !cfg.Catalog.Enabled || len(cfg.Catalog.Marketplaces) == 0 {
		return false
	}
	_, err = marketplace.LoadFile(root, cfg.Catalog.Marketplaces[0])
	return err == nil
}

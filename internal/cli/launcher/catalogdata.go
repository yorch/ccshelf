package launcher

import (
	"context"
	"fmt"

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
// used from its verified cache: the commit the trust lockfile pinned for its
// tag, else the newest cached checkout that passes the gitsource verification
// (the same fallback the launcher uses offline). Sources are tried in the
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
	return nil, clicore.ErrNoCatalog
}

// cachedCheckout prepares g from a verified checkout already in the cache,
// without network access, and returns the commit it used ("" when none).
func (s *session) cachedCheckout(ctx context.Context, g PreparedSource, sc config.SourceConfig) string {
	cp, ok := g.(cachedPreparer)
	if !ok {
		return ""
	}
	if commit := s.lockedCommit(sc); commit != "" && cp.PrepareCached(ctx, commit) == nil {
		return commit
	}
	cl, ok := g.(cachedLister)
	if !ok {
		return ""
	}
	commits, err := cl.CachedCommits()
	if err != nil {
		return ""
	}
	for _, c := range commits {
		if cp.PrepareCached(ctx, c) == nil {
			return c
		}
	}
	return ""
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

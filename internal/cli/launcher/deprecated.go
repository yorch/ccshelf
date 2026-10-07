package launcher

import (
	"fmt"
	"sort"

	"github.com/yorch/ccshelf/internal/catalog/sidecar"
	"github.com/yorch/ccshelf/internal/marketplace"
	"github.com/yorch/ccshelf/internal/orgconfig"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
)

// deprecationWarnings returns one warning for every plugin the resolved
// profile includes whose catalog sidecar (catalog/plugins/<name>.toml of an
// org source, or the single-file metadata the org config selects) says it is
// deprecated or superseded. It only informs: it never blocks a run. Sources
// without sidecar data, and sidecars that cannot be read, are silent (lint and
// doctor are where those are reported). The list is sorted and holds each
// plugin once.
func (s *session) deprecationWarnings(r *profile.Resolved) []string {
	include := r.Merged.WithDefaults().Plugins.Include
	if len(include) == 0 {
		return nil
	}
	// known is keyed by the full plugin id: a sidecar describes the plugins of
	// the marketplaces its org source publishes, not a plugin of the same name
	// from another marketplace.
	known := map[string]*sidecar.Sidecar{}
	seenRoot := map[string]bool{}
	for _, f := range r.Chain {
		src := f.Source
		if src == nil || src.Kind() != profile.KindOrg || src.Root() == "" || seenRoot[src.Root()] {
			continue
		}
		seenRoot[src.Root()] = true
		cfg, _, err := orgConfigOf(src)
		if err != nil {
			continue
		}
		sc, _, err := sidecar.LoadSidecars(src.Root(), cfg)
		if err != nil {
			continue
		}
		for _, mkt := range marketplaceNames(src.Root(), cfg) {
			for name, v := range sc {
				if _, dup := known[name+"@"+mkt]; !dup {
					known[name+"@"+mkt] = v
				}
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range include {
		sc := known[id]
		if sc == nil || seen[id] || (sc.Status != sidecar.StatusDeprecated) {
			continue
		}
		seen[id] = true
		msg := fmt.Sprintf("plugin %s is deprecated", ui.SanitizeLine(id))
		if sc.SupersededBy != "" {
			msg += "; use " + ui.SanitizeLine(sc.SupersededBy)
		}
		out = append(out, msg+fmt.Sprintf(" (profile %s includes it)", ui.SanitizeLine(r.Name)))
	}
	sort.Strings(out)
	return out
}

// marketplaceNames returns the names of the marketplaces that the org config
// of the source at root lists (catalog.marketplaces). A file that cannot be
// read contributes nothing, so its plugins get no warning.
func marketplaceNames(root string, cfg *orgconfig.Config) []string {
	var names []string
	for _, f := range cfg.Catalog.Marketplaces {
		m, err := marketplace.LoadFile(root, f)
		if err != nil || m.Name == "" {
			continue
		}
		names = append(names, m.Name)
	}
	return names
}

package launcher

import (
	"fmt"
	"sort"

	"github.com/ccshelf/ccshelf/internal/catalog/sidecar"
	"github.com/ccshelf/ccshelf/internal/claude"
	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/ui"
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
		for name, v := range sc {
			if _, dup := known[name]; !dup {
				known[name] = v
			}
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range include {
		name, _ := claude.SplitID(id)
		sc := known[name]
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

package lint

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/ccshelf/ccshelf/internal/catalog/codeowners"
	"github.com/ccshelf/ccshelf/internal/catalog/safepath"
	"github.com/ccshelf/ccshelf/internal/catalog/sidecar"
	"github.com/ccshelf/ccshelf/internal/marketplace"
	"github.com/ccshelf/ccshelf/internal/orgconfig"
)

// BundlePrefix is the name prefix of generated profile bundle plugins.
const BundlePrefix = "profile-"

// PluginRef is a marketplace entry together with what was learned about it.
type PluginRef struct {
	// MarketplaceFile is the repo-relative marketplace.json the entry is in.
	MarketplaceFile string
	// MarketplaceName is that marketplace's name.
	MarketplaceName string
	Plugin          marketplace.Plugin
	// Line is the line of the entry's name in the marketplace file, 0 if unknown.
	Line int
	// Info is the inspection of an in-repo plugin (External for others); nil
	// when inspection failed (see InfoErr).
	Info    *marketplace.PluginInfo
	InfoErr error
	// Dup is true for the second and later entries with the same name.
	Dup bool
}

// IsBundle reports whether the entry is a generated profile bundle.
func (p PluginRef) IsBundle() bool { return strings.HasPrefix(p.Plugin.Name, BundlePrefix) }

// Data is everything the lint and the catalog build read from a repo.
type Data struct {
	Root         string
	Marketplaces []*marketplace.Marketplace
	// MarketplaceFiles parallels Marketplaces.
	MarketplaceFiles []string
	// Plugins lists every entry of every marketplace in file order.
	Plugins []PluginRef
	// Sidecars maps plugin name to its catalog metadata.
	Sidecars map[string]*sidecar.Sidecar
	// Taxonomy is nil when the file does not exist.
	Taxonomy *sidecar.Taxonomy
	// Owners is nil when no CODEOWNERS file exists; OwnersPath is its location.
	Owners     *codeowners.File
	OwnersPath string
	// Profiles are the names of the profile manifests (file names without .toml).
	Profiles []string
	// Findings collected while loading.
	Findings []Finding
}

var entryName = regexp.MustCompile(`"name"\s*:\s*"((?:[^"\\]|\\.)*)"`)

// entryLines maps plugin name to the line of its "name" key inside "plugins".
func entryLines(data []byte) map[string]int {
	out := map[string]int{}
	inPlugins := false
	for i, l := range strings.Split(string(data), "\n") {
		if !inPlugins {
			if strings.Contains(l, `"plugins"`) {
				inPlugins = true
			} else {
				continue
			}
		}
		for _, m := range entryName.FindAllStringSubmatch(l, -1) {
			if _, ok := out[m[1]]; !ok {
				out[m[1]] = i + 1
			}
		}
	}
	return out
}

// LoadData reads everything the rules need. Problems with individual files
// become findings in Data.Findings; the error is only for a root that cannot
// be used at all.
func LoadData(root string, cfg *orgconfig.Config) (*Data, error) {
	st, err := safepath.Stat(root, ".")
	if err != nil {
		return nil, fmt.Errorf("open repository root: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("repository root %q is not a directory", root)
	}
	d := &Data{Root: root, Sidecars: map[string]*sidecar.Sidecar{}}
	fail := func(code, file, msg, hint string) {
		d.Findings = append(d.Findings, Finding{Severity: Error, Code: code, Message: msg, File: file, Hint: hint})
	}

	seen := map[string]bool{}
	for _, mf := range cfg.Catalog.Marketplaces {
		data, err := safepath.ReadFile(root, mf, marketplace.MaxFileSize)
		if err != nil {
			fail("CAT001", mf, "cannot read marketplace file: "+err.Error(), "")
			continue
		}
		m, err := marketplace.Parse(data)
		if err != nil {
			fail("CAT001", mf, "invalid marketplace file: "+err.Error(), "")
			continue
		}
		d.Marketplaces = append(d.Marketplaces, m)
		d.MarketplaceFiles = append(d.MarketplaceFiles, mf)
		lines := entryLines(data)
		for _, p := range m.Plugins {
			ref := PluginRef{MarketplaceFile: mf, MarketplaceName: m.Name, Plugin: p, Line: lines[p.Name], Dup: seen[p.Name]}
			seen[p.Name] = true
			ref.Info, ref.InfoErr = inspect(root, p)
			d.Plugins = append(d.Plugins, ref)
		}
	}

	if cfg.Catalog.MetadataSource == orgconfig.SourceMarketplace {
		for i, m := range d.Marketplaces {
			sc, probs := sidecar.FromMarketplace(d.MarketplaceFiles[i], m)
			for k, v := range sc {
				if _, dup := d.Sidecars[k]; !dup {
					d.Sidecars[k] = v
				}
			}
			d.problems(probs)
		}
	} else {
		sc, probs, err := sidecar.LoadSidecars(root, cfg)
		if err != nil {
			fail("CAT060", sidecar.Dir, "cannot read the sidecar directory: "+err.Error(), "")
		}
		for k, v := range sc {
			d.Sidecars[k] = v
		}
		d.problems(probs)
	}

	tax, err := sidecar.LoadTaxonomy(root, cfg)
	if err != nil {
		fail("CAT008", cfg.Lint.Taxonomy, err.Error(), "")
	}
	d.Taxonomy = tax

	owners, loc, err := codeowners.Find(root)
	if err != nil {
		fail("CAT060", loc, err.Error(), "")
	}
	d.Owners, d.OwnersPath = owners, loc

	entries, err := safepath.ReadDir(root, cfg.Profiles.Dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		fail("CAT060", cfg.Profiles.Dir, "cannot list the profiles directory: "+err.Error(), "")
	default:
		for _, e := range entries {
			n := e.Name()
			if strings.HasSuffix(n, ".toml") && e.Type().IsRegular() {
				d.Profiles = append(d.Profiles, strings.TrimSuffix(n, ".toml"))
			}
		}
		sort.Strings(d.Profiles)
	}
	return d, nil
}

func (d *Data) problems(probs []sidecar.Problem) {
	for _, p := range probs {
		d.Findings = append(d.Findings, Finding{
			Severity: Error, Code: "CAT012", Message: "invalid catalog metadata: " + p.Message,
			File: p.File, Line: p.Line, Plugin: p.Plugin,
		})
	}
}

// inspect inspects a local plugin only when its path is safe.
func inspect(root string, p marketplace.Plugin) (*marketplace.PluginInfo, error) {
	if !p.Source.IsLocal() {
		return &marketplace.PluginInfo{External: true}, nil
	}
	if rel := p.Source.LocalPath(); rel != "" {
		if err := safepath.CheckRel(rel); err != nil {
			return nil, fmt.Errorf("%w: %v", safepath.ErrEscape, err)
		}
	}
	return marketplace.Inspect(root, p)
}

// pluginDir is the repo-relative directory of a local plugin ("" for none).
// PluginDir is the repo-relative directory of a local plugin, or "" for an
// external one.
func (r PluginRef) PluginDir() string {
	if r.Plugin.Source.IsLocal() {
		return path.Clean(r.Plugin.Source.LocalPath())
	}
	return ""
}

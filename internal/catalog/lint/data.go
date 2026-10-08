package lint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/yorch/ccshelf/internal/catalog/codeowners"
	"github.com/yorch/ccshelf/internal/catalog/safepath"
	"github.com/yorch/ccshelf/internal/catalog/sidecar"
	"github.com/yorch/ccshelf/internal/marketplace"
	"github.com/yorch/ccshelf/internal/orgconfig"
)

// BundlePrefix is the name prefix of generated profile bundle plugins.
const BundlePrefix = "profile-"

// PluginRef is a marketplace entry together with what the lint learned about it.
type PluginRef struct {
	// MarketplaceFile is the repo-relative marketplace.json the entry is in.
	MarketplaceFile string
	// MarketplaceName is that marketplace's name.
	MarketplaceName string
	Plugin          marketplace.Plugin
	// Line is the line of the entry's name in the marketplace file, 0 if unknown.
	Line int
	// Info is the inspection of an in-repo plugin (External for others). It is
	// nil when inspection failed (see InfoErr).
	Info    *marketplace.PluginInfo
	InfoErr error
	// Dup is true for the second and later entries with the same name.
	Dup bool
	// bundle is set by LoadData when the name has the bundle prefix and a
	// profile manifest of that name exists.
	bundle bool
}

// IsBundle reports whether the entry is a generated profile bundle: its name
// is profile-<name> and a profile manifest <name>.toml exists. A plugin that
// merely has the prefix is an ordinary plugin (and CAT051 reports it).
func (p PluginRef) IsBundle() bool { return p.bundle }

// HasBundlePrefix reports whether the name starts with the bundle prefix,
// whether or not a profile manifest exists.
func (p PluginRef) HasBundlePrefix() bool { return strings.HasPrefix(p.Plugin.Name, BundlePrefix) }

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
	// Owners is nil when no CODEOWNERS file exists. OwnersPath is its location.
	Owners     *codeowners.File
	OwnersPath string
	// Profiles are the names of the profile manifests (file names without .toml).
	Profiles []string
	// ProfileEmpty maps a profile name to true when it resolves to no plugins
	// (an abstract parent such as a shared base), so it gets no bundle. A
	// profile that cannot be read or parsed is absent from the map.
	ProfileEmpty map[string]bool
	// Findings collected while loading.
	Findings []Finding
}

// entryLines maps plugin name to the line of its "name" key inside the
// top-level "plugins" array. It scans JSON tokens, so author names, escapes
// and text such as "plugins" inside strings cannot confuse the line numbers.
// The first entry with a name wins; malformed input yields what was found.
func entryLines(data []byte) map[string]int {
	out := map[string]int{}
	dec := json.NewDecoder(bytes.NewReader(data))
	line := func() int { return 1 + bytes.Count(data[:dec.InputOffset()], []byte("\n")) }
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return out
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return out
		}
		if key, _ := kt.(string); key != "plugins" {
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return out
			}
			continue
		}
		if t, err := dec.Token(); err != nil || t != json.Delim('[') {
			return out
		}
		for dec.More() {
			if t, err := dec.Token(); err != nil || t != json.Delim('{') {
				// Not an object (or broken): consume if it was a scalar and go on.
				if err != nil {
					return out
				}
				continue
			}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return out
				}
				at := line()
				var raw json.RawMessage
				if dec.Decode(&raw) != nil {
					return out
				}
				if key, _ := kt.(string); key == "name" {
					var name string
					if json.Unmarshal(raw, &name) == nil {
						if _, ok := out[name]; !ok {
							out[name] = at
						}
					}
				}
			}
			if _, err := dec.Token(); err != nil { // closing brace
				return out
			}
		}
		return out
	}
	return out
}

// LoadData reads everything the rules need. Problems with individual files
// become findings in Data.Findings. The error is only for a root that
// LoadData cannot use at all.
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

	if cfg.Catalog.Enabled {
		d.loadCatalog(root, cfg, fail)
	} else {
		d.noteDisabledCatalog(root, cfg)
	}

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
	d.ProfileEmpty = emptyProfiles(root, cfg.Profiles.Dir, d.Profiles)
	have := map[string]bool{}
	for _, n := range d.Profiles {
		have[BundlePrefix+n] = true
	}
	for i := range d.Plugins {
		d.Plugins[i].bundle = have[d.Plugins[i].Plugin.Name]
	}
	return d, nil
}

// noteDisabledCatalog is the whole catalog side of LoadData for a profiles-only
// repo ([catalog] enabled = false): nothing is read, and the files and keys
// that would have been used are reported as ignored (CAT061, CAT062).
func (d *Data) noteDisabledCatalog(root string, cfg *orgconfig.Config) {
	warn := func(code, file, msg, hint string) {
		d.Findings = append(d.Findings, Finding{Severity: Warning, Code: code, Message: msg, File: file, Hint: hint})
	}
	const hint = "delete it, or remove enabled = false from [catalog] in ccshelf.toml to use the catalog"
	for _, mf := range cfg.Catalog.Marketplaces {
		if _, err := safepath.Stat(root, mf); err == nil {
			warn("CAT061", mf, "the lint ignores the marketplace file because [catalog] enabled = false", hint)
		}
	}
	if st, err := safepath.Stat(root, "bundles"); err == nil && st.IsDir() {
		warn("CAT061", "bundles", "the lint ignores bundles/ because [catalog] enabled = false", hint)
	}
	if len(cfg.CatalogIgnored) > 0 {
		warn("CAT062", orgconfig.FileName, "these [catalog] keys have no effect because enabled = false: "+strings.Join(cfg.CatalogIgnored, ", "),
			"remove them, or remove enabled = false to use the catalog")
	}
}

// loadCatalog reads the marketplaces, the catalog metadata and the taxonomy.
func (d *Data) loadCatalog(root string, cfg *orgconfig.Config, fail func(code, file, msg, hint string)) {
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
			return nil, fmt.Errorf("%w: %w", safepath.ErrEscape, err)
		}
	}
	return marketplace.Inspect(root, p)
}

// PluginDir is the repo-relative directory of a local plugin, or "" for an
// external one.
func (p PluginRef) PluginDir() string {
	if p.Plugin.Source.IsLocal() {
		return path.Clean(p.Plugin.Source.LocalPath())
	}
	return ""
}

// maxProfileSize caps one profile manifest read for emptiness.
const maxProfileSize = 1 << 20

// emptyProfiles reports, per readable profile, whether it resolves to no
// plugins: no plugins.include in itself or any parent it extends, after the
// excludes. It reads only the keys it needs and is lenient; the launcher is
// the strict parser.
func emptyProfiles(root, dir string, names []string) map[string]bool {
	type man struct {
		Extends []string `toml:"extends"`
		Plugins struct {
			Include []string `toml:"include"`
			Exclude []string `toml:"exclude"`
		} `toml:"plugins"`
	}
	loaded := map[string]*man{}
	for _, n := range names {
		data, err := safepath.ReadFile(root, path.Join(dir, n+".toml"), maxProfileSize)
		if err != nil {
			continue
		}
		var m man
		if toml.Unmarshal(data, &m) != nil {
			continue
		}
		loaded[n] = &m
	}
	var resolve func(n string, depth int) (map[string]bool, bool)
	resolve = func(n string, depth int) (map[string]bool, bool) {
		m := loaded[n]
		if m == nil || depth > 16 {
			return nil, false
		}
		out := map[string]bool{}
		for _, par := range m.Extends {
			ids, ok := resolve(par, depth+1)
			if !ok {
				return nil, false
			}
			for id := range ids {
				out[id] = true
			}
		}
		for _, id := range m.Plugins.Include {
			out[id] = true
		}
		for _, id := range m.Plugins.Exclude {
			delete(out, id)
		}
		return out, true
	}
	res := map[string]bool{}
	for _, n := range names {
		if ids, ok := resolve(n, 0); ok {
			res[n] = len(ids) == 0
		}
	}
	return res
}

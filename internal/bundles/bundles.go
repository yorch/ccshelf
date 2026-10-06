package bundles

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Prefix is the directory prefix of generated bundles.
const Prefix = "profile-"

// Dir is the directory, relative to the data repo root, that holds bundles.
const Dir = "bundles"

var (
	profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	pluginPart  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Input is one profile to compile.
type Input struct {
	// Profile is the profile name (^[a-z0-9][a-z0-9-]{0,62}$).
	Profile string
	// Marketplace is the name of the org marketplace that will host the
	// bundle (required, same character set as a plugin part). Plugins of
	// this marketplace are written as bare names; plugins of any other
	// marketplace are written as {"marketplace", "name"} objects.
	Marketplace string
	// Plugins are the profile's resolved plugins.include ids, name@marketplace.
	// An empty list marks an abstract profile (such as a shared base): it is
	// skipped, not an error.
	Plugins []string
}

// File is one generated file.
type File struct {
	// Path is relative to the data repo root, with forward slashes.
	Path string
	// Content is the exact bytes.
	Content []byte
}

// BundleInfo describes one compiled bundle.
type BundleInfo struct {
	// Profile is the profile name.
	Profile string
	// Path is the manifest path (same as the matching File.Path).
	Path string
	// CrossMarketplaces are the other marketplaces the bundle depends on,
	// sorted. The hosting marketplace must list each of them in
	// allowCrossMarketplaceDependenciesOn or Claude Code will not install
	// those dependencies.
	CrossMarketplaces []string
}

// Result is the output of Compile.
type Result struct {
	// Files are the generated files, sorted by path.
	Files []File
	// Bundles has one entry per generated file, in the same order.
	Bundles []BundleInfo
	// Skipped are the profiles that resolve to no plugins (abstract
	// profiles), sorted.
	Skipped []string
}

// crossDep is the object form of a dependency; the field order is the
// canonical key order (marketplace, name).
type crossDep struct {
	Marketplace string `json:"marketplace"`
	Name        string `json:"name"`
}

// manifest fixes the key order: dependencies, then name. Each dependency is
// a string or a crossDep.
type manifest struct {
	Dependencies []any  `json:"dependencies"`
	Name         string `json:"name"`
}

// FilePath returns the relative path of the bundle manifest of a profile.
func FilePath(profile string) string {
	return Dir + "/" + Prefix + profile + "/.claude-plugin/plugin.json"
}

// render produces the canonical bytes.
func render(name string, deps []any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest{Dependencies: deps, Name: name}); err != nil {
		return nil, fmt.Errorf("encode bundle manifest: %w", err)
	}
	return buf.Bytes(), nil // Encode already ends with one newline
}

// Compile generates one bundle manifest per input with plugins, sorted by
// profile name. Profiles without plugins are reported in Result.Skipped. It
// rejects invalid profile names, duplicate profiles, a missing or invalid
// marketplace and invalid plugin ids.
func Compile(in []Input) (*Result, error) {
	sorted := append([]Input(nil), in...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Profile < sorted[j].Profile })
	res := &Result{}
	seen := map[string]bool{}
	for _, x := range sorted {
		if !profileName.MatchString(x.Profile) {
			return nil, fmt.Errorf("invalid profile name %q: must match %s", x.Profile, profileName)
		}
		if seen[x.Profile] {
			return nil, fmt.Errorf("profile %q given more than once", x.Profile)
		}
		seen[x.Profile] = true
		if !pluginPart.MatchString(x.Marketplace) {
			return nil, fmt.Errorf("profile %q: invalid or missing hosting marketplace %q", x.Profile, x.Marketplace)
		}
		if len(x.Plugins) == 0 {
			res.Skipped = append(res.Skipped, x.Profile)
			continue
		}
		type key struct{ name, mkt string }
		uniq := map[key]bool{}
		for _, id := range x.Plugins {
			name, mkt, err := splitID(id)
			if err != nil {
				return nil, fmt.Errorf("profile %q: %w", x.Profile, err)
			}
			uniq[key{name, mkt}] = true
		}
		keys := make([]key, 0, len(uniq))
		for k := range uniq {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, b := keys[i].name+"@"+keys[i].mkt, keys[j].name+"@"+keys[j].mkt
			return a < b
		})
		deps := make([]any, 0, len(keys))
		cross := map[string]bool{}
		for _, k := range keys {
			if k.mkt == x.Marketplace {
				deps = append(deps, k.name)
				continue
			}
			deps = append(deps, crossDep{Marketplace: k.mkt, Name: k.name})
			cross[k.mkt] = true
		}
		content, err := render(Prefix+x.Profile, deps)
		if err != nil {
			return nil, err
		}
		info := BundleInfo{Profile: x.Profile, Path: FilePath(x.Profile)}
		for m := range cross {
			info.CrossMarketplaces = append(info.CrossMarketplaces, m)
		}
		sort.Strings(info.CrossMarketplaces)
		res.Files = append(res.Files, File{Path: info.Path, Content: content})
		res.Bundles = append(res.Bundles, info)
	}
	return res, nil
}

func splitID(id string) (name, mkt string, err error) {
	name, mkt, ok := strings.Cut(id, "@")
	if !ok || strings.Contains(mkt, "@") {
		return "", "", fmt.Errorf("invalid plugin id %q: want name@marketplace", id)
	}
	if !pluginPart.MatchString(name) || !pluginPart.MatchString(mkt) {
		return "", "", fmt.Errorf("invalid plugin id %q: name and marketplace may use letters, digits, '.', '_' and '-'", id)
	}
	return name, mkt, nil
}

var errPath = errors.New("unsafe path")

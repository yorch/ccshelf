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
	// Plugins are the profile's resolved plugins.include ids, name@marketplace.
	Plugins []string
}

// File is one generated file.
type File struct {
	// Path is relative to the data repo root, with forward slashes.
	Path string
	// Content is the exact bytes.
	Content []byte
}

// manifest fixes the key order: dependencies, then name.
type manifest struct {
	Dependencies []string `json:"dependencies"`
	Name         string   `json:"name"`
}

// FilePath returns the relative path of the bundle manifest of a profile.
func FilePath(profile string) string {
	return Dir + "/" + Prefix + profile + "/.claude-plugin/plugin.json"
}

// render produces the canonical bytes.
func render(name string, deps []string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest{Dependencies: deps, Name: name}); err != nil {
		return nil, fmt.Errorf("encode bundle manifest: %w", err)
	}
	return buf.Bytes(), nil // Encode already ends with one newline
}

// Compile generates one bundle manifest per input, sorted by profile name.
// It rejects invalid profile names, duplicate profiles, a profile without
// plugins, invalid plugin ids and ambiguous names.
func Compile(in []Input) ([]File, error) {
	sorted := append([]Input(nil), in...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Profile < sorted[j].Profile })
	var files []File
	seen := map[string]bool{}
	for _, x := range sorted {
		if !profileName.MatchString(x.Profile) {
			return nil, fmt.Errorf("invalid profile name %q: must match %s", x.Profile, profileName)
		}
		if seen[x.Profile] {
			return nil, fmt.Errorf("profile %q given more than once", x.Profile)
		}
		seen[x.Profile] = true
		if len(x.Plugins) == 0 {
			return nil, fmt.Errorf("profile %q has no plugins: a bundle needs at least one", x.Profile)
		}
		names := map[string]string{} // plugin name -> marketplace
		for _, id := range x.Plugins {
			name, mkt, err := splitID(id)
			if err != nil {
				return nil, fmt.Errorf("profile %q: %w", x.Profile, err)
			}
			if prev, ok := names[name]; ok && prev != mkt {
				return nil, fmt.Errorf("profile %q: plugin %q comes from two marketplaces (%s and %s); bundle dependencies are bare names and would be ambiguous", x.Profile, name, prev, mkt)
			}
			names[name] = mkt
		}
		deps := make([]string, 0, len(names))
		for n := range names {
			deps = append(deps, n)
		}
		sort.Strings(deps)
		content, err := render(Prefix+x.Profile, deps)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Path: FilePath(x.Profile), Content: content})
	}
	return files, nil
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

// isGenerated reports whether content is byte-identical to a generated
// manifest for the given directory name.
func isGenerated(dirName string, content []byte) bool {
	var m manifest
	dec := json.NewDecoder(bytes.NewReader(content))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil || m.Name != dirName || len(m.Dependencies) == 0 {
		return false
	}
	want, err := render(m.Name, m.Dependencies)
	return err == nil && bytes.Equal(want, content)
}

var errPath = errors.New("unsafe path")

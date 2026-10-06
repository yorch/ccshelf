package marketplace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ccshelf/ccshelf/internal/catalog/safepath"
)

// FilePath is the location of the marketplace file below a repository root.
const FilePath = ".claude-plugin/marketplace.json"

// MaxFileSize is the size limit for marketplace.json.
const MaxFileSize = 8 << 20

// Owner is the marketplace owner block.
type Owner struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Author is a plugin author, written either as a string or as an object.
type Author struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// UnmarshalJSON accepts a plain string (the name) or an object.
func (a *Author) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*a = Author{Name: s}
		return nil
	}
	type plain Author
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return errors.New("must be a string or an object")
	}
	*a = Author(p)
	return nil
}

// IsZero reports whether no author information is present.
func (a Author) IsZero() bool { return a == Author{} }

// String returns the name, or the email or URL when there is no name.
func (a Author) String() string {
	switch {
	case a.Name != "":
		return a.Name
	case a.Email != "":
		return a.Email
	default:
		return a.URL
	}
}

// Dependency is one entry of a plugin's dependencies, written as a name string
// ("name" or "name@marketplace") or as an object.
type Dependency struct {
	Name        string `json:"name"`
	Version     string `json:"version,omitempty"`
	Marketplace string `json:"marketplace,omitempty"`
}

// UnmarshalJSON accepts a string or an object.
func (d *Dependency) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		name, mkt, _ := strings.Cut(s, "@")
		*d = Dependency{Name: name, Marketplace: mkt}
		return nil
	}
	type plain Dependency
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return errors.New("must be a string or an object")
	}
	*d = Dependency(p)
	return nil
}

// Source says where a plugin comes from. A string source is a relative path
// inside the marketplace repository (Kind "path"); an object source has a Kind
// taken from its "source" key (for example "github", "url", "git-subdir" or
// "npm") and keeps every key in Raw.
type Source struct {
	// Path is the local path relative to the repository root, slash
	// separated, without a leading "./" and with metadata.pluginRoot applied.
	// It is empty for external sources.
	Path string
	// Declared is the path as written in the file.
	Declared string
	// Kind is "path" for a string source, else the object's "source" value.
	Kind string
	// Raw holds the object form, nil for a string source.
	Raw map[string]any
}

// IsLocal reports whether the plugin lives inside the marketplace repository.
func (s Source) IsLocal() bool { return s.Kind == "path" }

// LocalPath returns the repository-relative path of a local plugin, or "".
func (s Source) LocalPath() string {
	if !s.IsLocal() {
		return ""
	}
	return s.Path
}

// Summary returns a short description such as "plugins/design-kit" or
// "github:owner/repo", built only from known keys.
func (s Source) Summary() string {
	if s.IsLocal() {
		return s.Path
	}
	kind := s.Kind
	if kind == "" {
		kind = "unknown"
	}
	for _, k := range []string{"repo", "url", "package"} {
		if v, ok := s.Raw[k].(string); ok && v != "" {
			return kind + ":" + v
		}
	}
	return kind
}

// ManifestDep is one manifestDeps relevance signal.
type ManifestDep struct {
	File    string `json:"file"`
	Pattern string `json:"pattern"`
}

// Signals are the relevance signals of a plugin.
type Signals struct {
	Cwd          []string      `json:"cwd,omitempty"`
	CLI          []string      `json:"cli,omitempty"`
	Hosts        []string      `json:"hosts,omitempty"`
	FilesRead    []string      `json:"filesRead,omitempty"`
	ManifestDeps []ManifestDep `json:"manifestDeps,omitempty"`
}

// Relevance is the relevance block of a plugin entry.
type Relevance struct {
	Topic   string  `json:"topic,omitempty"`
	Signals Signals `json:"signals"`
}

// Plugin is one entry of the marketplace's plugin list.
type Plugin struct {
	Name           string
	Source         Source
	Description    string
	Category       string
	Tags           []string
	Version        string
	Strict         *bool
	DisplayName    string
	DefaultEnabled *bool
	Author         Author
	Homepage       string
	Repository     string
	License        string
	Keywords       []string
	Dependencies   []Dependency
	Relevance      *Relevance
	// Metadata is free-form and not read by Claude Code; ccshelf reads it only
	// in single-file catalog mode.
	Metadata map[string]any
	// Extra holds unknown keys (and inline component keys such as hooks and
	// mcpServers) exactly as written.
	Extra map[string]json.RawMessage
}

// Marketplace is a parsed marketplace.json.
type Marketplace struct {
	Name        string
	Owner       Owner
	Description string
	Plugins     []Plugin
	// Renames maps an old plugin name to its new name; a null value means the
	// plugin was removed.
	Renames                             map[string]*string
	ForceRemoveDeletedPlugins           bool
	AllowCrossMarketplaceDependenciesOn []string
	// PluginRoot is metadata.pluginRoot, the base directory for bare relative
	// plugin source paths.
	PluginRoot string
	// Raw holds unknown top-level keys (and unknown metadata keys as
	// "metadata.<key>") exactly as written.
	Raw map[string]json.RawMessage
}

// Plugin returns the entry with the given name.
func (m *Marketplace) Plugin(name string) (Plugin, bool) {
	for _, p := range m.Plugins {
		if p.Name == name {
			return p, true
		}
	}
	return Plugin{}, false
}

// Load reads <root>/.claude-plugin/marketplace.json.
func Load(root string) (*Marketplace, error) {
	data, err := safepath.ReadFile(root, FilePath, MaxFileSize)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", FilePath, err)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FilePath, err)
	}
	return m, nil
}

// LoadFile reads a marketplace file at the repository-relative path rel.
func LoadFile(root, rel string) (*Marketplace, error) {
	data, err := safepath.ReadFile(root, rel, MaxFileSize)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	return m, nil
}

// decoder accumulates the first type error with its JSON path.
type decoder struct{ err error }

func (d *decoder) field(path string, raw json.RawMessage, dst any) {
	if d.err != nil {
		return
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		d.err = fmt.Errorf("%s: %s", path, describe(err))
	}
}

func describe(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return "expected " + te.Type.String() + " but found " + te.Value
	}
	return err.Error()
}

// Parse parses marketplace.json bytes.
func Parse(data []byte) (*Marketplace, error) {
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("file is larger than %d bytes", MaxFileSize)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("invalid JSON object: %s", describe(err))
	}
	m := &Marketplace{Raw: map[string]json.RawMessage{}}
	d := &decoder{}
	for _, k := range sortedKeys(top) {
		raw := top[k]
		switch k {
		case "name":
			d.field("name", raw, &m.Name)
		case "owner":
			d.field("owner", raw, &m.Owner)
		case "description":
			d.field("description", raw, &m.Description)
		case "renames":
			d.field("renames", raw, &m.Renames)
		case "forceRemoveDeletedPlugins":
			d.field("forceRemoveDeletedPlugins", raw, &m.ForceRemoveDeletedPlugins)
		case "allowCrossMarketplaceDependenciesOn":
			d.field("allowCrossMarketplaceDependenciesOn", raw, &m.AllowCrossMarketplaceDependenciesOn)
		case "metadata":
			var meta map[string]json.RawMessage
			d.field("metadata", raw, &meta)
			for _, mk := range sortedKeys(meta) {
				switch mk {
				case "description":
					var s string
					d.field("metadata.description", meta[mk], &s)
					if m.Description == "" {
						m.Description = s
					}
				case "pluginRoot":
					d.field("metadata.pluginRoot", meta[mk], &m.PluginRoot)
				default:
					m.Raw["metadata."+mk] = meta[mk]
				}
			}
		case "plugins":
			var list []json.RawMessage
			d.field("plugins", raw, &list)
			for i, pr := range list {
				if d.err != nil {
					break
				}
				p, err := parsePlugin(i, pr)
				if err != nil {
					d.err = err
					break
				}
				m.Plugins = append(m.Plugins, p)
			}
		default:
			m.Raw[k] = raw
		}
		if d.err != nil {
			return nil, d.err
		}
	}
	if len(m.Raw) == 0 {
		m.Raw = nil
	}
	if m.Name == "" {
		return nil, errors.New("name: required")
	}
	for i := range m.Plugins {
		applyPluginRoot(m, &m.Plugins[i])
	}
	return m, nil
}

func parsePlugin(i int, raw json.RawMessage) (Plugin, error) {
	at := func(k string) string { return fmt.Sprintf("plugins[%d].%s", i, k) }
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return Plugin{}, fmt.Errorf("plugins[%d]: %s", i, describe(err))
	}
	var p Plugin
	d := &decoder{}
	for _, k := range sortedKeys(obj) {
		v := obj[k]
		switch k {
		case "name":
			d.field(at(k), v, &p.Name)
		case "source":
			p.Source = parseSource(at(k), v, d)
		case "description":
			d.field(at(k), v, &p.Description)
		case "category":
			d.field(at(k), v, &p.Category)
		case "tags":
			d.field(at(k), v, &p.Tags)
		case "version":
			d.field(at(k), v, &p.Version)
		case "strict":
			d.field(at(k), v, &p.Strict)
		case "displayName":
			d.field(at(k), v, &p.DisplayName)
		case "defaultEnabled":
			d.field(at(k), v, &p.DefaultEnabled)
		case "author":
			d.field(at(k), v, &p.Author)
		case "homepage":
			d.field(at(k), v, &p.Homepage)
		case "repository":
			d.field(at(k), v, &p.Repository)
		case "license":
			d.field(at(k), v, &p.License)
		case "keywords":
			d.field(at(k), v, &p.Keywords)
		case "dependencies":
			d.field(at(k), v, &p.Dependencies)
		case "relevance":
			d.field(at(k), v, &p.Relevance)
		case "metadata":
			d.field(at(k), v, &p.Metadata)
		default:
			if p.Extra == nil {
				p.Extra = map[string]json.RawMessage{}
			}
			p.Extra[k] = v
		}
		if d.err != nil {
			return Plugin{}, d.err
		}
	}
	if p.Name == "" {
		return Plugin{}, fmt.Errorf("plugins[%d].name: required", i)
	}
	if _, ok := obj["source"]; !ok {
		return Plugin{}, fmt.Errorf("plugins[%d].source: required (plugin %q)", i, p.Name)
	}
	return p, nil
}

func parseSource(at string, raw json.RawMessage, d *decoder) Source {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		d.field(at, raw, &s)
		return Source{Kind: "path", Declared: s, Path: s}
	}
	var o map[string]any
	d.field(at, raw, &o)
	if d.err != nil {
		return Source{}
	}
	kind, _ := o["source"].(string)
	return Source{Kind: kind, Raw: o}
}

// applyPluginRoot normalizes a local source path: a bare relative path gets
// metadata.pluginRoot prepended, then the path is cleaned and stripped of a
// leading "./". A path that stays unusable (absolute, or containing "..")
// is left cleaned but is reported by lint through safepath.
func applyPluginRoot(m *Marketplace, p *Plugin) {
	s := &p.Source
	if !s.IsLocal() {
		return
	}
	rel := s.Declared
	if m.PluginRoot != "" && !strings.HasPrefix(rel, "./") && !strings.HasPrefix(rel, "/") && !strings.HasPrefix(rel, "../") {
		rel = m.PluginRoot + "/" + rel
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	cleaned := path.Clean(rel)
	if cleaned == "." {
		cleaned = ""
	}
	s.Path = cleaned
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

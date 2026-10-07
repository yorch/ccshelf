package marketplace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/yorch/ccshelf/internal/catalog/safepath"
)

// maxManifestSize caps plugin.json and .mcp.json reads.
const maxManifestSize = 1 << 20

// PluginInfo describes an in-repo plugin directory.
type PluginInfo struct {
	// External is true for a plugin whose source is not in this repository;
	// nothing else is filled in.
	External bool
	// Dir is the plugin directory relative to the repository root.
	Dir string
	// ManifestMissing is true when .claude-plugin/plugin.json does not exist
	// (allowed: the marketplace entry can describe the plugin).
	ManifestMissing bool
	// Name, Version, Description and Dependencies come from plugin.json.
	Name         string
	Version      string
	Description  string
	Dependencies []Dependency
	// HasHooks is true for a hooks/ directory, a hooks key in plugin.json or
	// in the marketplace entry.
	HasHooks bool
	// HasMCP is true for a .mcp.json file or an mcpServers key in plugin.json
	// or in the marketplace entry.
	HasMCP bool
	// HasLSP is true for a .lsp.json file or an lspServers key in plugin.json
	// or in the marketplace entry. LSP servers run a command {U}.
	HasLSP bool
	// ManifestExec lists, sorted, the exact plugin.json keys that declare
	// executable components: hooks, mcpServers, lspServers, monitors and
	// experimental.monitors. Non-empty means plugin.json itself carries or
	// points at code that runs on developers' machines.
	ManifestExec []string
	// ExecFiles are the repository-relative, slash-separated files that exist
	// and carry hook, MCP, LSP or monitor configuration: the default files
	// (hooks/hooks.json, .mcp.json, .lsp.json, monitors/monitors.json) and
	// every path that plugin.json declares for those keys.
	ExecFiles []string
	// ScriptRefs are repository-relative paths named as
	// ${CLAUDE_PLUGIN_ROOT}/<path> inside hook, MCP, LSP or monitor
	// configuration (inline, in the entry, or in an ExecFile). They are files
	// that run code and so need the same ownership as the configuration.
	ScriptRefs []string
	// Skills counts skills/*/SKILL.md; Agents counts agents/*.md; Commands
	// counts commands/*.md.
	Skills, Agents, Commands int
}

// Inspect looks at the plugin directory of a local plugin. It never follows a
// symlink out of root. A missing directory fails with an error wrapping
// fs.ErrNotExist; a malformed plugin.json is an error naming the file.
func Inspect(root string, p Plugin) (*PluginInfo, error) {
	if !p.Source.IsLocal() {
		return &PluginInfo{External: true}, nil
	}
	dir := p.Source.LocalPath()
	if dir == "" {
		dir = "."
	}
	info := &PluginInfo{Dir: dir}
	rel := func(parts ...string) string {
		if dir == "." {
			return strings.Join(parts, "/")
		}
		return dir + "/" + strings.Join(parts, "/")
	}
	st, err := safepath.Stat(root, dir)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: %w", p.Name, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("plugin %q: source %q is not a directory", p.Name, dir)
	}

	mpath := rel(".claude-plugin", "plugin.json")
	data, err := safepath.ReadFile(root, mpath, maxManifestSize)
	var manifestExec map[string]json.RawMessage
	switch {
	case errors.Is(err, fs.ErrNotExist):
		info.ManifestMissing = true
	case err != nil:
		return nil, fmt.Errorf("plugin %q: read %s: %w", p.Name, mpath, err)
	default:
		man, err := decodeManifest(data)
		if err != nil {
			return nil, fmt.Errorf("plugin %q: %s: %w", p.Name, mpath, err)
		}
		if err := man.fill(info); err != nil {
			return nil, fmt.Errorf("plugin %q: %s: %w", p.Name, mpath, err)
		}
		manifestExec = man.fields
	}
	info.ManifestExec = ExecKeys(manifestExec)
	for _, k := range info.ManifestExec {
		switch k {
		case "hooks":
			info.HasHooks = true
		case "mcpServers":
			info.HasMCP = true
		case "lspServers":
			info.HasLSP = true
		}
	}
	entryExec := ExecKeys(p.Extra)
	for _, k := range entryExec {
		switch k {
		case "hooks":
			info.HasHooks = true
		case "mcpServers":
			info.HasMCP = true
		case "lspServers":
			info.HasLSP = true
		}
	}
	if safepath.IsDir(root, rel("hooks")) {
		info.HasHooks = true
	}
	if safepath.IsFile(root, rel(".mcp.json")) {
		info.HasMCP = true
	}
	if safepath.IsFile(root, rel(".lsp.json")) {
		info.HasLSP = true
	}
	if err := collectExec(root, dir, rel, p.Name, manifestExec, p.Extra, info); err != nil {
		return nil, err
	}

	if entries, err := safepath.ReadDir(root, rel("skills")); err == nil {
		for _, e := range entries {
			if safepath.IsFile(root, rel("skills", e.Name(), "SKILL.md")) {
				info.Skills++
			}
		}
	}
	info.Agents = countMarkdown(root, rel("agents"))
	info.Commands = countMarkdown(root, rel("commands"))
	return info, nil
}

func countMarkdown(root, dir string) int {
	entries, err := safepath.ReadDir(root, dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".md") && safepath.IsFile(root, dir+"/"+e.Name()) {
			n++
		}
	}
	return n
}

// execTopKeys are the plugin.json keys that declare components running code.
var execTopKeys = []string{"hooks", "lspServers", "mcpServers", "monitors"}

// ExecKeys returns, sorted, which of the executable-component keys (hooks,
// lspServers, mcpServers, monitors and experimental.monitors) are present
// with a non-null value in m. Key names are matched exactly, as Claude Code
// does {U}. It works on a plugin.json object or a marketplace entry's extra
// keys.
func ExecKeys(m map[string]json.RawMessage) []string {
	var out []string
	for _, k := range execTopKeys {
		if v, ok := m[k]; ok && !isNull(v) {
			out = append(out, k)
		}
	}
	if raw, ok := m["experimental"]; ok {
		var exp map[string]json.RawMessage
		if json.Unmarshal(raw, &exp) == nil {
			if v, ok := exp["monitors"]; ok && !isNull(v) {
				out = append(out, "experimental.monitors")
			}
		}
	}
	sort.Strings(out)
	return out
}

func isNull(v json.RawMessage) bool {
	t := bytes.TrimSpace(v)
	return len(t) == 0 || string(t) == "null"
}

// manifest is a plugin.json decoded to its exact top-level keys.
type manifest struct {
	fields map[string]json.RawMessage
}

// decodeManifest parses plugin.json into exact-case keys. Unlike a struct
// decode it cannot be fooled by {"hooks": {...}, "HOOKS": null}: any key that
// repeats, in the same or a different case, is an error.
func decodeManifest(data []byte) (*manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, errors.New(describe(err))
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("expected a JSON object")
	}
	m := &manifest{fields: map[string]json.RawMessage{}}
	folded := map[string]string{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, errors.New(describe(err))
		}
		key, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, errors.New(describe(err))
		}
		lower := strings.ToLower(key)
		if prev, dup := folded[lower]; dup {
			if prev == key {
				return nil, fmt.Errorf("duplicate key %q", key)
			}
			return nil, fmt.Errorf("keys %q and %q differ only in letter case; Claude Code and this tool could read different ones", prev, key)
		}
		folded[lower] = key
		m.fields[key] = raw
	}
	if _, err := dec.Token(); err != nil { // closing brace
		return nil, errors.New(describe(err))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return m, nil
}

// ExactKeys decodes a JSON object into its top-level keys, matched exactly.
// It is the strict reading that the rest of the tool applies to plugin.json:
// a key that repeats, in the same or in a different letter case, is an error
// (Go's own decoder would silently keep the last one of a case-insensitive
// match, and Claude Code could read another).
func ExactKeys(data []byte) (map[string]json.RawMessage, error) {
	m, err := decodeManifest(data)
	if err != nil {
		return nil, err
	}
	return m.fields, nil
}

// fill copies name, version, description and dependencies into info.
func (m *manifest) fill(info *PluginInfo) error {
	get := func(key string, dst any) error {
		raw, ok := m.fields[key]
		if !ok {
			return nil
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return fmt.Errorf("%s: %s", key, describe(err))
		}
		return nil
	}
	for _, f := range []struct {
		key string
		dst any
	}{{"name", &info.Name}, {"version", &info.Version}, {"description", &info.Description}, {"dependencies", &info.Dependencies}} {
		if err := get(f.key, f.dst); err != nil {
			return err
		}
	}
	return nil
}

// defaultExecFiles are loaded by Claude Code without being declared.
var defaultExecFiles = []string{"hooks/hooks.json", ".mcp.json", ".lsp.json", "monitors/monitors.json"}

var scriptRefRe = regexp.MustCompile(`\$\{?CLAUDE_PLUGIN_ROOT\}?[/\\]([A-Za-z0-9_./\\@+-]+)`)

// collectExec fills ExecFiles and ScriptRefs.
func collectExec(root, dir string, rel func(...string) string, name string, manifest, entry map[string]json.RawMessage, info *PluginInfo) error {
	seen := map[string]bool{}
	var files []string
	add := func(p string) {
		if !seen[p] && safepath.IsFile(root, p) {
			seen[p] = true
			files = append(files, p)
		}
	}
	for _, d := range defaultExecFiles {
		add(rel(strings.Split(d, "/")...))
	}
	for _, key := range append(append([]string{}, execTopKeys...), "experimental") {
		raw, ok := manifest[key]
		if !ok {
			continue
		}
		if key == "experimental" {
			var exp map[string]json.RawMessage
			if json.Unmarshal(raw, &exp) != nil {
				continue
			}
			raw = exp["monitors"]
		}
		for _, decl := range declaredPaths(raw) {
			p := path.Join(dir, decl)
			if !within(dir, p) {
				return fmt.Errorf("plugin %q: plugin.json: %s path %q is outside the plugin directory", name, key, decl)
			}
			if err := safepath.CheckRel(p); err != nil {
				return fmt.Errorf("plugin %q: plugin.json: %s path %q: %w", name, key, decl, err)
			}
			add(p)
		}
	}
	sort.Strings(files)
	info.ExecFiles = files

	var texts []json.RawMessage
	for _, k := range append(append([]string{}, execTopKeys...), "experimental") {
		if v, ok := manifest[k]; ok {
			texts = append(texts, v)
		}
		if v, ok := entry[k]; ok {
			texts = append(texts, v)
		}
	}
	for _, f := range files {
		data, err := safepath.ReadFile(root, f, maxManifestSize)
		if err != nil {
			return fmt.Errorf("plugin %q: read %s: %w", name, f, err)
		}
		texts = append(texts, data)
	}
	refs := map[string]bool{}
	for _, t := range texts {
		for _, s := range stringsOf(t) {
			for _, m := range scriptRefRe.FindAllStringSubmatch(s, -1) {
				ref := strings.ReplaceAll(m[1], "\\", "/")
				p := path.Join(dir, ref)
				if p == ".." || strings.HasPrefix(p, "../") || seen[p] {
					continue
				}
				refs[p] = true
			}
		}
	}
	for r := range refs {
		info.ScriptRefs = append(info.ScriptRefs, r)
	}
	sort.Strings(info.ScriptRefs)
	return nil
}

// within reports whether p equals dir or lies below it (both cleaned,
// slash separated, "." meaning the repository root).
func within(dir, p string) bool {
	if p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	return dir == "." || p == dir || strings.HasPrefix(p, dir+"/")
}

// declaredPaths returns the relative file paths named by a component key: a
// string, or the strings of an array. Objects are inline configuration, and
// URLs (an mcpServers bundle) are not repository paths.
func declaredPaths(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return pathsOnly([]string{one})
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return nil
	}
	var out []string
	for _, e := range arr {
		var s string
		if json.Unmarshal(e, &s) == nil {
			out = append(out, s)
		}
	}
	return pathsOnly(out)
}

func pathsOnly(in []string) []string {
	var out []string
	for _, s := range in {
		if s != "" && !strings.Contains(s, "://") {
			out = append(out, strings.ReplaceAll(s, "\\", "/"))
		}
	}
	return out
}

// stringsOf returns every string value found in JSON text, or the text
// itself when it is not valid JSON.
func stringsOf(raw []byte) []string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return []string{string(raw)}
	}
	var out []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			out = append(out, t)
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}

package marketplace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/ccshelf/ccshelf/internal/catalog/safepath"
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
	switch {
	case errors.Is(err, fs.ErrNotExist):
		info.ManifestMissing = true
	case err != nil:
		return nil, fmt.Errorf("plugin %q: read %s: %w", p.Name, mpath, err)
	default:
		var man struct {
			Name         string          `json:"name"`
			Version      string          `json:"version"`
			Description  string          `json:"description"`
			Dependencies []Dependency    `json:"dependencies"`
			Hooks        json.RawMessage `json:"hooks"`
			MCPServers   json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(data, &man); err != nil {
			return nil, fmt.Errorf("plugin %q: %s: %s", p.Name, mpath, describe(err))
		}
		info.Name, info.Version, info.Description, info.Dependencies = man.Name, man.Version, man.Description, man.Dependencies
		info.HasHooks = len(man.Hooks) > 0 && string(man.Hooks) != "null"
		info.HasMCP = len(man.MCPServers) > 0 && string(man.MCPServers) != "null"
	}
	if _, ok := p.Extra["hooks"]; ok {
		info.HasHooks = true
	}
	if _, ok := p.Extra["mcpServers"]; ok {
		info.HasMCP = true
	}
	if safepath.IsDir(root, rel("hooks")) {
		info.HasHooks = true
	}
	if safepath.IsFile(root, rel(".mcp.json")) {
		info.HasMCP = true
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

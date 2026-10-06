package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Plugin is one installed plugin as reported by `claude plugin list --json`.
type Plugin struct {
	// ID is the full identifier, name@marketplace.
	ID string
	// Name and Marketplace are derived from ID (Marketplace is empty when
	// the ID has no "@").
	Name, Marketplace string
	// Version, Scope (user, project, local, synced, ...), InstallPath,
	// InstalledAt and LastUpdated are passed through as reported.
	Version, Scope, InstallPath, InstalledAt, LastUpdated string
	// Enabled is true when the plugin is enabled in the current context;
	// ProjectEnabled when the project settings enable it.
	Enabled, ProjectEnabled bool
	// MCPServers holds the plugin's MCP server definitions, uninterpreted.
	MCPServers map[string]json.RawMessage
	// RequiredByOrg is best effort: true when the JSON carries any of
	// requiredByOrg, requiredByPolicy, managed or forced set to true. Claude
	// Code's exact marker is not documented, so a false value does not prove
	// the plugin can be masked.
	RequiredByOrg bool
	// Extra keeps every key not listed above, so nothing is lost.
	Extra map[string]json.RawMessage
}

var requiredKeys = []string{"requiredByOrg", "requiredByPolicy", "managed", "forced"}

// SplitID splits name@marketplace. The marketplace is empty without an "@".
func SplitID(id string) (name, marketplace string) {
	i := strings.LastIndex(id, "@")
	if i < 0 {
		return id, ""
	}
	return id[:i], id[i+1:]
}

// UnmarshalJSON decodes one plugin object. Known keys with the wrong type
// are errors; unknown keys land in Extra.
func (p *Plugin) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("plugin entry: %w", err)
	}
	*p = Plugin{}
	take := func(key string, dst any) error {
		v, ok := raw[key]
		if !ok {
			return nil
		}
		delete(raw, key)
		if err := json.Unmarshal(v, dst); err != nil {
			return fmt.Errorf("plugin key %q: %w", key, err)
		}
		return nil
	}
	for _, f := range []struct {
		k string
		d any
	}{
		{"id", &p.ID},
		{"version", &p.Version},
		{"scope", &p.Scope},
		{"enabled", &p.Enabled},
		{"installPath", &p.InstallPath},
		{"installedAt", &p.InstalledAt},
		{"lastUpdated", &p.LastUpdated},
		{"mcpServers", &p.MCPServers},
		{"projectEnabled", &p.ProjectEnabled},
	} {
		if err := take(f.k, f.d); err != nil {
			return err
		}
	}
	for _, k := range requiredKeys {
		var b bool
		if v, ok := raw[k]; ok && json.Unmarshal(v, &b) == nil && b {
			p.RequiredByOrg = true
		}
	}
	if p.ID == "" {
		return fmt.Errorf("plugin entry has no id")
	}
	p.Name, p.Marketplace = SplitID(p.ID)
	if len(raw) > 0 {
		p.Extra = raw
	}
	return nil
}

// AvailablePlugin is an entry of the "available" list of
// `claude plugin list --json --available`.
type AvailablePlugin struct {
	PluginID        string          `json:"pluginId"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	MarketplaceName string          `json:"marketplaceName"`
	Source          json.RawMessage `json:"source"`
	InstallCount    int             `json:"installCount"`
	Version         string          `json:"version,omitempty"`
}

// parseInstalled accepts the array form and, tolerantly, the object form
// with an "installed" key.
func parseInstalled(data []byte) ([]Plugin, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("empty plugin list output")
	}
	var list []Plugin
	if data[0] == '[' {
		if err := json.Unmarshal(data, &list); err != nil {
			return nil, fmt.Errorf("parse plugin list: %w", err)
		}
		return list, nil
	}
	installed, _, err := parseAvailable(data)
	return installed, err
}

func parseAvailable(data []byte) ([]Plugin, []AvailablePlugin, error) {
	var obj struct {
		Installed []Plugin          `json:"installed"`
		Available []AvailablePlugin `json:"available"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &obj); err != nil {
		return nil, nil, fmt.Errorf("parse plugin list: %w", err)
	}
	return obj.Installed, obj.Available, nil
}

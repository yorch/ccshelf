package claude

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
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
	// Enabled is true when the plugin is enabled in the current context.
	// ProjectEnabled is true when the project settings enable it.
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
// are errors. Unknown keys land in Extra.
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

// shapeOf names the JSON shape of data for error messages.
func shapeOf(data []byte) string {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return "empty output"
	}
	switch data[0] {
	case '[':
		return "an array"
	case '{':
		var m map[string]json.RawMessage
		if json.Unmarshal(data, &m) != nil {
			return "malformed JSON"
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 0 {
			return "an empty object"
		}
		return fmt.Sprintf("an object with keys %s", strings.Join(keys, ", "))
	case '"':
		return "a string"
	case 'n':
		if string(data) == "null" {
			return "null"
		}
	case 't', 'f':
		return "a boolean"
	}
	if json.Valid(data) {
		return "a number"
	}
	return "malformed JSON"
}

// parseInstalled accepts only the JSON array `claude plugin list --json`
// prints. Anything else (null, an object, a string) is an error that names
// the shape it got: a list that fails open would silently mask nothing.
func parseInstalled(data []byte) ([]Plugin, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("empty plugin list output")
	}
	if data[0] != '[' {
		return nil, fmt.Errorf("plugin list: expected a JSON array, got %s", shapeOf(data))
	}
	var list []Plugin
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse plugin list: %w", err)
	}
	return list, nil
}

// parseAvailable accepts only the object `claude plugin list --json
// --available` prints: "installed" must be a non-null array and "available",
// when present, an array.
func parseAvailable(data []byte) ([]Plugin, []AvailablePlugin, error) {
	data = bytes.TrimSpace(data)
	var obj map[string]json.RawMessage
	if len(data) == 0 || data[0] != '{' || json.Unmarshal(data, &obj) != nil || obj == nil {
		return nil, nil, fmt.Errorf("plugin list --available: expected a JSON object with an installed array, got %s", shapeOf(data))
	}
	rawInst, ok := obj["installed"]
	if !ok {
		return nil, nil, fmt.Errorf("plugin list --available: the installed key is missing, got %s", shapeOf(data))
	}
	if len(rawInst) == 0 || rawInst[0] != '[' {
		return nil, nil, fmt.Errorf("plugin list --available: the installed key must be an array, got %s", shapeOf(rawInst))
	}
	installed, err := parseInstalled(rawInst)
	if err != nil {
		return nil, nil, err
	}
	var avail []AvailablePlugin
	if rawAvail, ok := obj["available"]; ok && string(bytes.TrimSpace(rawAvail)) != "null" {
		if err := json.Unmarshal(rawAvail, &avail); err != nil {
			return nil, nil, fmt.Errorf("parse available plugins: %w", err)
		}
	}
	return installed, avail, nil
}

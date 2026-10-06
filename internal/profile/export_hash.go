package profile

import (
	"encoding/json"
	"fmt"
)

// HashItems returns the closure hash of items exactly as Resolve computes it.
// The trust package uses it to confirm that a Closure's Hash matches its Items
// before a trust decision is made or recorded. The items must already be in
// the canonical order Resolve produces (sorted by kind, name and digest).
func HashItems(items []ClosureItem) string { return hashItems(items) }

// ControlsJSON returns the canonical JSON whose SHA-256 is the digest of m's
// ItemProfileControls closure item. The trust package stores it in the
// lockfile so that a later change to what a profile can do (environment
// names, plugin includes and excludes, inherit_user_settings, account, ...)
// can be explained field by field, not only as a changed digest. It must stay
// byte-identical to what Resolve hashes; the trust package's tests compare the
// two.
func ControlsJSON(m *Manifest) ([]byte, error) {
	env := m.Session.Env
	if env == nil {
		env = map[string]string{}
	}
	b, err := json.Marshal(profileControls{
		Account: m.Account, Extends: append([]string{}, m.Extends...),
		PluginMode: m.Plugins.Mode, PluginExclude: sortedCopy(m.Plugins.Exclude), PluginInclude: sortedCopy(m.Plugins.Include),
		SkillsOff: sortedCopy(m.Skills.Off), SkillsNameOnly: sortedCopy(m.Skills.NameOnly),
		MCPServers: sortedCopy(m.MCP.Servers), MCPStrict: m.MCP.Strict, MCPClaudeAIConnectors: m.MCP.ClaudeAIConnectors,
		InheritUserSettings: m.Session.InheritUserSettings, Env: env,
		AppendSystemPromptFile: m.Session.AppendSystemPromptFile, OnBlocked: m.Policy.OnBlocked,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding the controls of profile %q: %w", m.Name, err)
	}
	return b, nil
}

// DigestBytes returns the closure digest (hex SHA-256) of b.
func DigestBytes(b []byte) string { return digest(b) }

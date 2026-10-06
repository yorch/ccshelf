package policy

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// known lists every top-level key this package interprets or deliberately
// ignores; anything else is reported by name in Policy.Other.
var known = map[string]bool{
	"disableSideloadFlags": true, "enabledPlugins": true, "strictKnownMarketplaces": true,
	"allowedMarketplaces": true, "blockedMarketplaces": true, "extraKnownMarketplaces": true,
	"pluginSuggestionMarketplaces": true, "allowManagedHooksOnly": true,
	"allowManagedPermissionRulesOnly": true, "allowManagedMcpServersOnly": true,
	"permissions": true, "deniedMcpServers": true, "allowedMcpServers": true,
	"managedMcpServers": true, "disableAllHooks": true, "disableClaudeAiConnectors": true,
	"allowAllClaudeAiMcps": true, "wslInheritsWindowsSettings": true, "managedSourcesBehavior": true,
}

// interpret fills the typed fields from the effective document. A wrong
// type is a warning; the restrictive lock keys then read as true ("fail
// closed", as Claude Code does).
func (d *detector) interpret(doc map[string]any) {
	p := d.p
	for k := range doc {
		if !known[k] {
			p.Other = append(p.Other, k)
		}
	}
	warn := func(key, want string) {
		p.Warnings = append(p.Warnings, fmt.Sprintf("managed key %s should be %s; ignoring the value", key, want))
	}
	boolKey := func(key string, failClosed bool) *bool {
		v, ok := doc[key]
		if !ok || v == nil {
			return nil
		}
		if b, ok := toBool(v); ok {
			return &b
		}
		if failClosed {
			p.Warnings = append(p.Warnings, fmt.Sprintf("managed key %s should be a boolean; treating it as true (fail closed)", key))
			t := true
			return &t
		}
		warn(key, "a boolean")
		return nil
	}
	p.DisableSideloadFlags = boolKey("disableSideloadFlags", true)
	p.AllowManagedHooksOnly = boolKey("allowManagedHooksOnly", true)
	p.AllowManagedPermissionRulesOnly = boolKey("allowManagedPermissionRulesOnly", true)
	p.AllowManagedMcpServersOnly = boolKey("allowManagedMcpServersOnly", true)
	p.DisableAllHooks = boolKey("disableAllHooks", false)
	p.DisableClaudeAiConnectors = boolKey("disableClaudeAiConnectors", false)
	p.AllowAllClaudeAiMcps = boolKey("allowAllClaudeAiMcps", false)
	if p.WSLInheritsWindowsSettings == nil {
		p.WSLInheritsWindowsSettings = boolKey("wslInheritsWindowsSettings", false)
	}

	if v, ok := doc["permissions"]; ok && v != nil {
		if m, ok := v.(map[string]any); ok {
			if dv, ok := m["disableBypassPermissionsMode"]; ok && dv != nil {
				b := dv == "disable"
				p.DisableBypassPermissionsMode = &b
				if !b {
					warn("permissions.disableBypassPermissionsMode", `"disable"`)
				}
			}
		} else {
			warn("permissions", "an object")
		}
	}

	if v, ok := doc["enabledPlugins"]; ok && v != nil {
		if m, ok := v.(map[string]any); ok {
			p.EnabledPlugins = map[string]bool{}
			for id, e := range m {
				if b, ok := e.(bool); ok {
					p.EnabledPlugins[id] = b
				} else {
					warn("enabledPlugins."+id, "a boolean")
				}
			}
		} else {
			warn("enabledPlugins", "an object")
		}
	}

	strictKey := "strictKnownMarketplaces"
	if _, ok := doc[strictKey]; !ok {
		strictKey = "allowedMarketplaces"
	}
	p.StrictKnownMarketplaces = d.marketplaceList(doc, strictKey)
	p.BlockedMarketplaces = d.marketplaceList(doc, "blockedMarketplaces")

	if v, ok := doc["extraKnownMarketplaces"]; ok && v != nil {
		if m, ok := v.(map[string]any); ok {
			p.ExtraKnownMarketplaces = map[string]MarketplaceSource{}
			for name, e := range m {
				em, _ := e.(map[string]any)
				sm, _ := em["source"].(map[string]any)
				if sm == nil {
					warn("extraKnownMarketplaces."+name, "an object with a source")
					continue
				}
				p.ExtraKnownMarketplaces[name] = redactMarketplace(sm)
			}
		} else {
			warn("extraKnownMarketplaces", "an object")
		}
	}
	if v, ok := doc["pluginSuggestionMarketplaces"]; ok && v != nil {
		if l, ok := v.([]any); ok {
			p.PluginSuggestionMarketplaces = []string{}
			for _, e := range l {
				if s, ok := e.(string); ok {
					p.PluginSuggestionMarketplaces = append(p.PluginSuggestionMarketplaces, s)
				} else {
					warn("pluginSuggestionMarketplaces entry", "a string")
				}
			}
		} else {
			warn("pluginSuggestionMarketplaces", "an array")
		}
	}
	p.DeniedMcpServers = d.serverRules(doc, "deniedMcpServers")
	p.AllowedMcpServers = d.serverRules(doc, "allowedMcpServers")
	if v, ok := doc["managedMcpServers"]; ok && v != nil {
		if m, ok := v.(map[string]any); ok {
			p.ManagedMcpServers = []string{}
			for name := range m {
				p.ManagedMcpServers = append(p.ManagedMcpServers, name)
			}
			sort.Strings(p.ManagedMcpServers)
		} else {
			warn("managedMcpServers", "an object")
		}
	}
}

func (d *detector) marketplaceList(doc map[string]any, key string) []MarketplaceSource {
	v, ok := doc[key]
	if !ok || v == nil {
		return nil
	}
	l, ok := v.([]any)
	if !ok {
		d.p.Warnings = append(d.p.Warnings, fmt.Sprintf("managed key %s should be an array; ignoring the value", key))
		return nil
	}
	out := []MarketplaceSource{} // non-nil: an empty list blocks everything
	for _, e := range l {
		m, ok := e.(map[string]any)
		if !ok {
			d.p.Warnings = append(d.p.Warnings, fmt.Sprintf("managed key %s has an entry that is not an object; skipping it", key))
			continue
		}
		out = append(out, redactMarketplace(m))
	}
	return out
}

func redactMarketplace(m map[string]any) MarketplaceSource {
	kind, _ := m["source"].(string)
	for _, f := range []string{"repo", "url", "path", "hostPattern", "pathPattern"} {
		if s, ok := m[f].(string); ok && s != "" {
			ref := s
			if f == "url" || kind == "git" || kind == "url" {
				ref = redactURL(s)
			}
			return MarketplaceSource{Kind: kind, Ref: ref}
		}
	}
	return MarketplaceSource{Kind: kind}
}

// redactURL removes credentials, query and fragment from a URL or scp-like
// git address.
func redactURL(s string) string {
	if !strings.Contains(s, "://") {
		if i := strings.Index(s, "@"); i >= 0 && !strings.Contains(s[:i], "/") {
			s = s[i+1:]
		}
		if i := strings.IndexAny(s, "?#"); i >= 0 {
			s = s[:i]
		}
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "(unparseable url)"
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}

func (d *detector) serverRules(doc map[string]any, key string) []ServerRule {
	v, ok := doc[key]
	if !ok || v == nil {
		return nil
	}
	l, ok := v.([]any)
	if !ok {
		d.p.Warnings = append(d.p.Warnings, fmt.Sprintf("managed key %s should be an array; ignoring the value", key))
		return nil
	}
	out := []ServerRule{} // non-nil: an empty allowlist blocks everything
	for _, e := range l {
		m, ok := e.(map[string]any)
		if !ok {
			d.p.Warnings = append(d.p.Warnings, fmt.Sprintf("managed key %s has an entry that is not an object; skipping it", key))
			continue
		}
		switch {
		case m["serverName"] != nil:
			s, _ := m["serverName"].(string)
			out = append(out, ServerRule{Kind: "serverName", Value: s})
		case m["serverUrl"] != nil:
			s, _ := m["serverUrl"].(string)
			out = append(out, ServerRule{Kind: "serverUrl", Value: redactURL(s)})
		case m["serverCommand"] != nil:
			exe := ""
			if c, ok := m["serverCommand"].([]any); ok && len(c) > 0 {
				exe, _ = c[0].(string)
			}
			out = append(out, ServerRule{Kind: "serverCommand", Value: exe})
		default:
			d.p.Warnings = append(d.p.Warnings, fmt.Sprintf("managed key %s has an entry with no serverName, serverUrl or serverCommand; skipping it", key))
		}
	}
	return out
}

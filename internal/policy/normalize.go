package policy

import "fmt"

// aliases maps an alias spelling to its canonical key. Claude Code reads an
// alias exactly like the canonical key, and when both are set in one file the
// canonical value wins (settings-reference, "Marketplace key aliases").
var aliases = [][2]string{
	{"additionalMarketplaces", "extraKnownMarketplaces"},
	{"allowedMarketplaces", "strictKnownMarketplaces"},
}

// lockBoolKeys are top-level keys with a single restrictive value. A value
// Claude Code cannot read reads as true ("Keys that fail closed" in
// managed-settings); the strings "true" and "false" read as that boolean with
// a notice. wslInheritsWindowsSettings is not documented as a lock, but
// reading the Windows chain only adds policy, so it is treated the same way.
var lockBoolKeys = []string{
	"allowManagedHooksOnly", "allowManagedMcpServersOnly", "allowManagedPermissionRulesOnly",
	"disableSideloadFlags", "wslInheritsWindowsSettings",
}

// plainBoolKeys are boolean keys that do not fail closed: an invalid value
// (a quoted boolean included) is dropped with a warning.
var plainBoolKeys = []string{"disableAllHooks", "disableClaudeAiConnectors", "allowAllClaudeAiMcps"}

// normalizeDoc canonicalizes one managed document (one file, plist or
// registry value) before it is merged with anything else, so that merging
// and first-wins selection only ever see canonical keys and real booleans:
// aliases are renamed, lock keys are coerced fail-closed, other boolean keys
// are validated and permissions.disableBypassPermissionsMode reads as its
// restrictive value. Problems are appended to warnings.
func (d *detector) normalizeDoc(m map[string]any, where string) {
	warn := func(format string, a ...any) {
		d.p.Warnings = append(d.p.Warnings, where+": "+fmt.Sprintf(format, a...))
	}
	for _, ac := range aliases {
		alias, canon := ac[0], ac[1]
		v, ok := m[alias]
		if !ok {
			continue
		}
		if _, has := m[canon]; has {
			warn("both %s and its alias %s are set, so ccshelf uses %s", canon, alias, canon)
		} else {
			m[canon] = v
		}
		delete(m, alias)
	}
	for _, k := range lockBoolKeys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case bool:
		case string:
			switch x {
			case "true":
				m[k] = true
				warn("%s is the string \"true\", so ccshelf reads it as true. Drop the quotes", k)
			case "false":
				m[k] = false
				warn("%s is the string \"false\", so ccshelf reads it as false. Drop the quotes", k)
			default:
				m[k] = true
				warn("%s is present but invalid, so ccshelf treats it as true (fail closed)", k)
			}
		default:
			m[k] = true
			warn("%s is present but invalid, so ccshelf treats it as true (fail closed)", k)
		}
	}
	for _, k := range plainBoolKeys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		if _, isBool := v.(bool); !isBool {
			delete(m, k)
			warn("%s must be a JSON boolean, so ccshelf ignores the value", k)
		}
	}
	if pm, ok := m["permissions"].(map[string]any); ok {
		if v, ok := pm["disableBypassPermissionsMode"]; ok && v != nil && v != "disable" {
			pm["disableBypassPermissionsMode"] = "disable"
			warn("permissions.disableBypassPermissionsMode is present but invalid, so ccshelf treats it as \"disable\" (fail closed)")
		}
	}
}

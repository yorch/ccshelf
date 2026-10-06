package policy

// lockKeys are restrictive switches: under "merge" the strictest value of
// any admin source applies. disableSideloadFlags is treated as a lock, the
// safest reading for a launcher.
var lockKeys = map[string]bool{
	"allowManagedHooksOnly":           true,
	"allowManagedPermissionRulesOnly": true,
	"allowManagedMcpServersOnly":      true,
	"disableSideloadFlags":            true,
}

// unionKeys are lists that combine across sources under "merge".
var unionKeys = map[string]bool{
	"deniedMcpServers":             true,
	"blockedMarketplaces":          true,
	"pluginSuggestionMarketplaces": true,
}

// effective computes the document the policy is interpreted from. using are
// the contributing tiers in rank order (one under "first-wins"); admins are
// all admin tiers, used for the keys Claude Code reads from every admin
// source.
func effective(using, admins []*tier, behavior string) map[string]any {
	doc := map[string]any{}
	if len(using) == 0 {
		return doc
	}
	if behavior == "merge" {
		for i := len(using) - 1; i >= 0; i-- {
			mergeAcross(doc, using[i].m)
		}
		return doc
	}
	for k, v := range using[0].m {
		doc[k] = v
	}
	crossSource(doc, admins)
	return doc
}

// crossSource applies the keys read from every admin source (documented in
// managed-settings, "Keys read from every admin source").
func crossSource(doc map[string]any, admins []*tier) {
	for _, k := range []string{"allowAllClaudeAiMcps", "allowManagedMcpServersOnly", "disableClaudeAiConnectors"} {
		for _, t := range admins {
			if b, ok := toBool(t.m[k]); ok && b {
				doc[k] = true
			}
		}
	}
	var denied []any
	for _, t := range admins {
		if l, ok := t.m["deniedMcpServers"].([]any); ok {
			denied = unionLists(denied, l)
		}
	}
	if denied != nil {
		doc["deniedMcpServers"] = denied
	}
	if b, _ := doc["allowManagedMcpServersOnly"].(bool); b {
		for _, t := range admins {
			if l, ok := t.m["allowedMcpServers"]; ok && l != nil {
				doc["allowedMcpServers"] = l
				break
			}
		}
	}
}

// mergeAcross folds a higher-ranked document hi into acc.
func mergeAcross(acc, hi map[string]any) {
	for k, v := range hi {
		if v == nil {
			continue
		}
		cur, has := acc[k]
		switch {
		case lockKeys[k]:
			if b, ok := toBool(cur); has && ok && b {
				continue
			}
			acc[k] = v
		case unionKeys[k]:
			l, lok := v.([]any)
			c, cok := cur.([]any)
			if lok && cok {
				acc[k] = unionLists(c, l)
			} else {
				acc[k] = v
			}
		case k == "managedMcpServers":
			if has {
				tmp := map[string]any{k: cur}
				mergeInto(tmp, map[string]any{k: v}, true)
				acc[k] = tmp[k]
			} else {
				acc[k] = v
			}
		case k == "permissions":
			cm, cok := cur.(map[string]any)
			vm, vok := v.(map[string]any)
			if !cok || !vok {
				acc[k] = v
				continue
			}
			strict := cm["disableBypassPermissionsMode"] == "disable"
			mergeInto(cm, vm, false)
			if strict {
				cm["disableBypassPermissionsMode"] = "disable"
			}
		default:
			acc[k] = v
		}
	}
}

// toBool accepts only a JSON boolean. Documents are normalized before they
// reach this point (see normalizeDoc), so a string never counts as a boolean
// here.
func toBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

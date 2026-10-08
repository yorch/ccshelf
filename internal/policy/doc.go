// Package policy detects Claude Code managed policy on this machine and turns
// it into a capability matrix for the launcher (requirement R3, decision D-06).
//
// Detection is read-only and best effort. It reads only the documented managed
// sources and never runs claude, never probes a flag by its exit code and
// never tries to bypass a restriction. A source that exists but cannot be
// read, or is malformed, is reported as Unknown plus a Warning and is never
// silently treated as "no policy". Only key names and typed, redacted fields
// are kept. Values that could be secrets (headers, URLs with credentials,
// command arguments) are never stored or printed.
//
// # Sources and precedence
//
// Verified against https://code.claude.com/docs/en/managed-settings (section
// "Where each mechanism stores the policy", "How Claude Code combines managed
// sources", "Split a file-based policy across teams", "Check that a policy is
// in force") on 2026-10-06:
//
//   - macOS: /Library/Managed Preferences/com.anthropic.claudecode.plist
//     (converted to JSON with plutil) ranks above
//     /Library/Application Support/ClaudeCode/managed-settings.json plus
//     managed-settings.d/*.json.
//   - Linux and WSL: /etc/claude-code/managed-settings.json plus
//     managed-settings.d/*.json.
//   - Windows: HKLM\SOFTWARE\Policies\ClaudeCode value Settings (REG_SZ or
//     REG_EXPAND_SZ holding JSON) ranks above
//     C:\Program Files\ClaudeCode\managed-settings.json plus managed-settings.d,
//     and HKCU\SOFTWARE\Policies\ClaudeCode is used only when no admin
//     document is present. The legacy C:\ProgramData\ClaudeCode path is not
//     read (the docs say Claude Code does not read it either).
//   - Drop-ins: managed-settings.json first, then every *.json in
//     managed-settings.d in alphabetical order. The later file replaces
//     single values, lists combine without duplicates, nested blocks merge
//     key by key, and entries of extraKnownMarketplaces and managedMcpServers
//     replace whole.
//   - By default ("first-wins") the highest-ranked admin source that sets at
//     least one policy key supplies the policy. A few keys are read from every
//     admin source (allowAllClaudeAiMcps, allowManagedMcpServersOnly,
//     deniedMcpServers, disableClaudeAiConnectors, and allowedMcpServers when
//     the MCP lock is on). With managedSourcesBehavior "merge" every admin
//     source contributes (lists combine, locks take the strictest value,
//     allowlists come whole from the highest source). wslInheritsWindowsSettings
//     and managedSourcesBehavior are control keys and do not count as policy
//     keys.
//   - WSL: with wslInheritsWindowsSettings true in the Windows policy folder
//     (/mnt/c/Program Files/ClaudeCode), that folder ranks above /etc/claude-code,
//     which is read only when no Windows admin document is present. The
//     Windows registry is not readable from WSL and is reported as Unknown.
//   - managed-mcp.json in the same directory is only checked for existence
//     (https://code.claude.com/docs/en/managed-mcp, "Servers passed with
//     --mcp-config or --strict-mcp-config"): it puts MCP under exclusive
//     control, and on a workstation Claude Code exits at startup when
//     --mcp-config (even an empty one) or --strict-mcp-config is passed, so
//     both capabilities are Blocked.
//   - macOS per-user preferences (/Library/Managed Preferences/<user>/...)
//     are not read and are listed as Unknown.
//
// Drop-in files must end in lower-case ".json". Hidden files and other
// extensions (a ".JSON" file included) are ignored and not listed, as in
// Claude Code.
//
// Documents are limited to 1 MiB and must resolve to a regular file. A
// symbolic link that leaves the managed directory is followed only on Unix,
// to a root-owned file that no group or other user can write (see readFile).
// On Windows file ownership is not evaluated, so such a link is never
// followed (documented limitation). An empty file counts as {}. A present
// but unparseable file, plist or HKLM value is Unreadable. A broken HKCU value
// never blocks and is only noted, as in Claude Code.
//
// # Normalization before merging
//
// Each document (file, drop-in, plist, registry value) is normalized on its
// own before anything is merged ("Find entries Claude Code dropped" and
// "Keys that fail closed" in managed-settings, "Marketplace key aliases" in
// settings-reference). Aliases become canonical keys. An unreadable lock key
// reads as true and an invalid permissions.disableBypassPermissionsMode reads
// as "disable" (fail closed). A non-array allowlist admits nothing. Other
// invalid values are dropped with a warning. normalizeDoc, marketplaceList
// and serverRules give the exact rules.
//
// Because this happens per source, a lower source's invalid lock value cannot
// be overwritten by a higher source's false under "merge".
//
// # Keys read
//
// Each key below was verified in the settings reference
// (https://code.claude.com/docs/en/settings-reference) and, for the plugin
// keys, in the control matrix of https://code.claude.com/docs/en/plugins/org:
//
//	disableSideloadFlags             rejects --plugin-dir, --plugin-url, --agents, non-SDK --mcp-config (so --strict-mcp-config too), CLAUDE_CODE_PLUGIN_DIRS (v2.1.193+)
//	enabledPlugins                   true force-enables, false blocks, at every scope
//	strictKnownMarketplaces          marketplace allowlist (alias allowedMarketplaces), [] blocks all
//	blockedMarketplaces              marketplace blocklist
//	extraKnownMarketplaces           marketplaces declared by policy
//	pluginSuggestionMarketplaces     marketplaces that may suggest plugins
//	allowManagedHooksOnly            lock
//	allowManagedPermissionRulesOnly  lock
//	allowManagedMcpServersOnly       lock
//	permissions.disableBypassPermissionsMode  "disable" turns bypass mode off
//	deniedMcpServers, allowedMcpServers       serverName, serverUrl, serverCommand entries
//	managedMcpServers                names only (v2.1.259+)
//	disableAllHooks                  turns hooks off
//	disableClaudeAiConnectors        turns claude.ai connectors off
//	allowAllClaudeAiMcps             keeps connectors under managed-mcp.json
//	wslInheritsWindowsSettings       WSL reads the Windows policy chain (v2.1.282+)
//	managedSourcesBehavior           "first-wins" or "merge" (v2.1.242+)
//
// Every other top-level key is listed by name in Policy.Other and ignored.
// The documented key is permissions.disableBypassPermissionsMode, not a flat
// disableBypassPermissionsMode. Typed keys with the wrong type produce a
// Warning (see "Normalization before merging").
//
// # Capability matrix
//
// Evaluate and Plan turn a Policy into what the launcher may do. Their doc
// comments give the rules. See docs/design/security.md, "Policy spectrum and
// open source (R3, R4)".
//
// # Blind spots
//
// Server-managed settings (claude.ai admin console, Claude apps gateway)
// cannot be read locally, rank above every local source and are always listed
// as Unknown. Text and JSON always say so (JSON: "server_managed_readable":
// false, additive to version 1). Machine-specific blind spots set
// Policy.PartialVisibility with reasons: WSL with a Windows policy folder
// that is absent or unreadable, or that turns on wslInheritsWindowsSettings
// (the Windows registry cannot be read from WSL). While it is true, every
// feature whose state depends on a lock key that the invisible source could
// set (the sideload-flag features, --strict-mcp-config, setting-sources and
// forced-plugins) reports Unknown instead of Available. A visible Blocked state
// still wins. A fully visible machine without policy reports Available.
//
// # MCP flags
//
// StrictMCPConfig is Blocked when managed-mcp.json exists or sideload flags
// are disabled. When allowedMcpServers, allowManagedMcpServersOnly or
// deniedMcpServers filter servers (they also filter --mcp-config servers and
// --strict-mcp-config does not bypass them), the AddMCPConfig reason names
// them and Plan warns when a profile adds servers.
//
// # Platform notes
//
// The registry reader is a stub outside Windows. Options makes every OS path
// testable on any machine.
package policy

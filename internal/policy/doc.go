// Package policy detects Claude Code managed policy on this machine and turns
// it into a capability matrix for the launcher (requirement R3, decision D-06).
//
// Detection is read-only and best effort. It reads only the documented managed
// sources and never runs claude, never probes a flag by its exit code and
// never tries to bypass a restriction. A source that exists but cannot be
// read, or is malformed, is reported as Unknown plus a Warning and is never
// silently treated as "no policy". Server-managed settings (claude.ai admin
// console, Claude apps gateway) cannot be read locally and are always listed
// as Unknown. Only key names and typed, redacted fields are kept; values that
// could be secrets (headers, URLs with credentials, command arguments) are
// never stored or printed.
//
// # Sources and precedence
//
// Verified against https://code.claude.com/docs/en/managed-settings (section
// "Where each mechanism stores the policy", "How Claude Code combines managed
// sources", "Split a file-based policy across teams", "Check that a policy is
// in force") on 2026-10-06:
//
//   - macOS: /Library/Managed Preferences/com.anthropic.claudecode.plist
//     (managed preferences domain com.anthropic.claudecode, converted with
//     /usr/bin/plutil -convert json -o -, 5 s timeout) ranks above
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
//     managed-settings.d in alphabetical order; hidden files and other
//     extensions are ignored; single values are replaced by the later file,
//     lists combine without duplicates, nested blocks merge key by key, and
//     entries of extraKnownMarketplaces and managedMcpServers replace whole.
//   - By default ("first-wins") the highest-ranked admin source that sets at
//     least one policy key supplies the policy; a few keys are read from every
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
//     (https://code.claude.com/docs/en/managed-mcp): it puts MCP under
//     exclusive control, so --mcp-config servers are not used.
//
// Documents are limited to 1 MiB, must be regular files, and a symbolic link is
// followed only when it stays inside the managed directory. An empty file counts
// as {}. A present but unparseable file, plist or HKLM value is Unreadable. A
// broken HKCU value never blocks and is only noted, as in Claude Code.
//
// # Keys read
//
// Each key below was verified in the settings reference
// (https://code.claude.com/docs/en/settings-reference) and, for the plugin
// keys, in the control matrix of https://code.claude.com/docs/en/plugins/org:
//
//	disableSideloadFlags             rejects --plugin-dir, --plugin-url, --agents, non-SDK --mcp-config, CLAUDE_CODE_PLUGIN_DIRS (v2.1.193+)
//	enabledPlugins                   true force-enables, false blocks, at every scope
//	strictKnownMarketplaces          marketplace allowlist (alias allowedMarketplaces); [] blocks all
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
// The task text used a flat disableBypassPermissionsMode; the documented key is
// permissions.disableBypassPermissionsMode, which is what is read. Typed keys
// with the wrong type produce a Warning; the four lock keys then read as true
// ("fail closed", as the docs describe for keys with a single restrictive value).
//
// # Capability matrix
//
// Evaluate is a pure function from a Policy and the installed plugins (whose
// RequiredByOrg marker names force-enabled plugins) to a Matrix. Plan applies
// a profile's needs with on_blocked "warn" (drop and warn) or "fail" (a
// *BlockedError, exit code 3). Features in the Unknown state are attempted with
// a warning, because a blocked state is only claimed from evidence. Text and
// JSON (version 1) serve doctor --policy.
//
// # Platform notes
//
// The registry reader lives in registry_windows.go (golang.org/x/sys/windows/registry)
// and is a stub elsewhere. Every root, the OS, WSL detection, the plutil runner
// and the registry reader are injectable through Options, so every OS path is
// testable on any machine.
package policy

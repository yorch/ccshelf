// Package policy detects Claude Code managed policy on this machine and turns
// it into a capability matrix for the launcher (requirement R3, decision D-06).
//
// Detection is read-only and best effort. It reads only the documented managed
// sources and never runs claude, never probes a flag by its exit code and
// never tries to bypass a restriction. A source that exists but cannot be
// read, or is malformed, is reported as Unknown plus a Warning and is never
// silently treated as "no policy". Server-managed settings (claude.ai admin
// console, Claude apps gateway) cannot be read locally and are always listed
// as Unknown. Only key names and typed, redacted fields are kept. Values that
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
//     managed-settings.d in alphabetical order. Hidden files and other
//     extensions are ignored. The later file replaces single values, lists
//     combine without duplicates, nested blocks merge key by key, and
//     entries of extraKnownMarketplaces and managedMcpServers replace whole.
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
// extensions (a ".JSON" file included) are ignored, as the docs say Claude
// Code ignores files that do not end in ".json". Such files are not listed.
//
// Documents are limited to 1 MiB. The resolved target must be a regular file
// (checked with Stat before opening, so a FIFO or device cannot block). It is
// opened non-blocking and read with a context deadline. A symbolic link is
// followed when its target stays inside the managed directory or, on Unix,
// when the target is a regular file owned by root that is not group or world
// writable (the way configuration management links policy into place).
// Otherwise the source is Unknown with the reason. On Windows file ownership
// is not evaluated, so a link that leaves the managed directory is never
// followed (documented limitation). An empty file counts as {}. A present
// but unparseable file, plist or HKLM value is Unreadable. A broken HKCU value
// never blocks and is only noted, as in Claude Code.
//
// # Normalization before merging
//
// Each document (file, drop-in, plist, registry value) is normalized on its
// own before anything is merged, so first-wins selection and merge see
// canonical keys and typed values only ("Find entries Claude Code dropped" and
// "Keys that fail closed" in managed-settings; "Marketplace key aliases" in
// settings-reference):
//
//   - allowedMarketplaces is read as strictKnownMarketplaces and
//     additionalMarketplaces as extraKnownMarketplaces. With both spellings in
//     one document the canonical value wins and a warning is recorded.
//   - Lock keys (disableSideloadFlags, allowManagedHooksOnly,
//     allowManagedPermissionRulesOnly, allowManagedMcpServersOnly, and
//     wslInheritsWindowsSettings, treated the same way because reading the
//     Windows chain only adds policy) read as true when their value cannot be
//     read. The strings "true" and "false" read as that boolean with a
//     warning, which is what the docs say for these keys. permissions.
//     disableBypassPermissionsMode reads as "disable" when invalid.
//   - Other boolean keys (disableAllHooks, disableClaudeAiConnectors,
//     allowAllClaudeAiMcps) accept JSON booleans only: any other value, a
//     quoted boolean included, is dropped with a warning. null removes the key.
//   - A strictKnownMarketplaces or allowedMcpServers value that is not an
//     array is enforced as an empty allowlist (admits nothing) with a warning.
//     A wholly invalid blockedMarketplaces or deniedMcpServers value is
//     dropped. An invalid entry is stripped and the valid subset kept. Rule
//     entries whose serverName, serverUrl or serverCommand have the wrong
//     type are dropped and counted in one warning.
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
// with the wrong type produce a Warning. See "Normalization before merging"
// for what each one reads as.
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
// # Blind spots
//
// Server-managed settings cannot be read locally and rank above every local
// source. Text and JSON always say so (JSON: "server_managed_readable":
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
// The registry reader lives in registry_windows.go (golang.org/x/sys/windows/registry)
// and is a stub elsewhere. Every root, the OS, WSL detection, the plutil runner
// and the registry reader are injectable through Options, so every OS path is
// testable on any machine.
package policy

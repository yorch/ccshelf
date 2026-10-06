// Package settings generates and validates the --settings JSON that ccshelf
// hands to Claude Code (security requirement SR1: a closed schema).
//
// # Closed key set
//
// The only top-level keys that can ever be produced or accepted are
// enabledPlugins, skillOverrides, disableClaudeAiConnectors,
// deniedMcpServers, model and env. permissions, hooks, apiKeyHelper,
// allowedMcpServers, disableAllHooks, statusLine and every other key are
// rejected by [Validate] and cannot be expressed by [Spec]. Environment
// variable names are decided by internal/envpolicy; this package never
// duplicates those rules.
//
// # Build
//
// [Build] turns a [Spec] (installed plugins plus the profile's choices) into
// a [Result] whose Doc marshals deterministically (sorted keys, two-space
// indent, trailing newline) through [Result.JSON]. Semantics:
//
//   - allow-only mode (the default): every installed plugin that is neither
//     in Include nor Protected nor Locked is written false. Include entries
//     are written true even when the plugin is disabled at user scope, because
//     a project-level false is overridden only by an explicit true. Include
//     ids that are not installed are listed in Missing and not written.
//   - additive mode: only Exclude entries are masked; Include still writes
//     true.
//   - Locked: installed plugins reporting RequiredByOrg, plus the ids in
//     Spec.PolicyLocked (what the caller learned from managed policy, for
//     example policy.Matrix.LockedPlugins(); the RequiredByOrg marker in
//     `plugin list` is best effort), can never be masked by a settings file,
//     so they are never written (neither true nor false) and all of them are
//     listed in Locked.
//   - An empty Installed list in allow-only mode adds the warning "no
//     installed plugins were found; nothing will be masked": it usually means
//     the plugin listing was run in the wrong place.
//   - Protected ids are never written false: they are omitted (or true when
//     also in Include). Result.Protected lists installed protected ids that
//     would otherwise have been masked. If an id is both Protected and
//     Exclude, protection wins and a warning is added.
//   - skillOverrides takes "off" and "name-only" for standalone skills only;
//     Claude Code ignores overrides for plugin skills, and a warning is added
//     when a name looks like pluginname:skill of an installed plugin.
//   - deniedMcpServers entries are {"serverName": label} objects with full
//     server labels (for example plugin:context7:context7 or "claude.ai
//     Shopify"); they are deduplicated and sorted.
//   - HideConnectors writes disableClaudeAiConnectors true, never false.
//   - Spec.ProtectedMCP lists MCP server labels that must keep working (SR3):
//     DenyMCP naming one is [ErrProtectedMCP], and HideConnectors while a
//     protected label starts with "claude.ai " is [ErrProtectedConnector].
//   - Env may only use names accepted by envpolicy, with values free of
//     control characters; Profile is the only way to set CCSHELF_PROFILE, and
//     Env containing it is an error.
//   - model must be at most 128 characters matching [A-Za-z0-9._:/\[\]-]+.
//
// # Validate
//
// [Validate] must run on the exact bytes about to be passed to --settings,
// before every launch: Claude Code silently ignores an invalid settings file
// (exit 0), which would turn a mask into no mask. It rejects duplicate keys,
// unknown keys, wrong types and files over 1 MiB, and names the offending
// key in each error. It applies the same rules as Build (control characters
// and NUL in env values, model name syntax and length, server label length)
// and also rejects a UTF-8 byte order mark and invalid UTF-8. A file that
// contains CCSHELF_PROFILE in env is valid (the launcher writes it through
// Build); being stricter than Claude Code is safe because Claude Code silently
// ignores files it cannot parse.
package settings

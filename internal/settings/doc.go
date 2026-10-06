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
//   - Locked: installed plugins reporting RequiredByOrg can never be masked
//     by a settings file, so they are never written (neither true nor false)
//     and all of them are listed in Locked.
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
//   - Env may only use names accepted by envpolicy; Profile adds
//     CCSHELF_PROFILE.
//
// # Validate
//
// [Validate] must run on the exact bytes about to be passed to --settings,
// before every launch: Claude Code silently ignores an invalid settings file
// (exit 0), which would turn a mask into no mask. It rejects duplicate keys,
// unknown keys, wrong types and files over 1 MiB, and names the offending
// key in each error.
package settings

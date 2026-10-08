// Package settings generates and validates the --settings JSON that ccshelf
// gives to Claude Code (security requirement SR1: a closed schema).
//
// # Closed key set
//
// The only top-level keys that this package can ever produce or accept are
// enabledPlugins, skillOverrides, disableClaudeAiConnectors,
// deniedMcpServers, model and env. [Validate] rejects permissions, hooks,
// apiKeyHelper, allowedMcpServers, disableAllHooks, statusLine and every other
// key, and [Spec] cannot express them. internal/envpolicy decides the
// environment variable names. This package never duplicates those rules.
//
// # Build
//
// [Build] turns a [Spec] (installed plugins plus the profile's choices) into
// a [Result]. [Result.JSON] marshals its Doc deterministically (sorted keys,
// two-space indent, trailing newline). Semantics:
//
//   - allow-only mode (the default): Build writes false for every installed
//     plugin that is not in Include, Protected or Locked. Build writes true
//     for Include entries even when the plugin is disabled at user scope,
//     because only an explicit true overrides a project-level false. Include
//     ids that are not installed go in Missing, and Build does not write them.
//   - additive mode: Build masks only Exclude entries. Include still writes
//     true.
//   - Locked: these are the installed plugins that report RequiredByOrg, plus
//     the ids in Spec.PolicyLocked. Spec.PolicyLocked is what the caller
//     learned from managed policy, for example policy.Matrix.LockedPlugins().
//     The RequiredByOrg marker in `plugin list` is best effort. A settings
//     file can never mask a locked plugin, so Build never writes one (neither
//     true nor false) and lists all of them in Locked.
//   - UserLayerDropped: the session runs with --setting-sources
//     project,local, so the user layer that enables plugins is gone. Build
//     writes true for installed protected and locked plugins, because if it
//     omits them, they stay disabled. Build never writes false for them.
//   - An empty Installed list in allow-only mode adds the warning "no
//     installed plugins were found. Nothing will be masked". It usually means
//     that the plugin listing ran in the wrong place.
//   - Build never writes false for Protected ids. It omits them (or writes
//     true when they are also in Include). Result.Protected lists installed
//     protected ids that Build would otherwise mask. If an id is both
//     Protected and Exclude, protection wins and Build adds a warning.
//   - skillOverrides takes "off" and "name-only" for standalone skills only.
//     Claude Code ignores overrides for plugin skills. Build adds a warning
//     when a name looks like pluginname:skill of an installed plugin.
//   - deniedMcpServers entries are {"serverName": label} objects with full
//     server labels (for example plugin:context7:context7 or "claude.ai
//     Shopify"). Build removes duplicates and sorts them.
//   - HideConnectors writes disableClaudeAiConnectors true, never false.
//   - Spec.ProtectedMCP lists MCP server labels that must keep working (SR3).
//     If DenyMCP names one, Build returns [ErrProtectedMCP]. If
//     HideConnectors is set while a protected label starts with "claude.ai ",
//     Build returns [ErrProtectedConnector].
//   - Env may use only names that envpolicy accepts, with values free of
//     control characters. Profile is the only way to set CCSHELF_PROFILE.
//     If Env contains it, Build returns an error.
//   - model must be at most 128 characters matching [A-Za-z0-9._:/\[\]-]+.
//
// # Validate
//
// [Validate] must run on the exact bytes that ccshelf is about to give to
// --settings, before every launch. Claude Code silently ignores an invalid
// settings file (exit 0), and that would turn a mask into no mask. Validate
// rejects duplicate keys, unknown keys, wrong types and files over 1 MiB, and
// names the offending key in each error. It applies the same rules as Build
// (control characters and NUL in env values, model name syntax and length,
// server label length). It also rejects a UTF-8 byte order mark and invalid
// UTF-8. A file that contains CCSHELF_PROFILE in env is valid, because the
// launcher writes it through Build. Validate is stricter than Claude Code.
// This is safe because Claude Code silently ignores files it cannot parse.
package settings

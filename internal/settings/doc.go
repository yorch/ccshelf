// Package settings generates and validates the --settings JSON that ccshelf
// gives to Claude Code (security requirement SR1: a closed schema).
//
// # Closed key set
//
// The only top-level keys that this package can ever produce or accept are
// enabledPlugins, skillOverrides, disableClaudeAiConnectors,
// deniedMcpServers, model, outputStyle and env. [Validate] rejects permissions, hooks,
// apiKeyHelper, allowedMcpServers, disableAllHooks, statusLine and every other
// key, and [Spec] cannot express them. internal/envpolicy decides the
// environment variable names. This package never duplicates those rules.
//
// # Build
//
// [Build] turns a [Spec] into a [Result]. The [Spec] fields say what each
// input does. The rules that span fields:
//
//   - allow-only mode (the default): Build writes false for every installed
//     plugin that is not in Include, Protected or Locked. Build writes true
//     for Include entries even when the plugin is disabled at user scope,
//     because only an explicit true overrides a project-level false. Include
//     ids that are not installed go in Missing, and Build does not write them.
//   - additive mode: Build masks only Exclude entries. Include still writes
//     true.
//   - Locked plugins (RequiredByOrg, a best-effort marker, plus
//     Spec.PolicyLocked) cannot be masked by a settings file, so Build never
//     writes one, except as true when UserLayerDropped.
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
//   - HideConnectors never writes false. Spec.ProtectedMCP (SR3) makes Build
//     fail rather than hide a protected server or connector.
//   - Env values must be free of control characters. model must be at most
//     128 characters matching [A-Za-z0-9._:/\[\]-]+. outputStyle must be at most
//     64 characters, start and end with a letter, digit, dot, underscore or
//     hyphen, and contain only letters, digits, space, dot, underscore, colon
//     and hyphen.
//
// # Validate
//
// [Validate] must run on the exact bytes that ccshelf is about to give to
// --settings, before every launch. Claude Code silently ignores an invalid
// settings file (exit 0), and that would turn a mask into no mask. It rejects
// duplicate keys, unknown keys, wrong types and files over 1 MiB. A file that
// contains CCSHELF_PROFILE in env is valid, because the launcher writes it
// through Build.
package settings

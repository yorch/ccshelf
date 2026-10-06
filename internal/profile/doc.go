// Package profile parses profile manifests, resolves them across sources and
// computes the trust closure (SR1 to SR3). It reads files only: no network, no
// git, no Claude Code state.
//
// # Manifest
//
// A profile is one TOML file, profiles/<name>.toml, decoded into Manifest with
// a closed schema: every unknown key is an error, and the keys permissions,
// hooks, apiKeyHelper, allowedMcpServers, deniedMcpServers, disableAllHooks,
// statusLine, env and command get the message "not allowed in a profile: SR1".
// A profile can never carry MCP definitions (it names servers that live in the
// registry, mcp/registry.toml, see LoadRegistry) or permission settings.
// Parse returns a *ValidationError listing every problem with its line.
//
// # Kinds and trust
//
// Every Source has a Kind that says where its profiles came from:
//
//   - KindPersonal: the user's own directory (PersonalDir).
//   - KindOrg: any shared source (a pinned git repo, a plugin, an org dir).
//   - KindProject: a repository's .ccshelf folder. Project sources are skipped
//     unless ResolveOptions.AllowProject is set, which the caller does only
//     after explicit per-repo trust (SR2). A project profile may not set
//     mcp.servers, session.env, session.append_system_prompt_file, account or
//     session.inherit_user_settings = false, and may not extend a profile of
//     another kind that sets any of those (ErrProjectForbidden). A project
//     profile never shadows and is never shadowed (ErrCollision).
//   - Shared profiles (org and project) may not set inherit_user_settings =
//     false, and neither may a shared profile that extends one that does
//     (ErrSharedDropsUserLayer, SR3). Personal profiles may.
//
// # Lookup and collisions
//
// A name that exists in more than one source is an ErrCollision, except that
// exactly one personal profile may shadow non-project sources, which produces
// a Warning. Parents named in extends are looked up by the same rule across all
// sources. Cycles are ErrCycle (with the path) and the chain may be at most
// MaxExtendsDepth deep.
//
// # Merge rules
//
// The chain is applied root parent first, the requested profile last:
//
//   - Lists (plugins.include, plugins.exclude, skills.off, skills.name_only,
//     mcp.servers) are unioned, keeping first-seen order.
//   - Exclusion is sticky: an exclude or skills.off at any level removes the
//     entry from the merged include or name_only list, whether it came from an
//     earlier or a later level (a later include of something a parent excluded
//     is dropped with a warning). Within one manifest, listing the same id in
//     include and exclude, or the same skill in off and name_only, is a
//     validation error.
//   - Scalars (plugins.mode, mcp.claudeai_connectors, mcp.strict, session
//     model, effort, append_system_prompt_file, inherit_user_settings,
//     policy.on_blocked, account): the latest profile that sets the value wins.
//   - session.env maps merge, the later value winning.
//   - name, description, owner, status, superseded_by, extends, when_to_use and
//     avoid_when come from the requested profile only.
//   - Defaults (Manifest.WithDefaults) are applied after merging: status
//     active, mode allow-only, on_blocked warn, inherit_user_settings true.
//
// MCP servers are looked up in the registry (mcp/registry.toml) of the source
// of each profile that lists them. A server found with different definitions
// in more than one registry is an error (no shadowing); one missing everywhere
// is ErrUnknownMCPServer. The prompt file is read from the source root of the
// profile that set it, must be a regular file of at most MaxPromptSize bytes,
// and its path may not be absolute, contain ".." or resolve (after
// symlinks) outside that root.
//
// # Closure
//
// Resolved.Closure lists what a trust decision covers: each manifest in the
// chain, each registry entry used (canonical JSON including per-OS overrides),
// the prompt, each included plugin id, and each source id with its commit.
// Hash is a SHA-256 over the sorted, length-prefixed items. Directory sources
// appear as "dir:personal", "dir:org" or "dir:project", never as machine paths,
// and CRLF line endings are normalized, so the hash is the same on every OS.
// Registry entries, the prompt, plugin includes and profiles that set
// session.env are marked Risky.
//
// # Environment names
//
// Names in session.env and in registry env_refs are checked with
// internal/envpolicy. Registry env_refs are variable names only: the generated
// MCP config passes them as ${NAME} references and nothing here resolves them.
package profile

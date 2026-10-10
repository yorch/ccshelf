// Package profile parses profile manifests, resolves them across sources and
// computes the trust closure (SR1 to SR3). It reads files only: no network, no
// git, no Claude Code state.
//
// # Manifest
//
// A profile is one TOML file, profiles/<name>.toml, with a closed schema.
// Every unknown key is an error. The keys permissions, hooks, apiKeyHelper,
// allowedMcpServers, deniedMcpServers, disableAllHooks, statusLine, env and
// command get the message "not allowed in a profile: SR1". A profile never
// carries MCP definitions or permission settings. It names servers that live
// in the registry, mcp/registry.toml. Keys must be spelled exactly as
// documented, because the TOML decoder matches struct tags case-insensitively
// and a second spelling could silently override the first. Free-text fields
// (description, owner, when_to_use, avoid_when, env values, and the
// registry's commands, arguments and URLs) may not contain C0 or C1 control
// characters or Unicode bidirectional controls.
//
// # Kinds and trust
//
// The zero Kind is KindInvalid, so a source that forgets to set its kind is
// never treated as personal. Resolve skips project sources unless
// ResolveOptions.AllowProject is set (SR2):
//
//   - A project profile may not set mcp.servers, session.env,
//     session.append_system_prompt_file, account or
//     session.inherit_user_settings = false. It also may not extend a profile
//     of another kind that sets any of those (ErrProjectForbidden). A project
//     profile never shadows and is never shadowed (ErrCollision).
//   - Shared profiles (org and project) may not set inherit_user_settings =
//     false, and neither may a shared profile that extends one that does
//     (ErrSharedDropsUserLayer, SR3). Personal profiles may, with a warning
//     that lists what is not loaded.
//
// # Lookup and collisions
//
// A name in more than one source is an ErrCollision, except that exactly one
// personal profile may shadow non-project sources (with a Warning). Parents
// named in extends use the same rule. Cycles are ErrCycle (with the path),
// and the chain may be at most MaxExtendsDepth deep. See
// docs/design/profiles.md, "Profile sources and sharing".
//
// # Merge rules
//
// Resolve applies the chain root parent first, the requested profile last:
//
//   - Lists (plugins.include, plugins.exclude, skills.off, skills.name_only,
//     mcp.servers) are unioned, keeping first-seen order.
//   - Exclusion is sticky. An exclude or skills.off at any level removes the
//     entry from the merged include or name_only list, whether it came from an
//     earlier or a later level. (Resolve drops a later include of something a
//     parent excluded, with a warning.) Within one manifest, the same id in
//     include and exclude, or the same skill in off and name_only, is a
//     validation error.
//   - Scalars (plugins.mode, mcp.claudeai_connectors, mcp.strict, session
//     model, effort, output_style, append_system_prompt_file, inherit_user_settings,
//     policy.on_blocked, account): the latest profile that sets the value wins.
//   - session.env maps merge, and the later value wins.
//   - name, description, owner, status, superseded_by, extends, when_to_use and
//     avoid_when come from the requested profile only.
//   - Manifest.WithDefaults applies after the merge.
//
// Plugin ids and skill names are compared ignoring case. Two ids of one
// manifest that differ only by case are a validation error. An exclude
// removes an include whatever the case of the plugin or marketplace part
// (with a warning when the spellings differ). This is deliberately
// conservative because Claude Code's own matching is unverified.
//
// Resolve looks up MCP servers in the registry of the source of each profile
// that lists them. A server found with different definitions in more than one
// registry is an error (no shadowing). A server missing everywhere is
// ErrUnknownMCPServer.
//
// The registry is fully trusted code: a stdio command runs with the user's
// rights. Registry entries are therefore Risky in the closure, and
// MCPWarnings makes shell-like commands visible. A registry URL must be a
// lowercase https:// URL with no userinfo, fragment or query string. (Secrets
// belong in env_refs, never in a URL.)
//
// # Confinement
//
// Below a Source's Root, this package reads only the profiles,
// prompts/<file> (the only legal session.append_system_prompt_file, see
// CheckPromptPath) and mcp/registry.toml. DirSource says how Root is derived
// and which roots are refused. Every component below the root is checked
// with Lstat, and a symlink anywhere is refused. Files are opened with
// O_NOFOLLOW where the OS has it. On Windows the Lstat check, which also
// refuses junctions, is the protection. The prompt must be a regular file of
// at most MaxPromptSize bytes.
//
// # Closure
//
// Resolved.Closure lists what a trust decision covers (see ClosureItem and the
// Item* kinds). Hash does not depend on declaration order, comments,
// whitespace, line endings or machine paths, so it is the same on every OS.
//
// # Redaction
//
// Describe (used by show) never prints environment values, MCP URL secrets or
// prompt text.
//
// # Environment names
//
// internal/envpolicy checks the names in session.env and in registry
// env_refs. The launcher sets CCSHELF_PROFILE, so a profile may not set it in
// session.env (a registry env_refs entry may still name it). Registry
// env_refs are variable names only. The generated MCP config passes them as
// ${NAME} references, and nothing here resolves them.
package profile

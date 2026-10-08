// Package profile parses profile manifests, resolves them across sources and
// computes the trust closure (SR1 to SR3). It reads files only: no network, no
// git, no Claude Code state.
//
// # Manifest
//
// A profile is one TOML file, profiles/<name>.toml. Parse decodes it into
// Manifest with a closed schema. Every unknown key is an error. The keys
// permissions, hooks, apiKeyHelper, allowedMcpServers, deniedMcpServers,
// disableAllHooks, statusLine, env and command get the message "not allowed
// in a profile: SR1". A profile can never carry MCP definitions or permission
// settings. (It names servers that live in the registry, mcp/registry.toml,
// see LoadRegistry.) Keys must be spelled exactly as documented. The TOML
// decoder matches struct tags case-insensitively, so Parse also compares every
// key with the exact tag (key "Inherit_User_Settings" must be spelled
// "inherit_user_settings"). Otherwise a second spelling could silently
// override the first. Free-text fields (description, owner, when_to_use,
// avoid_when, env values, and the registry's commands, arguments and URLs) may
// not contain C0 or C1 control characters or Unicode bidirectional controls.
// Parse returns a *ValidationError that lists every problem with its line.
//
// # Kinds and trust
//
// Every Source has a Kind that says where its profiles came from. The zero
// value is KindInvalid, and Resolve and List reject a source that reports it.
// Thus a source that forgets to set its kind is never treated as personal:
//
//   - KindPersonal: the user's own directory (PersonalDir).
//   - KindOrg: any shared source (a pinned git repo, a plugin, an org dir).
//   - KindProject: a repository's .ccshelf folder. Resolve skips project
//     sources unless ResolveOptions.AllowProject is set. The caller sets it
//     only after explicit per-repo trust (SR2). A project profile may not set
//     mcp.servers, session.env, session.append_system_prompt_file, account or
//     session.inherit_user_settings = false. It also may not extend a profile
//     of another kind that sets any of those (ErrProjectForbidden). A project
//     profile never shadows and is never shadowed (ErrCollision).
//   - Shared profiles (org and project) may not set inherit_user_settings =
//     false, and neither may a shared profile that extends one that does
//     (ErrSharedDropsUserLayer, SR3). Personal profiles may.
//
// # Lookup and collisions
//
// A name that exists in more than one source is an ErrCollision, with one
// exception: exactly one personal profile may shadow non-project sources,
// which produces a Warning. Resolve looks up parents named in extends by the
// same rule across all sources. Cycles are ErrCycle (with the path), and the
// chain may be at most MaxExtendsDepth deep.
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
//     model, effort, append_system_prompt_file, inherit_user_settings,
//     policy.on_blocked, account): the latest profile that sets the value wins.
//   - session.env maps merge, and the later value wins.
//   - name, description, owner, status, superseded_by, extends, when_to_use and
//     avoid_when come from the requested profile only.
//   - Resolve applies the defaults (Manifest.WithDefaults) after the merge:
//     status active, mode allow-only, on_blocked warn, inherit_user_settings
//     true.
//
// Plugin ids and skill names are compared ignoring case. Two ids of one
// manifest that differ only by case are a validation error. An exclude
// removes an include whatever the case of the plugin or marketplace part
// (with a warning when the spellings differ). This is deliberately
// conservative because Claude Code's own matching is unverified.
//
// A personal profile that sets inherit_user_settings = false produces a
// warning. The warning says that user settings, user plugins, user skills,
// user MCP, hooks and the model are not loaded.
//
// Resolve looks up MCP servers in the registry (mcp/registry.toml) of the
// source of each profile that lists them. A server found with different
// definitions in more than one registry is an error (no shadowing). A server
// missing everywhere is ErrUnknownMCPServer.
//
// The registry is fully trusted code: a stdio command runs with the user's
// rights. Registry entries are therefore Risky in the closure. Resolve adds a
// warning for each used server whose command (or per-OS override) is a shell
// or launcher (sh, bash, zsh, fish, cmd, powershell, pwsh, env), or whose
// arguments carry -c, /c or -e style flags (MCPWarnings). A registry URL must
// be a lowercase https:// URL with no userinfo, fragment or query string at
// all. (Secrets belong in env_refs, never in a URL.)
//
// # Confinement
//
// A Source's Root is an explicit directory it owns. Below it, this package
// reads only two things besides the profiles themselves:
//
//   - prompts/<file>, the only legal value of
//     session.append_system_prompt_file. See CheckPromptPath: no path
//     component may start with ".", no "..", no absolute or drive paths.
//   - mcp/registry.toml
//
// DirSource derives Root as the parent of the profiles folder only when that
// folder is named "profiles". Otherwise Root is the folder itself, and the
// source has no prompts and no registry. This package refuses a root that is
// the user's home directory, the file system root, or (for a bare folder) the
// ccshelf config directory. It calls Lstat on every component below the root
// and refuses a symlink anywhere. Where the OS has O_NOFOLLOW, it opens files
// with it and compares them with the Lstat result. (On Windows, the Lstat
// check, which also refuses junctions, is the protection.) The prompt must be
// a regular file of at most MaxPromptSize bytes.
//
// # Closure
//
// Resolved.Closure lists what a trust decision covers. Each manifest in the
// chain gives two items:
//
//   - "profile" (identity: name, description, owner, status, when_to_use,
//     avoid_when, model, effort)
//   - "profile-controls" (a digest over plugins.mode, include and exclude,
//     skills.off and name_only, mcp.servers, strict and claudeai_connectors,
//     account, extends, inherit_user_settings, session.env, the prompt file
//     name and policy.on_blocked)
//
// Then come each registry entry used (canonical JSON including per-OS
// overrides), the prompt, each included plugin id, and each source id with
// its commit. Hash is a SHA-256 over the items sorted by kind, name and
// digest, length-prefixed, so it does not depend on declaration order.
// Digests cover canonical forms, not the raw file, so comments and whitespace
// do not matter. Directory sources appear as "dir:personal", "dir:org" or
// "dir:project", never as machine paths. CRLF line endings in prompts are
// normalized. Thus the hash is the same on every OS. Profile controls,
// registry entries, the prompt and plugin includes are marked Risky.
//
// # Redaction
//
// Describe (used by show) prints environment variable names only (values are
// <redacted>), MCP URLs as scheme://host/path without userinfo, query or
// fragment, and never the prompt text, only its size and a short digest.
//
// # Environment names
//
// internal/envpolicy checks the names in session.env and in registry
// env_refs. The launcher sets CCSHELF_PROFILE, so a profile may not set it in
// session.env (a registry env_refs entry may still name it). Registry
// env_refs are variable names only. The generated MCP config passes them as
// ${NAME} references, and nothing here resolves them.
package profile

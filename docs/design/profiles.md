# Profiles: sources, sharing and the manifest

Where profile files come from, how shared profiles are trusted, and the manifest format.

## Profile sources and sharing
Claude Code has no native concept of a profile repo: marketplaces distribute plugins, and a bundle plugin distributes "which plugins go together" but not the activation side (masking, MCP server definitions, skill overrides, session defaults). Profile files need their own distribution path. Design proposal:

| Source type | How it works | Notes |
|---|---|---|
| `plugin` (implemented, but newer than `dir` and `git`) | The marketplace publishes a data-only plugin (e.g. `org-profiles@acme`) containing `profiles/*.toml`. The launcher locates it via `claude plugin list --json` (install path) and binds it to the real marketplace source reported by `claude plugin marketplace list --json` {V}: the source is part of the trust key, so a different marketplace needs trust again, and a `marketplace` value in the config makes a mismatch an error. Other source kinds than github, git and directory in that list are {U}. | Reuses native auth, version pinning, `autoUpdate` and `strictKnownMarketplaces`; no second fetch path for IT to approve. The plugin must be installed, and the launcher must never mask it. Profile versions follow the plugin version. |
| `git` | The launcher clones/pulls a repo into `~/.cache/ccshelf/<name>` at a pinned `ref`. | Works for a profiles-only repo. Adds an auth and fetch path of its own. |
| `dir` | A local directory, e.g. `~/.config/ccshelf/profiles/` (personal) or `.ccshelf/profiles/` in a project repo (per-project defaults). | Always available. |

Config sketch (`~/.config/ccshelf/config.toml`):
```toml
# The order of sources does not matter. Every source is checked, and a
# profile name found in more than one source is an error, except that exactly
# one personal profile may shadow a name from the non-project sources (with a
# warning). A project profile never shadows another source and is never shadowed.

[[sources]]
type = "dir"
path = "~/.config/ccshelf/profiles"      # personal profiles

# [[sources]]                                   # OFF by default (SR2): a cloned repo must not be able to add profiles
# type = "dir"
# path = ".ccshelf/profiles"                    # per-project defaults; needs explicit per-repo trust

[[sources]]
type = "git"
url = "git@ghe.example.com:acme/claude-marketplace.git"   # the org data repo (use the GHE URL)
ref = "v2026.10.1"                              # pinned tag or commit, not a moving branch
path = "profiles"                               # folder inside the repo

# [[sources]]                                   # org profiles shipped as a data-only plugin
# type = "plugin"
# plugin = "org-profiles@acme"
# marketplace = "acme/claude-marketplace"        # expected marketplace source: owner/repo for a GitHub marketplace, or a git URL for a git one; a mismatch is refused
# path = "profiles"

# Optional published catalog.json for `ccshelf search` outside an org repo.
# This takes precedence over catalog data from the configured profile sources.
# [catalog]
# remote_url = "https://catalog.example.com/catalog.json"

[trust]
require_pin = true            # refuse git sources without a pinned ref (the tag is resolved to a commit SHA)
trust_project_profiles = false # project .ccshelf/ folders are ignored unless trusted per repo
on_change = "prompt"          # prompt | fail. What to do when the resolved closure of an accepted profile changes in ANY way
                              # (there is no auto-accept: `allow` is rejected). Risky changes (a new MCP command, env
                              # name, prompt text or plugin) are listed first in the diff and marked risky.
```
`catalog.remote_url` is read only from the user's config file. It must be an
HTTPS URL without embedded credentials, a query string or a fragment. Search
fetches it on each invocation, checks the `catalog.json` format version, and
rejects responses larger than 8 MiB. It supports `ccshelf search` and profile
recommendations. Plugin recommendations still need the org data repo's
marketplace relevance rules; commands that build or lint the catalog also need
the source files.
**Precedence:** the order of `[[sources]]` does not matter. Every source is checked, and a name found in more than one source is a collision error, with one exception: exactly one personal profile may shadow a name from the non-project sources (org, git, plugin), and the launcher prints a warning naming what it shadows. `extends` parents are looked up by the same rule across all sources, so an org profile `frontend` that extends `base` uses the user's personal `base` when one exists (with the shadow warning). The resolved closure, and so trust, covers whichever parent was chosen. **Project profiles (a `.ccshelf/` folder in a repository) are off by default** (SR2): when explicitly trusted per repo they can never shadow a name from another source and can never define MCP commands, env or prompt text. A project profile is also never shadowed by another source: if a trusted repo's `.ccshelf/` holds a profile with the same name as an org or personal one, running that name fails with a collision error. That fails closed, but it means a trusted repo can block a name for as long as it is trusted.

**Trust model (SR1 and SR2).** A shared profile is a closed schema (it cannot carry permissions, hooks, auth or endpoint settings, and MCP definitions live in a reviewed registry), but it still selects plugins and MCP servers that run code, so loading one is effectively running code from that source. The launcher therefore:
1. loads org profiles only from sources already trusted by the org (a pinned git ref, later an allowlisted marketplace), and project profiles only after explicit per-repo trust;
2. records, in a lockfile (`~/.config/ccshelf/lock.json`), a hash of the **resolved closure** (profile, parents, referenced registry entries, prompt bytes) plus the **commit SHA** the tag resolved to;
3. when the closure changes in any way (including identity-only fields such as `description`, `when_to_use`, `model` or `effort`), shows a diff, listing risky changes (a new MCP command, env name, prompt text or plugin) first and marking them, and asks before accepting (`ccshelf trust <profile>`); a tag that now resolves to a different SHA is an untrusted update; non-interactive runs fail closed;
4. never bypasses org policy (unchanged principle).

Still undecided: whether the data-only plugin approach works smoothly when `strictKnownMarketplaces` or other policy applies (untested), and how a profile that lists a plugin the user hasn't installed should be reported (current design: report and print the install command, never install silently).

## Creating profiles (`ccshelf new`)
`ccshelf new <name>` writes exactly one TOML file and nothing else. With the default `--scope user` it goes to the user profiles directory (the XDG/APPDATA location above); with `--scope project` it goes to the nearest Git working-tree root's `.ccshelf/profiles/` (a `.git` directory and a worktree's `.git` file both count), and outside a Git repository to the current directory. Discovery creates nothing and grants no trust.

- **Creation does not grant trust.** Writing a project profile never enables project profiles, writes a trust record or changes configuration. The file stays inert until `trust.trust_project_profiles = true` is set and the folder is reviewed with `ccshelf trust --project`; the command prints that sequence, and warns when the file would land in the home directory, which the runtime does not discover as a project.
- **Validation before the write.** The candidate is resolved in memory through the ordinary origin, parent, MCP and project-restriction rules, so a missing parent, an unknown MCP server, a restricted inherited value or a schema violation is reported and nothing is written.
- **Exclusive, confined writes.** The file is created 0600 inside newly created 0700 directories below an opened-directory anchor that refuses symlinks and replacement ancestors; an existing file is never replaced. A project destination must be a plain directory chain. The personal config root may be reached through symlinks (a dotfiles-managed config directory), matching the reader; the `profiles` directory below it must be a plain directory, as the reader also requires, and the writer resolves the root and refuses a symlink at or below it. An uninspectable namespace (a symlinked `.ccshelf`, an unsafe `.git` marker) only narrows the collision evidence: user creation proceeds with a warning, while project creation still fails closed.
- **Collision and outage policy.** Creation refuses a name that already exists in an accessible personal, local or shared namespace, or in the intended project namespace, because it would shadow it. It never fetches to check a name, inspecting only verified cached git checkouts. When it cannot prove the name is free (an unavailable git source, or a plugin source with no offline namespace cache) project creation fails closed and names the preparation paths, while user creation still succeeds and reports the unavailable source. This is stricter creation behavior only; the runtime resolver's existing shadow rules are unchanged.

A full wizard (no content flags) asks for the location unless `--scope` was given, and ends in the default-no `Create this profile? (does not grant trust)` confirmation; `--yes` answers that question alone. See [cli.md](cli.md), "Wizards and write confirmations".

## Manifest sketch (`profiles/frontend.toml`)
A design proposal; field names are not final. See [workflows.md](workflows.md) for how it is used.
```toml
# One file per profile. The file name must match `name`.

# ---- Identity (shown in `ls`, `search`, and the catalog page) ----
name = "frontend"             # Unique id, kebab-case. What you type: `ccshelf run frontend`.
                              # Also the name of the generated bundle plugin `profile-frontend`.
description = "React, CSS and accessibility work"   # One line, shown in listings and the catalog.
owner = "@web-platform"       # Team or person who maintains the profile. Required for org profiles
                              # (CI lint); optional for personal ones.
status = "active"             # active | experimental | deprecated. Deprecated profiles still run
                              # but print a warning and point to `superseded_by`.
superseded_by = ""            # Only when deprecated: the name of the replacement profile.

# ---- Account (optional; see launcher.md) ----
account = ""                  # Default account (separate CLAUDE_CONFIG_DIR) for this profile. Precedence:
                              # --account flag, then this field, then config default_account, then
                              # Claude Code's default dir. Empty = the default dir.

# ---- Composition ----
extends = ["base"]            # Parent profiles, applied in order. Lists are unioned; an `exclude` or
                              # `off` at any level removes that id for good (a child cannot re-include
                              # it). Cycles are an error.

# ---- Discovery hints (used by `search`, `recommend`, and the catalog) ----
when_to_use = ["UI components", "visual QA"]   # Phrases describing tasks this profile fits.
avoid_when  = ["infra changes"]                # Tasks it does not fit; helps pick between profiles.

# ---- Plugins ----
[plugins]
mode = "allow-only"           # allow-only (default-deny): every installed plugin not listed is masked
                              #   with enabledPlugins:false, regenerated on each launch so newly
                              #   installed plugins stay off.
                              # additive: only plugins in `exclude` are masked.
include = ["design-kit@acme", "playwright@acme"]   # Plugin ids as name@marketplace. Dependencies are
                              # followed, so a bundle brings its parts.
exclude = []                  # Always masked, even if a parent profile included them.

# ---- Standalone skills (~/.claude/skills; NOT plugin skills) ----
[skills]
off = ["legacy-helper"]       # Hidden via skillOverrides "off". Plugin skills can only be controlled
                              # per whole plugin, so use [plugins] for those.
name_only = ["docs-writer"]   # Name stays visible, description dropped (smaller skill listing).

# ---- MCP servers ----
[mcp]
servers = ["figma"]           # Names resolved from an MCP registry file (shared or personal);
                              # these become the per-session --mcp-config.
claudeai_connectors = "none"  # none | keep. "none" writes disableClaudeAiConnectors: true into the generated
                              # settings (valid under sideload policy; connectors ignore enabledPlugins).
strict = true                 # Only the servers above load: every other known MCP server is added to
                              # deniedMcpServers (full names), or --strict-mcp-config where allowed.
                              # Inherited: a child that lists no servers of its own still gets strict
                              # with an empty set, which drops the user's own MCP servers. Unset means
                              # not strict (`show` prints "strict: false (default)"). `claudeai_connectors`
                              # unset means keep (`show` prints "keep (default)").

# ---- Session defaults ----
[session]
model = "opus"                # Passed to claude as the model for this profile (optional).
effort = "high"               # low | medium | high | xhigh | max (optional).
append_system_prompt_file = "prompts/frontend.md"   # Appended via --append-system-prompt-file. The path must stay inside the
                              # profile's source root (no .., no symlinks).
inherit_user_settings = true  # true: keep your ~/.claude/settings.json and mask plugins per key.
                              # false: use --setting-sources project,local (drops user plugins,
                              # skills, MCP, hooks, model) and re-add only what the profile sets.
                              # Personal profiles only: a shared profile may not set false (SR3), because
                              # it would drop the user's deny rules and hooks.
[session.env]
FIGMA_TOKEN_REF = "op://dev/figma/token"   # Allowlist: only CCSHELF_VAR_<NAME> or names ending in _REF (never
                              # ANTHROPIC_*, *_PROXY, NODE_*, PYTHON*, ...). Values are references passed to tools at spawn;
                              # the launcher never resolves secrets to disk or argv. Never commit secrets.

# ---- Policy behavior ----
[policy]
on_blocked = "warn"           # warn | fail. What to do when org policy blocks something the profile
                              # needs (e.g. disableSideloadFlags). The launcher never bypasses policy.
```
Rule for `extends`: lists union in order. Exclusion is sticky in both directions: a `plugins.exclude` or `skills.off` entry at any level of the chain (a parent or the profile itself) removes the id from the merged include and `name_only` lists, including an include in a later (child) profile, which is dropped with a warning. Ids are compared ignoring case. A child therefore cannot re-include something a parent excluded. Personal profiles in `~/.config/ccshelf/profiles/` may extend org profiles. `compile` also writes a bundle plugin `profile-<name>` (dependencies only) into the marketplace so the profile's plugins can be installed natively.

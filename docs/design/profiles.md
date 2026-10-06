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
# Sources are searched in order; the first match for a profile name wins,
# so list the most specific source first (personal, then project, then org).

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

[trust]
require_pin = true            # refuse git sources without a pinned ref (the tag is resolved to a commit SHA)
trust_project_profiles = false # project .ccshelf/ folders are ignored unless trusted per repo
on_change = "prompt"          # prompt | fail. What to do when an accepted profile changes (there is no auto-accept: `allow` is rejected)
                              # in a way that adds MCP commands, env values or system-prompt text.
```
**Precedence:** personal, then org. A personal profile with the same name overrides the org one, and `extends` can still pull in org profiles. **Project profiles (a `.ccshelf/` folder in a repository) are off by default** (SR2): when explicitly trusted per repo they can never shadow a name from another source and can never define MCP commands, env or prompt text.

**Trust model (SR1 and SR2).** A shared profile is a closed schema (it cannot carry permissions, hooks, auth or endpoint settings, and MCP definitions live in a reviewed registry), but it still selects plugins and MCP servers that run code, so loading one is effectively running code from that source. The launcher therefore:
1. loads org profiles only from sources already trusted by the org (a pinned git ref, later an allowlisted marketplace), and project profiles only after explicit per-repo trust;
2. records, in a lockfile (`~/.config/ccshelf/lock.json`), a hash of the **resolved closure** (profile, parents, referenced registry entries, prompt bytes) plus the **commit SHA** the tag resolved to;
3. when the closure changes in a risky way (a new MCP command, env name, prompt text or plugin), shows a diff and asks before accepting (`ccshelf trust <profile>`); a tag that now resolves to a different SHA is an untrusted update; non-interactive runs fail closed;
4. never bypasses org policy (unchanged principle).

Still undecided: whether the data-only plugin approach works smoothly when `strictKnownMarketplaces` or other policy applies (untested), and how a profile that lists a plugin the user hasn't installed should be reported (current design: report and print the install command, never install silently).

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
extends = ["base"]            # Parent profiles, applied in order. Lists are unioned; a later `off` or
                              # `exclude` beats an earlier include. Cycles are an error.

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
Rule for `extends`: lists union in order; a later `off` or `exclude` beats an earlier include. Personal profiles in `~/.config/ccshelf/profiles/` may extend org profiles. `compile` also writes a bundle plugin `profile-<name>` (dependencies only) into the marketplace so the profile's plugins can be installed natively.

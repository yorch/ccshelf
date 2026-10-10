# Profiles: sources, sharing and the manifest

Where profile files come from, how shared profiles are trusted, and the manifest format.

## Profile sources and sharing
Claude Code has no native concept of a profile repo. Marketplaces distribute plugins. A bundle plugin distributes "which plugins go together" but not the activation side (masking, MCP server definitions, skill overrides, session defaults). Profile files need their own distribution path. Design proposal:

| Source type | How it works | Notes |
|---|---|---|
| `plugin` (implemented, but newer than `dir` and `git`) | The marketplace publishes a data-only plugin (e.g. `org-profiles@acme`) containing `profiles/*.toml`. The launcher locates it via `claude plugin list --json` (install path) and binds it to the real marketplace source reported by `claude plugin marketplace list --json` {V}. The source is part of the trust key, so a different marketplace needs trust again. A `marketplace` value in the config makes a mismatch an error. Other source kinds than github, git and directory in that list are {U}. | Reuses native auth, version pinning, `autoUpdate` and `strictKnownMarketplaces`. There is no second fetch path for IT to approve. The plugin must be installed, and the launcher must never mask it. Profile versions follow the plugin version. |
| `git` | The launcher clones/pulls a repo into `~/.cache/ccshelf/<name>` at a pinned `ref`. | Works for a profiles-only repo. Adds an auth and fetch path of its own. |
| `dir` | A local directory, e.g. `~/.config/ccshelf/profiles/` (personal) or `.ccshelf/profiles/` in a project repo (per-project defaults). | Always available. |

After `ccshelf init`, `ccshelf config source add`, `pin` and `rm` add, re-pin and remove `[[sources]]` entries. `ccshelf config set` changes a few settings. You do not need to edit the file by hand (see [cli.md](cli.md), D-50).

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
ref = "v2026.10.1"                              # pinned tag or commit. A branch name is refused here; use `branch` below
path = "profiles"                               # folder inside the repo

# [[sources]]                                   # the same repo, tracking a branch instead of a tag (D-55)
# type = "git"
# url = "git@ghe.example.com:acme/claude-marketplace.git"
# branch = "main"                               # use branch or ref, never both. The branch can move, but trust stays per commit
# path = "profiles"

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
require_pin = true            # refuse a `ref` that is not a tag or a full commit. A `branch` is allowed: the key is explicit
branch_check_interval = "24h" # how often a run checks the remote for a new commit on a tracked branch (1h to 8760h)
trust_project_profiles = false # project .ccshelf/ folders are ignored unless trusted per repo
on_change = "prompt"          # prompt | fail. What to do when the resolved closure of an accepted profile changes in ANY way
                              # (there is no auto-accept: `allow` is rejected). Risky changes (a new MCP command, env
                              # name, prompt text or plugin, or any change to the profile's controls) are listed first in the diff and marked risky.
```
ccshelf reads `catalog.remote_url` only from the user's config file. It must be an
HTTPS URL without embedded credentials, a query string or a fragment. Search
fetches it on each invocation, checks the `catalog.json` format version, and
rejects responses larger than 8 MiB. It supports `ccshelf search` and profile
recommendations. Plugin recommendations still need the org data repo's
marketplace relevance rules. Commands that build or lint the catalog also need
the source files.
**Precedence:** the order of `[[sources]]` does not matter. Every source is checked, and a name found in more than one source is a collision error, with one exception. Exactly one personal profile may shadow a name from the non-project sources (org, git, plugin), and the launcher prints a warning naming what it shadows. Only the built-in personal directory is personal-kind. Any other configured `dir` source, even your own folder, is an org source and collides instead of shadowing. `extends` parents are looked up by the same rule across all sources. So an org profile `frontend` that extends `base` uses the user's personal `base` when one exists (with the shadow warning). The resolved closure, and so trust, covers whichever parent was chosen. **Project profiles (a `.ccshelf/` folder in a repository) are off by default** (SR2, which also limits what they can define). When explicitly trusted per repo, they can never shadow a name from another source, and another source never shadows them. If a trusted repo's `.ccshelf/` holds a profile with the same name as an org or personal one, running that name fails with a collision error. That fails closed, but it means a trusted repo can block a name for as long as it is trusted.

On `strict`: `mcp.servers` is a union across the chain, so a child inherits strict together with every server listed by it and its parents. Only when no level lists a server does strict run with an empty set, which drops the user's own MCP servers. `--strict-mcp-config` with an empty config removes all MCP servers {V} (Stage 0 T5, `../research/stage0.md`). Where the flag cannot be used, the launcher uses `deniedMcpServers` instead (see [launcher.md](launcher.md), "2. What is shared between profiles").

**Trust model (SR1 and SR2).** A shared profile is a closed schema, and MCP definitions live in a reviewed registry. The launcher loads it only from a trusted source, records a hash of its resolved closure in a lockfile and asks before it accepts a change. The full rules are in [security.md](security.md), SR2.

**Tracking a branch (D-55).** A git source with `branch = "<name>"` follows a branch. The name follows `git check-ref-format --branch` and cannot be a full ref such as `refs/heads/main`. Setting `ref` and `branch` together is an error, and so is setting neither. The branch is allowed with `trust.require_pin = true`, because the key is explicit. A `ref = "main"` still fails, and its message points to `branch = "main"`.

- ccshelf resolves `refs/heads/<name>` with `git ls-remote` to a full commit SHA. The checkout, the content check and the lockfile entry are per commit, as for a tag. The lockfile records the ref as `branch:<name>`, so a branch and a tag with the same name never share a record.
- `run` and `dry-run` use the commit that the profile being run trusted, from the cache, with no network. Two profiles of one source may have been trusted at two commits, and each one keeps its own (see the next point for the update). If that commit is not in the cache, ccshelf fetches exactly that commit by id. It never checks the branch head to fill a gap. At most once per `trust.branch_check_interval`, they check the remote for a new commit on the branch. ccshelf stores the time of the last attempt for each source in the private cache (`branch-check.json`), also when the attempt fails, so a host that is down does not slow every run.
- If the head moved, a terminal shows the same diff and question as `ccshelf trust`. If the user accepts, the run uses the new commit. If the user declines, the run uses the trusted commit and says so. Without a terminal, with `--yes`, or with `on_change = "fail"`, ccshelf keeps the trusted commit and prints one line that names the source and `ccshelf trust <profile>`. A failed check prints at most one warning line and never blocks the run.
- A commit that another profile accepted is offered at once (D-58). When a profile runs at a commit and the lockfile holds a more recently accepted commit for the same source and branch, ccshelf offers that commit to this profile with no interval and no network call to find it: it reads the lockfile and the cached checkout. It offers the commit only when the commit in use is a strict ancestor of it, proven offline (the parent line of the verified commit object, or `git merge-base --is-ancestor`). If it cannot prove this, it makes no offer, because the newest record may be a rollback or a commit that a force-push removed. The cached checkouts have depth 1, so the proof works for a direct parent. The question and the notice follow the rules of a moved head (`--yes`, no terminal and `on_change = "fail"` give one notice line), but the text names the commit and the profile that trusted it, and does not say that the branch moved. The hint is `ccshelf run <profile>` in a terminal, because `ccshelf trust` reads the head. The commit runs only after this profile trusts it. A decline is remembered for this profile, branch and commit in `branch-check.json`: until the commit changes or one `trust.branch_check_interval` passes, only the notice is printed. The periodic head check still runs when it is due, and its result replaces the offer for that branch.
- `run --refresh` and `dry-run --refresh` check the head now, whatever the interval says. `ccshelf trust` and `ccshelf ls --refresh` also check the head now. After such a refresh, the normal trust check applies: a prompt on a terminal, or exit 4 without one or with `on_change = "fail"`.
- `ls`, `show` and `config show` label the source, for example `branch main @ 1a2b3c4`. The commit is the one the source is prepared at, so the label shows what a run uses. In `--json`, the `source` and `sources` fields keep their old format. The label is in the separate `tracks` field (and `trusted_commit` in `config show --json`).
- If a source is switched from a tag to a branch at the same commit, the next run that finds the closure trusted by content rewrites the refs in the lockfile (`Store.Rekey`). It needs equal hashes, the same URL and the same commit, so it can never create or move trust.
- Tag and commit sources have no periodic check.

Still undecided:
- Whether the data-only plugin approach works smoothly when `strictKnownMarketplaces` or other policy applies (untested).
- How to report a profile that lists a plugin the user hasn't installed. Current design: report and print the install command, never install silently.

## Creating profiles (`ccshelf new`)
`ccshelf new <name>` writes exactly one TOML file and nothing else. With the default `--scope user`, it goes to the user profiles directory (the XDG/APPDATA location above). With `--scope project`, it goes to the nearest Git working-tree root's `.ccshelf/profiles/` (a `.git` directory and a worktree's `.git` file both count). Outside a Git repository, `--scope project` uses the current directory. Discovery creates nothing and grants no trust.

- **Creation does not grant trust.** Writing a project profile never enables project profiles, writes a trust record or changes configuration. The file stays inert until `trust.trust_project_profiles = true` is set and the folder is reviewed with `ccshelf trust --project`. The command prints that sequence. It also warns when the file would land in the home directory, which the runtime does not discover as a project.
- **Validation before the write.** `ccshelf new` resolves the candidate in memory through the ordinary origin, parent, MCP and project-restriction rules. It reports a missing parent, an unknown MCP server, a restricted inherited value or a schema violation, and then writes nothing.
- **Exclusive, confined writes.** The file is created 0600 inside newly created 0700 directories below an opened-directory anchor that refuses symlinks and replacement ancestors. An existing file is never replaced. A project destination must be a plain directory chain. The personal config root may be reached through symlinks (a dotfiles-managed config directory), as the reader allows. The `profiles` directory below it must be a plain directory, as the reader also requires. The writer resolves the root and refuses a symlink at or below it. An uninspectable namespace (a symlinked `.ccshelf`, an unsafe `.git` marker) only narrows the collision evidence: user creation proceeds with a warning, while project creation still fails closed.
- **Collision and outage policy.** Creation refuses a name that already exists in an accessible personal, local or shared namespace, or in the intended project namespace, because it would shadow it. It never fetches to check a name, inspecting only verified cached git checkouts. If it cannot prove the name is free (an unavailable git source, or a plugin source with no offline namespace cache), project creation fails closed and names the preparation paths. User creation still succeeds and reports the unavailable source. This is stricter creation behavior only. The runtime resolver's existing shadow rules are unchanged.

A full wizard (no content flags) asks for the location unless `--scope` was given, and ends in the default-no `Create this profile? (does not grant trust)` confirmation. `--yes` answers that question alone. See [cli.md](cli.md), "Wizards and write confirmations".

## Manifest sketch (`profiles/frontend.toml`)
A design proposal. Field names are not final. See [workflows.md](workflows.md) for how it is used.
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
                              # Inherited by every child that does not set it; the allowed set is the
                              # union of the servers listed along the chain (see the note below). Unset means
                              # not strict (`show` prints "strict: false (default)"). `claudeai_connectors`
                              # unset means keep (`show` prints "keep (default)").

# ---- Session defaults ----
[session]
model = "opus"                # Passed to claude as the model for this profile (optional).
effort = "high"               # low | medium | high | xhigh | max (optional).
output_style = "Explanatory"  # Claude Code output style (optional). Written as outputStyle in the generated settings.
                              # Case-sensitive. A name that Claude Code does not know gives the Default style, without an error.
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

# ---- Instructions (optional) ----
[instructions]
files = ["prompts/company.md", "prompts/frontend.md"]   # Paths below prompts/ in the source of this profile. The same path
                              # rules as append_system_prompt_file. At most 32 files, no duplicates. The launcher joins the
                              # files into one CLAUDE.md (see "Instructions" below).
inherit = true                # true (default): the files come after the files of the parent profiles.
                              # false: drop the files of the parent profiles (the profiles it extends, directly or not).

# ---- Policy behavior ----
[policy]
on_blocked = "warn"           # warn | fail. What to do when org policy blocks something the profile
                              # needs (e.g. disableSideloadFlags). The launcher never bypasses policy.
```
### Output style
`session.output_style` sets the Claude Code output style of the session. The launcher writes it as `outputStyle` in the generated settings file, next to `model`. A command-line settings file wins over the `outputStyle` of a project {V} ([stage 0 note](../research/output-style-stage0.md)). The docs rank it above the user settings too {R}, but no run checked that. Managed policy settings win over the command-line settings file {R}, and the launcher does not try to change that (R3).

Rules:
- The value is one name. The pattern is `^[A-Za-z0-9]([A-Za-z0-9 ._:-]{0,62}[A-Za-z0-9._-])?$`, so a name has at most 64 characters, and it does not start or end with a space. Letters, digits, space, dot, underscore, colon and hyphen are allowed. Control characters and all other characters are refused. A style whose name has a non-ASCII letter or a parenthesis cannot be selected by a profile. The settings validator applies the same rule before every launch.
- The built-in names are `Default`, `Proactive`, `Concise` (Claude Code 2.1.237 or later), `Explanatory` and `Learning` {V}. A custom style is a Markdown file in `~/.claude/output-styles`, in `.claude/output-styles` of a project or in the managed settings directory {V}. A plugin can ship styles in `output-styles/` {V}. A plugin style is selected as `<plugin>:<Name>`, for example `acme-kit:Terse`. The bare name `Terse` and the name `acme-kit:terse` did not select it {V}. The 64-character limit includes the plugin prefix.
- The value is case-sensitive {V}. A name that matches no style gives the Default style and no error {V}. The launcher therefore warns when the value matches a built-in name other than `Default` only without case (for example `explanatory`) and names the correct spelling. The launcher does not look for custom style files. A misspelled custom name is not found, and Claude Code uses the Default style.
- Inheritance is the same as for `model`. A child that sets `output_style` replaces the value of its parents. A child that does not set it inherits the value. There is no way to unset an inherited value. Setting `Default` is not the same as unset: it overrides the choice of the user or of the project, and an unset value leaves that choice in effect.
- The value is part of the controls item of the closure, which is always risky. A change needs trust again and shows in the trust diff. A custom style can replace the coding instructions of the system prompt. A profile without `output_style` has the same closure hash and the same generated settings as before the key existed.
- The profile pins a name, not content. A style file with the same name in the project shadows the built-in style: a project file `.claude/output-styles/Explanatory.md` with `name: Explanatory` decided the reply, although the profile selected `Explanatory` {V}. Custom styles drop the coding instructions unless `keep-coding-instructions: true` is set {V}. User and plugin style files may shadow a name too {U}.
- A project profile may set `output_style`, as it may set `model`. This gives a repository no new power. The repository can already set `outputStyle` in its own `.claude/settings.json`, because the launcher keeps the `project` and `local` setting sources. It can also shadow any style name with a file. `[instructions]` and `append_system_prompt_file` are different: they carry content through the own channel of ccshelf, so SR2 limits them.
- Claude Code applies an output style to the main conversation and to forks. Other subagents run their own system prompt and do not get the style {V}.
- The docs say that a plugin style with `force-for-plugin: true` applies whenever its plugin is enabled and overrides the `outputStyle` setting {R}. A profile that enables such a plugin then cannot choose another style {U}. No run checked this.
- Claude Code reads the style files when it starts. There is no command-line flag for an output style {V}, so the launcher uses the settings file.
- `show` prints the value, `show --json` has it as `session.output_style`, and `diff` compares it.

Compare the three channels that change what the model is told:

| Key | What it changes | Reaches | Merge along `extends` |
|---|---|---|---|
| `session.output_style` | Role, tone and format. A custom style can drop the coding instructions of the system prompt. | Main conversation and forks | Child replaces parent |
| `session.append_system_prompt_file` | Appends one file to the system prompt. | Main conversation only | Child replaces parent |
| `[instructions]` | CLAUDE.md-style text in a user message. | Main conversation and subagents that load CLAUDE.md files | Files add up |

### Instructions
`[instructions]` gives a profile CLAUDE.md-style text. The launcher joins the effective files into one generated `CLAUDE.md` and passes it with `--add-dir` (see [launcher.md](launcher.md)). Claude Code loads it for the main conversation and for general-purpose subagents {V} ([stage 0 note](../research/instructions-stage0.md)). Claude Code reads the file once, at session start {V}. An edit of the file during a session changes nothing in that session. The launcher writes the loading variable into the process environment and into the generated settings `env`, so a project settings file cannot turn it off. Managed settings can, and the launcher then warns (see [launcher.md](launcher.md)).

Merge rules:
- The resolver walks the `extends` chain in its normal order, root parent first. It collects the files of each profile and reads each file from the source root of the profile that lists it.
- A profile with `inherit = false` drops the files declared by its ancestors: the profiles it extends, directly or not. Files of a profile that is not its ancestor stay. Thus the order of `extends` does not change which files stay. It only changes the join order. In a diamond (`base` is extended by `l` and by `r`, and `r` sets `inherit = false`), the files of `base` are dropped and the files of `l` stay. Its own files, and the files of its children, still apply. `show` prints the profile whose `inherit = false` dropped files.
- The same path from the same source appears once. The resolver removes duplicates after the drops, and the first one stays.
- One file is at most 64 KiB. The joined text is at most 64 KiB. Over the limit, the profile does not resolve.
- The launcher joins the files in order. It changes CRLF to LF, trims the trailing newlines of each file and puts one blank line between files.
- The resolver refuses every `@` that is followed by a character that is not white space or a backslash. Claude Code expands such a token into the content of another file. The extractor of Claude Code 2.1.295 lexes the file with a Markdown lexer, skips code and code span tokens, and runs the pattern `(?:^|\s)@(...)` on each text token {V} ([stage 0 note](../research/instructions-stage0.md#extractor-in-the-claude-code-2-1-295-binary-2026-10-09)). The check does not copy the lexer. It looks at the raw characters and has no exemption for code spans, fenced blocks or other Markdown structure. Thus the tokenization does not matter. Two cases stay allowed: an `@` after a letter or a digit (an email address) and an `@` after an odd number of backslashes. The one remaining assumption is that no Markdown construct makes a text token start directly after a letter or a digit that precedes the `@` in the raw text {R}. A differential run against the extractor supports it.
- The check refuses the character references `&#64;`, `&#x40;` and `&commat;` too, as defense in depth.
- Limitation: the rule refuses `@` inside code too. Write `\@types/node` or `\@property` to keep the character. Inside code the backslash stays visible. A later change may relax the rule when evidence supports it.
- The resolver also refuses a file with a leading byte order mark, with a first line of `---` (front matter, which Claude Code reads in memory files), or with a carriage return that is not part of a CRLF line end. Each message names the line and what to change.
- The resolver runs the check on each file and again on the joined text, because a fence can start in one file and end in the next.
- The generated `CLAUDE.md` starts with the line `# Instructions from the ccshelf profile <name>` and a blank line. The header keeps the file from starting with front matter. The closure holds the header as the item `00 header`, so a new header text needs trust again. The size limit does not count the header.
- A project profile may not set `[instructions]`, and it may not extend a profile of another kind that sets it (SR2).
- The closure pins every effective file with its content hash. The order is part of the closure, so a reorder needs trust again.

Compare with `session.append_system_prompt_file`. That key is one file at system-prompt level. It reaches the main conversation only, and a child profile replaces the file of its parent. Instructions arrive as CLAUDE.md content (a user message), reach subagents (not the built-in Explore and Plan agents) and add up along the chain {V}. A profile may set both. The two channels do not depend on each other.

Rule for `extends`: lists union in order. Exclusion is sticky in both directions: a `plugins.exclude` beats a plugin include and a `skills.off` beats `skills.name_only`, at any level of the chain. Concretely, a `plugins.exclude` or `skills.off` entry at any level of the chain (a parent or the profile itself) removes the id from the merged include and `name_only` lists. This also applies to an include in a later (child) profile, which is dropped with a warning. Ids are compared ignoring case. A child therefore cannot re-include something a parent excluded. Personal profiles in `~/.config/ccshelf/profiles/` may extend org profiles. `compile` also writes a bundle plugin `profile-<name>` (dependencies only) into the marketplace so the profile's plugins can be installed natively.

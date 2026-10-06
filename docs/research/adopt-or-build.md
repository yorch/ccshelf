# Adopt, build or contribute (Phase 0.2, O-04)

Evaluated 2026-10-06 against `fuzzyalej/claude-profile` and `edimuj/claude-rig`. Read-only: repository metadata through `gh`, and a shallow clone of each repository into a scratch directory, where I read the README, `docs/` and the launch code. I did not build or run either tool, and I did not run `claude`.

Labels: {V} read in the project's repository this session (or returned by `gh` this session); {R} reported by a secondary source, not re-checked; {U} inferred, not observed. A claim about what a tool does at run time is at best {U}, because nothing was run.

## 1. The two candidates

| | fuzzyalej/claude-profile | edimuj/claude-rig |
|---|---|---|
| Language, license | Rust, MIT {V} | Go (standard library only, Go 1.25.7), MIT {V} |
| Stars, forks | 10 stars, 0 forks {V} | 9 stars, 2 forks {V} |
| Created, last push | 2026-07-10, 2026-09-28 {V} | 2026-02-10, 2026-06-08 {V} |
| Releases | v0.6.0 on 2026-09-28; at least eight releases between 2026-07-15 and 2026-09-28 {V} | v0.31.0 on 2026-06-08; releases every one to three weeks from 2026-04-18 to 2026-06-08, none since {V} |
| Open issues, PRs | 0 issues, 0 PRs ever {V} | 2 open issues (unknown flags silently ignored; `--help` read as a value), 10 in total, 0 PRs {V} |
| People | one contributor (26 commits) {V} | one contributor (at least 30 commits; the API page I read was capped at 30) {V} |
| CI | test matrix on Linux, macOS and Windows; `contents: read`; actions on major tags (`@v6`, `@v2`), not full SHAs {V} | no test workflow in the tree, only a tag-triggered release workflow with `contents: write` and actions on major tags (`@v4`, `@v5`, `@v6`) {V} |
| Size | about 4,400 lines of Rust in `src/`, plus docs and 15 reference profiles {V} | about 8,000 lines of Go in `cmd/claude-rig/` (a 7,000 line `commands.go`) plus tests {V} |

Maintenance health: both are single-author projects with no external contributors or pull requests. claude-profile is active (a push eight days before this evaluation); claude-rig has been quiet for four months after a fast start. Neither has a security policy or a threat model in the tree {V} (no `SECURITY.md` in either file list).

## 2. How each one works

### claude-profile

- A profile is a JSON file with `marketplaces`, `plugins` (as `plugin@marketplace`), `pluginDirs`, `mcpServers`, `bare`, `extends` and `removePlugins`. The Rust struct uses `deny_unknown_fields`, so unknown keys are rejected {V}. Inheritance is one level deep {V}.
- Launch: each referenced plugin or skill is copied ("vendored") out of a commit-pinned marketplace clone into `~/.claude-profiles/store/<profile>/vendor/`, then it spawns `claude --strict-mcp-config --mcp-config <json> --plugin-dir <each vendored dir> [--bare]` and forwards the exit code {V}.
- The code never passes `--settings` or `--setting-sources`, and never writes `enabledPlugins`; a unit test asserts that no `--settings` flag is emitted {V}. The README says nothing else on the machine is "registered" {V}. Whether plugins that are already installed and enabled in the user's `~/.claude/settings.json` still load alongside the `--plugin-dir` ones is therefore not something the tool's code prevents; I did not run it, so the effect is {U}. Our Stage 0 result says `--plugin-dir` adds and does not subtract {R} (this reading is consistent with the code, but it was not tested on this tool).
- It documents that global and project `CLAUDE.md` and auto-memory are not gated, and offers `bare` (API-key only, drops OAuth) as the only remedy {V}.
- Pinning: a `<profile>.lock` records the marketplace commit SHA on first use; `update profiles --frozen` fails when a lock is stale {V}. The lock pins marketplaces, not a hash of the resolved closure (the plugin contents are copied after resolution, with no content hash found in the docs I read) {U}.
- Sharing: `install owner/repo` clones a "pack" of profiles from any repository; profile search order includes project-local `./profiles/` and `./.claude-profiles/` {V}. There is no per-source trust state, no off-by-default switch for project profiles, and no rule that a project profile cannot shadow a personal one; the first match wins {V}. That is the opposite of SR3.
- Extras: `find` (an offline cross-marketplace plugin index), combining several profiles in one launch, a `new` scaffold, a statusline snippet, shell completions for four shells {V}.
- Platforms: macOS, Linux, Windows; Windows needs Git for Windows and `claude` on `PATH`; `.exe`, `.cmd`, `.bat` lookup is handled in `src/exe.rs` {V}. Release tooling is `cargo-dist`-style installers and a Homebrew tap {V}.
- GitHub hosts: marketplaces are `owner/repo` (GitHub.com shorthand) or a full `https://` or `git@` URL, so a GHE host is reachable by full URL {V}; I found no GHE-specific documentation or test {U}. No mention of managed settings or policy in the README, docs or source (grep for `managed`, `policy`, `enterprise`, `GHE` found only unrelated Windows lines) {V}.

### claude-rig

- A "rig" is a separate config directory selected through `CLAUDE_CONFIG_DIR`, with per-rig `settings.json`, `CLAUDE.md`, `.claude.json`, plugins, skills, agents, hooks and MCP servers; everything else in `~/.claude/` is shared through symlinks, and "isolated" means the symlink is replaced by a local copy {V}. Auth can be linked to the global login by symlink (`--link-auth`) {V}.
- Plugins and MCP servers are synced from the global config into each rig (`sync`) {V}. Rig-level blueprints can be exported and imported as `.tar.gz` (secrets redacted, paths templatized per its docs) {V}.
- Launch: `syscall.Exec` on Unix, spawn-and-wait on Windows {V}. That matches AGENTS.md.
- Windows requires Developer Mode because the design depends on symlinks; `init` fails fast otherwise {V}. Session detection scans `/proc` and is not available on Windows {V}.
- Pinning Claude Code versions per rig, project binding through a `.claude-rig` file, shell wrapper injection into the user's shell profile, and agents mode are features we do not plan {V}.
- No profile schema with a closed set of keys: a rig's `settings.json` is the user's own settings file, so a rig can hold permissions, hooks and env by design {V}. There is no trust model for shared rigs beyond `export` and `import` of archives {V}.
- Nothing about managed policy, GHE or a catalog {V} (searched the docs and source).

## 3. Comparison with ccshelf's requirements

Legend: yes / partial / no / unknown. Based on what I read; unrun, so "yes" means the design and code say so, not that I observed it.

| Requirement | claude-profile | claude-rig |
|---|---|---|
| R1 six targets (darwin, linux, windows; arm64, amd64) | partial: three OSes with CI; arm64 targets not checked {U}; Windows needs Git for Windows | partial: releases for macOS, Linux and Windows; Windows needs Developer Mode (symlinks); no test CI |
| R2 GitHub.com, GHE Cloud and Server, Actions | partial: full git URLs work, no GHE docs or tests; no Actions integration | no: not addressed |
| R3 managed policy, never bypass | unknown: no mention; `--plugin-dir` and `--mcp-config` may be restricted by policy {U}; no capability detection | no: its own config directory and settings file bypass the user layer by construction; behavior under managed policy untested {U} |
| R4 open source, no org data | yes (MIT; ships fictional-looking public reference profiles) | yes (MIT) |
| R5 profiles in an org's private repo, tool is public | partial: packs from any git repo work; nothing org-specific | no: blueprints are archives, not git-sourced shared profiles |
| R6 flags alone, prompts optional | mostly: a confirm prompt before vendoring, with `--yes` to skip {V} | partial: init and shell wrapper are interactive {U} |
| SR1 closed schema, no permissions, hooks, auth, endpoint | partial: closed keys (`deny_unknown_fields`) and no `--settings` use, so it cannot write permissions or hooks; but `mcpServers` is free-form JSON and plugin contents can carry hooks | no: a rig is a full settings file |
| SR2 trust the resolved closure by commit SHA | partial: marketplace commit SHAs locked per profile; no closure hash, no trust prompt keyed to content | no |
| SR3 project profiles off by default, no shadowing | no: project-local dirs are searched before personal and packs, first match wins | partial: `.claude-rig` names a rig but a rig is a local directory {U} |
| SR4 protected plugins (org controls cannot be masked away) | no | no |
| SR5 private cache files, re-hash before reuse | not found | not found; symlinks into `~/.claude/` are the design |
| Default-deny masking over one shared plugin store | no (additive `--plugin-dir`; masking of installed plugins not done {V code}) | no (a second config directory instead; plugin caches duplicated) |
| Concurrency of two sessions | per-profile vendor dirs, so unshared mutable state {V}; credentials untouched | separate `.claude.json` per rig {V}; shared auth tokens reported to cause conflicts for Remote Control {V} |
| Catalog (lint, generated CATALOG.md) | partial: `find` index, no lint or owner and status metadata | no |
| Symlink-free, `$TMPDIR`-free | yes (copies) {V} | no (symlinks are central; Windows Developer Mode) |

## 4. Gaps against what ccshelf set out to do

Neither tool does default-deny masking on one shared plugin store, so neither answers "load only these plugins" while sharing auth, history and plugin installs. The three decisions that make ccshelf different are all contradicted by one of them:

- D-02 (generated `--settings` mask, nothing shared written) versus claude-profile's vendored copies plus `--plugin-dir`, and claude-rig's separate config directories.
- "Profiles share auth, history and memory (no `CLAUDE_CONFIG_DIR` by default)" versus claude-rig's whole design.
- SR1 to SR5 (closed schema, trust closure, project off by default, protected plugins, private cache) have no counterpart in either project. They are the product for an organization that distributes profiles.
- R3 and R2 have no counterpart in either project, and the catalog part has none either.

## 5. Recommendation

**Build; do not adopt; contribute nothing structural; take specific ideas.** This is a recommendation for the user to review, recorded as D-33.

Reasons:
1. Adopting claude-rig would reverse D-02 and the shared-auth decision, and would bring symlinks, which AGENTS.md forbids as a design assumption (and which need Developer Mode on Windows).
2. Adopting claude-profile is the closest fit, but it is a different mechanism (additive vendoring, no masking), in Rust where we chose Go, with no policy, trust, protected-plugin or catalog model. Adding those would change most of the code, which is a rewrite under someone else's repository, for a single-author project with ten stars. A contribution of that size is unlikely to be wanted or to land.
3. Neither is a maintenance risk we would inherit by building: both are small, so the cost of a fourth tool is mostly our own scope, which is already bounded by the roadmap.
4. The routing eval (Phase 0.1) still decides whether the launcher is worth shipping as a product at all; this evaluation only answers "does an existing tool already cover it". It does not, on what I read.

What to take (ideas, not code; both are MIT, so code could be reused with attribution, but the languages differ):
- From claude-profile: the `find` offline cross-marketplace index (relevant to `search` and `recommend`), combining several profiles in one launch, `update --frozen` as a CI assertion for stale locks (compare our lockfile verification), a statusline snippet that shows the active profile, an explicit "what is not gated" section (CLAUDE.md and memory), and the fact that `bare` is unusable for subscription users.
- From claude-rig: `isolation` and `diff` style inspection output, `--json` on every read command, `doctor --fix`, the secret-redaction and path-templating rules of its blueprint export, and its lesson that two sessions with shared OAuth tokens can conflict (for Remote Control).
- Avoid: symlink-based sharing, `CLAUDE_CONFIG_DIR` by default, free-form settings in a shared artifact, first-match-wins profile search across project and personal sources, floating tags for CI actions.

What would change the recommendation (revisit): claude-profile adds settings-based masking, a managed-policy mode and trust or closure pinning; either maintainer says they want those contributions; the routing eval shows no benefit (then the answer becomes "alias recipe", see the roadmap kill criteria); or a third tool appears that covers default-deny masking plus policy.

## 6. What I could not verify

- Run-time behavior of either tool (nothing was built or run). In particular, whether claude-profile's session still loads the user's installed plugins is {U}.
- Whether either tool works under a managed policy; both are silent on it {V} for the docs, {U} for the behavior.
- GHE behavior of either tool.
- arm64 builds for Windows and Linux (release configs were not read in detail).
- Contributor responsiveness: there are no external issues or pull requests to judge by.

## Sources (read 2026-10-06)

- https://github.com/fuzzyalej/claude-profile (README, `docs/how-it-works.md`, `docs/profiles.md`, `docs/commands.md`, `src/launch.rs`, `src/profile.rs`, `.github/workflows/ci.yml`; `gh repo view`, `gh api` for releases, commits and issues)
- https://github.com/edimuj/claude-rig (README, `docs/isolation.md`, `docs/windows.md`, `cmd/claude-rig/*.go`, `.github/workflows/release.yml`; `gh` for releases and issues)
- [landscape.md](landscape.md) section C for the earlier star and push-date table.

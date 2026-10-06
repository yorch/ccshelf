# 04. Recommendation and roadmap

## Recommendation
**Code layout of the tool repo** (the public repo; the org's private data repo is described in `08-org-data-repo-structure.md`). One Go module that builds one binary, split into three packages with no cross-dependencies except on `core`:

- `core/`: read `marketplace.json` and installed state (`claude plugin list --json` for installed plugins, `--available` when the catalog needs uninstalled ones), resolve sets. Pure, no side effects.
- `profiles/`: local per-terminal launcher. Depends on `core`.
- `catalog/`: metadata lint + static catalog site, runs in CI. Depends on `core`.

Separate releases. Git plus CI is the registry; no server or database.

### Design principles
1. **Never write shared state** (`~/.claude/settings.json`, `~/.claude.json`, plugin cache). The launcher writes only its own generated files (content-addressed, in its own cache dir; see "Cross-platform requirement") and starts `claude`. This is what makes concurrent terminals safe and what mcpick-style tools probably lack (U).
2. **Respect org policy.** Detect `disableSideloadFlags`, managed `enabledPlugins`, `strictKnownMarketplaces`; report "blocked by policy". Never work around them.
3. **Prefer `--settings` (`enabledPlugins` masking + `skillOverrides`) over sideload flags**, since `--plugin-dir`, `--agents`, `--mcp-config` can be disabled by policy. Use sideload flags only when policy allows.
4. **Everything cheap to delete.** Anthropic is shipping into this space. The manifest and catalog conventions are the durable parts; the launcher should be thin enough to delete if native profiles (#91770) ship.
5. **Catalog is derived**, never a second source of truth: `marketplace.json` + per-plugin sidecar files + git data.
6. **Lead the pitch with routing quality and clutter**, not token cost.

## Stack and platform decisions
Decided by the user (2026-10-06): **Go** for the implementation; **GitHub / GitHub Enterprise (GHE) and GitHub Actions** for hosting and CI.

### What GHE + GitHub Actions imply (design assessment; not yet tested against the user's GHE)
| Area | Implication |
|---|---|
| Which GHE | **Both GHE Cloud and GHE Server must be supported (R2).** The flavor matters for Actions availability, Pages and egress, so the design assumes the lowest common denominator (see R2 below). |
| CI lint/catalog action | Provide a **composite action** in this repo. Two ways to get the binary: download a pinned release asset from the tool's public GitHub Releases (or from an internal mirror of them, which GHE Server without internet access needs), or build from source with `actions/setup-go` (slower; works where release downloads are restricted). On GHE Server, third-party actions such as `setup-go` must be mirrored or allowed by the admin. Pin by tag or SHA. |
| Releases | `goreleaser` supports GitHub Enterprise endpoints (`github_urls`). Publish binaries for all six targets to the public tool repo's GitHub Releases; adopters can mirror them to an internal package registry, Homebrew tap or Scoop bucket. A mirror that lives in a private repo needs a token for downloads, which affects `brew`/`scoop` install steps. |
| Catalog hosting | GitHub Pages (access-controlled on GHE Cloud; available on GHE Server if enabled), or any internal static host. The CI job publishes `catalog.json` + HTML as a Pages artifact. Pages visibility must match who may see the plugin list. |
| Marketplace source type | Claude Code marketplace sources of type `github` target github.com; for GHE hosts a **git URL** source is likely required (`git@ghe.example.com:org/marketplace.git` or https). Needs verification against the marketplace docs and the user's auth (SSH keys/credential helper). Also affects `strictKnownMarketplaces` patterns. |
| Profile `git` source | Same: use git URLs for GHE; honor the user's existing git credential helper/SSH config rather than storing tokens. |
| Tags and bundles | Native bundle resolution uses git tags `<plugin>--v<version>`; CI should create them (`claude plugin tag`), which needs write permission for `GITHUB_TOKEN` on tags. |
| Usage data | Enterprise Analytics API / OTel exist independently of GitHub; the catalog job needs a secret for the Analytics API key if used. |
| Provenance | Artifact attestations / Sigstore signing of releases are desirable; support on GHE Server is limited. Treat as optional. |

### Requirement R2: support both GHE Cloud and GHE Server (decided 2026-10-06)
Design to the lowest common denominator, so nothing assumes github.com or the newest Actions features.
1. **CLI first, workflow second.** All logic (`lint`, `compile`, `catalog build`) lives in the Go binary; the GitHub Actions workflow is a thin wrapper that calls it. The same binary then runs in any other CI (or locally) if Actions is restricted or a different runner is used.
2. **No hard-coded hosts.** GitHub API/Git/Pages/release URLs come from configuration or the Actions environment (`GITHUB_SERVER_URL`, `GITHUB_API_URL`), never from `github.com` literals. Test against a non-github.com host.
3. **Minimal Actions dependencies.** Prefer composite steps that call the binary and plain `git`/`gh`. Avoid third-party actions where practical; where unavoidable (`setup-go`, `upload-pages-artifact`), document that GHE Server admins must mirror/allow them (e.g. via `actions-sync` or GitHub Connect) and offer an alternative without them.
4. **Offline/air-gapped tolerant.** GHE Server may have no internet: the release binary must come from the internal instance, builds must not fetch from `github.com` at runtime, and the catalog site must be self-contained (no CDN fonts/scripts).
5. **Version skew.** GHE Server lags github.com. Avoid features that may not exist on older Server versions (newer Actions syntax, artifact attestations, some Pages options) or make them optional with a fallback. Record the minimum supported GHE Server version once known.
6. **Auth via the user's git setup.** Git operations shell out to `git` using the existing credential helper or SSH config; API calls (if any) use `GITHUB_TOKEN` in CI or `gh` locally. No tokens stored by the tool.
7. **Marketplace source types.** For GHE hosts use git URL sources; verify the `github` source type behavior against the marketplace docs for both flavors (open).
8. **Catalog hosting is pluggable.** Output a plain static directory. Publishing to Pages is one option (differences between Cloud and Server noted above), but any static host works.

Still to verify: how Claude Code's marketplace and plugin source types behave with GHE Cloud vs Server hosts (including data-residency domains), and the minimum GHE Server version the Actions workflows must support.

### Go stack choices (proposed)
- Module layout: `core/`, `profiles/`, `catalog/` as Go packages in one module; one `cprof` binary with subcommands (project name `claude-profile`, command `cprof`; see "Name" below). Cobra or a small stdlib-based CLI parser; stdlib `os/exec`, `encoding/json`, `html/template`, `embed`.
- TOML: a maintained library (`pelletier/go-toml/v2` or `BurntSushi/toml`; pick one that preserves useful error positions).
- JSON Schema validation for manifests: a Go validator library, with schemas in `schema/` shared with editors.
- Git access for `git` sources: shell out to the user's `git` (inherits credential helper/SSH config, important for GHE) rather than embedding a git library.
- Catalog frontend: generated static HTML plus one small vanilla JS file for filtering; data in `catalog.json`.
- Release: `goreleaser`; CI matrix on `macos-latest`, `ubuntu-latest`, `windows-latest` (see cross-platform section).
- Tests: golden files for generated settings, a fake `claude` binary built from `testdata/`, opt-in real-`claude` integration test.

## Cross-platform requirement (macOS, Linux, Windows)
**Requirement R1:** the launcher, the catalog tooling and the CI lint must work on macOS (arm64, x64), Linux (x64, arm64; WSL counts as Linux) and native Windows 10/11 (x64, arm64), with the same behavior and the same profile files everywhere. Everything below is a design assessment; Stage 0 was run on macOS only, so Linux and Windows behavior is **not yet tested**.

### Facts from the docs (reported by a research agent; managed-settings registry details were truncated)
- Native Windows: `%USERPROFILE%\.local\bin\claude.exe` (PowerShell/CMD installer or WinGet), or npm (Node 22+). The shell tool is PowerShell, and Git Bash is optional (`CLAUDE_CODE_GIT_BASH_PATH`). WSL is a separate Linux install with its own `~/.claude`.
- Config: `~/.claude/` and `~/.claude.json` (`%USERPROFILE%` on Windows). Linux does not use XDG for Claude Code.
- Managed settings: macOS `/Library/Application Support/ClaudeCode/`, Linux `/etc/claude-code/`, Windows `C:\Program Files\ClaudeCode\`. Windows registry/MDM/GPO is mentioned but its details were not retrieved.
- Credentials: macOS Keychain (keyed per `CLAUDE_CONFIG_DIR`, file fallback; verified in the docs by the adversary's check); **Linux and Windows use `<config dir>/.credentials.json`**, with no locking documented.
- Paths in JSON settings/MCP configs: forward slashes everywhere, `~` and `${VAR}` expansion supported. `CLAUDE_CODE_PLUGIN_DIRS` uses `:` on Unix and `;` on Windows; `--plugin-dir` is repeated per path.
- No Claude Code temp-dir override; it uses OS defaults (`TMPDIR`, or `TEMP`/`TMP` on Windows).
- Symlinks on Windows need Developer Mode or admin. WinGet upgrades can fail while `claude.exe` is running.
- **Conflict to resolve:** the agent says MCP `npx` servers need no `cmd /c` wrapper on native Windows. I recall the MCP docs recommending `cmd /c npx ...` there. Treat as unverified until tested.

### How it changes the design
| Area | Unix-only assumption in earlier drafts | Change |
|---|---|---|
| Starting `claude` | `exec claude` | Windows has no `exec`. Spawn a child with inherited stdio, ignore Ctrl+C in the launcher (the child shares the console), forward termination, return the child's exit code. On Unix, exec is optional; spawn-and-wait everywhere is the simpler uniform choice. |
| Generated files | Per-process files under `$TMPDIR`, deleted on exit | With `exec` nobody can clean up afterwards. Use **content-addressed files** (name = hash of the resolved config) in the launcher's cache dir. Identical content is reused, so concurrent runs never collide. Write atomically (temp file + rename) and garbage-collect old files at launch. Also avoids Windows file-lock problems from overwriting an open file. |
| Finding `claude` | `PATH` lookup | Use PATH lookup (handles `.exe`/`.cmd`), fall back to `~/.local/bin`, allow an override (config or env). npm installs on Windows give a `.cmd` shim, which needs care when spawning. |
| Paths in profile files | `~/...` and `/` | Accept `~` and forward slashes; normalize backslashes on read; always write forward slashes into generated JSON. Never build paths by string concatenation. |
| Launcher's own dirs | `~/.config/claude-profile/` | Config: `$XDG_CONFIG_HOME` or `~/.config/claude-profile` on macOS/Linux, `%APPDATA%\claude-profile` on Windows. Cache: `~/.cache/claude-profile` or `%LOCALAPPDATA%\claude-profile`. Docs examples show the Unix form. |
| Shell aliases | `alias cf=...` | Aliases don't exist in PowerShell/cmd. `cprof shell-init` emits bash/zsh/fish functions and PowerShell functions; cmd gets `.cmd` shims. |
| Symlinks | Not used | Keep it that way: never depend on symlinks (Windows needs Developer Mode). Copy or content-address instead. |
| MCP `npx` servers | `command = "npx"` | The MCP registry should allow a per-OS command override (e.g. `windows = ["cmd", "/c", "npx", ...]`) until the question above is settled. |
| Policy detection | Read managed-settings files | File paths are known per OS, but Windows registry/MDM and server-managed settings can't be read reliably. Make detection best-effort and also **detect by failure**: if `claude` exits 1 on a sideload flag, report "blocked by policy". |
| Credentials | "Keychain per config dir" | Not our concern as long as we never set `CLAUDE_CONFIG_DIR`. On Linux/Windows credentials are one shared file with no documented locking, so concurrent token refresh is **untested** there. |
| Encoding | Not discussed | Write UTF-8 without BOM and LF; read tolerant of BOM and CRLF. Windows PowerShell 5 `>` redirection writes UTF-16, which would break TOML/JSON; document it and reject non-UTF-8 input with a clear error. |
| Self-update | n/a | A running `.exe` can't be replaced on Windows. No self-update; ship through package managers (Homebrew, Scoop/WinGet, apt/deb later) and release downloads. |
| Distribution/trust | n/a | Unsigned binaries trigger macOS Gatekeeper (notarization) and Windows SmartScreen. Internal distribution needs signing or a package manager that handles it. |
| WSL | n/a | Treated as Linux. A Windows-native launcher and a WSL launcher are separate installs with separate configs; the launcher never crosses the boundary. Profile sources on a Windows path aren't visible from WSL unless configured. |
| Testing | macOS only so far | CI matrix on macos, ubuntu and windows runners. Use a **fake `claude`** test double (records its arguments and generated files) for unit and integration tests on every OS; keep the real-`claude` init-event test (Stage 0 method) opt-in because it needs authentication. |

### Supported targets for the first release (decided 2026-10-06)
All six: **darwin/arm64, darwin/amd64, linux/amd64, linux/arm64, windows/amd64, windows/arm64**. WSL counts as linux.
- **Build:** Go cross-compiles all six from one runner (`goreleaser`).
- **Test in CI:** run unit and fake-`claude` integration tests natively wherever a hosted runner exists (macOS arm64 and Intel, Ubuntu x64, Windows x64; Ubuntu arm64 and Windows arm64 runners are reportedly available for public repos, which needs verifying before committing to them). Targets without a native runner are at minimum compile-checked, and flagged "built, not natively tested" in the release notes until a runner is available.
- **Publish:** release archives for all six; Homebrew tap (macOS and Linux), Scoop and WinGet (Windows). Binaries need macOS notarization and Windows signing, or the package manager must cover that.
- **Not covered by CI:** the real-`claude` integration test stays opt-in (needs authentication), so Linux and Windows behavior of `claude` itself (credentials, `cmd /c` for `npx`, managed-settings paths) must be verified manually or in a self-hosted job before claiming support.

### Effect on the language choice
This strengthens **Go**: `os/exec`, build tags for platform differences, `filepath`, `os.UserConfigDir`/cache dirs, trivial cross-compilation, and `goreleaser` publishing to Homebrew, Scoop and WinGet. TypeScript with `bun --compile` remains possible, but Windows signal and console handling are the weaker spot. Shell scripts are out as an implementation (they're fine only as generated aliases).

### Open questions (new)
1. Do concurrent sessions refresh credentials safely on Linux/Windows (shared `.credentials.json`)?
2. Does masking via `--settings` behave identically on Windows (Stage 0 repeated there)?
3. Does the MCP `npx` command need `cmd /c` on native Windows?
4. How do Windows registry/MDM managed settings appear, and can they be detected?
5. ~~Which architectures are in scope~~: **decided, all six targets** (see "Supported targets"). Still open: which Linux distros and packaging formats beyond release archives.

## Policy spectrum and open source (R3, R4)
Decided 2026-10-06. The user's org **does enforce managed settings** (exact keys not yet known), and the tool must also work with no policy and with partial policy. It will be used inside the org and released as **open source**.

**R3: the launcher is capability-driven, not tier-driven.** Instead of hard-coding "strict" and "loose" modes, it probes each capability and degrades per feature:

| Capability | Needed for | If blocked |
|---|---|---|
| `--settings` masking (`enabledPlugins`, `skillOverrides`, env) | plugin/skill filtering | Core feature. If even this fails, refuse and explain. |
| `--strict-mcp-config` + `--mcp-config` | hiding MCP servers and claude.ai connectors | Skip MCP control, warn that servers/connectors stay active. |
| `--plugin-dir` / `CLAUDE_CODE_PLUGIN_DIRS` | session-only plugins (none are generated by default, per the standalone-skills decision) | Skip those plugins; suggest packaging them in the marketplace. |
| `--agents` | profile-defined subagents | Skip. |
| `--setting-sources` | dropping the user layer | Fall back to per-key masking (`inherit_user_settings = true` behavior). |
| Force-enabled plugins (managed `enabledPlugins: true`) | masking | Can't be masked; list them in `show`/`doctor` as "always on by policy". |

- Each profile feature maps to a required capability, and `[policy] on_blocked` (`warn` or `fail`) decides what happens.
- **Detection:** read managed-settings files per OS (best-effort) and **detect by failure** at launch (exit 1 on a blocked flag becomes "blocked by policy: <flag>"). `cprof doctor --policy` prints the capability matrix (available / blocked / unknown). Server-managed settings and Windows registry/MDM can't be read reliably, so "unknown" is a valid state.
- **Never bypass policy.** This holds in every mode, including open-source use.
- **Cases to test:** no policy; permissive policy (e.g. only `strictKnownMarketplaces`); sideload blocked; force-enabled plugins; both. Each needs a fixture (a fake managed-settings file plus the fake `claude` that mimics the exit-1 behavior).

**R4: open source.** No org-specific names, URLs or assumptions in code, schemas or defaults; everything org-specific lives in config and in the org's marketplace repo. Needs a license, contribution docs, a project and command name (decided: `claude-profile` / `cprof`, with the brand risk noted; see "Name"), and docs that don't depend on internal infrastructure. This reinforces R2 (GitHub.com, GHE Cloud and GHE Server all supported) and R1 (all three operating systems).

## Tool repo vs data repo (R5)
Decided 2026-10-06: the **tool is hosted in a public GitHub repo** (this one), and an adopting company stores its **profiles and catalog data in its own private GHE repo**. The tool never assumes the two live together.

| | Tool repo (public, github.com) | Org data repo (private, GHE Cloud or Server) |
|---|---|---|
| Holds | Go source, schemas, docs, release binaries, a reusable GitHub Action, example/fictional profiles, a starter template | The org's `marketplace.json`, plugins, `profiles/*.toml`, generated profile bundles (`bundles/`), org config. The built catalog (`catalog.json`, site) is produced in CI and not committed |
| Contains org data? | Never. Examples are fictional. | Yes |
| Released how | Public GitHub Releases (goreleaser); package managers; adopters may mirror internally | Not released; consumed by the tool |
| Changes by | Open-source contributors | The org's platform team |

Consequences:
- **Reusable CI:** the data repo's workflow calls the public tool, either `uses: <owner>/claude-profile/action@<pinned tag or SHA>` or a step that downloads a pinned release binary. GHE Cloud can use public actions directly; **GHE Server needs GitHub Connect or a mirror** (e.g. `actions-sync`), or the binary-download variant (also mirrorable to an internal registry). Both variants must be documented; the logic stays in the binary (R2).
- **Pin everything.** The data repo pins the tool version and (where supported) verifies a checksum, since the tool runs in the org's CI and on developers' machines.
- **Starter template** (layout in `08-org-data-repo-structure.md`): ship a template/example data repo (`examples/org-data-repo/`, possibly also a GitHub template repository) with a sample `marketplace.json`, `profiles/`, a CI workflow and a catalog publish recipe, so adopting takes minutes.
- **Configuration lives with the adopter, not in the tool:** profile sources (`dir`/`git`, later `plugin`), the catalog metadata schema location and lint rules come from the org's `claude-profile.toml` in the data repo and the user's own `~/.config/claude-profile/config.toml`, with sane defaults. The tool repo never needs to know about a particular org.
- **Catalog hosting is the adopter's choice** (R2): the tool outputs a plain static directory; the starter template shows GitHub Pages and an internal static host. We don't pick one for the org.
- **Telemetry:** none by default. An open-source tool that runs in corporate CI must not phone home.
- **Security reporting, license and contribution docs** live in the public repo (R4).

### License (decided 2026-10-06)
**MIT** for the tool repo (code and docs). Still to do: add a `LICENSE` file (needs the copyright holder name and year), and confirm that the user's employer allows open-sourcing this before the first public commit. The data repo (the org's profiles and catalog data) is the org's own and is not covered by this license.

### Name (decided 2026-10-06)
Project **`claude-profile`**, command **`cprof`**. The user accepted two known risks: "Claude" in the name of an open-source tool may conflict with Anthropic's brand guidelines if published widely, and a similarly named Go tool already exists (`claude-profile`, a Go binary that wraps `CLAUDE_CONFIG_DIR`; see 02). Revisit before the first public release; a rename is cheap now and costly later.

### Do not build (yet)
Registry server/DB, vector search, TUI, custom install path (bundles cover install), MCP gateway, config-dir-per-profile, a concierge search tool before there is usage data, anything that writes shared settings.

## Staged roadmap
**Build order (decided 2026-10-06): both tracks in parallel.** Start with the shared `core/` package (read `marketplace.json`, installed-plugin state, resolve sets, capability probe), then one thin slice of each track: launcher `run`/`show`/`dry-run` with policy detection, and catalog metadata lint plus a minimal static page. Risk to watch: spreading effort. Keep each slice shippable on its own.
- **Stage 0 (DONE 2026-10-06): de-risk the launcher.** Full results in `05-stage0-results.md` (run on macOS only; reported by a subagent with raw outputs kept):
  - T1 `--settings` `enabledPlugins:false` masks a user-level `true`: confirmed, per-key merge.
  - T2 `--setting-sources project,local` drops user plugins, skills and MCP; auth still works: confirmed.
  - T3 `skillOverrides`: works for standalone skills only; token effect within noise.
  - T4 concurrent sessions: no `~/.claude.json` corruption seen (not a stress test).
  - T5 `--strict-mcp-config` with an empty config removes plugin MCP servers and claude.ai connectors: confirmed.
  - T6 managed settings: none on the test machine (MDM and server-side not checked).
  - T7 bundle behavior: skipped (needs installing a plugin).
  - Token savings were small (~2.4k of ~27k), so the pitch rests on routing and clutter. Still untested: bundles, locked-down policy, Linux and Windows.
- **Catalog track (no experiments needed, low risk, can start now):**
  1. Metadata convention: native `author`, `category`, `tags` stay in the marketplace entry; catalog-only fields (`owner`, `status` (active/experimental/deprecated), `superseded_by`, `when_to_use`, `avoid_when`, `overlaps_with`, `review_by`, `support`) live in a **sidecar file per plugin**, `catalog/plugins/<name>.toml` (decided 2026-10-06; see `08-org-data-repo-structure.md`). Small registries may opt into single-file mode.
  2. CI lint (extends `claude plugin validate`) failing on missing required fields.
  3. Static catalog generator: facets by category/tag/team/status, overlap view, "new or changed since last tag" diff, owner/status badges; optional usage join from the Analytics API.
  4. `relevance` blocks and profile bundles (`bundles/`) in marketplace.json (native, push-style discovery).
- **Profiles track:**
  1. Profile manifest + resolver + `run`/`show`/`ls`/`dry-run`; content-addressed generated files in the cache dir; policy detection.
  2. Inheritance (`extends`), `diff`, `doctor` (resolved set, leaks, overlap, token estimate).
  3. `compile`: emit bundle meta-plugins (`profile-<name>`) into `bundles/` (committed; `compile --check` fails CI on drift). The bundle's `marketplace.json` entry is written by hand once and checked by `lint`. Catalog-published bundles then become selectable profiles.
- **Project setup (both tracks):** add the `LICENSE` file (MIT; copyright holder and year) and confirm the employer allows open-sourcing; add `SECURITY.md` and `CONTRIBUTING.md`; reusable GitHub Action and goreleaser packaging (Homebrew, Scoop, WinGet); starter template data repo (`examples/org-data-repo/`); trust and lockfile for shared profiles; `shell-init`; the `--account` option (see 07).
- **Later, only if data shows need:** OTel importer + `recommend`; concierge.

## Profile sources and sharing
Claude Code has no native concept of a profile repo: marketplaces distribute plugins, and a bundle plugin distributes "which plugins go together" but not the activation side (masking, MCP server definitions, skill overrides, session defaults). Profile files need their own distribution path. Design proposal:

| Source type | How it works | Notes |
|---|---|---|
| `plugin` (**planned after `dir` and `git`**; not in the first release) | The marketplace publishes a data-only plugin (e.g. `org-profiles@acme`) containing `profiles/*.toml`. The launcher locates it via `claude plugin list --json` (install path). | Reuses native auth, version pinning, `autoUpdate` and `strictKnownMarketplaces`; no second fetch path for IT to approve. The plugin must be installed, and the launcher must never mask it. Profile versions follow the plugin version. |
| `git` | The launcher clones/pulls a repo into `~/.cache/claude-profile/<name>` at a pinned `ref`. | Works for a profiles-only repo. Adds an auth and fetch path of its own. |
| `dir` | A local directory, e.g. `~/.config/claude-profile/profiles/` (personal) or `.claude-profile/profiles/` in a project repo (per-project defaults). | Always available. |

Config sketch (`~/.config/claude-profile/config.toml`):
```toml
# Sources are searched in order; the first match for a profile name wins,
# so list the most specific source first (personal, then project, then org).

[[sources]]
type = "dir"
path = "~/.config/claude-profile/profiles"      # personal profiles

[[sources]]
type = "dir"
path = ".claude-profile/profiles"               # per-project defaults, relative to the repo root

[[sources]]
type = "git"
url = "git@ghe.example.com:acme/claude-marketplace.git"   # the org data repo (use the GHE URL)
ref = "v2026.10.1"                              # pinned tag or commit, not a moving branch
path = "profiles"                               # folder inside the repo

# [[sources]]                                   # planned for a later release, after testing under policy
# type = "plugin"
# plugin = "org-profiles@acme"                  # org profiles shipped as a data-only plugin
# path = "profiles"

[trust]
require_pin = true            # refuse git sources without a pinned ref
on_change = "prompt"          # prompt | fail | allow. What to do when an accepted profile changes
                              # in a way that adds MCP commands, env values or system-prompt text.
```
**Precedence:** personal, then project, then org. A personal profile with the same name overrides the org one, and `extends` can still pull in org profiles.

**Trust model.** A shared profile can define MCP server commands, environment and system-prompt additions, so loading one is effectively running code from that source. The launcher therefore:
1. loads org profiles only from sources already trusted by the org (an allowlisted marketplace, or a pinned git ref);
2. records a hash of each accepted profile in a lockfile (`~/.config/claude-profile/lock.json`);
3. when a profile changes in a risky way (new MCP command, new env, new system-prompt text), shows the diff and asks before accepting (`cprof trust <profile>`);
4. never bypasses org policy (unchanged principle).

Still undecided: whether the data-only plugin approach works smoothly when `strictKnownMarketplaces` or other policy applies (untested), and how a profile that lists a plugin the user hasn't installed should be reported (current design: report and print the install command, never install silently).

## Manifest sketch (`profiles/frontend.toml`)
A design proposal; field names are not final. See `06-example-workflows.md` for how it is used.
```toml
# One file per profile. The file name must match `name`.

# ---- Identity (shown in `ls`, `search`, and the catalog page) ----
name = "frontend"             # Unique id, kebab-case. What you type: `cprof run frontend`.
                              # Also the name of the generated bundle plugin `profile-frontend`.
description = "React, CSS and accessibility work"   # One line, shown in listings and the catalog.
owner = "@web-platform"       # Team or person who maintains the profile. Required for org profiles
                              # (CI lint); optional for personal ones.
status = "active"             # active | experimental | deprecated. Deprecated profiles still run
                              # but print a warning and point to `superseded_by`.
superseded_by = ""            # Only when deprecated: the name of the replacement profile.

# ---- Account (optional; see 07) ----
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
claudeai_connectors = "none"  # none | keep. claude.ai connectors ignore enabledPlugins; "none" needs
                              # --strict-mcp-config, which org policy can block.
strict = true                 # Pass --strict-mcp-config so only the servers above load.

# ---- Session defaults ----
[session]
model = "opus"                # Passed to claude as the model for this profile (optional).
effort = "high"               # low | medium | high | xhigh | max (optional).
append_system_prompt_file = "prompts/frontend.md"   # Extra instructions appended to the system prompt.
inherit_user_settings = true  # true: keep your ~/.claude/settings.json and mask plugins per key.
                              # false: use --setting-sources project,local (drops user plugins,
                              # skills, MCP, hooks, model) and re-add only what the profile sets.
[session.env]
FIGMA_TOKEN_REF = "op://dev/figma/token"   # Environment for the session; prefer references to a
                              # secret store over raw values. Never commit secrets.

# ---- Policy behavior ----
[policy]
on_blocked = "warn"           # warn | fail. What to do when org policy blocks something the profile
                              # needs (e.g. disableSideloadFlags). The launcher never bypasses policy.
```
Rule for `extends`: lists union in order; a later `off` or `exclude` beats an earlier include. Personal profiles in `~/.config/claude-profile/profiles/` may extend org profiles. `compile` also writes a bundle plugin `profile-<name>` (dependencies only) into the marketplace so the profile's plugins can be installed natively.

## CLI sketch
`run <profile> [-- claude args]` · `ls` · `show <p>` (resolved closure, overrides, token estimate) · `diff <a> <b>` · `dry-run <p>` (prints exact `claude` command) · `init` (create config, optionally from an org data repo URL) · `new <p> [--from <p2>]` · `edit <p>` · `trust <p>` · `account add <name>` · `shell-init <bash|zsh|fish|pwsh>` · `compile [--check]` · `lint` · `catalog build` · `search <q>` · `recommend` (rule-based, no LLM) · `doctor [--policy]` (overlap, unused, deprecated-in-use, stale owners, policy shadowing, capability matrix).

## Repo layout
Tool repo (this one): `core/ profiles/ catalog/ schema/ action/ site/ examples/ docs/`.
Org data repo: see `08-org-data-repo-structure.md` (marketplace, plugins, `profiles/`, generated `bundles/`, `catalog/` sidecars, org config, CI). Built catalog output is not committed.

## Open decisions
1. ~~Does the user's org use managed settings?~~ **Decided:** yes, enforced; the tool must work across none, partial and strict policy (R3), and be open source (R4). Still unknown: which managed keys the org actually sets.
2. ~~Order~~: **decided, both in parallel** (core first, then a thin slice of each).
3. ~~Implementation language~~: **decided, Go** (see "Stack and platform decisions"). Still open: whether `core/` is a Go library package or just a convention. GHE Cloud vs Server: **both must be supported** (R2).
4. ~~Where the catalog is published~~: **adopter's choice** (tool outputs a static directory; the starter template shows GitHub Pages and a static host); see R5. **Metadata ownership decided: hybrid.** Plugin authors write their own metadata; a platform reviewer approves it (wording, overlaps, deprecations, taxonomy). Mechanics: CI lint enforces required fields; `CODEOWNERS` routes review of a plugin's sidecar file (`catalog/plugins/`) to the platform team, while plugin source stays with the owning team (sidecar location decided 2026-10-06); `review_by` dates let `doctor` flag stale entries. The tool provides the lint rules and the staleness check; review routing is the data repo's `CODEOWNERS`, so the tool stays out of org process.
5. ~~Profile distribution~~: **decided, `dir` + `git` sources first; `plugin` source later**, once tested under managed policy and `strictKnownMarketplaces`. This changes the earlier lean (plugin as default): the tool-repo / private-data-repo split (R5) makes a pinned git source the natural fit, since it works with any private repo and the user's git credentials and doesn't depend on marketplace policy.
6. ~~Standalone skills~~: **decided, explicit off-list + guidance to package as plugins.** Profiles list standalone skills to hide via `skillOverrides` (tested, works under any policy). `doctor` warns about standalone skills that no profile mentions, and the docs explain how to package them into a marketplace plugin so profiles can control them as a unit. No generated `--plugin-dir` plugin (blocked by sideload policy) and no silent auto default-deny. For non-plugin MCP servers: `--strict-mcp-config` where allowed; to verify, `deniedMcpServers` inside the `--settings` file as a fallback where `--mcp-config` is blocked (docs say it works in any settings file; untested here). Original question: how `profiles` handles standalone `~/.claude/skills` and non-plugin MCP, which bundles cannot express (idea: library folder injected as a generated local plugin, subject to sideload policy).

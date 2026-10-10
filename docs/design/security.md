# Security and policy

Security requirements SR1 to SR5 (added after the security review) and the managed-policy model (R3).

## Security requirements (SR1 to SR5)
Added 2026-10-06 after the security review. The verdict of the review was that the trust model was "not acceptable as written". SR1 to SR3 are required before a first release. SR4 and SR5 are required before anyone runs the tool in corporate CI. A written threat model and a `SECURITY.md` are also required before the repo goes public. I verified the settings-file claim underneath SR1 (see [stage0.md](../research/stage0.md)): a `--settings` file can switch a session to `bypassPermissions`.

**SR1: closed profile schema.** The generator writes only an allowlist of settings keys: `enabledPlugins`, `skillOverrides`, `disableClaudeAiConnectors`, `deniedMcpServers`, `model`, `outputStyle` and env names that pass the env allowlist (`internal/envpolicy`). The env allowlist accepts **only** these names:
- Names in the `CCSHELF_VAR_<NAME>` namespace.
- Names ending in `_REF`.
- `CCSHELF_PROFILE`.

As defense in depth, the env policy first applies a denylist of known-dangerous prefixes such as `ANTHROPIC_`, `NODE_`, `PYTHON`, `JAVA_`, `LD_`, suffixes such as `_OPTIONS`, and exact names. The launcher passes `effort` as a command-line flag, not as a settings key. A profile can never write `permissions`, `hooks`, `apiKeyHelper`, `*McpServers` allow lists or `disableAllHooks`. Otherwise a profile's env alone could redirect every prompt and file to another endpoint or run code at process start. MCP server definitions are not part of a profile: a profile names servers, and the definitions live in a reviewed registry in the org data repo. A golden test proves that no profile, however crafted, produces a key outside the allowlist.

**SR2: trust the resolved closure, pinned by commit SHA.** A shared profile cannot carry permissions, hooks, auth or endpoint settings (SR1). But it still selects plugins and MCP servers that run code, so loading one is effectively running code from that source.
- Every non-personal source needs explicit trust. The launcher loads org profiles only from sources already trusted by the org (a pinned git ref, an explicit git branch, later an allowlisted marketplace). Project sources (a `.ccshelf/` folder in a cloned repo) are **off by default**, and the user trusts them per repo (path plus hash, like direnv `allow`). A project source can never define MCP commands, env, prompt text or instructions. Otherwise a malicious repository could run code through the launcher while skipping Claude Code's own workspace trust and per-server MCP approval.
- The lockfile (`~/.config/ccshelf/lock.json`) hashes the **fully resolved closure**: the profile, its `extends` parents, the MCP registry entries it references, the prompt file bytes and the source commit SHA. A change to `mcp/registry.toml` that alters what `figma` runs changes the hash.
- For a closure that includes a non-personal (org or project) source, every change needs trust again: `ccshelf trust <profile>` shows a diff and asks. This includes identity-only edits such as `description`, `when_to_use`, `model` or `effort`. A personal-only closure is always trusted. A closure never trusted before still prompts on a terminal.
- The diff lists risky items first and marks them:
  - every profile's controls (plugins, skills, MCP servers and controls, `extends`, account, `inherit_user_settings`, `session.env`, prompt file name, `on_blocked`)
  - registry entries
  - prompt text
  - every effective instructions file, with its content hash and its position in the join order
  - plugin includes (plugins carry hooks and MCP).

  Only the identity item (name, description, owner, status, `when_to_use`, `avoid_when`, `model`, `effort`) is not risky. With `[trust] on_change = "fail"` a changed or moved closure exits 4 even on a terminal.
- When trust is granted, ccshelf resolves a tag to a commit SHA and stores it in the lockfile. `run` uses the pinned commit (from the cache, without network, when it is already fetched). Only `ccshelf trust` and `ccshelf ls --refresh` notice a tag that later points elsewhere, and they show it as an untrusted update. Recommend tag-protection rulesets on the org data repo. Once a day, ccshelf prunes cache files older than the retention age. It never prunes the checkouts a lock pins.
- A source can track a **branch** only through the explicit `branch` key (D-55). The `ref` key still refuses branch names.
  - Trust stays per commit. ccshelf resolves the branch head to a commit SHA, and every new commit on the branch is an untrusted update (`TagMoved`, shown as "the branch ... now points to").
  - Each profile runs the commit that it trusted. If that checkout is not in the cache, ccshelf fetches it by that commit id, never by the head.
  - `run` uses the trusted commit with no network. At most once per `trust.branch_check_interval` (default 24h), it checks the branch head with one `git ls-remote`.
  - A moved head starts a question only in a terminal, and never with `--yes` or `on_change = "fail"`. Without a terminal, ccshelf keeps the trusted commit and prints one line that names `ccshelf trust`. A failed check never blocks the run.
  - A commit that another profile accepted is offered to a profile that still runs an older commit of the same branch source (D-58). ccshelf reads only the trust lockfile and the verified cached checkout for it: no `git ls-remote`, and the candidate is a commit that a lockfile record holds, never a head. It must be a strict descendant of the commit in use, proven offline, so a rollback or a force-pushed commit is never offered. The profile runs it only after its own trust, with the same rules as a moved head. A decline is remembered in the private cache for one interval.
  - `run --refresh`, `ccshelf trust` and `ccshelf ls --refresh` check the head now. Then `on_change = "fail"` exits 4, as for a moved tag.
  - Recommend branch protection with required reviews on the tracked branch. Whoever can push to it can change what the next review shows.
  - Adding a branch source, or switching a source to a branch, is a weakening change in `ccshelf config`.
- Compile from the same in-memory bytes that were hashed (no re-read), to close the gap between accepting and running.
- Non-interactive and CI runs fail closed (exit 4). `--yes` never accepts trust (R6).
- The launcher never bypasses org policy.

**SR3: no shadowing, protected controls.** A project profile can never shadow a profile from another source (a collision is an error), and two org sources cannot define the same name. A personal profile may shadow an org profile of the same name because the user owns it (the listing marks it "shadows"). The org config (`ccshelf.toml`) declares `[protect] plugins` and `[protect] mcp` (audit, secret-scanning or required servers) that are never masked. ccshelf reads the org config from the pinned tree of a git source, so a git source without one gets a visible warning. The lists are part of the trust closure, so a change to them needs re-trust. If a source is unreachable, ccshelf continues to enforce its last-known lists from the lockfile for every profile. A broken org config stops the command. `inherit_user_settings = false` drops the user settings layer that normally enables plugins. With it, the generator writes protected and policy-locked plugins as enabled, never off. Shared profiles cannot set `inherit_user_settings = false`, because that drops the user's deny rules, hooks and MCP. Personal profiles can set it, with a warning that lists what is dropped. Only managed settings with `allowManagedHooksOnly`, `allowManagedPermissionRulesOnly` and `disableBypassPermissionsMode` are real enforcement. The docs must say so.

**SR4: private, verified local artifacts.**
- Cache directory mode 0700 (owner-only ACL on Windows) and files 0600, created with exclusive-create and no-follow.
- Re-hash a content-addressed file before reuse.
- Refuse a cache directory owned by another user or reached through a symlink.
- Never write resolved secrets to disk or argv. Prefer `--append-system-prompt-file`, which keeps prompt text off the process list.
- Redact values (key names only) in `dry-run`, `show` and `doctor` output. People paste this output into issues.
- Treat the instructions directory as untrusted at every launch. Claude Code can edit a directory given with `--add-dir` {V}. Before each launch the launcher checks that `<cache>/instructions-<hash>` is a real directory that the user owns, holds exactly one regular file `CLAUDE.md` (no link) and that the file hashes to the name. Otherwise it rebuilds the directory atomically. The directory has mode 0500 and the file mode 0400 on Unix, and the check requires both. This stops accidental writes only, because a process of the same user can change the modes. The check runs at launch. It cannot stop an edit during a session, and the next launch repairs it. See [launcher.md](launcher.md), step 4.
- Confine every path a profile names (prompt files, parents) to its source root, with no `..` and no symlinks.

**Instructions (extends SR2 and SR4).**
- A shared profile can send text into the context of the model through `[instructions]`, so the text is part of the trusted closure. A change to a file, the file list or the order needs trust again.
- An `@` import token in an instructions file is refused when the profile resolves. Claude Code would expand the token into the content of another file, for example a file outside the profile. The extractor of Claude Code 2.1.295 runs its pattern on each text token of a Markdown lexer and skips only code tokens {V} ([stage 0 note](../research/instructions-stage0.md#extractor-in-the-claude-code-2-1-295-binary-2026-10-09)). A check that copies the lexer is not safe: review found inputs that the lexer reads as text but a code-span or fence rule hides. The check therefore reads raw characters and has no code span or fence exemption, so the tokenization does not matter. It allows an `@` only after a letter or digit, or after an odd number of backslashes. The remaining assumption is that no Markdown construct starts a text token right after a letter or digit that precedes the `@` {R}. It refuses the character references of `@` and also a byte order mark, front matter and a lone carriage return (see [profiles.md](profiles.md), "Instructions"). The resolver checks each file and the joined text. The limitation: `@` in code needs a backslash.
- The generated `CLAUDE.md` starts with a fixed header, and the closure pins the header. The header keeps the first instruction file from becoming front matter.
- `inherit = false` drops the files of the ancestors of the profile only. A profile cannot drop the instructions of an unrelated parent.
- `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD` is global. When it is set, Claude Code also loads CLAUDE.md files from directories that the user adds with their own `--add-dir` {V}. The launcher warns when the passthrough arguments contain `--add-dir`.
- The launcher prunes cache entries that were not used for 30 days, including instructions directories. A session that runs longer than that may lose the directory. A new launch rebuilds it.

**SR5: hardened CI and releases.** The reusable Action is pinned by **full commit SHA**, and the Action embeds the expected SHA-256 of the binary it downloads. Releases carry keyless signatures (cosign) and SLSA provenance on github.com, with offline-verifiable bundles for GHE Server mirrors. The project also has:
- An SBOM, `-trimpath` builds, `govulncheck` and Dependabot.
- Minimal dependencies (standard library plus one TOML library).
- Branch protection and two-maintainer release approval on the public repo.
- An OpenSSF Scorecard.

Org data repo workflows (the starter template applies these rules, see [catalog-and-org-repo.md](catalog-and-org-repo.md), "CI workflows"):
- Use `permissions: {}` at the top level and grant permissions per job (`contents: write` only for tagging, and `pages` and `id-token` only for the catalog).
- Keep secrets (for example an Analytics API key) in a protected environment deployable only from `main`. Do not send secrets to fork pull requests.
- Never interpolate `${{ }}` values from plugin names, versions, descriptions or sidecars into shell. Pass them through `env:` and validate them (for example against semver).
- Require code-owner review and at least two approvals through a ruleset, and dismiss stale reviews.
- Add `/.github/` to `CODEOWNERS`.
- Do not publish PR preview artifacts. They are built from untrusted content.

The catalog site renders sidecar and marketplace text with `textContent` and a strict CSP. It renders Markdown with raw HTML off and only `http` and `https` links. The publish step fails unless Pages visibility is private or internal.

**Updates (D-40).** The tool never contacts the network on its own. `ccshelf update` is explicit, and the automatic check (`[update] mode`) is off by default and opt-in. An update:
- Is downloaded over HTTPS without credentials.
- Must match the SHA-256 in `checksums.txt` of the same release.
- Must also pass `cosign verify-blob` against the release workflow's exact identity when cosign is installed (`--require-signature` makes cosign mandatory).
- Is extracted as the single expected entry into a private file next to the binary.
- Is run once with `version --json` before it replaces anything.
- Never replaces a binary a package manager owns.

The automatic `install` mode is limited to the same major version, stable releases and never a downgrade. See [update.md](update.md).

**Smaller items to track:**
- Trust fatigue (show a risk-only summary).
- `ccshelf init --org <url>` must only write source entries and never trust anything.
- An account must be a name, never a path.
- Employer approval before the repo goes public.
- Outside contributors' code running in corporate CI is why SHA pinning is mandatory.

## Policy spectrum and open source (R3, R4)
Decided 2026-10-06. The user's org **does enforce managed settings** (exact keys not yet known), and the tool must also work with no policy and with partial policy. It will be used inside the org and released as **open source**.

**R3: the launcher is capability-driven, not tier-driven.** Instead of hard-coding "strict" and "loose" modes, it probes each capability and degrades per feature:

| Capability | Needed for | If blocked |
|---|---|---|
| `--settings` masking (`enabledPlugins`, `skillOverrides`, env) | plugin/skill filtering | Core feature. If even this fails, refuse and explain. |
| `disableClaudeAiConnectors` and `deniedMcpServers` in the generated settings (valid in any settings file) | hiding claude.ai connectors and MCP servers | Not sideload flags, so they work under `disableSideloadFlags`. `deniedMcpServers` needs full server names (for example `plugin:context7:context7`, `claude.ai Shopify`). Short names do not match. If policy rejects a key, warn that those servers stay active. |
| `--mcp-config` (optionally with `--strict-mcp-config`) | adding servers the profile defines | Skip adding them and warn. (`--strict-mcp-config` alone, without `--mcp-config`, also yields zero MCP servers.) |
| `--plugin-dir` / `CLAUDE_CODE_PLUGIN_DIRS` | session-only plugins (none are generated by default, per the standalone-skills decision) | Skip those plugins and suggest packaging them in the marketplace. |
| `--agents` | profile-defined subagents | Skip. |
| `--setting-sources` | dropping the user layer | Fall back to per-key masking (`inherit_user_settings = true` behavior). |
| Force-enabled plugins (managed `enabledPlugins: true`) | masking | Can't be masked. List them in `show`/`doctor` as "always on by policy". |

- Each profile feature maps to a required capability, and `[policy] on_blocked` (`warn` or `fail`) decides what happens.
- **Detection:** read managed-settings sources per OS as best-effort: files, and the Windows registry policy location documented for Claude Code. Also read the "required by your org" marker that `claude plugin list --json` reports for force-enabled plugins. Detecting a blocked flag by its exit code is **not feasible**. The launcher runs `claude` with inherited stdio and cannot read its error, exit 1 is ambiguous, and a `-p` probe costs roughly 22-28k tokens. Where a probe is unavoidable, cache the result per Claude Code version. The core path needs no sideload flags, so most of the matrix never has to be probed. `ccshelf doctor --policy` prints the capability matrix (available / blocked / unknown). "Unknown" is a valid state, since server-managed settings can't be read reliably.
- **Never bypass policy.** This holds in every mode, including open-source use.
- **Cases to test:** no policy, permissive policy (e.g. only `strictKnownMarketplaces`), sideload blocked, force-enabled plugins, and both. Each needs a fixture (a fake managed-settings file plus the fake `claude` that mimics the exit-1 behavior).

# Security and policy

Security requirements SR1 to SR5 (added after the security review) and the managed-policy model (R3).

## Security requirements (SR1 to SR5)
Added 2026-10-06 after the security review. The verdict of the review was that the trust model was "not acceptable as written". SR1 to SR3 are required before a first release. SR4 and SR5 are required before anyone runs the tool in corporate CI. A written threat model and a `SECURITY.md` are also required before the repo goes public. I verified the settings-file claim underneath SR1 (see [stage0.md](../research/stage0.md)): a `--settings` file can switch a session to `bypassPermissions`.

**SR1: closed profile schema.** The generator writes only an allowlist of settings keys: `enabledPlugins`, `skillOverrides`, `disableClaudeAiConnectors`, `deniedMcpServers`, `model` and env names that pass the env allowlist (`internal/envpolicy`). The env allowlist accepts **only** these names:
- Names in the `CCSHELF_VAR_<NAME>` namespace.
- Names ending in `_REF`.
- `CCSHELF_PROFILE`.

As defense in depth, the env policy first applies a denylist of known-dangerous prefixes such as `ANTHROPIC_`, `NODE_`, `PYTHON`, `JAVA_`, `LD_`, suffixes such as `_OPTIONS`, and exact names. The launcher passes `effort` as a command-line flag, not as a settings key. A profile can never write `permissions`, `hooks`, `apiKeyHelper`, `*McpServers` allow lists or `disableAllHooks`. Otherwise a profile's env alone could redirect every prompt and file to another endpoint or run code at process start. MCP server definitions are not part of a profile: a profile names servers, and the definitions live in a reviewed registry in the org data repo. A golden test proves that no profile, however crafted, produces a key outside the allowlist.

**SR2: trust the resolved closure, pinned by commit SHA.**
- Every non-personal source needs explicit trust. Project sources (a `.ccshelf/` folder in a cloned repo) are **off by default**, and the user trusts them per repo (path plus hash, like direnv `allow`). A project source cannot shadow a name from another source (a collision is an error), and it can never define MCP commands, env or prompt text. Otherwise a malicious repository could run code through the launcher while skipping Claude Code's own workspace trust and per-server MCP approval.
- The lockfile hashes the **fully resolved closure**: the profile, its `extends` parents, the MCP registry entries it references, the prompt file bytes and the source commit SHA. A change to `mcp/registry.toml` that alters what `figma` runs changes the hash. For a closure that includes a non-personal source, every change needs trust again, including identity-only edits such as `description` or `model`. A personal-only closure is always trusted. The diff marks risky items and lists them first: profile controls, registry entries, prompt text and plugin includes (plugins carry hooks and MCP). With `[trust] on_change = "fail"` a changed closure exits 4 even on a terminal.
- When trust is granted, ccshelf resolves a tag to a commit SHA and stores it in the lockfile. `run` uses the pinned commit (from the cache, without network, when it is already fetched). Only `ccshelf trust` and `ccshelf ls --refresh` notice a tag that later points elsewhere, and they show it as an untrusted update. Recommend tag-protection rulesets on the org data repo. Once a day, ccshelf prunes cache files older than the retention age. It never prunes the checkouts a lock pins.
- Compile from the same in-memory bytes that were hashed (no re-read), to close the gap between accepting and running.
- Non-interactive and CI runs fail closed. `--yes` never accepts trust (R6).

**SR3: no shadowing, protected controls.** A project profile can never shadow a profile from another source, and two org sources cannot define the same name. A personal profile may shadow an org profile of the same name because the user owns it (the listing marks it "shadows"). The org config (`ccshelf.toml`) declares `[protect] plugins` and `[protect] mcp` (audit, secret-scanning or required servers) that are never masked. ccshelf reads the org config from the pinned tree of a git source, so a git source without one gets a visible warning. The lists are part of the trust closure, so a change to them needs re-trust. If a source is unreachable, ccshelf continues to enforce its last-known lists from the lockfile for every profile. A broken org config stops the command. `inherit_user_settings = false` drops the user settings layer that normally enables plugins. With it, the generator writes protected and policy-locked plugins as enabled, never off. Shared profiles cannot set `inherit_user_settings = false`, because that drops the user's deny rules, hooks and MCP. Personal profiles can set it, with a warning that lists what is dropped. Only managed settings with `allowManagedHooksOnly`, `allowManagedPermissionRulesOnly` and `disableBypassPermissionsMode` are real enforcement. The docs must say so.

**SR4: private, verified local artifacts.**
- Cache directory mode 0700 (owner-only ACL on Windows) and files 0600, created with exclusive-create and no-follow.
- Re-hash a content-addressed file before reuse.
- Refuse a cache directory owned by another user or reached through a symlink.
- Never write resolved secrets to disk or argv. Prefer `--append-system-prompt-file`, which keeps prompt text off the process list.
- Redact values (key names only) in `dry-run`, `show` and `doctor` output. People paste this output into issues.
- Confine every path a profile names (prompt files, parents) to its source root, with no `..` and no symlinks.

**SR5: hardened CI and releases.** The reusable Action is pinned by **full commit SHA**, and the Action embeds the expected SHA-256 of the binary it downloads. Releases carry keyless signatures (cosign) and SLSA provenance on github.com, with offline-verifiable bundles for GHE Server mirrors. The project also has:
- An SBOM, `-trimpath` builds, `govulncheck` and Dependabot.
- Minimal dependencies (standard library plus one TOML library).
- Branch protection and two-maintainer release approval on the public repo.
- An OpenSSF Scorecard.

Org data repo workflows:
- Use `permissions: {}` at the top level and grant permissions per job (`contents: write` only for tagging, and `pages` and `id-token` only for the catalog).
- Keep secrets in a protected environment deployable only from `main`.
- Pass values through `env:` and validate them (for example against semver) instead of interpolating `${{ }}` into shell.
- Do not send secrets to fork pull requests.

The catalog site uses `textContent` and a strict CSP. It renders Markdown with raw HTML off and only `http` and `https` links. The publish step fails unless Pages visibility is private or internal. Add `/.github/` to `CODEOWNERS`.

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
- Employer approval and `SECURITY.md` before the repo goes public.
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

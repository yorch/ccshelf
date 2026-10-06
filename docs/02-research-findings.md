# 02. Research findings

Labels: **V** verified in docs/gh/CLI, **R** reported by subagent, **U** unverified.

## A. Is profiles a real problem?

### Evidence (anthropics/claude-code, gh data live)
| Issue | Ask | Status |
|---|---|---|
| #14882 | Skills consume full token count at startup | OPEN, 20 thumbs, 20 comments |
| #26352 | `--enable-plugin/--disable-plugin` flags | closed not_planned (stale bot) |
| #43928 | Enable/disable individual skills (7 thumbs) | closed not_planned (stale bot) |
| #14843 | Bulk plugin enable/disable | closed not_planned (stale) |
| #11596, #62174 | Per-project plugin enable | closed not_planned (#11461 closed "completed"; contradictory) |
| #91770 | Profiles within one account | OPEN, 1 thumb |
| #92645 | Machine-wide plugin scope; author runs 8 `CLAUDE_CONFIG_DIR` profiles, symlinking `plugins/` triggers "corrupted" warnings (#82272, #85325) | OPEN |
| #90818 | VSCode per-session settings profiles | OPEN |

- The closures are by the stale bot, not a human reply; no roadmap statement was found. They show nobody triaged them, not that the need is unmet. (R)
- No Reddit/HN evidence found (absence is not proof).

### Token-cost argument is weaker than first thought (adversary, V against docs)
- Skill listing is capped at **1% of the context window** (~2k tokens on 200k, ~10k on 1M).
- The 42k-token / 189-skill report in #14882 predates v2.1.196; docs say earlier `/context` counted full description text and could show values several times the budget. Likely a display artifact.
- MCP tool search is on by default: only tool names cost context up front.
- What remains is **routing quality** (when the listing overflows, descriptions of rarely used skills get dropped so matching degrades) plus clutter: plugin hooks, agent descriptions, MCP startup, tool-name noise. **Pitch should lead with routing and clutter, not tokens.**

## B. Native mechanisms (profiles)
- Settings precedence: managed > CLI `--settings` > local project > shared project > user. Keys merge; arrays append, objects nest. (V)
- `enabledPlugins` is valid in any settings file, including a `--settings` file. Plugins install globally but load only where enabled. (V)
- `--settings`, `--setting-sources user,project,local` (user-facing, V), `--plugin-dir` (repeatable; folder of plugins v2.1.265+), `CLAUDE_CODE_PLUGIN_DIRS` (v2.1.280+), `--mcp-config` + `--strict-mcp-config`, `--agents`, `--disable-slash-commands`, `--safe-mode`. (V)
- `--bare` skips hooks, skills, plugins, MCP, memory, CLAUDE.md and **never reads OAuth/keychain** (API key only), so unusable for subscription users. (V)
- `skillOverrides` per skill: `on | name-only | user-invocable-only | off`. **Does not apply to plugin skills**; those are controlled per whole plugin via `enabledPlugins`. #54996 (override didn't free context) is closed as fixed. (V)
- `disable-model-invocation: true` removes a skill description from context. Skill text capped 1,536 chars; tune with `skillListingBudgetFraction`. (V)
- `CLAUDE_CONFIG_DIR` relocates settings, `.claude.json`, plugins, skills, agents, history, credentials. **Keychain entry is keyed per config dir** (separate login per profile), correcting an earlier claim that auth is shared. Plugin caches are duplicated, no dedupe. (V)
- Concurrent sessions are an expected case (`~/.claude/sessions/` has one file per running session). Atomicity of `~/.claude.json` writes not verified. (V/U)
- Native gaps: no named profiles, no per-session toggle by plugin name, no default-deny ("only these plugins"), no allowlist mode for `~/.claude/skills`, no inheritance, no active-profile indicator.

## C. Existing profile tools (gh stars/push dates verified)
| Tool | Approach | Notes |
|---|---|---|
| quinnjr/claude-code-profiles (96★) | Full config dir per profile | Account-oriented; duplicates plugins/skills |
| spences10/mcpick (94★) | TUI toggling MCP servers + plugins, saved profiles | Likely edits shared config, so probably not concurrent-safe (U) |
| henkisdabro MCP selector (8★), guibes/claude-profile-switch (6★, stale), ukogan/claude-account-switcher (2★), claude-profile (Go), Claude Switch | Config-dir/symlink model | Account-oriented |
| MetaMCP (2.7k★) | MCP gateway | MCP only. MCP Router discontinued 2026-09-18 |
| Hand-rolled gists/blogs | `CLAUDE_CONFIG_DIR` aliases + symlinks | Same plugin-store sharing problems |

**Nothing found** that defines task profiles spanning plugins+skills+MCP and launches per terminal.

## D. Discoverability evidence
- **#35319** (43 thumbs, closed): an org went from 67 to 183 skills in under a month, citing bloat/redundancy. Anthropic closed it pointing to OTel `claude_code.skill_activated` (needs `OTEL_LOG_TOOL_DETAILS=1`): telemetry, not a catalog.
- **#9716** (75 thumbs, open): Claude unaware of available skills.
- **#86098** (closed 2026-09-20): overlapping plugins cause persistent suggestion noise; Anthropic shipped plugin relevance suggestions.
- No issue found asking for plugin search/tags/enterprise catalog UI (search shallow; U).
- Practitioner posts (Thoughtworks Radar "Trial", DEV.to, mpt.solutions) treat the marketplace as a governance layer (`strictKnownMarketplaces`, SHA pinning). One commenter: listings need rights/cost/verification metadata or shadow IT returns.

## E. Native discovery features (V against docs, https://code.claude.com/docs/en/plugins/*.md)
- marketplace.json entries: `name, source, description, category (free-form), tags (free-form), version, strict, relevance, dependencies, defaultEnabled, displayName, metadata`. `metadata` is free-form and **not read by Claude Code** (v2.1.222+). Unknown keys only warn in `validate`.
- Also accepted: `author, homepage, repository, license, keywords`. **No deprecation/maintenance-status field.**
- **Bundles**: a manifest with just `name` + `dependencies` installs the whole set ("Bundle plugins for a team"); admin can force it via managed `enabledPlugins`. Tags `<name>--v<version>`; cross-marketplace via `allowCrossMarketplaceDependenciesOn`. Unverified: whether disabling a bundle disables its dependencies (probably not).
- **Relevance signals**: `relevance{topic, signals{cwd, cli, hosts, filesRead, manifestDeps}}`; needs `pluginSuggestionMarketplaces`. Suggestion at most once per 3 sessions; notification max 2 times; Discover pin once per machine; only while the plugin is not installed.
- `/plugin` Discover: "type to search"; no faceted filter, no new/trending badge; cost and last-updated only for the official marketplace. Tag/category searchability in the UI unverified.
- CLI: `claude plugin list --json --available` gives `pluginId, name, marketplaceName, source, description, version, installCount`; **no tags/category/metadata**; no search subcommand.
- `/skill-doctor` (v2.1.252+, needs feature-flag fetch): per-skill context cost and unused skills; with `-p` prints text. A "Not used recently" rule in `/plugin` is suppressed under `strictKnownMarketplaces`.
- Telemetry: OTel `plugin_installed`, `plugin_loaded`, `skill_activated`; org-marketplace names also redacted unless `OTEL_LOG_TOOL_DETAILS=1`. Enterprise-plan Analytics API `GET /v1/organizations/analytics/plugins` (per-plugin/day, group by user/RBAC group).
- **Managed-policy constraints that bite a launcher**: `disableSideloadFlags` rejects `--plugin-dir`, `--plugin-url`, `--agents`, non-SDK `--mcp-config` and `CLAUDE_CODE_PLUGIN_DIRS` (Claude Code exits 1). Managed `enabledPlugins: true` can't be overridden; `false` blocks and hides at every scope. `strictKnownMarketplaces` also stops `skills-dir` plugins unless `{ "source": "skills-dir" }` is allowed.

## F. Existing discoverability tools
- Public: skills.sh / `npx skills` (vercel-labs/skills, 33k★), claudemarketplaces.com and other aggregators, official community mirror (4.5k★), awesome lists. Public only; unaware of a private marketplace.
- Closest to an org catalog: **iflytek/skillhub** (5.1k★), self-hosted registry with RBAC/versioning/audit; separate server, not git-marketplace-native.
- Skill routers: sorcerai/skill-router (7★), K-Dense claude-skills-mcp (407★). Search skills, not plugins; no governance/profiles.
- Backstage: no Claude plugin catalog exists (possible later integration).
- **Nothing found** combining catalog + search + recommendation + bundles for a git-based `marketplace.json` (search not exhaustive).

## G. Corrections made along the way
- Keychain: per-config-dir, not shared. (V)
- `--setting-sources` is public; `--bare` semantics as above. (V)
- `/skill-doctor` does have text output with `-p`; "7-day usage" not in docs. (V)
- "No ownership fields" is overstated (`author`, `repository`, etc. exist); only deprecation/status is missing. (V)
- `disableSideloadFlags` is broader than first reported. (V)

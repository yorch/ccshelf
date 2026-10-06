# 02. Research findings

Labels: **V** verified in docs/gh/CLI, **R** reported by subagent, **U** unverified.

## A. Is profiles a real problem?

### Evidence (anthropics/claude-code, gh data live)
| Issue | Ask | Status |
|---|---|---|
| #14882 | Skills consume full token count at startup | OPEN, 20 thumbs, 20 comments |
| #26352 | `--enable-plugin/--disable-plugin` flags | closed not_planned (stale bot), 1 thumb |
| #43928 | Enable/disable individual skills (7 thumbs) | closed not_planned (stale bot) |
| #14843 | Bulk plugin enable/disable | closed not_planned (stale), 5 thumbs |
| #11596, #62174 | Per-project plugin enable | closed not_planned, 0 thumbs each (#11461, 7 thumbs, closed "completed"; contradictory) |
| #91770 | Profiles within one account: separate history, memory and sign-in for client machines (an account/identity request, **not** task-scoped plugin sets) | OPEN, 1 thumb, 1 comment |
| #92645 | Machine-wide plugin scope; author runs 8 `CLAUDE_CONFIG_DIR` profiles, symlinking `plugins/` triggers "corrupted" warnings (#82272, #85325) | OPEN |
| #90818 | VSCode per-session settings profiles | OPEN, 0 thumbs |

- The closures are by the stale bot, not a human reply; no roadmap statement was found. They show nobody triaged them, not that the need is unmet. (R)
- No Reddit/HN evidence found (absence is not proof).

### Token-cost argument is weaker than first thought (adversary, V against docs)
- Skill listing is capped at **1% of the context window** (~2k tokens on 200k, ~10k on 1M).
- The 42k-token / 189-skill report in #14882 predates v2.1.196; docs say earlier `/context` counted full description text and could show values several times the budget. Likely a display artifact.
- MCP tool search is on by default: only tool names cost context up front.
- What remains is **routing quality** (when the listing overflows, descriptions of rarely used skills get dropped so matching degrades) plus clutter: plugin hooks, agent descriptions, MCP startup, tool-name noise. **Pitch should lead with routing and clutter, not tokens.** Caveat (U): the routing-quality benefit of profiles is an inference and was not measured; Stage 0 only measured token effects (small).

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

## C. Existing profile tools
Stars, language, license and last push are from `gh` on 2026-10-06 (verified). The "Approach" text comes from each project's README or description; anything I did not check is marked unverified.

| Tool | Lang, license | Stars | Last push | Approach | Notes and limits |
|---|---|---|---|---|---|
| [fuzzyalej/claude-profile](https://github.com/fuzzyalej/claude-profile) | Rust, MIT | 10 | 2026-09-28 | Task profiles: a small JSON file lists exactly the plugins, skills, marketplaces and MCP servers a session loads; `claude-profile <name>`; profiles can be combined; shared profile repos installed with `install` and pinned by a lockfile; plugins and skills are vendored into the profile's own directory; never writes to the real `~/.claude` | **Closest to our launcher**: same goal (focused sessions, many terminals at once, shareable profiles, macOS/Linux/Windows, Homebrew/Cargo/shell/PowerShell installers). Different mechanism (vendoring, not masking). Not checked: how it handles auth, `CLAUDE_CONFIG_DIR`, managed policy, or the catalog problem. Same name as our current working name. |
| [edimuj/claude-rig](https://github.com/edimuj/claude-rig) | Go (stdlib only), MIT | 9 | 2026-06-08 | Isolated "rigs" via `CLAUDE_CONFIG_DIR` and `--add-dir`: separate settings, skills, plugins, agents, hooks, MCP and instructions; choose per rig what is isolated, inherited or shared; auth shared or separate; per-project auto-selection; status, diff, export and import | Overlaps on isolation and concurrency; also covers multiple accounts. Config-dir model, so separate plugin caches. Last push is four months old. |
| [agh/cwtch](https://github.com/agh/cwtch) | Shell, MIT | 15 | 2026-09-14 | Named credentials (setup-token or API-key profiles) plus syncing selected user configuration from Git repos (a `Cwtchfile`) | macOS only; needs `jq` and `yq`; account-oriented. Relevant for the "sync config from git" idea. |
| [diranged/claude-profile](https://github.com/diranged/claude-profile) | Go, Apache-2.0 | 9 | 2026-10-05 | Wrapper that sets `CLAUDE_CONFIG_DIR` per profile, one keychain entry per profile; binaries for Linux, macOS and Windows (amd64, arm64) | Account-oriented. **Same name as our working name** (`claude-profile`). See the blog post [Managing multiple Claude Code profiles](https://blog.wiredgeek.net/tools/claude-code/2026/04/06/managing-multiple-claude-code-profiles.html). |
| [JakubKontra/claude-profile-manager](https://github.com/JakubKontra/claude-profile-manager) | Go, MIT | 22 | 2026-09-25 | Multiple Claude Code accounts side by side with isolated credentials | Account-oriented. |
| [quinnjr/claude-code-profiles](https://github.com/quinnjr/claude-code-profiles) | Shell, MIT | 96 | 2026-09-18 | Full config dir per profile (work, personal, MCP setups) | Account-oriented; duplicates plugins and skills. |
| [spences10/mcpick](https://github.com/spences10/mcpick) ([post](https://scottspence.com/posts/mcpick-manage-mcp-servers-and-plugins-in-claude-code)) | TypeScript, MIT | 94 | 2026-10-06 | Now described as a vendor-neutral MCP configuration manager: add, toggle and audit MCP servers and skills across clients; saved profiles | Probably edits shared config, so likely not concurrent-safe (unverified). |
| [yarikleto/claude-profile](https://github.com/yarikleto/claude-profile) | Shell, MIT | 11 | 2026-09-25 | Global config profiles | Same name as our working name. |
| [julianleopold/claude-profiles](https://github.com/julianleopold/claude-profiles) | TypeScript, MIT | 8 | 2026-06-30 | Swap settings, hooks, MCP servers and commands between configurations | Switching, not concurrent. |
| [henkisdabro/Claude-Code-MCP-Server-Selector](https://github.com/henkisdabro/Claude-Code-MCP-Server-Selector) | TypeScript, MIT | 8 | 2026-09-19 | TUI that enables only the MCP servers you need | MCP only. |
| [guibes/claude-profile-switch](https://github.com/guibes/claude-profile-switch) | Shell, MIT | 6 | 2026-04-21 | Isolated profiles via `CLAUDE_CONFIG_DIR` | Small, stale. |
| [ukogan/claude-account-switcher](https://github.com/ukogan/claude-account-switcher) | Shell, MIT | 2 | 2026-09-15 | Isolated dir per account with symlinked settings | Account-oriented. |
| [Claude Switch](https://claudeswitch.dev/) | Not checked | n/a | n/a | Shared skills, settings and CLAUDE.md via symlinks, isolated auth | Account-oriented (unverified details). |
| [MetaMCP](https://github.com/metatool-ai/metamcp) | TypeScript, MIT | 2,693 | 2026-06-22 | MCP aggregator and gateway in Docker | MCP only. |
| [MCP Router](https://github.com/mcp-router/mcp-router) | TypeScript | 2,145 | 2026-09-18 | MCP gateway | **Archived**: development and support ended 2026-09-18. |
| Hand-rolled guides: [gist](https://gist.github.com/jamesfishwick/abb5c1203c7ba6140eaf5bcfbdd98c1c), [wmedia.es](https://wmedia.es/en/tips/claude-code-multiple-profiles-config-dir), [leek.io](https://leek.io/articles/multiple-claude-code-profiles-one-shared-setup) | n/a | n/a | n/a | `CLAUDE_CONFIG_DIR` aliases plus symlinks | Same plugin-store sharing problems (#92645). |

**What changed since the first pass:** the first research said nothing defined task profiles across plugins, skills and MCP and launched them per terminal. That was wrong: `fuzzyalej/claude-profile` does, with a different mechanism, and `edimuj/claude-rig` covers per-project isolation. What none of them appear to cover (not fully verified; I read READMEs only): default-deny masking against a single shared plugin store, capability-driven behavior under managed policy (`disableSideloadFlags`, force-enabled plugins), an org catalog built from a git marketplace, or the data-repo and CODEOWNERS structure. See the open decision in 04 about evaluating these tools before building the launcher.

## D. Discoverability evidence
- **#35319** (43 thumbs, closed): an org went from 67 to 183 skills in under a month, citing bloat/redundancy. Anthropic closed it pointing to OTel `claude_code.skill_activated` (needs `OTEL_LOG_TOOL_DETAILS=1`): telemetry, not a catalog.
- **#9716** (75 thumbs, 69 comments, open): Claude unaware of available skills.
- **#86098** (closed, not planned): a complaint that overlapping plugins keep being suggested indefinitely, because relevance suggestions suppress per plugin. It shows the relevance feature exists and has noise; it is not evidence that Anthropic acted on the request (an earlier version of these notes misread it).
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
Stars and last push from `gh` on 2026-10-06 (verified) unless marked.
- Public directories and registries: [skills.sh / `npx skills`](https://github.com/vercel-labs/skills) (33,243★, MIT, pushed 2026-10-05); the official read-only [community plugin mirror](https://github.com/anthropics/claude-plugins-community) (4,502★); [anthropics/skills](https://github.com/anthropics/skills) (179,862★, public skills); [claudemarketplaces.com](https://claudemarketplaces.com) and other web aggregators (unverified, snippet only). Public only; unaware of a private marketplace.
- Awesome lists: [travisvn/awesome-claude-skills](https://github.com/travisvn/awesome-claude-skills) (15,287★, last push 2026-04-28) and [VoltAgent/awesome-claude-code-subagents](https://github.com/VoltAgent/awesome-claude-code-subagents) (25,527★): manual, no search.
- [davila7/claude-code-templates](https://github.com/davila7/claude-code-templates) (32,417★, MIT, pushed 2026-10-06): a CLI that configures and monitors Claude Code, with a public template catalog.
- Closest to an org catalog: [iflytek/skillhub](https://github.com/iflytek/skillhub) (5,150★, Apache-2.0, Java, pushed 2026-10-01): self-hosted registry with RBAC, versioning and audit; a separate server, not git-marketplace-native. Smaller: [oujingzhou/skillbase](https://github.com/oujingzhou/skillbase) (16★, Go, no license) and [ComeOnOliver/skillshub](https://github.com/ComeOnOliver/skillshub) (65★).
- Skill routers: [sorcerai/skill-router](https://github.com/sorcerai/skill-router) (7★) and [K-Dense-AI/claude-skills-mcp](https://github.com/K-Dense-AI/claude-skills-mcp) (407★). Search skills, not plugins; no governance or profiles.
- [Backstage](https://github.com/backstage/backstage) (34,570★): no Claude plugin catalog exists (possible later integration).
- Practitioner posts: [Thoughtworks Radar, Claude Code plugin marketplace (Trial)](https://www.thoughtworks.com/en-us/radar/tools/claude-code-plugin-marketplace); [Building an enterprise Claude Code marketplace (DEV.to)](https://dev.to/flavio_sacca_b0ab52158604/building-an-enterprise-claude-code-marketplace-5d3j); [Your Claude plugin marketplace needs more than a git repo](https://www.mpt.solutions/your-claude-plugin-marketplace-needs-more-than-a-git-repo/); [LiteLLM, Claude Code plugin marketplace](https://docs.litellm.ai/docs/tutorials/claude_code_plugin_marketplace) (snippet only).
- **Nothing found** combining catalog + search + recommendation + bundles for a git-based `marketplace.json` (search not exhaustive). The review round also noted that claude.ai has an organization setting (Organization settings > Plugins & skills) that syncs an org marketplace from GitHub, GitLab or GHES; this is reported, not yet checked against the docs.

## G. Corrections made along the way
- Keychain: per-config-dir, not shared. (V)
- `--setting-sources` is public; `--bare` semantics as above. (V)
- `/skill-doctor` does have text output with `-p`; "7-day usage" not in docs. (V)
- "No ownership fields" is overstated (`author`, `repository`, etc. exist); only deprecation/status is missing. (V)
- `disableSideloadFlags` is broader than first reported. (V)

- A settings file can set permissions (including `bypassPermissions`), hooks and env, and an invalid file is ignored silently. **Verified by me** after the review round. (V)
- `disableClaudeAiConnectors` and `deniedMcpServers` are valid in any settings file, so connectors and MCP servers need no sideload flags. (V in docs)
- #91770 is an account request, not task-scoped plugin sets, so it is a weak obsolescence signal; #86098 was misread. (V)
- `--append-system-prompt-file` exists. (V)

## H. Naming and brand findings (2026-10-06)
- **Anthropic's guidance** (verified by an agent against the pages): the [trademark guidelines](https://www.anthropic.com/legal/trademark-guidelines) say its marks may only be used as permitted and not in a way that implies sponsorship or affiliation (questions: marketing@anthropic.com). The [Agent SDK docs](https://code.claude.com/docs/en/agent-sdk/overview) say a product may use "Claude Agent" or "{YourAgentName} Powered by Claude", must not use "Claude Code" or "Claude Code Agent" as its branding, and should not appear to be Claude Code or an Anthropic product. No rule specific to open-source tool names was found.
- **Plugin names:** `claude plugin validate` errors on names that start with `claude-`, `anthropic-` or `cc-plugin-` and warns on "claude" as a whole word elsewhere ([manifest reference](https://code.claude.com/docs/en/plugins/manifest-reference)); marketplace names that impersonate official ones are refused ([marketplace reference](https://code.claude.com/docs/en/plugins/marketplace-reference)). This matters for anything the tool generates.
- **Precedent** (press reports, not an Anthropic statement): in January 2026 Anthropic asked the "Clawdbot" project to rename over similarity to "Claude" ([Laravel News](https://laravel-news.com/index.php/clawdbot-rebrands-to-moltbot-after-trademark-request-from-anthropic)).
- **Collision on GitHub:** at least three tools use the name `claude-profile`: [diranged](https://github.com/diranged/claude-profile) (Go), [fuzzyalej](https://github.com/fuzzyalej/claude-profile) (Rust, same purpose as our launcher) and [yarikleto](https://github.com/yarikleto/claude-profile) (shell), plus many close variants. The name is free on npm, PyPI and Homebrew.
- **Candidate names** that avoid "claude" and were free on GitHub, npm, PyPI, crates.io, Homebrew and Scoop when checked: `kitshelf` (command `kshelf`), `kitstow` (`kstow`), `loadmux` (`lmux`). A second check of `cc`- and `claude`-prefixed names is in progress. Not checked: winget, a real trademark search. The decision (2026-10-06) is `ccshelf`; see 04 "Name" for what was checked and the caveats.

- **Second name check, `cc`- and `claude`-prefixed candidates (2026-10-06, read-only):** by command, free everywhere checked (GitHub account, npm, PyPI, crates.io, Homebrew, Scoop, AUR, `.dev`, `.com`): `ccshelf`, `ccstow`, `ccpreset`, `ccbundle`, `ccwardrobe`, `ccshed`, `ccharbor` (`.com` registered for some); among `claude` names `claudeshelf`, `claudestow`, `claudecatalog`, `claudeprofiles`, `claudebay`, `claudedeck`, `claudedock` (higher trademark exposure). Taken or not worth it: `cckit`, `ccdeck`, `ccmux`, `cclens`, `claudekit`, `claudepack`, `claudeset`, `ccdock`, `ccprof`, `cprof`, `ccfit`, `ccrack`, `ccbench`. **`ccloadout`** and **`claudeloadout`** are free on registries but a tool called Claude Loadout already exists on PyPI (a goal-aware launcher that loads only the MCP servers, plugins and skills each session needs: the same concept). For `ccprofiles`, the check found the GitHub user taken and an unrelated 0-star `CCProfiles` repo; the agent ranked it below `ccshelf`, `ccstow` and `ccpreset` because "profile" names are crowded. `.io` and `.sh` domain results were unreliable. Related existing tools it surfaced: `XBlueSky/cc-loadout` (2★), `O6lvl4/ccp` (symlink overlay), `felipeadeildo/claude-code-profiles`, `hota1024/ccswitch`, and an "ccp" profile-manager skill.

## I. Review round findings relevant to the tools landscape (2026-10-06)
From the product, scope and strategy adversary review (not yet independently checked unless noted):
- The routing-quality benefit that carries the pitch has not been measured; Stage 0 measured tokens only.
- The exit trigger named in 04 (#91770) is about account profiles for one user (1 thumb), not task-scoped plugin sets, so it is a weak signal. Other more likely obsolescence paths: a native allowlist or default-deny `enabledPlugins` mode, smarter skill loading, or a faceted `/plugin` Discover.
- Competing tools exist that the first pass missed (see section C). Their low star counts also suggest modest demand in this category.


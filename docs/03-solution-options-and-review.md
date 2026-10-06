# 03. Solution options and adversarial review

## Profiles: solution space (Opus brainstorm)
| ID | Approach | Verdict |
|---|---|---|
| A / A′ | **Compiling launcher**: profile compiles to a per-session `--settings` file (+ `--mcp-config`, `--plugin-dir`). A′ adds `--setting-sources project,local` to drop the user layer, avoiding reliance on masking with `false`. Nothing shared is written. | **Core** |
| B | One `CLAUDE_CONFIG_DIR` per profile with shared plugin store | Fallback. Separate login per dir, cache duplication, "corrupted" warnings on symlinked `plugins/` |
| C | Per-project `enabledPlugins` + direnv | Default layer only: a profile follows the task, not the repo |
| D | Profile as a plugin (bundle) / managed layer | Packaging; can't subtract globally installed plugins |
| E | MCP gateway with namespaces | MCP only; crowded space; later if ever |
| F | Subagent profiles (`--agent`, `--agents`, scoped skills/mcpServers) | Complement; scopes behavior, not necessarily context cost |
| G | TUI/config manager (mcpick-style) | Concurrency-broken if it edits shared config; fine only as a front-end that writes profile files |
| H | Upstream comment (#14882, #91770) | Parallel track, not a plan |
| I | Profile workspace directories | Experiment; `--add-dir` may not load `.claude/` settings or skills (U) |
| J | Composition/inheritance (`frontend = base + react`) | Resolver feature, orthogonal |
| K | Overlay filesystem sandbox | Reject |

### What aliases already cover
`alias cc-fe='claude --settings fe.json --strict-mcp-config --mcp-config fe.mcp.json'` gives a per-terminal, concurrent, per-session set of plugins and MCPs with no state changes: roughly 80-90% of the need (adversary estimate).

### What is genuinely unserved
1. **Default-deny for plugins**: `enabledPlugins` is a per-name map; newly installed plugins slip through; nothing says "only these".
2. **Default-deny for user skills** (`~/.claude/skills`): no allowlist mode.
3. **A single named, composable profile** with inheritance and an active-profile indicator.
4. **Keeping profiles in sync** after plugin installs.

## Discoverability: solution space
| ID | Architecture | Verdict |
|---|---|---|
| A | One profile manifest in git that compiles to launch files, a bundle meta-plugin and a catalog entry | Brainstorm's pick |
| B | Bundle-native minimalism: profile = bundle plugin; discovery stays native | Small, low risk |
| C | CI static catalog from marketplace.json + `metadata` convention + git data | Core of the catalog |
| D | Concierge skill/MCP ("what should I use for X") | Later; high upstream risk (relevance suggestions + tool search exist) |
| E | OTel telemetry loop (rank/prune/doctor) | Later; needs infra and `OTEL_LOG_TOOL_DETAILS=1` |
| F, G | Config dir per profile; MCP gateway | Reject |

## Adversarial review (Opus): key objections, ranked
1. **The launcher is least useful in locked-down orgs** (`disableSideloadFlags`, managed force-enables), which are the orgs with big marketplaces.
2. The "shared core" is about 200 lines of `marketplace.json` parsing, thin for justifying one product.
3. Below about 50 plugins the catalog duplicates native features, and upstream is closing the gap (relevance, `/skill-doctor`, Stats tab, installCount, Analytics API). Faceted Discover is a plausible next release.
4. Metadata drift and unowned fields: any field without a CI gate will rot.
5. Adoption: users live in `/plugin`; a separate CLI catalog will be ignored (a web page linked from README might not be). Put recommendations into `relevance` blocks.
6. Security: a launcher must detect policy and say "blocked by policy"; it must not route around org controls, especially when the user also governs them.

### Catalog sizing (adversary)
- N=20: README table generated from marketplace.json + CI lint requiring `author/tags/category` is enough.
- N=100: flat search breaks; overlap is real; a static page with facets, diff since last tag, owner, usage counts is worth it.
- N=500: needs a governed registry (required metadata, deprecation workflow, dedup review, usage-driven pruning): skillhub/Backstage territory.
- Threshold for a custom catalog: roughly **50-80 plugins** or more than one publishing team. The user's ~50 sits at the threshold.

## Reconciling "one project"
- Brainstorm: one manifest is the shared core (profile = curated set; catalog entry = curated "what to use for X").
- Adversary: mostly a rationalization; lifecycles differ (local ephemeral per-terminal vs org-published versioned reviewed), trust models conflict, bundles are install-side not per-session subtraction.
- **Resolution adopted**: one repo, independent modules sharing a small `core/`; "profile = bundle" is the bridge (catalog-published bundles become selectable profiles). See 04.

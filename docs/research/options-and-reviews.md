# Options and adversarial reviews

## Profiles: solution space (Opus brainstorm)
IDs are prefixed `P-` here; catalog options below use `C-`. The report uses the same IDs.

| ID | Approach | Concurrent-safe | Effort | Upstream risk | Verdict |
|---|---|---|---|---|---|
| P-A / P-A′ | **Compiling launcher**: profile compiles to a per-session `--settings` file (+ `--mcp-config`, `--plugin-dir`). A′ adds `--setting-sources project,local` to drop the user layer, avoiding reliance on masking with `false`. Nothing shared is written. | Yes | S | Medium | **Core** |
| P-B | One `CLAUDE_CONFIG_DIR` per profile with shared plugin store | Yes | M | High | Fallback. Separate login per dir, cache duplication, "corrupted" warnings on symlinked `plugins/` |
| P-C | Per-project `enabledPlugins` + direnv | Yes | XS | Low | Default layer only: a profile follows the task, not the repo |
| P-D | Profile as a plugin (bundle) / managed layer | Yes | S | Medium | Packaging; can't subtract globally installed plugins |
| P-E | MCP gateway with namespaces | Yes | L | Low | MCP only; crowded space; later if ever |
| P-F | Subagent profiles (`--agent`, `--agents`, scoped skills/mcpServers) | Yes | S | Low | Complement; scopes behavior, not necessarily context cost |
| P-G | TUI/config manager (mcpick-style) | No, if it edits shared config | M | Medium | Fine only as a front-end that writes profile files |
| P-H | Upstream comment (#14882, #91770) | n/a | n/a | n/a | Parallel track, not a plan |
| P-I | Profile workspace directories | Yes | S | Medium | Experiment; `--add-dir` may not load `.claude/` settings or skills {U} |
| P-J | Composition/inheritance (`frontend = base + react`) | n/a | n/a | n/a | Resolver feature, orthogonal |
| P-K | Overlay filesystem sandbox | n/a | Heavy | n/a | Reject |

### What aliases already cover
`alias cc-fe='claude --settings fe.json --strict-mcp-config --mcp-config fe.mcp.json'` gives a per-terminal, concurrent, per-session set of plugins and MCPs with no state changes: roughly 80-90% of the need (adversary estimate).

### What is genuinely unserved
1. **Default-deny for plugins**: `enabledPlugins` is a per-name map; newly installed plugins slip through; nothing says "only these".
2. **Default-deny for user skills** (`~/.claude/skills`): no allowlist mode.
3. **A single named, composable profile** with inheritance and an active-profile indicator.
4. **Keeping profiles in sync** after plugin installs.

## Discoverability: solution space
| ID | Architecture | Effort | Upstream risk | Verdict |
|---|---|---|---|---|
| C-A | One profile manifest in git that compiles to launch files, a bundle meta-plugin and a catalog entry | M | Low to medium | Brainstorm's pick |
| C-B | Bundle-native minimalism: profile = bundle plugin; discovery stays native | S | Low | Small, low risk |
| C-C | CI static catalog from marketplace.json + per-plugin sidecar metadata + git data | S to M | Low | Core of the catalog |
| C-D | Concierge skill/MCP ("what should I use for X") | M | High (relevance suggestions + tool search exist) | Later |
| C-E | OTel telemetry loop (rank/prune/doctor) | M to L | Medium | Later; needs infra and `OTEL_LOG_TOOL_DETAILS=1` |
| C-F | Config dir per profile | n/a | n/a | Reject |
| C-G | MCP gateway | n/a | n/a | Reject |

## Adversarial review (Opus): key objections, ranked
1. **The launcher is least useful in locked-down orgs** (`disableSideloadFlags`, managed force-enables), which are the orgs with big marketplaces.
2. The "shared core" is about 200 lines of `marketplace.json` parsing, thin for justifying one product.
3. Below about 50 plugins the catalog duplicates native features, and upstream is closing the gap (relevance, `/skill-doctor`, Stats tab, installCount, Analytics API). Faceted Discover is a plausible next release.
4. Metadata drift and unowned fields: any field without a CI gate will rot.
5. Adoption: users live in `/plugin`; a separate CLI catalog will be ignored (a web page linked from README might not be). Put recommendations into `relevance` blocks.
6. Security: a launcher must detect policy and say "blocked by policy"; it must not route around org controls, especially when the user also governs them.

### Catalog sizing (adversary)

<!-- widget: catalog-size -->
- N=20: README table generated from marketplace.json + CI lint requiring `author/tags/category` is enough.
- N=100: flat search breaks; overlap is real; a static page with facets, diff since last tag, owner, usage counts is worth it.
- N=500: needs a governed registry (required metadata, deprecation workflow, dedup review, usage-driven pruning): skillhub/Backstage territory.
- Threshold for a custom catalog: roughly **50-80 plugins** or more than one publishing team. The user's ~50 sits at the threshold.
- The report's size slider turns these reference points into bands: up to about 30 plugins native features are enough; about 31 to 79 is the threshold (convention, lint and a generated page pay off); about 80 to 299 a custom catalog is worth it (facets, overlap view, new-since diff, usage); 300 and above needs a governed registry.

## Reconciling "one project"
- Brainstorm: one manifest is the shared core (a profile is a curated set; a catalog entry is a curated "what to use for X").
- Adversary: mostly a rationalization; lifecycles differ (local ephemeral per-terminal vs org-published versioned reviewed), trust models conflict, bundles are install-side not per-session subtraction.
- **Resolution adopted**: one repo, independent modules sharing a small `core/`; "profile = bundle" is the bridge (catalog-published bundles become selectable profiles). See 04.

## Review round (2026-10-06): four adversarial reviews
Four independent Opus reviews attacked the whole plan: product and scope, technical design, security and trust, and the org repo, catalog and operability. Outcome:
- **Verdict:** the core mechanism (a generated `--settings` file that masks plugins) is sound. The plan was wider than its evidence, had real security gaps, and several recorded facts were wrong. The product reviewer put the chance that the plan should proceed unchanged at about 10%.
- **Facts corrected** (details and verification status in [stage0.md](stage0.md)): a settings file can set `bypassPermissions`, hooks and env (verified); an invalid settings file is ignored silently (verified); connectors and MCP servers can be hidden through settings keys, so the core path needs no sideload flags; `--append-system-prompt-file` exists (verified); #91770 is an account request and #86098 was misread (verified); use `exec` on Unix; clean the cache by age; drop exit-code policy probing; bundles and masking can conflict (untested).
- **Where the reviewers converged:** scope exceeds evidence; the routing benefit is unmeasured; several "decided" items rest on unverified claims; two sources of truth in the docs; the sidecar rationale does not hold as stated.
- **Tensions, and the resolution adopted:** product wanted trust, git sources and open source cut while security called the trust model unacceptable, so shrink the surface (closed schema, `dir` sources only for the MVP, no implicit project profiles) and the trust problem mostly disappears; product wanted one track while technical and ops wanted slices of both, so sequence them behind evidence checks; product wanted internal-first while the notes decided open source, so keep open source as the direction and gate the scaffolding on employer approval.
- **Outcome:** direction changed to evidence first, then trimmed scope (see [roadmap.md](../design/roadmap.md)); security requirements SR1 to SR5 added ([security.md](../design/security.md)); decisions reopened ([DECISIONS.md](../DECISIONS.md)).


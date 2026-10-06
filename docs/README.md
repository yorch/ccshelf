# claude-profile: research & design notes

Status: research and Stage 0 experiments complete (2026-10-06). Masking via `--settings` confirmed, concurrency looked safe, token savings small on the test machine (see `05-stage0-results.md`). No code yet. `report.html` is the interactive version of these notes and must say the same thing (see "Keeping the report in sync" in `AGENTS.md`).

| File | What it holds |
|---|---|
| [01-context-and-problem.md](01-context-and-problem.md) | The two use cases, user context, how the research was run |
| [02-research-findings.md](02-research-findings.md) | Evidence of the problems, native Claude Code mechanisms, existing tools |
| [03-solution-options-and-review.md](03-solution-options-and-review.md) | Solution space (brainstorm) and the adversarial review, conflicts resolved |
| [04-recommendation-and-roadmap.md](04-recommendation-and-roadmap.md) | Recommended architecture, requirements, staged roadmap, manifest + CLI sketch, open decisions |
| [05-stage0-results.md](05-stage0-results.md) | Empirical tests of the launcher assumptions |
| [06-example-workflows.md](06-example-workflows.md) | Example workflows showing how profiles and the catalog would be used (mockups) |
| [07-how-it-invokes-claude.md](07-how-it-invokes-claude.md) | How the launcher invokes `claude`, what is shared between profiles, combining profiles with accounts |
| [08-org-data-repo-structure.md](08-org-data-repo-structure.md) | How an adopting org's private repo should be structured (layout, sidecar metadata, CODEOWNERS, CI) |
| [report.html](report.html) | Interactive single-file version of all of the above |

## Glossary
These terms are used the same way in every document and in the report.

| Term | Meaning |
|---|---|
| **Profile** | A named recipe (`profiles/<name>.toml`) saying which plugins, standalone skills and MCP servers are active for one Claude Code session, plus session defaults. Started with `cprof run <name>`. It filters what is already installed; it is not an identity or security boundary. Part of the **launcher** (use case 1). |
| **Profile bundle** | A generated, dependency-only plugin named `profile-<name>`, kept in `bundles/`, so a profile's plugins can be installed natively with one `/plugin install`. It is not itself a profile. |
| **Catalog** | A generated, browsable index of an org's plugins and profiles: a static site plus `catalog.json`, built in CI from `marketplace.json`, the sidecars and git data. Also queried locally with `cprof search` and `cprof doctor`. Not committed. Answers "what exists, which should I use, who owns it". Part of use case 2 (discoverability). |
| **Sidecar** | The per-plugin catalog metadata file `catalog/plugins/<name>.toml` (owner, status, when_to_use, overlaps, review date). |
| **Marketplace** | The Claude Code concept: a git repo with `.claude-plugin/marketplace.json` and plugins that Claude Code installs from. Not the same as the catalog, which is a richer view built on top of it. |
| **Tool repo** | This public repo: the Go source, schemas, docs, releases, the reusable Action and a starter template. Never holds org data. |
| **Org data repo** | An adopting org's private repo (GHE Cloud or Server) holding its marketplace, plugins, profiles, sidecars, org config and CI. Also called the "profile catalog repo" in conversation. |
| **Account** | A Claude Code identity and data directory (a separate `CLAUDE_CONFIG_DIR`). An independent axis from profiles: a profile chooses what is active, an account chooses who you are. |
| **Profile source** | Where profile files come from: `dir`, `git` (both first release) and later `plugin`. Config key `[[sources]]` in the user's `config.toml`. |
| **Masking** | Setting `enabledPlugins: false` (per plugin) in a generated `--settings` file for every installed plugin the profile doesn't list. |
| **Capability** | One thing the launcher needs from Claude Code (settings masking, strict MCP config, `--plugin-dir`, ...). It probes each one and degrades per feature when managed policy blocks it. |
| **Launcher, `core`, catalog (modules)** | The three Go packages in the tool repo: `profiles/` (launcher), `core/` (shared parsing and resolution) and `catalog/`. |

## How to read the confidence labels
- **Verified** (●): confirmed in official docs (code.claude.com/docs), via `gh`, or by running the local CLI.
- **Reported** (◐): stated by a research subagent and not independently re-checked. This includes the Stage 0 experiments, which a subagent ran and reported with raw outputs kept; they are labeled "reported" for that reason.
- **Unverified** (○): snippet-only, or inferred.
- `02` uses V, R and U; `05` uses Confirmed, Partly and No issue seen with the same meaning; `04` to `08` state "tested", "untested" or "unverified" in prose; `report.html` uses the ●, ◐ and ○ markers. The report's loadout demo on the Overview uses fictional plugin names and shows design intent; only the masking mechanism itself was tested.

## Requirements
- **R1:** macOS, Linux and native Windows; six targets: darwin/arm64, darwin/amd64, linux/amd64, linux/arm64, windows/amd64, windows/arm64 (WSL counts as Linux). Design assessment in 04; only macOS tested.
- **R2:** GitHub.com, GHE Cloud and GHE Server, with GitHub Actions; no hard-coded hosts (decided 2026-10-06; untested).
- **R3:** works with no, partial and strict managed policy; capability-driven; never bypasses policy.
- **R4:** open source (MIT); no org-specific assumptions in code, schemas, defaults or examples.
- **R5:** the tool lives in a public GitHub repo; adopting companies keep profiles and catalog data in their own private GHE repo.

## Decisions so far (all 2026-10-06; details in 04)
| Decision | Result |
|---|---|
| Managed policy | The user's org enforces it; the tool works across none, partial and strict policy |
| Build order | Both tracks in parallel, shared `core/` first |
| Language | Go |
| Name | Project `claude-profile`, command `cprof` was chosen, but it is **open again** (three existing tools share the name; see `02` section H and `04` open decisions) |
| Catalog hosting | The adopter's choice; the tool outputs a static directory |
| Profile sources | `dir` and `git` first, `plugin` later |
| Standalone skills | Explicit off-list via `skillOverrides`, plus guidance to package skills as plugins |
| Metadata ownership | Hybrid: authors write, the platform team reviews |
| Metadata home | One sidecar file per plugin (single-file mode for small registries) |
| Generated bundles | Committed in `bundles/`, checked for drift in CI |
| Shared state across profiles | Auth, history and memory shared; profiles combine with accounts via `CLAUDE_CONFIG_DIR` |
| Targets | All six listed in R1 |
| License | MIT (LICENSE file still to add; confirm employer approval first) |

## Versions
Research ran against Claude Code 2.1.290. The local CLI reported 2.1.291 when Stage 0 ran, probably because it auto-updated in between. Claude Code changes fast; re-verify flag and field behavior before relying on it.

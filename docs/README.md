# ccshelf: research and design notes

**Status (2026-10-06):** implemented (launcher, catalog, Action, starter template, CI and release workflows) and reviewed in several adversarial rounds. Not released. Known gaps:

- Only macOS has run real Stage 0 experiments.
- The Windows code paths compile and vet but have not run on Windows.
- The Phase 0 evidence items are still open.

See the [roadmap](design/roadmap.md) and the [decision log](DECISIONS.md) (D-22).

`ccshelf` is an open-source Go tool for Claude Code with two parts:

- a **launcher** that starts `claude` with a named profile of plugins, skills and MCP servers per terminal.
- a **catalog** that lints and publishes an org's plugin marketplace.

The interactive report, [report.html](report.html), is **generated from these files** by `build_report.py`. Never edit it by hand.

## Where things are

| File | What it holds |
|---|---|
| [DECISIONS.md](DECISIONS.md) | The decision log: what was decided, why, with what confidence, when to revisit. Also superseded decisions and open questions |
| [design/architecture.md](design/architecture.md) | Code layout of the tool repo, design principles, the Go stack |
| [design/launcher.md](design/launcher.md) | How the launcher invokes `claude`, what profiles share, accounts, hazards |
| [design/profiles.md](design/profiles.md) | Profile sources, the trust model for shared profiles, the manifest format |
| [design/cli.md](design/cli.md) | The command set and the interactive and flag-based interaction model (R6) |
| [design/catalog-and-org-repo.md](design/catalog-and-org-repo.md) | Tool repo versus org data repo (R5), and how the org data repo is structured |
| [design/security.md](design/security.md) | Security requirements SR1 to SR5 and the managed-policy model (R3) |
| [design/platform.md](design/platform.md) | GitHub, GHE and cross-platform support (R1, R2) |
| [design/project.md](design/project.md) | Open source (R4), license and the project name |
| [design/release.md](design/release.md) | Versioning and the release pull request (release-please), the token, tag and signing chain, PR title rules, failure and rollback, first-release runbook, and the assessment of three other projects |
| [design/update.md](design/update.md) | `ccshelf update`: verification (SHA-256, cosign), install-method detection, replacing the binary, rollback and the opt-in automatic update |
| [design/workflows.md](design/workflows.md) | Example workflows (mockups) |
| [../site/](../site/index.html) | The project website: a static page with a loadout demo from captured output, plus these notes published as a `/docs` section (D-35, built by `scripts/build_docs.py`). `scripts/check-site.sh` validates it. A workflow deploys it to GitHub Pages automatically on every change to `main` (D-45), see `site/README.md` |
| [reference/cli.md](reference/cli.md) | The command reference, **generated from the real binary** (`scripts/gen-cli-reference.sh`). CI fails when it is stale {V} |
| [design/roadmap.md](design/roadmap.md) | Phase 0 evidence, the MVP, deferred work and non-goals |
| [research/context.md](research/context.md) | The two use cases and how the research was run |
| [research/landscape.md](research/landscape.md) | Evidence of the problems, native mechanisms, existing tools, naming findings |
| [research/options-and-reviews.md](research/options-and-reviews.md) | Solution options and the adversarial reviews |
| [research/stage0.md](research/stage0.md) | The macOS experiments and the corrections made after the review round |
| [research/adopt-or-build.md](research/adopt-or-build.md) | Phase 0.2: evaluation of `fuzzyalej/claude-profile` and `edimuj/claude-rig`, and the build recommendation |
| [research/routing-eval-protocol.md](research/routing-eval-protocol.md) | Phase 0.1: the fixed protocol and success criterion of the routing eval (harness template in `research/routing-eval/`) |

`research/` holds dated findings that are updated only to correct them. `design/` holds the current design.

## Glossary
Every document and the report use these terms the same way.

| Term | Meaning |
|---|---|
| **Profile** | A named recipe (`profiles/<name>.toml`) saying which plugins, standalone skills and MCP servers are active for one Claude Code session, plus session defaults. Started with `ccshelf run <name>`. It filters what is already installed. It is not an identity or security boundary. Part of the **launcher** (use case 1). |
| **Profile bundle** | A generated, dependency-only plugin named `profile-<name>`, kept in `bundles/`, so a profile's plugins can be installed natively with one `/plugin install`. It is not itself a profile. |
| **Catalog** | A generated, browsable index of an org's plugins and profiles: a static site plus `catalog.json`, built in CI from `marketplace.json`, the sidecars and git data. Also queried locally with `ccshelf search` and `ccshelf doctor`. Not committed. Answers "what exists, which should I use, who owns it". Part of use case 2 (discoverability). |
| **Sidecar** | The per-plugin catalog metadata file `catalog/plugins/<name>.toml` (owner, status, when_to_use, overlaps, review date). |
| **Marketplace** | The Claude Code concept: a git repo with `.claude-plugin/marketplace.json` and plugins that Claude Code installs from. Not the same as the catalog, which is a richer view built on top of it. |
| **Tool repo** | This public repo: the Go source, schemas, docs, releases, the reusable Action and a starter template. Never holds org data. |
| **Org data repo** | An adopting org's private repo (GHE Cloud or Server) holding its marketplace, plugins, profiles, sidecars, org config and CI. Also called the "profile catalog repo" in conversation. |
| **Account** | A Claude Code identity and data directory (a separate `CLAUDE_CONFIG_DIR`). An independent axis from profiles: a profile chooses what is active, an account chooses who you are. |
| **Profile source** | Where profile files come from: `dir` (the MVP), then `git` (after SR2), then `plugin`. Config key `[[sources]]` in the user's `config.toml`. |
| **Masking** | Setting `enabledPlugins: false` (per plugin) in a generated `--settings` file for every installed plugin the profile doesn't list. |
| **Capability** | One thing the launcher needs from Claude Code (settings masking, strict MCP config, `--plugin-dir`, ...). It probes each one and degrades per feature when managed policy blocks it. |
| **Launcher, `core`, catalog (modules)** | The three Go packages in the tool repo: `profiles/` (launcher), `core/` (shared parsing and resolution) and `catalog/`. |

## How to read the confidence labels
Claims about Claude Code behavior carry a marker:

- {V} verified in official docs (code.claude.com/docs), via `gh`, or by running the local CLI.
- {R} reported by a research agent and not independently re-checked. This includes the Stage 0 experiments, which a subagent ran with raw outputs kept.
- {U} unverified, snippet-only or inferred.

Stage 0 uses the words Confirmed, Partly and No issue seen with the same meaning. The report's loadout demo on the Overview uses fictional plugin names and shows design intent. Only the masking mechanism itself was tested.

## Requirements
Full text and reasoning are in the design files. Each has a row in the [decision log](DECISIONS.md).
- **R1** macOS, Linux and native Windows, six targets ([platform.md](design/platform.md)).
- **R2** GitHub.com, GHE Cloud and GHE Server with GitHub Actions ([platform.md](design/platform.md)).
- **R3** works with no, partial and strict managed policy, and never bypasses it ([security.md](design/security.md)).
- **R4** open source, MIT ([project.md](design/project.md)).
- **R5** a public tool repo and each adopter's private org data repo ([catalog-and-org-repo.md](design/catalog-and-org-repo.md)).
- **R6** an interactive mode and a flag-based mode. Flags are the contract ([cli.md](design/cli.md)).
- **SR1 to SR5** security requirements ([security.md](design/security.md)).

## Versions
Research ran against Claude Code 2.1.290. The local CLI reported 2.1.291 when Stage 0 ran, probably because it auto-updated in between. Claude Code changes fast. Re-verify flag and field behavior before you rely on it.

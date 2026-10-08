# Roadmap

Staged plan. Decisions and open questions are in [../DECISIONS.md](../DECISIONS.md).

**Direction (decided 2026-10-06, after the adversarial review): evidence first, then trimmed scope** (D-19, which supersedes the "both tracks in parallel" build order, S-01). The review's verdict was that the mechanism is sound but the plan was wider than its evidence. It also found that the benefit the pitch rests on (better skill routing, less clutter) was never measured.

## Phase 0: evidence (before writing product code)
1. **Routing eval.** Run 20 or more realistic prompts on a real plugin setup of the size the project targets (for example with `claude plugin eval` or `-p` runs). Compare the full set with a masked profile. Record which skill or plugin fires and whether it was the intended one. Fix the success criterion before running. The launcher is worth building only if the masked set measurably improves correct-first-try activation or reduces wrong-skill activations. If it does not, the launcher is documented as an alias recipe (`dry-run` prints the exact command) and not built as a product.
2. **Adopt, build or contribute.** Decide whether to contribute upstream instead of building a fourth tool. Evaluate `fuzzyalej/claude-profile` and `edimuj/claude-rig` against our gaps:
   - Default-deny masking on one shared plugin store.
   - Behavior under managed policy.
   - Concurrency.
   - Shared profiles from a private repo.
3. **Bundle and masking prototype (T7).** Does masking a plugin that an installed bundle depends on break the bundle? It needs a second config directory and a login only the user can do. It also settles whether `profile = bundle` stays the bridge between the launcher and the catalog.
4. **Stage 0 on Linux and Windows** (including `windows-11-arm` if a runner is used), repeating T1, T2 and the concurrency check, plus the MCP `cmd /c` question.
5. **Measure MCP and connector removal** through the settings keys (the one measurement that suggested a real saving).
6. **Kill and obsolescence criteria,** written down now and reviewed at every Claude Code minor release (K1 to K12 below).

## Phase 1: MVP, if Phase 0 supports it
- **Launcher:** `run`, `show`, `dry-run`, `ls`, `dir` sources only, `extends`, default-deny masking regenerated on every launch, settings validation before launch, `exec` on Unix and spawn on Windows. Closed schema from SR1. macOS and Linux first. Windows: compile-checked, and tested once Phase 0 item 4 passes.
- **Catalog:** `lint` (required fields from the marketplace entry plus a minimal sidecar: `owner`, `status`, `when_to_use`) and a generated `CATALOG.md`. Push "when to use" into native `description` and `relevance` blocks, which Claude Code itself reads. A static site only if people use the Markdown version.
- **Project setup:** employer approval for open-sourcing (the MIT `LICENSE` file already exists), `SECURITY.md` and a threat model, and `CONTRIBUTING.md`.
- **Release gates, staged:** github.com and GHE Cloud first. GHE Server and Windows arm64 are documented best-effort until tested (R1 and R2 stay the direction, not first-release gates). macOS, Linux and Windows amd64 are the first build targets. The real-`claude` smoke test runs nightly before any release.

## Phase 2: deferred until someone asks
- Accounts (`--account`).
- The trust lockfile and `git` sources. SR2 must land first, and the closure hash must be built before any sharing.
- `compile` and committed bundles (pending T7).
- `recommend`.
- `shell-init` for four shells.
- The static catalog site.
- Taxonomy.
- The Analytics join.
- The `plugin` source.
- A concierge.
- The OTel loop.
- The interactive wizards beyond the picker.

## What changed from the earlier roadmap
The shared `core/` is built only as far as the MVP needs. The catalog track starts with `CATALOG.md`, not the site (Phase 1). Open source remains the direction, but the scaffolding is gated on employer approval.

## Kill and obsolescence criteria (Phase 0.6, written 2026-10-06)

Written down before the evidence is in, so that stopping or shrinking is a decision made against a rule and not against sunk cost. "Stop" means archive the launcher and keep nothing but a documented recipe. "Shrink" means keep the smaller part that still earns its place (usually the catalog). Each trigger names how to check it. Claims about what Claude Code does today are {V} only where [stage0.md](../research/stage0.md) verified them. Everything about future releases is {U}.

### A. The launcher stops being worth building (stop or shrink to an alias recipe)
| # | Condition | How to check | Action |
|---|---|---|---|
| K1 | The routing eval fails its fixed criterion ([routing-eval-protocol.md](../research/routing-eval-protocol.md) section 5) | `run_eval.py analyze` verdict on the org's frozen prompt set | Stop the launcher as a product. Keep `dry-run` as a documented alias recipe |
| K2 | Claude Code ships a native allowlist or default-deny plugin mode (for example an "only these plugins" setting, or `enabledPlugins` with a deny-by-default switch) | Release notes and the settings reference for the words `allowlist`, `only`, `default deny`, `strict` near `enabledPlugins` or `plugins`. `claude --help` for a new `--plugins` or `--enable-plugin` style flag | Shrink: the launcher becomes a profile-to-settings generator, or stops if the native setting covers shared profiles |
| K3 | Claude Code ships native named, task-scoped profiles (a per-session choice of plugin, skill and MCP sets with inheritance). Not the account-profile request in anthropics/claude-code #91770 | `claude --help` and `claude config`/`/config` for `profile`, release notes for `profile`, and the open issues listed in [landscape.md](../research/landscape.md) | Stop the launcher. Migrate org profiles to the native format and keep the catalog |
| K4 | A managed-policy key or behavior blocks masking where the org needs it (for example `--settings` `enabledPlugins:false` ignored under managed settings, or `strictKnownMarketplaces`-style locks that remove the generated settings path) so that `doctor --policy` reports masking unavailable for most users | `ccshelf doctor --policy` on a managed machine, the managed-settings page for new keys, and a Stage 0 repeat (T1, T2) on each Claude Code minor release | Shrink to the paths policy still allows. Stop if none is left |
| K5 | The mechanism breaks and cannot be restored without bypassing policy or guessing (init event shows `--settings` masks no longer applied, or `claude plugin list --json` changes shape) for more than 2 consecutive Claude Code minor releases | The real-`claude` nightly smoke test and the Stage 0 harness | Stop (never work around by weakening SR1 to SR5) |
| K6 | An existing tool covers the gaps found in [adopt-or-build.md](../research/adopt-or-build.md): default-deny masking on one shared store, managed-policy awareness, trusted shared profiles | Re-run the evaluation when a tool above 100 stars or an org-backed tool appears. Run `gh search repos` for the topic quarterly | Contribute or adopt (D-33 revisit) |

### B. The catalog stops being worth building
| # | Condition | How to check | Action |
|---|---|---|---|
| K7 | Claude Code ships a native org-level catalog: faceted `/plugin` Discover over a private marketplace, with owner, status and when-to-use fields shown to users | Release notes for `/plugin` Discover, `marketplace.json` schema additions (`owner`, `status`, `relevance`, `tags`), plugin docs diff | Shrink to `lint` only, or stop if the native view needs no sidecar |
| K8 | Nobody reads `CATALOG.md`: no more than 5 views or searches per week after 3 months, measured where the org can see it (page views or `search` calls in an opted-in pilot, never telemetry in the tool, see AGENTS.md) | The org's own repository traffic or an agreed survey | Drop the static site and `recommend`. Keep `lint` |

### C. Adoption and maintenance (applies to the whole project)
| # | Condition | How to check | Action |
|---|---|---|---|
| K9 | Adoption: fewer than 5 people or teams use it weekly after 3 months of the first internal release, or fewer than 3 outside users or issues after 6 months of a public release | The org's own survey. GitHub stars, issues and releases downloads for the public repo | Move to maintenance only (security fixes), then archive at the next review |
| K10 | Maintenance burden: more than 25% of merged changes in two consecutive quarters are fixes for Claude Code changes (not our features), or the nightly smoke test is red for more than 14 days | `git log` classification and the Actions history | Shrink scope (drop the least used command group) or stop |
| K11 | Security: an SR1 to SR5 weakness that cannot be fixed without removing a feature, or a security report unanswered for 30 days | The security policy and issue tracker | Remove the feature. If the core is affected, stop |
| K12 | The org's employer approval to open-source is refused (O-11) | The approval | Keep it private and do not publish. The kill rules above still apply |

### What to watch and how
Check at every Claude Code minor release, and at least monthly:
1. Release notes (code.claude.com/docs changelog and the GitHub releases of anthropics/claude-code) for the keywords above: `profile`, `allowlist`, `default deny`, `enabledPlugins`, `skillOverrides`, `plugin enable`, `Discover`, `catalog`, `marketplace` owner or status fields, `managed`.
2. `claude --help` and `claude plugin --help`, diffed against the saved previous output (new flags such as `--plugins`, `--profile`, `--only-plugins`).
3. The settings reference for new keys that affect plugin or skill loading, and the managed-settings reference for new keys that restrict `--settings`, `--plugin-dir` or `--mcp-config`.
4. The `system/init` event of a Stage 0 run (counts of plugins, skills, agents, tools, MCP servers) after each upgrade. A count that changes for the same inputs means the mechanism moved.
5. `doctor --policy` on a managed machine (O-01).
6. The tracker issues listed in [landscape.md](../research/landscape.md). The closed `not_planned` ones show demand without a plan. An open one with a milestone is the signal.

### Review cadence
- **Every Claude Code minor release:** items 1 to 4 above (15 minutes). Record the result as one line in the decision log only when something changed.
- **Quarterly:** K6 (tools), K8 to K11, and the adoption numbers. Write one dated line in [DECISIONS.md](../DECISIONS.md) either way. The entry "reviewed, no trigger" is valid.
- **On the first trigger hit:**
  1. Open a decision row with the evidence and the action.
  2. Discuss with the user before acting (stopping is the user's decision).
  3. Mark the superseded rows.

## Non-goals (for now)
Phase 2 lists the deferred items. Other non-goals:
- A registry server/DB.
- Vector search.
- A full-screen TUI dashboard (interactive prompts and pickers are in scope, see R6).
- Custom install path (bundles cover install).
- MCP gateway.
- Config-dir-per-profile.
- A concierge search tool before there is usage data.
- Anything that writes shared settings.

## Checklist
Tick items in the report to track them (saved in your browser). The checked state below is the committed state.

- [x] Stage 0: launcher experiments on macOS (done 2026-10-06)
- [x] Adversarial review round absorbed and facts corrected (done 2026-10-06)
- [x] LICENSE added (MIT). Docs reorganized and the report generated from the Markdown
- [ ] Phase 0.1: routing eval on a real plugin set. Protocol and success criterion fixed (2026-10-06, [routing-eval-protocol.md](../research/routing-eval-protocol.md)), runs pending the org's plugin set
- [x] Phase 0.2: adopt, build or contribute: evaluated fuzzyalej/claude-profile and edimuj/claude-rig. Recommendation to build (D-33, [adopt-or-build.md](../research/adopt-or-build.md)), pending user review
- [ ] Phase 0.3: bundle and masking prototype (T7). Needs a second config directory and a login
- [ ] Phase 0.4: Stage 0 on Linux and Windows
- [x] Phase 0.5: measure MCP and connector removal through the settings keys (done 2026-10-06, saving about 5.7k tokens, 19%, see [stage0.md](../research/stage0.md))
- [x] Phase 0.6: write kill and obsolescence criteria (done 2026-10-06, section above)
- [ ] Employer approval for open-sourcing (SECURITY.md and CONTRIBUTING.md are written)
- [x] MVP launcher: run, show, dry-run, ls, dir sources, extends, default-deny masking, closed schema, settings validation
- [x] MVP catalog: lint (owner, status, when_to_use) and a generated CATALOG.md
- [x] Nightly real-claude smoke test before any release
- [x] Built ahead of the evidence (see D-22): accounts, trust lockfile and git/plugin sources, compiled bundles, recommend, shell-init, static site, doctor, usage analytics.
- [ ] Not built yet: the taxonomy and the Analytics join beyond the OTel and usage-API reader, and the marketplace-source check for plugin sources (the hook exists, the launcher does not pass it yet)


# Roadmap

Staged plan. Decisions and open questions are in [../DECISIONS.md](../DECISIONS.md).

**Direction (decided 2026-10-06, after the adversarial review): evidence first, then trimmed scope.** This supersedes the earlier "both tracks in parallel" build order. The review's verdict was that the mechanism is sound but the plan was wider than its evidence, and that the benefit the pitch rests on (better skill routing, less clutter) was never measured.

### Phase 0: evidence (before writing product code)
1. **Routing eval.** 20 or more realistic prompts on the real ~50-plugin setup, comparing the full set with a masked profile and recording which skill or plugin fires and whether it was the intended one (for example with `claude plugin eval` or `-p` runs). Fix the success criterion before running: the launcher is worth building only if the masked set measurably improves correct-first-try activation or reduces wrong-skill activations. If it does not, the launcher is documented as an alias recipe (`dry-run` prints the exact command) and not built as a product.
2. **Adopt, build or contribute.** Evaluate `fuzzyalej/claude-profile` and `edimuj/claude-rig` against our gaps: default-deny masking on one shared plugin store, behavior under managed policy, concurrency, shared profiles from a private repo. Decide whether to contribute upstream instead of building a fourth tool.
3. **Bundle and masking prototype (T7).** Does masking a plugin that an installed bundle depends on break the bundle? Needs a second config directory and a login only the user can do. Also settles whether `profile = bundle` stays the bridge between the launcher and the catalog.
4. **Stage 0 on Linux and Windows** (including `windows-11-arm` if a runner is used), repeating T1, T2 and the concurrency check, plus the MCP `cmd /c` question.
5. **Measure MCP and connector removal** through the settings keys (the one measurement that suggested a real saving).
6. **Kill and obsolescence criteria,** written down now: a native allowlist or default-deny `enabledPlugins` mode, a native task-scoped profiles feature (not #91770, which is about accounts), a faceted `/plugin` Discover, or an existing tool that covers the gaps. Review these at every Claude Code minor release.

### Phase 1: MVP, if Phase 0 supports it
- **Launcher:** `run`, `show`, `dry-run`, `ls`, `dir` sources only, `extends`, default-deny masking regenerated on every launch, settings validation before launch, `exec` on Unix and spawn on Windows. Closed schema from SR1. macOS and Linux first; Windows compile-checked and tested once Phase 0 item 4 passes.
- **Catalog:** `lint` (required fields from the marketplace entry plus a minimal sidecar: `owner`, `status`, `when_to_use`) and a generated `CATALOG.md`. Push "when to use" into native `description` and `relevance` blocks, which Claude Code itself reads. A static site only if people use the Markdown version.
- **Project setup:** employer approval for open-sourcing (the MIT `LICENSE` file already exists); `SECURITY.md` and a threat model; `CONTRIBUTING.md`.
- **Release gates, staged:** github.com and GHE Cloud first; GHE Server and Windows arm64 are documented best-effort until tested (R1 and R2 stay the direction, not first-release gates); macOS, Linux and Windows amd64 are the first build targets. The real-`claude` smoke test runs nightly before any release.

### Phase 2: deferred until someone asks
Accounts (`--account`), the trust lockfile and `git` sources (SR2 must land first, and the closure hash must be built before any sharing), `compile` and committed bundles (pending T7), `recommend`, `shell-init` for four shells, the static catalog site, taxonomy, the Analytics join, the `plugin` source, a concierge, the OTel loop, and the interactive wizards beyond the picker.

### What changed from the earlier roadmap
Both tracks in parallel became evidence first. The shared `core/` is built only as far as the MVP needs. The catalog track starts with `CATALOG.md`, not the site. Open source remains the direction, but the scaffolding is gated on employer approval.

## Non-goals (for now)
Accounts, the trust lockfile and git sources, `compile` and committed bundles, `recommend`, the static catalog site and taxonomy (all deferred to Phase 2); a registry server/DB, vector search, a full-screen TUI dashboard (interactive prompts and pickers are in scope, see R6), custom install path (bundles cover install), MCP gateway, config-dir-per-profile, a concierge search tool before there is usage data, anything that writes shared settings.

## Checklist
Tick items in the report to track them (saved in your browser); the checked state below is the committed state.

- [x] Stage 0: launcher experiments on macOS (done 2026-10-06)
- [x] Adversarial review round absorbed and facts corrected (done 2026-10-06)
- [x] LICENSE added (MIT); docs reorganized and the report generated from the Markdown
- [ ] Phase 0.1: routing eval on the real ~50 plugins, with the success criterion fixed first
- [ ] Phase 0.2: adopt, build or contribute: evaluate fuzzyalej/claude-profile and edimuj/claude-rig
- [ ] Phase 0.3: bundle and masking prototype (T7); needs a second config directory and a login
- [ ] Phase 0.4: Stage 0 on Linux and Windows
- [ ] Phase 0.5: measure MCP and connector removal through the settings keys
- [ ] Phase 0.6: write kill and obsolescence criteria
- [ ] Employer approval for open-sourcing (SECURITY.md and CONTRIBUTING.md are written)
- [x] MVP launcher: run, show, dry-run, ls, dir sources, extends, default-deny masking, closed schema, settings validation
- [x] MVP catalog: lint (owner, status, when_to_use) and a generated CATALOG.md
- [x] Nightly real-claude smoke test before any release
- [x] Built ahead of the evidence (see D-22): accounts, trust lockfile and git/plugin sources, compiled bundles, recommend, shell-init, static site, doctor, usage analytics.
- [ ] Not built yet: the taxonomy and the Analytics join beyond the OTel and usage-API reader; the marketplace-source check for plugin sources (the hook exists, the launcher does not pass it yet)


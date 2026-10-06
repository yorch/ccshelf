# Decision log

One row per decision, newest direction last. Never delete a row: when a decision changes, mark it **Superseded** and point to the row that replaces it. Add every new decision here, with the evidence and a trigger for revisiting it. Confidence markers: {V} verified in docs, `gh` or the CLI; {R} reported by a research agent and not independently re-checked; {U} unverified or inferred. "User" means the decision was made by the project owner.

## Decided

| ID | Date | Decision | Why and evidence | Confidence | Revisit when |
|---|---|---|---|---|---|
| D-01 | 2026-10-06 | One project with two parts: a **launcher** (per-terminal profiles of plugins, skills and MCP servers) and a **catalog** (discoverability over an org's git marketplace), same priority. | User decision. The review round called the shared `core/` thin (about 200 lines of parsing) and the "profile = bundle" bridge unproven; the parts were therefore sequenced behind evidence (D-19), and the bridge was reopened (D-17). | User; bridge {U} | After Phase 0, or if a track is cut |
| D-02 | 2026-10-06 | Core launcher mechanism: a generated `--settings` file that masks plugins (`enabledPlugins: false`), hides standalone skills (`skillOverrides`), connectors and MCP servers (`disableClaudeAiConnectors`, `deniedMcpServers`), then starts `claude`. Nothing shared is written. | Masking per key confirmed in Stage 0 {R}; settings keys documented as valid in any file {V}; reviewers confirmed in their experiments {R}. Aliases cover most of this (adversary estimate 80-90% {R}); the unserved parts are default-deny, composition and the catalog. | {R} | A native allowlist or default-deny `enabledPlugins`, or a task-scoped profiles feature in Claude Code |
| D-03 | 2026-10-06 | Implementation language **Go**, one module and one binary with packages `core`, `profiles`, `catalog`. | User confirmed. Single static binary, trivial cross-compile, fast launcher startup, good process handling. | User | If the maintainers' skills differ |
| D-04 | 2026-10-06 | **R2:** GitHub.com, GHE Cloud and GHE Server, with GitHub Actions. CLI first, workflow second; no hard-coded hosts; minimal third-party actions. | User decision. Designed to the lowest common denominator. GHE behavior untested. Release gates are staged (D-19). | User; behavior {U} | When a GHE Server minimum version is known |
| D-05 | 2026-10-06 | **R1:** macOS, Linux and native Windows; six targets (darwin, linux, windows, each arm64 and amd64); WSL counts as Linux. | User decision. Only macOS tested. First release gates are staged: macOS and Linux first, Windows amd64 once Stage 0 passes there, Windows arm64 best-effort. | User; behavior {U} | Phase 0.4 results |
| D-06 | 2026-10-06 | **R3:** works with no, partial and strict managed policy; capability-driven; never bypasses policy. Policy detection reads managed-settings sources and the "required by your org" marker; **no exit-code probing**. | User decision (the org enforces managed settings). Exit-code probing found infeasible by the technical review {R}. | User; {R} | When the org's managed keys are known |
| D-07 | 2026-10-06 | **R4:** open source under **MIT**. `LICENSE` added (holder Jorge Barnaby, 2026). No org-specific assumptions in code, schemas, defaults or examples. | User decision. Employer approval to open-source and an IP check are still needed before the first public commit. | User | Employer answer |
| D-08 | 2026-10-06 | **R5:** the tool lives in a public GitHub repo; each adopter keeps profiles and catalog data in its own private repo. The tool repo contains no real org data, only fictional examples and a starter template. No built-in default profiles. | User decision. | User | Never expected |
| D-09 | 2026-10-06 | **R6:** both an interactive mode (prompts, pickers, wizards) and a flag-based mode. Flags are the contract; prompts only on a TTY; every flow prints the equivalent command; trust is never auto-accepted. | User decision. | User | If an interactive library cannot meet the Windows constraint |
| D-10 | 2026-10-06 | **SR1 to SR5** security requirements: closed profile schema, trust the resolved closure pinned by commit SHA (project profiles off by default), no shadowing and protected controls, private verified local artifacts, hardened CI and releases. | Security review: trust model "not acceptable as written" {R}. The key claim, that a `--settings` file can set `bypassPermissions`, was verified directly {V}. | {V} for SR1; rest {R} | Before a first release (SR1-SR3) and before corporate CI (SR4-SR5) |
| D-11 | 2026-10-06 | Name **`ccshelf`** for the project and the command. | `claude-profile` collided with three existing tools; `ccprofiles` had its GitHub user taken and many near-identical names. `ccshelf` free on GitHub, npm, PyPI, crates.io, Homebrew, the Go proxy and the domains checked {V}. Brand risk of the `cc` prefix remains. | {V} availability; trademark {U} | Trademark search, winget and pkg.go.dev checks |
| D-12 | 2026-10-06 | Catalog hosting is the adopter's choice; the tool outputs a static directory. | User decision. | User | Never expected |
| D-13 | 2026-10-06 | Profile sources: `dir` and `git` first, `plugin` later. **The MVP narrows this to `dir` only**; git sources wait for SR2. | Tool-repo and data-repo split makes a pinned git source the natural fit; the plugin source is untested under policy. | {U} | When SR2 is implemented |
| D-14 | 2026-10-06 | Standalone skills: explicit off-list through `skillOverrides`, plus guidance to package skills as plugins. No generated `--plugin-dir` plugin. MCP servers and connectors hidden through settings keys. | `skillOverrides` works for standalone skills only {R}; settings keys are valid in any file {V}. | {V}/{R} | If Claude Code adds a skills allowlist |
| D-15 | 2026-10-06 | Metadata ownership is hybrid: plugin authors write it, the platform team reviews it. | User decision. | User | Adoption experience |
| D-16 | 2026-10-06 | Catalog-only metadata lives in a **sidecar file per plugin** (`catalog/plugins/<name>.toml`); single-file mode for small registries. | User decision, but the original rationale (CODEOWNERS cannot route per JSON entry) is incomplete: CODEOWNERS can route `plugins/*/catalog.toml` too. Kept for fewer merge conflicts and easier editing, with `owner` to be derived from CODEOWNERS (see O-09). | User; rationale {R} | Phase 1 catalog work |
| D-17 | 2026-10-06 | Generated profile bundles are committed in `bundles/` with a drift check; leave `version` out so the commit SHA is the version; no `tag-plugins.yml` until version ranges are used. **Reopened:** whether "profile = bundle" holds is pending T7. | Reviewers: masking a dependency of an installed bundle disables the bundle and raises errors {R}; tags only matter for version ranges {R}. T7 untested. | {U} | Phase 0.3 results |
| D-18 | 2026-10-06 | Profiles share auth, history, memory and the plugin cache (no `CLAUDE_CONFIG_DIR`). Accounts are a separate axis, combined by honoring the environment, optional built-in accounts, or an existing tool. | User decision (shared state is fine; document how to combine). Auth under the flags used confirmed {R}. Second-account test parked. | User; {R} | When a second config directory is available |
| D-19 | 2026-10-06 | **Direction: evidence first, then trimmed scope.** Phase 0 evidence, Phase 1 MVP, Phase 2 deferred. | User chose it after the adversarial review (product reviewer: about 10% chance the plan should proceed unchanged {R}). | User | Phase 0 results |
| D-20 | 2026-10-06 | **Exec on Unix, spawn on Windows**; cache cleanup by age only; settings JSON validated before every launch. | An invalid settings file is ignored silently {V}; spawn-and-wait breaks Unix job control {R}; settings carry through to background sessions {R}. | {V}/{R} | Linux and Windows Stage 0 |
| D-21 | 2026-10-06 | Docs layout: README with glossary, this decision log, `design/` and `research/`; the HTML report is **generated from the Markdown** by `docs/build_report.py` and is never edited by hand. | The audit found drift between eight notes and a hand-mirrored report; the ops reviewer proposed this layout. User decision. | User | If the generator becomes a burden |

## Superseded or corrected

| ID | Was | Replaced by |
|---|---|---|
| S-01 | Build both tracks in parallel, shared `core/` first | D-19 |
| S-02 | `plugin` source as the default for org profiles | D-13 |
| S-03 | Connectors can only be hidden with `--strict-mcp-config`, which sideload policy can block | D-02 and D-14 (settings keys) |
| S-04 | Spawn-and-wait everywhere | D-20 |
| S-05 | Detect policy by the exit code of a blocked flag | D-06 |
| S-06 | Working names `claude-profile` and `cprof`, then `ccprofiles` | D-11 |
| S-07 | #91770 as the signal to delete the launcher | Roadmap, Phase 0.6 (kill criteria); #91770 is an account request |
| S-08 | Project `.ccshelf/` profiles as a default source | D-10 (SR2): off by default |
| S-09 | "Do not relitigate decided requirements" | Reopen when evidence changes and record why |

## Open

| ID | Question | Where it is tracked |
|---|---|---|
| O-01 | Which managed-settings keys does the org actually set? Answerable by `doctor --policy` on a managed machine once built. | [security.md](design/security.md) |
| O-02 | Minimum supported GHE Server version for the workflows; Pages visibility on GHE Server. | [platform.md](design/platform.md) |
| O-03 | Is `core/` a public Go library or internal packages? Leaning internal, no API promise. | [architecture.md](design/architecture.md) |
| O-04 | Build, adopt or contribute: evaluate `fuzzyalej/claude-profile` and `edimuj/claude-rig` against the gaps. | [roadmap.md](design/roadmap.md) Phase 0.2 |
| O-05 | Routing eval: success criterion and prompt set, fixed before running. | [roadmap.md](design/roadmap.md) Phase 0.1 |
| O-06 | Bundle and masking interaction (T7); needs a second config directory and a login. | [roadmap.md](design/roadmap.md) Phase 0.3 |
| O-07 | Linux and Windows behavior: credential refresh under concurrency, MCP `npx` and `cmd /c`, managed-settings registry paths, masking parity. | [platform.md](design/platform.md) |
| O-08 | Name follow-ups: register the GitHub org `ccshelf`, trademark search (classes 9 and 42), winget and pkg.go.dev, the "unofficial, not affiliated" tagline. | [project.md](design/project.md) |
| O-09 | Sidecar details: derive `owner` from CODEOWNERS; whether the catalog also commits `CATALOG.md`; an extra lint review path for plugins that bundle hooks or MCP; release cadence for repo tags. | [catalog-and-org-repo.md](design/catalog-and-org-repo.md) |
| O-10 | Verification items: `deniedMcpServers` where `--mcp-config` is blocked, the `plugin` source under policy, bundle enable/disable, a real concurrency stress test, whether `name-only` reduces tokens, the effect of MCP removal. | [stage0.md](research/stage0.md) |
| O-11 | Employer approval to open-source, and no IP claim by the employer, before the first public commit. | [project.md](design/project.md) |
| O-12 | Second-account test (parked): a second `CLAUDE_CONFIG_DIR` and login. | [launcher.md](design/launcher.md) |

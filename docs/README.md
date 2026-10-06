# claude-profile: research & design notes

Status: research and Stage 0 experiments complete (2026-10-06). Masking via `--settings` confirmed, concurrency looked safe, token savings small on the test machine (see `05-stage0-results.md`). No code yet.

| File | What it holds |
|---|---|
| [01-context-and-problem.md](01-context-and-problem.md) | The two use cases, user context, how the research was run |
| [02-research-findings.md](02-research-findings.md) | Evidence of the problems, native Claude Code mechanisms, existing tools |
| [03-solution-options-and-review.md](03-solution-options-and-review.md) | Solution space (brainstorm) and the adversarial review, conflicts resolved |
| [04-recommendation-and-roadmap.md](04-recommendation-and-roadmap.md) | Recommended architecture, staged roadmap, manifest + CLI sketch, open decisions |
| [05-stage0-results.md](05-stage0-results.md) | Empirical tests of the launcher assumptions |
| [06-example-workflows.md](06-example-workflows.md) | Example workflows showing how profiles and the catalog would be used (mockups) |
| [07-how-it-invokes-claude.md](07-how-it-invokes-claude.md) | How the launcher invokes `claude`, what is shared between profiles, combining profiles with accounts |
| [08-org-data-repo-structure.md](08-org-data-repo-structure.md) | How an adopting org's private profile and catalog repo should be structured (layout, sidecar metadata, CODEOWNERS, CI) |
| [report.html](report.html) | Interactive single-file version of all of the above |

## How to read the confidence labels
- **Verified**: confirmed in official docs (code.claude.com/docs), via `gh`, or by running the local CLI.
- **Reported**: stated by a research subagent, not independently re-checked.
- **Unverified**: snippet-only, or inferred.

Requirements so far:
- **R1:** the tooling must support macOS, Linux and native Windows (design assessment in 04; only macOS tested).
- **R3:** the launcher must work across no, partial and strict managed policy (capability-driven; never bypasses policy).
- **R4:** the tool will be open source: no org-specific assumptions in code, schemas or defaults.
- **R5:** the tool lives in a public GitHub repo; adopting companies keep their profiles and catalog data in their own private GHE repo (tool repo and data repo are separate; see 04).
- **R2:** it must support both GitHub Enterprise Cloud and GitHub Enterprise Server, with GitHub Actions (design assessment in 04; untested).

Sources were gathered by subagents on 2026-10-05/06 against Claude Code 2.1.290. Claude Code changes fast; re-verify flag/field behavior before relying on it.

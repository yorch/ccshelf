## What and why

<!-- What does this change, and why? Link the issue or decision (D-xx) if there is one. -->

## Checklist

- [ ] Tests added or updated; they are hermetic (temp `HOME`, no network, no real `claude`) and pass on Linux, macOS and Windows
- [ ] `make ci` passes locally (or I explain below what I could not run)
- [ ] Docs changed in Markdown and the report regenerated with `python3 docs/build_report.py` (never edited `docs/report.html` by hand)
- [ ] Decision log (`docs/DECISIONS.md`) updated if this adds, changes or supersedes a decision
- [ ] No new dependency (or the need is explained below)

### Security checklist (required if this touches profiles, settings, cache, trust, policy, CI or releases)

- [ ] Schemas stay closed: unknown keys are errors, and nothing can write `permissions`, `hooks`, auth or endpoint settings (SR1)
- [ ] Trust applies to the resolved closure pinned by commit SHA; nothing is trusted implicitly (SR2)
- [ ] No name shadowing; protected plugins and MCP servers stay unmaskable (SR3)
- [ ] Files are 0600 and directories 0700, created exclusively with no-follow; paths are confined to their root; no secrets on disk or argv; values redacted in output (SR4)
- [ ] Workflow changes: every `uses:` pinned to a full SHA (`scripts/check-pins.sh`), `permissions: {}` at the top with per-job grants, no `${{ }}` of untrusted data inside `run:`, no secrets reachable from fork pull requests (SR5)
- [ ] Managed policy is never bypassed or probed by trial (R3)

## Notes for reviewers

<!-- Anything risky, anything you could not test, anything you deliberately left out. -->

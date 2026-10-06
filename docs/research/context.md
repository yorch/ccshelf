# Context and problem

## Use case 1: Profiles (per-terminal, task-specific sets)
Claude Code installs plugins, skills and MCP servers globally (user scope), so every session carries all of them regardless of the work type (frontend, backend, SRE, SEO, skill-authoring...).

Desired: named **profiles**, each with its own set of plugins, skills and MCP servers; different terminals can run different profiles **concurrently**.

User's candidate solution: a CLI wrapper `<wrapper> <profile>` that launches Claude Code with that profile's set.

## Use case 2: Discoverability (org registry)
Inside a company with many internal Claude Code plugins in a git-based marketplace (`marketplace.json`), people can't tell what exists, what to use when, which of overlapping plugins to pick, what's new, or whether something is owned/maintained.

## Decisions
Decisions are recorded in [../DECISIONS.md](../DECISIONS.md), not here. The user-level context that shaped them: the user is both platform owner and consumer of the internal registry; the registry is git-based with roughly 50 plugins; both use cases were meant to be one project at the same priority (since reopened in part, see the decision log); the process was to research the use cases without anchoring on the proposed solution, assess, then assess solutions including the wrapper, then discuss, with Opus subagents as adversary and brainstormer.

## Environment
macOS, zsh, Claude Code 2.1.290, repo `/Users/yorch/code/claude-profile` (empty at start).

## How the research was run
1. Native-capabilities agent (official docs).
2. Community/problem agent (GitHub issues via `gh`, web, existing tools).
3. Opus brainstorm agent (solution space).
4. Opus adversary agent (attacks framing, verifies against docs).
5. Second round for discoverability: native-features agent, community/org agent, then Opus adversary and Opus brainstorm over both use cases.
6. Stage 0: empirical experiments on the local CLI (see [stage0.md](stage0.md)).

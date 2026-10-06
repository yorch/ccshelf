# 01. Context and problem

## Use case 1: Profiles (per-terminal, task-specific sets)
Claude Code installs plugins, skills and MCP servers globally (user scope), so every session carries all of them regardless of the work type (frontend, backend, SRE, SEO, skill-authoring...).

Desired: named **profiles**, each with its own set of plugins, skills and MCP servers; different terminals can run different profiles **concurrently**.

User's candidate solution: a CLI wrapper `<wrapper> <profile>` that launches Claude Code with that profile's set.

## Use case 2: Discoverability (org registry)
Inside a company with many internal Claude Code plugins in a git-based marketplace (`marketplace.json`), people can't tell what exists, what to use when, which of overlapping plugins to pick, what's new, or whether something is owned/maintained.

## Decisions made by the user
- Both use cases belong to the **same project**, with the **same priority**.
- The user is both **platform owner** and **consumer** of the internal registry.
- Registry is **git-based**, roughly **50 plugins**.
- Must support **macOS, Linux and Windows** (requirement R1); both GHE Cloud and Server (R2); none/partial/strict managed policy (R3); open source (R4).
- Build order: **both tracks in parallel**. Name: **`ccprofiles`** for both the project and the command (decided 2026-10-06 after the earlier working name `claude-profile` turned out to collide with three existing tools; see 04 "Name").
- Implementation language: **Go**. Hosting/CI: **GitHub / GitHub Enterprise and GitHub Actions** (both GHE Cloud and Server, R2).
- "Catalog" = a generated, browsable view of the marketplace (static site built in CI), not a server (see 04).
- Process: research the use cases without anchoring on the proposed solution, assess, then assess solutions including the wrapper, then discuss. Opus subagents are used as adversary and brainstormer.

## Environment
macOS, zsh, Claude Code 2.1.290, repo `/Users/yorch/code/claude-profile` (empty at start).

## How the research was run
1. Native-capabilities agent (official docs).
2. Community/problem agent (GitHub issues via `gh`, web, existing tools).
3. Opus brainstorm agent (solution space).
4. Opus adversary agent (attacks framing, verifies against docs).
5. Second round for discoverability: native-features agent, community/org agent, then Opus adversary and Opus brainstorm over both use cases.
6. Stage 0: empirical experiments on the local CLI (see 05).

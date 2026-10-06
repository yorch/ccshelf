# 05. Stage 0 results (empirical)

Run 2026-10-06 on Claude Code 2.1.291, macOS, `--model haiku`, cwd = a scratch dir (no project settings). Harness: `claude -p "reply with the single word ok" --output-format stream-json --verbose --max-turns 1 --model haiku <args>`; the `system/init` event lists plugins, skills, slash commands, agents, tools and MCP servers. Raw outputs and scripts live in the session scratchpad (`.../scratchpad/stage0/`), not in the repo. Reported by a subagent; the numbers below are its measurements.

Safety: no user config was modified by the experiments (read-only inspection only). The CLI itself rewrites `~/.claude.json` on use (see T4).

## Machine under test
- 15 plugins enabled at user scope (claude-plugins-official), plus `design@synced` and `sp-global@synced` (scope "synced").
- Standalone user skills: only `~/.claude/skills/synced/**` (12 skills surfaced as `anthropic-skills:*`). No plain user skills/commands/agents. No `mcpServers` in user settings or `~/.claude.json`.
- Baseline init: plugins 20, skills 53, slash commands 99, agents 15, tools 157, MCP servers 20 (11 plugin-provided, 9 claude.ai connectors; mixed connection status).
- Init always includes 3 harness-injected `cc-plugin-*` entries; the init plugin list shows names without `@marketplace`.

## Results
| Test | Result | Verdict |
|---|---|---|
| **T1** `--settings` `enabledPlugins:{x:false}` (inline JSON and file) | Masks plugins that are enabled at user scope, including the synced-scope plugin. Masking 4 plugins: plugins 20→16, skills 53→39, agents 15→12, and their MCP servers disappeared. Plugins not listed stayed enabled (**per-key merge**). **claude.ai connectors are not affected by `enabledPlugins`.** | **Confirmed** |
| **T2** `--setting-sources project,local` | User layer dropped: plugins 3 (harness only), skills 19 (bundled only), agents 5; all plugin MCP servers and the 12 `anthropic-skills:*` gone; synced-scope plugins also dropped. 9 claude.ai connectors remained. **Auth still works** (OAuth). | **Confirmed** |
| **T3** `skillOverrides` | `"off"` removes standalone user skills (long key `anthropic-skills:pdf` and short key `pdf` both worked). `"off"` did **not** remove plugin skills under any key tried (long and short). `"name-only"` not observable in init. Token impact **not reliably measured** (total input tokens baseline 27,167; with 12 skills off 27,652, i.e. within noise). | **Partly confirmed**: works for standalone skills, not plugin skills (matches docs) |
| **T4** concurrency (3 parallel sessions, different masks, plus a JSON-parse loop on `~/.claude.json`) | About 4,771 parses, 0 failures. No cross-contamination between sessions. The CLI itself rewrote `~/.claude.json` (sha changed, size unchanged; content not diffed). | **No problem observed**; not a stress test (sessions ~10-20 s, partly overlapping) |
| **T5** `--strict-mcp-config --mcp-config '{"mcpServers":{}}'` | MCP servers → none, covering plugin servers **and claude.ai connectors**; tools 157→29. Plugins and skills unchanged (plugin skills still load, their MCP servers don't). Combined with T2: plugins 3, MCP none. | **Confirmed** |
| **T6** managed settings on this machine | None found (`/Library/Application Support/ClaudeCode/`, `/etc/claude-code`, defaults domain all absent). MDM or server-side managed settings not checked. | No policy here |
| **T7** bundle behavior | Skipped: needs installing a plugin. | Not tested |

## Token measurements (single run each, noisy)
| Run | Total (input + cache create + cache read) |
|---|---|
| baseline | 27,167 |
| masking 14 plugin skills (T1) | 26,688 |
| `--setting-sources project,local` (T2) | 24,765 |
| 12 `anthropic-skills:*` off | 27,652 |

Dropping the entire user layer (20 plugins, 53→19 skills, plugin MCP servers) saved only about 2.4k tokens (~9% of a ~27k baseline). On this machine, **the raw token argument for profiles is weak**; this agrees with the adversary's view. The case rests on routing quality, clutter, MCP startup and connection noise, not token cost. Tool-name counts also changed (157→29 with no MCP), which affects clutter and tool selection but was not measured as quality.

## Implications for the design
1. **Masking via `--settings` works and is per-key**, so a launcher doesn't need `--setting-sources` to subtract plugins. It does need the **list of installed plugins** (`claude plugin list --json`) to build a default-deny map. Newly installed plugins would still slip through unless the launcher regenerates each run (it should).
2. **`--setting-sources project,local` is a blunt but real "default-deny" for the user layer** and keeps auth; but it also drops user model/hooks/statusLine, so the launcher would have to re-add those through `--settings`. It also drops synced-scope plugins.
3. **claude.ai connectors need `--strict-mcp-config`** (they ignore `enabledPlugins`). That flag depends on `--mcp-config`, which `disableSideloadFlags` can block (non-SDK). Under such policy, connectors can't be controlled per session.
4. **`skillOverrides` is only useful for standalone skills.** Plugin skills are controlled per whole plugin: another reason to package standalone skills as plugins.
5. **Concurrency looked safe** for per-session generated settings; the CLI itself writes `~/.claude.json`, which is not something the launcher should touch.
6. **No managed policy on this machine**, so sideload-dependent paths were testable here but remain unvalidated for a locked-down org. Open decision #1 in 04 stands.

## Not yet tested
- Bundle (dependency) enable/disable semantics.
- Behavior under `disableSideloadFlags` or managed force-enables.
- A real stress test of concurrent writers; content diff of `~/.claude.json`.
- Whether `name-only` reduces tokens (needs `/context`-level measurement or a larger skill set).
- Description-routing quality with/without a profile (the actual benefit claim).

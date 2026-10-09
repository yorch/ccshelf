# Stage 0: per-profile instructions channels (2026-10-09)

**Question:** Which Claude Code channel can carry per-profile instructions to the main conversation and to subagents?

**Environment:** Claude Code 2.1.295 {V}, macOS, `--model sonnet` for the runs. The working directory was a scratch directory with no project settings. No user config was changed.

## Method

Each run used this command shape:

```
claude -p --model sonnet --output-format stream-json --verbose --allowedTools Agent <channel flags> -- "<prompt>"
```

- The prompt asked the main conversation to list every "secret marker" phrase in its instructions or context, without using tools. It then asked the main conversation to call the Agent tool once with `subagent_type` "general-purpose". The subagent got the same request, also without tools. Each channel had its own marker, so a phrase in the reply showed which channel reached which conversation.
- `--allowedTools Agent` allowed only the Agent tool. The model could not read a file to find a marker, so a marker in the reply came from the loaded context. The prompt also said not to read files.
- Each run used one `-p` session. The `system/init` event and the final reply were read from the stream-json output. Each channel ran once, so the results show presence or absence, not rates.
- For the `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD` runs, the environment variable was set to `1`. Runs without the variable unset it.

The channel flags for each run are in the table below.

## Results

| Run | Channel flags | Main saw marker | Subagent saw marker | Verdict |
|---|---|---|---|---|
| t1 | `--add-dir <dir with CLAUDE.md>` and env `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1` | yes {V} | yes {V} | Works for main and general-purpose subagent |
| t2 | `--add-dir` without the env var | no {V} | no {V} | Not loaded |
| t3 | t1 plus `--setting-sources project,local` | yes {V} | yes {V} | Survives the setting-sources restriction |
| t4 | `--append-system-prompt-file` | yes {V} | no {V} | Main only |
| t5 | t4 plus `--append-subagent-system-prompt-file` | yes {V} | yes {V} (subagent file) | Subagent file reaches the subagent |
| t6 | `--add-dir` CLAUDE.md containing an `@` import of a file outside the directory | no {V} | no {V} | Import not loaded in `-p` mode |
| t7 | `--settings` with `{"claudeMd": "..."}` | no {V} | no {V} | `claudeMd` is ignored outside managed scope |

Notes on the runs:

- t5 used `--append-subagent-system-prompt-file`. The docs say this flag works only with `-p`. The runs used `-p`.
- t6 showed no import in `-p` mode. In interactive mode, the docs say an approval dialog appears for each import outside the working directory {V}. Interactive behavior was not run.

## Verified doc facts

These facts come from the [official docs](https://code.claude.com/docs) {V}, checked under code.claude.com/docs.

- CLAUDE.md content is delivered as a user message after the system prompt. `--append-system-prompt` works at system-prompt level {V} (memory docs).
- Non-fork subagents load the full CLAUDE.md hierarchy. The built-in Explore and Plan agents skip it. Agents with `omitClaudeMd` skip it. Subagents never get the main system prompt or the output style {V} (sub-agents docs).
- `--add-dir` gives Claude read and edit access to the directory. Skills and agents under an `--add-dir` directory load (`.claude/skills`, `.claude/agents`). Sandboxed commands may write there {V} (CLI reference and settings reference).
- With `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1`, Claude Code loads `CLAUDE.md`, `.claude/CLAUDE.md`, `.claude/rules/*.md` and `CLAUDE.local.md` from each additional directory. The variable is global. It also applies to directories that the user adds with their own `--add-dir` {V}.
- `@path` imports in CLAUDE.md expand at launch. Imports outside the working directory need a one-time approval per project. Code spans and fenced code blocks are not expanded {V}.
- `disableSideloadFlags` rejects only `--plugin-dir`, `--plugin-url`, `--agents` and `--mcp-config`. `--add-dir` is not a sideload flag {V}.
- System prompt flags are recorded on the first request. With `--resume`, the recorded prompt stays until compaction {V}.

## Not verified

- {U} Interactive mode for the t1 route. The docs say the same CLAUDE.md hierarchy loads, but no interactive run was made.
- {U} Path-scoped rules (`.claude/rules/` files with path conditions) loaded from an added directory. This was not tested.
- {U} Explore and Plan subagents with the `--add-dir` route. The docs say they skip CLAUDE.md, but no run checked it.

## Consequences for ccshelf

- `--append-system-prompt-file` does not reach subagents. A profile that needs instructions in subagents cannot use this flag alone.
- `--add-dir` with `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1` reaches the main conversation and general-purpose subagents. It does not reach Explore or Plan, per the docs. It survives `--setting-sources project,local`.
- The added directory is editable by Claude. The launcher must check the directory before each launch.
- CLAUDE.md `@` imports in the added directory must be prevented. Otherwise a file outside the profile could enter the context.
- `claudeMd` in `--settings` is ignored. The launcher cannot deliver instructions through the settings file.

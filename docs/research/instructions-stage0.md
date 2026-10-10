# Stage 0: per-profile instructions channels (2026-10-09)

**Question:** Which Claude Code channel can carry per-profile instructions to the main conversation and to subagents?

**Environment:** Claude Code 2.1.295 {V}, macOS, `--model sonnet` for the runs. The working directory was a scratch directory with no project settings. No user config was changed.

## Method

Each run used this command shape:

```
claude -p --model sonnet --output-format stream-json --verbose --allowedTools Agent <channel flags> -- "<prompt>"
```

- The prompt asked the main conversation to list every "secret marker" phrase in its instructions or context, without using tools. It then asked the main conversation to call the Agent tool once with `subagent_type` "general-purpose". The subagent got the same request, also without tools. Each channel had its own marker, so a phrase in the reply showed which channel reached which conversation.
- `--allowedTools Agent` pre-approved the Agent tool. It did not remove the other tools, so the prompt also said not to read files. The stream-json output of every run shows one Agent call by the main conversation and no other tool call by either conversation {V}. A marker in a reply therefore came from the loaded context, not from a file read.
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

## Extractor in the Claude Code 2.1.295 binary (2026-10-09)

**Question:** Which text of a CLAUDE.md file does Claude Code read as an `@` import?

**Method:** A reviewer read the strings of the Claude Code 2.1.295 binary. Function names in the minified code are examples only. They change between versions, so a later version needs a new reading.

What the code does {V} (read from the binary, not run end to end):
- A function called from `FOe` lexes the file text with `new mC({gfm:false}).lex(...)`. `mC` is the bundled copy of the `marked` Markdown lexer, with GitHub extensions off.
- It walks the tokens and skips the tokens of type `code` (fenced and indented blocks) and `codespan`.
- It scans an `html` token only when the token starts with `<!--`, after it removes the comments.
- It runs the pattern `/(?:^|\s)@((?:[^\s\\]|\\ )+)/g` on each `text` token separately. The `^` therefore matches at the start of every text token, also after an inline token such as emphasis or a link.
- It keeps a path that starts with `./`, `~/` or `/`, or that matches `^[a-zA-Z0-9._-]`. It drops a path that starts with `@` or with a run of `#%^&*()`.

What is not known {R}: the token boundaries. A differential run compared inputs with `marked` 16.4.2 and the extractor logic above. The version of `marked` inside the binary is unknown, so the boundaries are reported, not verified. The run found inputs that import a file although a code-span or fence rule would hide them. Examples: inline HTML or a link destination that wins over a code span, a code span that pairs across a line break, a fence closer with a trailing `~` or backtick, and a fence inside a list item.

Consequence for ccshelf: the import check looks at raw characters and has no Markdown exemption. See [profiles.md](../design/profiles.md), "Instructions".

## Addendum: settings precedence and session reads (2026-10-09)

**Question:** Does a settings file that sets `CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD` override the process variable of the launcher? Does Claude Code read the added `CLAUDE.md` again during a session?

**Environment:** Claude Code 2.1.296 {V}, macOS, `-p` mode, `--model haiku`. The method is the one above: a marker phrase in the added `CLAUDE.md`, one Agent call to a general-purpose subagent, no file reads by the conversations. Each run happened once. The runs show presence or absence.

| Run | Setup | Main saw marker | Subagent saw marker | Verdict |
|---|---|---|---|---|
| E1 | Process env `=1` and `--add-dir` | yes {V} | yes {V} | Baseline works |
| E2 | E1 and the working directory has `.claude/settings.json` with `{"env":{"VAR":"0"}}` | no {V} | no {V} | Project settings env beats the process env |
| E3 | E2 and `--settings` file with `{"env":{"VAR":"1"}}` | yes {V} | yes {V} | The `--settings` layer beats project settings |
| E4 | No process env, only `--settings` with `{"env":{"VAR":"1"}}` | yes {V} | yes {V} | Settings env applies before CLAUDE.md loads |
| E5 | E2 with the value `"false"` (E5e) or `""` (E5f) | no {V} | no {V} | Both values turn the loading off |
| E6 | Writable added directory. A Bash command overwrote `CLAUDE.md` in the session. Two runs | no change {V} | no change {V} | Main and a later subagent saw the old text |

The user settings layer was not run. By the precedence below, a user `settings.json` env value of `0` would also turn the loading off {U}.

Docs quotes ([settings docs](https://code.claude.com/docs/en/settings) {V}):

- The precedence is: managed, then command line (`--settings`), then local project, then shared project, then user.
- "Nothing in your own settings files or `--settings` overrides a managed key."
- "A key you set there overrides the same key in your project and user settings files."

Consequences for ccshelf:

- The process variable alone is not enough. The launcher also writes the variable with the value `1` into the `env` of the generated settings file. That file is passed with `--settings`, so it beats project and user settings.
- Managed settings beat `--settings`. If managed settings turn the variable off, the launcher cannot override that, and it must not try (R3). It reads the managed `env` entry of this one variable and warns.
- Claude Code reads the added `CLAUDE.md` once, at session start. An edit during the session has no effect on that session. The read-only directory and the check before each launch protect the next launch.

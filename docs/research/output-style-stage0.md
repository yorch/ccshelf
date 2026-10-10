# Stage 0: the output style setting (2026-10-09)

**Question:** Can a generated `--settings` file set the Claude Code output style, and what happens with an unknown name or a project setting?

**Environment:** Claude Code 2.1.296 {V}, macOS, `-p` mode with `--model haiku`. The working directory was a scratch directory. No user config was changed.

## Method

Each run used this command shape and read the `output_style` field of the `system/init` event:

```
claude -p --model haiku --output-format stream-json --verbose <flags> -- "<prompt>"
```

- E7 set the style in a `--settings` file. One run had no style (the baseline). One run set `{"outputStyle":"Explanatory"}`. One run set `{"outputStyle":"NoSuchStyle"}`.
- E8 put `"outputStyle": "Learning"` in the `.claude/settings.json` of the project directory. One run used only that file. One run also passed `--settings` with `{"outputStyle":"Explanatory"}`.
- Each run was made once. The results show the value in the init event, not the text of the replies.

## Results

| Run | Settings | `output_style` in the init event | Verdict |
|---|---|---|---|
| E7 baseline | none | `default` {V} | The default value |
| E7 Explanatory | `--settings` `outputStyle` = `Explanatory` | `Explanatory` {V} | The key works in a command-line settings file |
| E7 NoSuchStyle | `--settings` `outputStyle` = `NoSuchStyle` | `NoSuchStyle` {V} | No error and exit status 0. The init event repeats the name |
| E8 project only | project `.claude/settings.json` = `Learning` | `Learning` {V} | The project setting works |
| E8 both | project `Learning` and `--settings` `Explanatory` | `Explanatory` {V} | The command-line settings win over the project settings |

`claude --help` shows no flag for an output style {V}.

## Shadowing (reviewer run, 2026-10-09)

Method: the project directory held `.claude/output-styles/Explanatory.md` with the frontmatter `name: Explanatory` and a body that told the model to reply with the word SHADOWED. The run passed `--settings '{"outputStyle":"Explanatory"}'`.

Result: the init event showed `Explanatory` and the reply was `SHADOWED` {V}. The project file replaced the built-in style of the same name. Thus a profile that selects a name does not pin the content. The docs say that custom styles drop the coding instructions of the system prompt unless `keep-coding-instructions: true` is set {V}. A project file can therefore change more than the tone. The same may hold for a user file or a plugin file {U}.

## Plugin style names (2026-10-09)

Method: the plugin `acme-kit` had `output-styles/Terse.md` with `name: Terse` and a body that told the model to reply with the word PLUGINSTYLE. Each run used `--plugin-dir` for the plugin, an empty working directory, a `--settings` file with the `outputStyle` below and the prompt "What is 2+2?".

| `outputStyle` | Reply | Verdict |
|---|---|---|
| `acme-kit:Terse` | `PLUGINSTYLE` {V} | The plugin style applied |
| `Terse` | `2 + 2 = 4` {V} | Not applied |
| `acme-kit:terse` | `2 + 2 = **4**` {V} | Not applied. The plugin part and the name part both match with case |

The init event repeats the given name in all runs, so it does not show whether a style applied. The plugin name part cannot hold a colon, so the pattern of ccshelf allows one colon and the 64-character limit includes the plugin prefix.

## Documented facts

These facts come from the [output styles page](https://code.claude.com/docs/en/output-styles) and the [plugin manifest reference](https://code.claude.com/docs/en/plugins-reference), read on 2026-10-09 {V}.

- Built-in styles: Default, Proactive, Concise (Claude Code 2.1.237 or later), Explanatory and Learning.
- "The value is case-sensitive, so write the built-in names as `Proactive`, `Concise`, `Explanatory`, and `Learning`. A value that doesn't match a style name exactly, such as `explanatory`, gives you the Default style. The `/output-style` command ignores case."
- Custom styles are Markdown files in `~/.claude/output-styles`, in `.claude/output-styles` or in the managed policy settings directory. The file name is the style name unless the frontmatter sets `name`. The example name is "Diagrams first", so a name can hold a space.
- "[Plugins] can also ship output styles in an `output-styles/` directory."
- `force-for-plugin: true` in a plugin style "applies this style automatically whenever the plugin is enabled ... Overrides the user's `outputStyle` setting."
- "Output styles apply to the main conversation and to a fork ... Other subagents run their own system prompt, so styles don't change how they respond."
- "Claude Code reads style files when it starts."

## Not verified

- {U} Whether a user style file or a plugin style file shadows a built-in name, and the precedence between a plugin style that has `force-for-plugin: true` and the profile value.
- {R} Managed settings win over the command-line settings file. This is the documented settings precedence. No run set a managed policy.
- {U} Interactive mode. All runs used `-p` mode.
- {U} The effect of the style on the text of the replies. The runs read the init event only.

## Consequences for ccshelf

- A profile can set the style through `outputStyle` in the generated settings file. No flag and no environment variable is needed.
- A wrong name fails without a message. The launcher warns about a built-in name with the wrong case. It cannot check custom names without a scan of style files, and it does not scan.
- The profile value wins over the project `outputStyle` setting {V}. The docs rank it above the user setting too, but no run checked that. Managed policy still wins {R}, and the launcher does not try to change that.
- The profile pins a name, not the content of the style. See "Shadowing" above.
- A plugin style needs the name `<plugin>:<Name>`. See "Plugin style names" above.

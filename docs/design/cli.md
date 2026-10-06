# CLI and interaction model

Command set and the interactive and flag-based interaction model (R6).

## CLI sketch
`run <profile> [-- claude args]` · `ls` · `show <p>` (resolved closure, overrides, token estimate) · `diff <a> <b>` · `dry-run <p>` (prints exact `claude` command) · `init` (create config, optionally from an org data repo URL) · `new <p> [--from <p2>]` · `edit <p>` · `trust <p>` · `account add <name>` · `shell-init <bash|zsh|fish|pwsh>` · `compile [--check]` · `lint` · `catalog build` · `search <q>` · `recommend` (rule-based, no LLM) · `doctor [--policy]` (overlap, unused, deprecated-in-use, stale owners, policy shadowing, capability matrix).

## Interaction model (R6)
**Requirement R6 (decided 2026-10-06):** the CLI supports both an **interactive mode** (prompts, pickers and wizards) and a **flag and option based mode**. Neither replaces the other.

### Rules
1. **Flags are the contract.** Every command is fully usable with flags and arguments alone, for scripts, CI, shell aliases and documentation. Interactive mode is an additional front-end over the same command definitions, never the only way to do something.
2. **When interactive mode is used.** Only when both stdin and stdout are terminals, `--no-interactive` is not set, the `CI` environment variable is not set, and the terminal is not `dumb`. Otherwise a missing required value is an error that names the missing flags and exits with code 2 (usage), never a prompt.
3. **Flags beat prompts.** Any value given on the command line is not asked again; interactive mode only asks for what is missing.
4. **Parity.** Every interactive flow ends by printing the equivalent flag-based command line (for example `Equivalent: ccshelf new sre --from base --plugin sre-kit@acme`), so a session can be replayed in a script.
5. **Trust prompts fail closed.** Accepting a risky profile change is an explicit interactive confirmation or an explicit flag that names what is accepted (for example `ccshelf trust sre --accept <closure-hash>`). `--yes` never accepts trust, and non-TTY and CI runs never default to allow (see the trust model in [profiles.md](profiles.md), "Profile sources and sharing").
6. **No prompts after `claude` starts.** The launcher prompts only before it starts `claude`. It restores the terminal to its original state before it starts or replaces itself with `claude`, so Claude Code's own terminal handling is untouched.
7. **Machine-readable output.** Read commands (`ls`, `show`, `search`, `doctor`, `diff`) support `--json` (stable schema, versioned) and a plain text format; `NO_COLOR` and `--no-color` are honored; color is never the only carrier of meaning; `--plain` gives a line-oriented prompt mode for screen readers and dumb terminals.
8. **Stable exit codes** (0 success, 1 failure, 2 usage error, 3 blocked by policy, 4 needs trust, 130 interrupted), documented, so scripts can react.
9. **Shell completion** for bash, zsh, fish and PowerShell is generated from the same command definitions.

### Scope for the first release
Interactive: a profile picker when `ccshelf run` or bare `ccshelf` is run in a terminal without a name (filter as you type); a `new` wizard (name, parent, multi-select of installed plugins, standalone skills to hide, MCP servers); an `init` wizard (config location, profile sources, optional account); the `trust` diff-and-confirm; `edit` opens `$EDITOR`. Not in scope for the first release: a full-screen dashboard or persistent TUI manager.

### Design implications
- **One command tree** (a single source for flags, help, completion and prompts) and a thin prompt layer behind an interface (`Prompter`), so flows can be tested by feeding scripted answers.
- **Library choice** is deferred to implementation, with constraints: static binary, no cgo, works on Windows consoles (Windows Terminal and legacy conhost, with a plain fallback), minimal dependencies (supply-chain surface), and no network use.
- **Testing:** non-interactive behavior with golden files on every OS; interactive flows with the scripted `Prompter` in unit tests plus a small pty smoke test per OS (ConPTY on Windows).
- **Cross-platform:** raw-mode terminal handling differs per OS; the picker must restore the terminal before spawning or replacing the process, which interacts with the Windows spawn-and-wait versus Unix `exec` choice.
- **Security:** prompts must not echo secrets, and an interactive default must never be less safe than the flag default.

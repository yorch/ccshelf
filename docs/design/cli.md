# CLI and interaction model

Command set and the interactive and flag-based interaction model (R6).

## CLI sketch
- `run <profile> [--refresh] [-- claude args]` (`--refresh` checks tracked branches for a new commit now)
- `ls`
- `show <p>` (resolved closure, overrides, token estimate)
- `diff <a> <b>`
- `dry-run <p>` (prints exact `claude` command)
- `init` (create config, optionally from an org data repo URL, pinned with `--ref` or tracking `--branch`)
- `new <p> [--scope user|project] [--from <p2>]`
- `edit <p>`
- `trust <p>`
- `account add <name>`
- `config [show|path|source ls|add|pin|rm|set|unset|edit]` (change the configuration after `init`, see below)
- `shell-init <bash|zsh|fish|pwsh>`
- `compile [--check]`
- `lint`
- `catalog init [dir]`
- `catalog build`
- `search <q>`
- `recommend` (rule-based, no LLM)
- `doctor [--policy]` (overlap, unused, deprecated-in-use, stale owners, policy shadowing, capability matrix)
- `update [--check] [--version vX.Y.Z] [--prerelease] [--dry-run] [--require-signature] [--rollback] [--yes] [--force] [--allow-downgrade]` (verified self-update, see [update.md](update.md)).

## Interaction model (R6)
**Requirement R6 (decided 2026-10-06):** the CLI supports both an **interactive mode** (prompts, pickers and wizards) and a **flag and option based mode**. Neither replaces the other.

### Rules
1. **Flags are the contract.** Every command is fully usable with flags and arguments alone, for scripts, CI, shell aliases and documentation. Interactive mode is an additional front-end over the same command definitions, never the only way to do something.
2. **When interactive mode is used.** Only when all of these are true:
   - both stdin and stdout are terminals
   - `--no-interactive` is not set
   - the `CI` environment variable is not set
   - the terminal is not `dumb`.

   Otherwise a missing required value is an error, never a prompt. The error names the missing flags and exits with code 2 (usage).
3. **Flags beat prompts.** The CLI does not ask again for a value given on the command line. Interactive mode only asks for what is missing.
4. **Parity.** Every interactive flow ends by printing the equivalent flag-based command line (for example `Equivalent: ccshelf new sre --from base --plugin sre-kit@acme`), so a script can replay a session.
5. **Trust prompts fail closed.** To accept a changed profile closure (any change, with risky items marked), the user must give one of these: an explicit interactive confirmation, or an explicit flag that names what is accepted. An example of the flag: `ccshelf trust sre --accept <closure-hash>`. `--yes` never accepts trust, and non-TTY and CI runs never default to allow (see the trust model in [profiles.md](profiles.md), "Profile sources and sharing").
6. **No prompts after `claude` starts.** The launcher prompts only before it starts `claude`. It restores the terminal to its original state before it starts or replaces itself with `claude`, so Claude Code's own terminal handling is untouched.
7. **Machine-readable output.**
   - Read commands (`ls`, `show`, `search`, `doctor`, `diff`) support `--json` (stable schema, versioned) and a plain text format.
   - The CLI honors `NO_COLOR` and `--no-color`.
   - Color is never the only carrier of meaning.
   - `--plain` gives a line-oriented prompt mode for screen readers and dumb terminals.
8. **Stable exit codes** (0 success, 1 failure, 2 usage error, 3 blocked by policy, 4 needs trust, 130 interrupted), documented, so scripts can react.
9. **Shell completion** for bash, zsh, fish and PowerShell comes from the same command definitions.

### Keyboard prompts (D-46)
`Select` and `MultiSelect` questions use an inline keyboard picker when stdin is a capable, file-backed terminal. The keys:
- up/down navigate without wrapping
- Enter chooses or submits
- Space toggles a multi-selection
- typing filters a filterable question (Backspace erases, Ctrl+U clears).

A multi-selection survives filtering, including an empty result, and returns the original option indexes in ascending order. Frames stay within 100 columns, 14 rows and 10 visible options, further bounded by the real terminal size. The picker sanitizes and caps each external segment, so a hostile profile cannot forge or overflow a prompt line.

The picker falls back to the numbered line prompt described above whenever the keyboard path cannot be trusted:
- `--plain`
- a non-TTY
- `CI`
- `--json`
- a redirected or non-file prompt stream
- an unsupported raw/VT console
- a terminal too small for the full key legend.

Both paths honor `NO_COLOR`/`--no-color` (the keyboard output contains no color escapes). Both cancel with the existing interrupted mapping (Escape, Ctrl+C, Ctrl+D, Ctrl+Z, EOF, context/signal cancellation).

Before returning, spawning or replacing itself with `claude`, a keyboard prompt restores the terminal to its original state (raw input and scoped output). It reports a restoration failure rather than ignoring it. A Linux PTY test asserts the exact before/after state and that the next reader still receives its whole line. The prompt reads input by synchronous polling on Linux/macOS and by Win32 console events on Windows, so no background reader is left behind. A resize during a picker exits safely with a `retry with --plain` hint instead of redrawing with stale dimensions.

A picker answers with the highlighted row on Enter, including a question that has no opt-in default (`HasDefault`). The numbered line prompt remains the strict form, which re-asks a question without a default.

### Wizards and write confirmations (D-47)
The full `init` and `new` wizards gather only what flags did not provide. Then they print a sanitized summary of exactly what they will write and ask a **default-no** confirmation (`Write this configuration?`, `Create this profile? (does not grant trust)`). The command-local `--yes` answers only that write question. It never accepts trust, and on its own it neither starts nor suppresses the wizard. Declining, aborting or cancelling before the gate writes nothing (the wizard re-checks a canceled read immediately before the first write). A declined confirmation is a failure with the command's own message (`canceled: nothing was written` for `new`, `configuration not written` for `init`). An abort or cancel keeps the interrupted code.

The equivalent command replays the run prompt-free:
- `init` records `--update-mode <effective> --yes` (plus `--force` and `--config` when used).
- `new` records `--scope`, `--yes` and `--no-interactive` with its content flags.

A partial-flag run prints no equivalent command because it asked nothing.

### `config`: change the configuration after `init` (D-50)
`init` stays the only command that creates `config.toml` (and the only one that replaces a whole file, with `--force`). Everything after setup goes through `ccshelf config`, which works with flags alone:
- `config path`
- `config show` (sources numbered from 1, `--json` kind `config`)
- `config source ls|add|pin|rm`
- `config set|unset <key> [value]`
- `config edit`.

In a terminal, bare `ccshelf config` is a menu (add a source, change a pin, remove a source, change a setting, open in the editor). The menu returns after each action, including a declined write or a refused change (it reports the problem). Only Done or an interrupt leaves it. With no terminal or `--no-interactive` it prints help and exits 2. A command that lacks a value (`source add` with no source flag, `source pin`/`rm` with no number, `set` with no key) asks only on a terminal. Otherwise it exits 2 and names the flag.

- **Allowlist.** `set` and `unset` accept only `trust.on_change`, `trust.require_pin`, `trust.trust_project_profiles`, `trust.branch_check_interval`, `update.mode`, `update.interval`, `catalog.remote_url`, `default_account`, `ui.color` and `ui.interactive`. The same validator that the file uses checks each one. The commands refuse these keys with exit 2 and a pointer to the dedicated command or to `config edit`, because they widen what ccshelf trusts or runs:
  - accounts
  - sources
  - `claude.path`
  - the update source (`update.base_url`, `update.cosign_identity_repo`, `update.asset_hosts`).
- **Write confirmation.** Like the wizards above, a write shows the changed lines (before and after) and asks a default-no `Write this configuration?`. `--yes` answers only that question, and declining exits 1 with `configuration not written`. Without a terminal, the command writes a non-weakening change without asking. Nothing here fetches a source or records trust. No command in this group touches `lock.json` or `project-trust.json`. In a terminal the wizard path prints the equivalent flag command, ending in `--yes`.
- **Weakening changes.** These need `--yes` when there is no terminal (exit 2 naming `--yes`) and always print a warning:
  - `trust.require_pin` turned off
  - `trust.trust_project_profiles` turned on
  - `update.mode` set to `install`
  - a new or re-pinned git source whose ref is not a tag or full commit id (possible only while `require_pin` is off)
  - a git source that is added with `--branch`, or switched to a branch or to another branch (D-55). The branch can move, but every new commit still needs trust.

  A change to what is trusted to supply code or releases also weakens:
  - `claude.path`
  - `update.base_url`
  - `update.cosign_identity_repo`
  - a new `update.asset_hosts` entry
  - the `marketplace` of an existing plugin source
  - the URL of an existing git source (compared by position when the number of sources is unchanged, so a removal is not flagged, but a one-step swap of sources is flagged, conservatively)
  - any new `dir` source whose path starts with `$` (the variable is read at every run).

  These changes are not weakening:
  - going back to a stricter or default value
  - changing `trust.on_change`
  - removing a source
  - adding a pinned source (a new source still needs trust before any of its profiles run).
- **Branches (D-55).** `init --git-url`, `config source add --git-url` and `config source pin` take `--branch <name>` instead of `--ref`. Giving both is a usage error (exit 2). `pin` can switch a source between a tag or commit and a branch, and it clears the other key. In a wizard, the question for a tag or commit also accepts `branch:NAME`, and the printed equivalent command uses `--branch`. `init` also prints the weakening warning for a branch. It never asks for `--yes` because of it, because `init` keeps its meaning for `--yes` (the write in the full wizard). `run` and `dry-run` take `--refresh` (see [profiles.md](profiles.md), "Tracking a branch"). `trust.branch_check_interval` takes a duration from 1h to 8760h and is not a weakening change.
- **Files.**
  - The file must already exist (exit 1 with a hint to run `init`).
  - A write re-encodes the struct, so **comments and layout are lost**. The summary says so when the file has a `#` comment. The command keeps the previous file as `config.toml.bak` (0600, replaced atomically, a symlink at either name is refused).
  - The command compares the bytes read at the start again just before the write. If the file changed meanwhile, nothing is written (exit 1, `changed while editing`).
  - The confirmation diff compares the file as it is with the exact bytes about to be written, so dropped comments, added empty tables and any layout change are visible.
  - The command validates paths (`~`, `$VAR` in account directories and `claude.path`) as they would expand, but writes them as the user wrote them, so a `set` changes only its own lines. `account add` and `rm` still use the plain, expanding save (`config.Save`) and keep neither the backup nor the changed-file check.
  - The command refuses duplicate sources (same type and repository, directory or plugin id) with a hint to use `source pin` or `source rm`. It compares git URLs after lower-casing the host and dropping a trailing `/` and `.git`, and `git@host:p` equals `ssh://git@host/p`. Another folder (`--path`) of the same repository is a different source.
  - Validation errors name sources from 1, like `show` and `ls`.
- **`config edit`** opens a copy (0600, created exclusively in the config folder) in `$VISUAL` or `$EDITOR` and checks it like the real file. Only if the copy is valid does it replace `config.toml` with the text exactly as saved (comments kept, previous file in `.bak`). For an invalid copy, the command prints the errors (with line and column when the parser gives one, and value errors name the key). It leaves the original untouched, keeps the copy and exits 1. If the editor fails, the command keeps the copy and prints its path, as for an invalid copy. A new `config edit` starts from the current file, so copy the fix over. `config edit` may repair a currently invalid file, needs a terminal (`--path` prints the path instead), and does not create a missing file.

### Human-readable presentation (D-42)
The CLI uses restrained presentation: task-grouped root help, a short getting-started sequence, readable labels, and explicit `hint:` lines rather than banners or emoji. Root help groups commands into profiles, discovery and diagnostics, org data repo maintenance, and setup and utilities. Usage errors point to the failing command's `--help` unless a more specific recovery hint or missing-value instruction is already available.

Tables keep their aligned layout at normal widths. When the terminal cannot fit even the minimum column widths, they switch to wrapped, labeled records separated by blank lines, retaining values rather than truncating them. The fallback also works with `--plain`. Piped output has no terminal-width limit and retains the aligned layout. A terminal only one column wide cannot display a two-column character, so that character becomes `?` in the fallback.

An empty profile or account list, a search with no matches, and an empty recommendation result are successful results on stdout, with a next-step `hint:`. Diagnostics and prompts remain on stderr. JSON keeps its existing envelope, collection types and exit codes, without human-only empty-state text. Scripts should use `--json` rather than parse presentation layouts.

### `update` (D-40)
`ccshelf update` follows the rules above. Its flags, confirmation, `Equivalent:` line, exit codes, `--json` output, trust model and the automatic update (`[update]` keys, terminal and `CI` conditions, `CCSHELF_NO_UPDATE_CHECK`) are in [update.md](update.md).

### Scope for the first release
Interactive:
- a profile picker when `ccshelf run` or bare `ccshelf` is run in a terminal without a name (filter as you type)
- a `new` wizard (name, user/project location, parent, multi-select of installed plugins, standalone skills to hide, MCP servers) ending in a default-no creation confirmation
- an `init` wizard (config location, profile sources, optional account, update mode) ending in a default-no write confirmation
- the `trust` diff-and-confirm
- `edit` opens `$EDITOR`.

Not in scope for the first release: a full-screen dashboard or persistent TUI manager.

### Design implications
- **One command tree** (a single source for flags, help, completion and prompts) and a thin prompt layer behind an interface (`Prompter`), so tests can feed scripted answers to flows.
- **Library choice** is deferred to implementation, with constraints:
  - static binary, no cgo
  - works on Windows consoles (Windows Terminal and legacy conhost, with a plain fallback)
  - minimal dependencies (supply-chain surface)
  - no network use.
- **Testing:** non-interactive behavior with golden files on every OS. Interactive flows with the scripted `Prompter` in unit tests plus a small pty smoke test per OS (ConPTY on Windows).
- **Cross-platform:** raw-mode terminal handling differs per OS. The terminal restore of rule 6 interacts with the Windows spawn-and-wait versus Unix `exec` choice.
- **Security:** prompts must not echo secrets, and an interactive default must never be less safe than the flag default. The implementation:
  - A question has no default unless it opts in (`HasDefault`).
  - Trust confirmations use `ConfirmRisky`, which has no default and needs the typed word `yes`.
  - Labels, hints and errors are single-line sanitized, so a hostile profile cannot forge a prompt line.
  - The terminal's echo state is restored when a secret prompt is cancelled.

  The `CI` variable set to any non-empty value (including `false`) disables prompts. `ccshelf` flags placed after the profile name (they would go to `claude`) exit 2 with a hint, `edit` needs a terminal, and `ls --refresh` re-resolves moved tags and tracked branches. In the printed equivalent command, the CLI redacts secret-looking flags and the argument after one.

## `catalog init`: set up an org data repo
`ccshelf catalog init [dir]` creates a new org data repo, or adds the missing pieces to an existing marketplace repo. It never changes an existing file silently (see [catalog-and-org-repo.md](catalog-and-org-repo.md), "Setting up the data repo"). It follows the interaction rules above:

- **Flags are the contract.**
  - `--marketplace-name`, `--org`, `--owner`, `--platform-owners` (repeatable or comma separated)
  - `--ccshelf-ref`, `--ccshelf-version`, `--runner-label`, `--default-branch`
  - `--mode new|adopt`, `--sidecars stub|none`
  - `--profiles-only`: a repo of profiles with no marketplace or catalog. `--marketplace-name`, `--owner` and `--sidecars stub` are then usage errors. The printed `Equivalent:` command keeps it. The wizard does not ask for it.
  - `--example-profile`
  - the `--no-<group>` flags (`--no-config`, `--no-marketplace`, `--no-sidecars`, `--no-codeowners`, `--no-workflows`, `--no-readme`, `--no-gitattributes`, `--no-gitignore`)
  - `--dry-run`, `--yes`, `--force`, `--write-suggestions`
  - `--quiet` (do not print the suggested lines of `needs-merge` files, which stay in `--json`)
  - `--git-init`
  - the global `--json` (data kind `catalog-init`) and `--root` (the directory when no argument is given).
- **Required values.** The command needs:
  - the marketplace name (unless an existing `marketplace.json` has it or the marketplace and README are skipped)
  - the platform owners (unless `ccshelf.toml` and CODEOWNERS both exist or are skipped).

  On a terminal, the command asks for missing values. It derives defaults from the directory name, an existing CODEOWNERS catch-all or the organization in the `origin` remote URL (read-only). It asks once for a missing action pin. Without a terminal (or with `CI`, `--no-interactive` or `--json`) a missing value exits 2 and names every missing flag, `--yes` included.
- **Confirmation.** Without `--yes` a terminal run shows the plan and asks `Write these files?` (default no). A non-interactive run needs `--yes`, or `--dry-run` to print the plan and write nothing. `--yes` confirms the plan only. It has nothing to do with trust.
- **Parity.** An interactive run ends with the `Equivalent:` line on stderr (every value that was chosen, plus `--yes`, or `--dry-run` when that was the choice).
- **Exit codes.**
  - 0 on success (including `nothing to do` and a plan with `needs-merge` entries).
  - 2 for an invalid or missing flag value and for a refused target argument (`..`, the file system root, the home directory or a directory above it, `~/.claude`, `$CLAUDE_CONFIG_DIR`, ccshelf's config and cache directories, the tool repository).
  - 1 for a file system condition: the target or a parent is a symbolic link or not a directory, a conflict such as an existing `.bak` or a directory where a file belongs, a write error (the files written are listed, and removed again when this run created them), a declined confirmation, `git init` failing.
  - 130 when interrupted.
- **Scope.** Nothing is committed, pushed or fetched. `--git-init` runs only `git init` (initial branch `main`) and only when there is no `.git`.

### Reading a published catalog
Outside an org data repo, `ccshelf search` can retrieve the published `catalog.json` when `[catalog].remote_url` is set in the user's `~/.config/ccshelf/config.toml` (or the platform's config directory). For each search it fetches over HTTPS, caps the response at 8 MiB, and requires the current catalog format version. This setting takes precedence over the configured profile sources for search and profile recommendations. Plugin recommendations still need the org data repo's marketplace relevance rules. `catalog build` and `doctor` still use org data repo files.

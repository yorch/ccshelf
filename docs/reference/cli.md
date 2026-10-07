# Command reference

**Generated file: do not edit by hand.** Run `scripts/gen-cli-reference.sh --write` to regenerate it.

{V} This page is **generated from the real `ccshelf` binary**: every section below is the output of `ccshelf --help` or `ccshelf <command> --help`, parsed and laid out here, and the exit codes are read from `internal/ui/exit.go`. `scripts/gen-cli-reference.sh --check` (run in CI) fails when this file is stale. It documents the command line as built from the repository; no version has been released yet.

## Global flags

These flags are accepted by every command. Flags of ccshelf itself go before a profile name: everything after the profile is passed to `claude` unchanged.

| Flag | Value | Description |
|---|---|---|
| `--account` | `string` | account to use for this invocation |
| `--claude` | `string` | path to the claude binary |
| `--config` | `string` | path to the user config file |
| `--json` |  | machine-readable output where supported |
| `--no-color` |  | disable color |
| `--no-interactive` |  | never prompt; fail naming the missing flag |
| `--plain` |  | line-oriented output and prompts |
| `--root` | `string` | org data repo root (default: current directory) |
| `-h`, `--help` |  | help for ccshelf |
| `-v`, `--version` |  | version for ccshelf |

## Exit codes

Scripts can rely on these; they never change meaning.

| Code | Meaning |
|---|---|
| `0` | success |
| `1` | the command failed |
| `2` | a usage error, including a value that was missing and could not be prompted for |
| `3` | the action was blocked by managed policy |
| `4` | the profile needs trust before it can run |
| `130` | the user interrupted the command (Ctrl+C, Ctrl+D at a prompt), by the shell convention 128 + SIGINT |

## Commands

| Command | What it does |
|---|---|
| [`ccshelf account`](#ccshelf-account) | Manage named Claude Code accounts (separate CLAUDE_CONFIG_DIR) |
| [`ccshelf account add`](#ccshelf-account-add) | Add an account and print the one-time steps to log in |
| [`ccshelf account ls`](#ccshelf-account-ls) | List the configured accounts |
| [`ccshelf account rm`](#ccshelf-account-rm) | Forget an account (its directory is left alone) |
| [`ccshelf catalog`](#ccshelf-catalog) | Build the plugin catalog of the org data repo |
| [`ccshelf catalog build`](#ccshelf-catalog-build) | Write catalog.json, CATALOG.md and the static site |
| [`ccshelf compile`](#ccshelf-compile) | Generate the profile-* bundle plugins from the profile manifests |
| [`ccshelf completion`](#ccshelf-completion) | Print a shell completion script |
| [`ccshelf diff`](#ccshelf-diff) | Compare two resolved profiles |
| [`ccshelf doctor`](#ccshelf-doctor) | Check the org's catalog and profiles for overlap, staleness and policy conflicts |
| [`ccshelf dry-run`](#ccshelf-dry-run) | Print the exact claude command a run would execute |
| [`ccshelf edit`](#ccshelf-edit) | Open a personal profile in $VISUAL or $EDITOR |
| [`ccshelf init`](#ccshelf-init) | Create the configuration file, optionally from an org data repo |
| [`ccshelf lint`](#ccshelf-lint) | Check the org data repo: marketplace, sidecars, ownership and profiles |
| [`ccshelf ls`](#ccshelf-ls) | List the profiles of all sources |
| [`ccshelf new`](#ccshelf-new) | Create a personal profile |
| [`ccshelf recommend`](#ccshelf-recommend) | Suggest plugins and profiles for a project directory (rule based, offline) |
| [`ccshelf run`](#ccshelf-run) | Start claude with a profile |
| [`ccshelf search`](#ccshelf-search) | Search the plugin catalog of the org data repo |
| [`ccshelf shell-init`](#ccshelf-shell-init) | Print shell functions cs-<profile> that run each profile |
| [`ccshelf show`](#ccshelf-show) | Show a resolved profile: parents merged, MCP servers, closure |
| [`ccshelf trust`](#ccshelf-trust) | Review and trust a shared profile (or a project folder) |
| [`ccshelf update`](#ccshelf-update) | Update ccshelf to the latest release (verified), or roll back |
| [`ccshelf version`](#ccshelf-version) | Print the ccshelf version |

## ccshelf account

An account is a name for a separate Claude Code configuration directory, so work and personal logins, plugins and history stay apart. ccshelf never reads, copies or moves credentials and never deletes a directory.

**Usage**

```text
ccshelf account [command]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for account |

**Subcommands**

| Command | What it does |
|---|---|
| [`ccshelf account add`](#ccshelf-account-add) | Add an account and print the one-time steps to log in |
| [`ccshelf account ls`](#ccshelf-account-ls) | List the configured accounts |
| [`ccshelf account rm`](#ccshelf-account-rm) | Forget an account (its directory is left alone) |

## ccshelf account add

Create the account's configuration directory (mode 0700), save it in the configuration and print the one-time steps you run yourself: start claude with the variable set, /login, then marketplace adds and plugin installs inside it. The directory defaults to ~/.claude-<name>.

**Usage**

```text
ccshelf account add [name] [flags]
```

**Examples**

```text
ccshelf account add work
ccshelf account add personal --dir ~/.claude-personal --default
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--default` |  | make this the default account |
| `--dir` | `string` | configuration directory (default ~/.claude-<name>) |
| `-h`, `--help` |  | help for add |
| `--marketplace` | `stringArray` | marketplace to add inside the account, such as acme/claude-plugins (repeatable) |
| `--plugin` | `stringArray` | plugin to install inside the account, name@marketplace (repeatable) |

## ccshelf account ls

List the configured accounts

**Usage**

```text
ccshelf account ls [flags]
```

**Aliases:** `ls`, `list`

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for ls |

## ccshelf account rm

Remove the account from the configuration. The directory, with its login and history, is never deleted.

**Usage**

```text
ccshelf account rm <name> [flags]
```

**Aliases:** `rm`, `remove`

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for rm |

## ccshelf catalog

Build the plugin catalog of the org data repo

**Usage**

```text
ccshelf catalog [flags]
ccshelf catalog [command]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for catalog |

**Subcommands**

| Command | What it does |
|---|---|
| [`ccshelf catalog build`](#ccshelf-catalog-build) | Write catalog.json, CATALOG.md and the static site |

## ccshelf catalog build

Build the catalog from the org data repo and write it into --out (default dist/catalog, relative to the current directory): catalog.json, CATALOG.md and, unless --no-site, the static site (index.html, app.js, style.css; with --no-site the files of an earlier site build are removed from --out). Nothing is written outside --out, and the output is published files: directories 0755, files 0644.

--out must not be the repo root or contain it, must not lie inside the repo's source directories (profiles, bundles, catalog, plugins, .github and so on) and must not pass through a symbolic link below the working directory or the repo root. A directory that is not an org data repo (its marketplace file cannot be read or parsed) is an error, exit 1, and nothing is written.

The catalog has no timestamp unless --timestamp is given, so the output is reproducible. Lint findings are printed to stderr; a repo with lint errors still produces its catalog (for previews) but the command exits 1.

**Usage**

```text
ccshelf catalog build [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--git-data` |  | add last-change and contributor data from git |
| `-h`, `--help` |  | help for build |
| `--no-site` |  | write only catalog.json and CATALOG.md |
| `--out` | `string` | output directory (default dist/catalog) |
| `--timestamp` |  | stamp generated_at into the output |

## ccshelf compile

Compile every profile of the org data repo into a profile bundle: a plugin under bundles/profile-<name>/ whose dependencies are the profile's resolved plugins. Profiles that resolve to no plugins (abstract bases) are skipped.

The whole bundles/ tree is generated output: files that are not produced by a profile are removed. With --check nothing is written; the command prints the differences and exits 1 when the committed bundles are stale.

**Usage**

```text
ccshelf compile [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--check` |  | write nothing; exit 1 when the bundles are stale |
| `-h`, `--help` |  | help for compile |

## ccshelf completion

Print a completion script generated from the command definitions, for bash, zsh, fish or PowerShell.

**Usage**

```text
ccshelf completion <bash|zsh|fish|powershell> [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for completion |

## ccshelf diff

Compare two profiles after their parents are merged: plugins, skills, MCP servers, environment variable names and session defaults. "+" marks what b adds to a and "-" what b lacks. Environment values and prompt text are never printed. The exit code is 0 whether or not they differ; --json has an "identical" field.

**Usage**

```text
ccshelf diff [profile-a] [profile-b] [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for diff |

## ccshelf doctor

Analyze the org data repo (--root, default the current directory): overlapping plugins, plugins in no profile, deprecated plugins in use, stale or missing review dates and owners, protected plugins that a profile masks, and plugins that need platform review.

Checks that need more input are skipped and listed as skipped, never silently passed: --installed reads 'claude plugin list' (read only), --usage-file reads an OpenTelemetry JSONL export, --usage-api asks the Enterprise Analytics API (needs the admin key in CCSHELF_ANALYTICS_KEY; this is the only network call), --skills-dir lists the standalone skills in a directory (read only; each subdirectory with a SKILL.md is a skill; ccshelf never reads ~/.claude on its own), and --policy reads the machine's managed Claude Code policy (read only, never bypassed) and prints the capability matrix.

With --policy every profile's needs (extra MCP servers, strict MCP, dropping user settings, a system prompt file, hiding claude.ai connectors, and the marketplaces of its plugins) are checked against the policy that could be read. Exit code 3 when the policy blocks something a profile needs (POL002, POL005, POL006); a feature whose state is unknown is a warning, never a failure. A policy that exists but could not be read is a warning, and exit 3 only with --strict.

Exit code 1 for error findings, and when the directory is not an org data repo (its marketplace file cannot be read). With --policy and no --root, a directory that is not an org data repo is not an error: only the policy part runs (the capability matrix and the policy findings that do not need profiles; the checks that need the catalog are listed as skipped), because the managed policy belongs to the machine, not to a repository. A developer who reaches the org through a git source can use it that way.

**Usage**

```text
ccshelf doctor [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for doctor |
| `--installed` |  | read the installed plugins with 'claude plugin list' |
| `--policy` |  | also read managed Claude Code policy and print the capability matrix |
| `--skills-dir` | `string` | directory of standalone skills to check against the profiles (read only) |
| `--strict` |  | with --policy: exit 3 also when a managed policy exists but could not be read |
| `--usage-api` |  | read plugin usage from the Enterprise Analytics API |
| `--usage-days` | `int` | usage window in days for --usage-api and --usage-file (default 30) |
| `--usage-file` | `string` | OpenTelemetry JSONL export to read plugin usage from |

## ccshelf dry-run

Run the whole pipeline of "run" (including the trust check) and print the exact claude command instead of starting it. The generated settings, MCP config and prompt files are written to the private cache so the printed command is valid. Environment values from profiles are never printed.

**Usage**

```text
ccshelf dry-run [profile] [-- claude args]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for dry-run |

## ccshelf edit

Open one of your personal profiles in the editor named by $VISUAL or $EDITOR (the command is split on spaces, quotes group words, and it is never run through a shell), then check the result. Profiles from shared sources are read-only here: copy one with "ccshelf new <name> --from <profile>" and edit the copy.

--path prints the file name instead of opening it, for scripts and for editors you start yourself.

**Usage**

```text
ccshelf edit [profile] [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for edit |
| `--path` |  | print the profile file path instead of opening it |

## ccshelf init

Create config.toml and your personal profiles directory. With --git-url the org data repo becomes a profile source (pin it with --ref: a tag or a full commit id). With --dir another local profiles directory becomes a source. In a terminal, anything you did not pass as a flag is asked for, and the equivalent flag command is printed at the end. Nothing is fetched here: profiles from a shared source are fetched, and need your trust, when you first use them.

Automatic updates are off unless you turn them on: --update-mode notify checks for a newer release once a day and prints one line when there is one, install also installs it (same major version only). In a terminal you are asked once.

**Usage**

```text
ccshelf init [flags]
```

**Examples**

```text
ccshelf init
ccshelf init --git-url git@ghe.example.com:acme/claude-marketplace.git --ref v2026.10.1
ccshelf init --account-name work
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--account-dir` | `string` | directory of that account (default ~/.claude-<name>) |
| `--account-name` | `string` | also create an account with this name (see: ccshelf account add) |
| `--dir` | `string` | absolute directory of profiles to add as a dir source |
| `--force` |  | replace an existing configuration file |
| `--git-url` | `string` | org data repo URL to add as a git source |
| `-h`, `--help` |  | help for init |
| `--path` | `string` | folder inside the repo that holds the profiles (default "profiles") |
| `--ref` | `string` | tag or full commit id the git source is pinned to |
| `--update-mode` | `string` | automatic update mode: off, notify or install (default: asked in a terminal, otherwise off) |

## ccshelf lint

Check the org data repo (--root, default the current directory) against its ccshelf.toml: marketplace entries, catalog sidecars, taxonomy, review dates, CODEOWNERS coverage of hooks and MCP servers, profile manifests and their profile-* bundle entries.

Formats: text (default), json (same as the global --json: the common {"version","kind":"lint","data":{"summary","findings"}} envelope) and github (workflow annotations, one ::error/::warning/::notice line per finding). Exit code 1 when there is any error finding (with --strict, any warning too).

**Usage**

```text
ccshelf lint [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--format` | `string` | output format: text, json or github (default "text") |
| `-h`, `--help` |  | help for lint |
| `--strict` |  | fail on warnings too |

## ccshelf ls

List the profiles of every source: your personal directory, the configured sources and, only when trusted, the repository's .ccshelf folder. Invalid profiles are listed with their error. Use --json for a stable machine-readable form.

**Usage**

```text
ccshelf ls [flags]
```

**Aliases:** `ls`, `list`

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for ls |
| `--refresh` |  | re-resolve git tags on the remote instead of using the commits the trust lockfile pins |

## ccshelf new

Create a personal profile file in your profiles directory. --from names a parent profile to extend (repeatable). In a terminal, anything not given as a flag is asked for, and the equivalent flag command is printed at the end.

**Usage**

```text
ccshelf new [name] [flags]
```

**Examples**

```text
ccshelf new sre-night --from base --plugin sre-kit@acme
ccshelf new notes --description "Writing and notes" --skill-off legacy-helper
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--description` | `string` | one-line description |
| `--effort` | `string` | default effort level |
| `--exclude-plugin` | `stringArray` | plugin to always mask, name@marketplace (repeatable) |
| `--from` | `stringArray` | parent profile to extend (repeatable) |
| `-h`, `--help` |  | help for new |
| `--mcp` | `stringArray` | MCP server from the registry to add (repeatable) |
| `--model` | `string` | default model |
| `--owner` | `string` | owner (team or person) |
| `--plugin` | `stringArray` | plugin to include, name@marketplace (repeatable) |
| `--skill-off` | `stringArray` | standalone skill to turn off (repeatable) |

## ccshelf recommend

Look at a project directory (--dir, default the current directory) and suggest plugins and profiles of the org data repo (--root, default the current directory) whose relevance signals and when_to_use text match it. The rules are deterministic; there is no model call and no network access. Only file names and a few small manifest files of the project are read.

Outside an org data repo (the marketplace file of ccshelf.toml cannot be read) and without --root, it uses the catalog data of the organization's source from config.toml, as search does (verified local cache, never a fetch). When no such catalog is available it fails with exit 1, like lint and compile.

**Usage**

```text
ccshelf recommend [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--dir` | `string` | project directory to look at (default the current directory) |
| `-h`, `--help` |  | help for recommend |
| `--limit` | `int` | show at most this many suggestions (0 for all) (default 10) |

## ccshelf run

Start claude with a profile: the plugins, skills and MCP servers it names.

Arguments after the profile name (and after -- when no profile is given) are passed to claude unchanged, so flags of ccshelf itself must come before the profile name: ccshelf run --account work sre --resume.

Without a profile name, a terminal gets a picker; anything else exits with code 2. A profile from a shared source must be trusted first (exit code 4 otherwise); --yes never accepts trust, only an interactive confirmation of the printed closure or "ccshelf trust <profile> --accept <closure-hash>" does.

**Usage**

```text
ccshelf run [profile] [-- claude args]
```

**Examples**

```text
ccshelf run sre
ccshelf run sre -- -p "summarize this repo"
ccshelf run --account personal sre --resume
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for run |
| `--yes` |  | answer yes to confirmations other than trust (trust is never auto-accepted) |

## ccshelf search

Search the catalog of the org data repo (--root, default the current directory) locally and offline. Every word of the query must match a field (name, display name, tags, category, when_to_use, description or owner); better matches come first, ties by name.

Outside an org data repo (the marketplace file of ccshelf.toml cannot be read) and without --root, it reads the catalog data of the organization's source from config.toml instead, so a developer who reaches the org through a git source needs no checkout: a dir source as it is, a git source from its verified local cache (the commit pinned by the trust lockfile, else the newest cached one; it never fetches, so run "ccshelf ls" or "ccshelf trust" once). The note on stderr says which one was used. When no such catalog is available it fails with exit 1, like lint and compile, instead of reporting "no match". A query that matches nothing in a real catalog is not an error.

**Usage**

```text
ccshelf search <query> [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for search |
| `--limit` | `int` | show at most this many matches (0 for all) (default 10) |

## ccshelf shell-init

Print one function per profile (cs-<profile>) that runs "ccshelf run <profile>" with the remaining arguments passed through. Load it with:

```text
eval "$(ccshelf shell-init zsh)"          bash, zsh
ccshelf shell-init fish | source          fish
ccshelf shell-init pwsh | Out-String | Invoke-Expression
```

Only profiles from local directories are listed, so starting a shell never touches the network; add others with --profile. For cmd.exe, --write-cmd-shims <dir> writes cs-<profile>.cmd files into a directory on PATH.

**Usage**

```text
ccshelf shell-init [bash|zsh|fish|pwsh|cmd] [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for shell-init |
| `--profile` | `stringArray` | also define a function for this profile name (repeatable) |
| `--write-cmd-shims` | `string` | write cs-<profile>.cmd files into this directory (cmd.exe) |

## ccshelf show

Show a profile as it would run: the extends chain, plugins, skills, MCP servers, session defaults, warnings and the closure that a trust decision pins, plus the trust state. Environment values are never printed. Use --json for a stable, versioned machine-readable form.

**Usage**

```text
ccshelf show [profile] [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for show |

## ccshelf trust

Show what a profile's closure runs (MCP servers, prompts, plugins, environment names) and record it as trusted. In a terminal you are asked to confirm what is shown. For scripts, name exactly what you accept:

```text
ccshelf trust <profile> --accept <closure-hash>
```

The hash is printed by "ccshelf trust <profile>" and "ccshelf show <profile>". --yes does not exist here: trust is never accepted by default.

--project reviews the repository's .ccshelf folder (loaded only when trust.trust_project_profiles is true); --accept then takes the folder hash. --revoke removes the records of a profile (or of the project folder).

**Usage**

```text
ccshelf trust [profile] [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--accept` | `string` | closure hash (or, with --project, folder hash) you accept |
| `-h`, `--help` |  | help for trust |
| `--project` |  | act on the repository's .ccshelf folder |
| `--revoke` |  | remove the trust records instead |

## ccshelf update

Download the newest ccshelf release, verify it and replace this binary.

The archive's SHA-256 must match checksums.txt of the same release, fetched over HTTPS without credentials. If cosign is on PATH the keyless signature of checksums.txt is verified as well, against the project's release workflow, and a mismatch stops the update; --require-signature makes a missing cosign an error. The previous binary is kept as <name>.old; --rollback restores it.

Nothing contacts the network unless you run this command or set [update] mode in config.toml (off by default). A copy installed by Homebrew, Scoop, WinGet, "go install" or a system package, and development builds, are not replaced unless you pass --force; the right command is printed instead.

**Usage**

```text
ccshelf update [flags]
```

**Examples**

```text
ccshelf update --check
ccshelf update
ccshelf update --version v0.2.0 --yes
ccshelf update --rollback
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--allow-downgrade` |  | allow --version to install an older release |
| `--check` |  | only report the current and latest version (exit 0 either way) |
| `--dry-run` |  | show what would be downloaded, verified and replaced; change nothing |
| `--force` |  | also replace a package-managed or development build, or reinstall the same version |
| `-h`, `--help` |  | help for update |
| `--prerelease` |  | consider pre-releases when looking for the latest |
| `--require-signature` |  | fail unless cosign is available to verify the release signature |
| `--rollback` |  | restore the previous binary kept by the last update |
| `--version` | `string` | install this release (for example v0.2.0) instead of the latest |
| `--yes` |  | do not ask for confirmation |

## ccshelf version

Print the ccshelf version

**Usage**

```text
ccshelf version [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for version |

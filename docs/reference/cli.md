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
| `--no-interactive` |  | never prompt, and fail with the name of the missing flag |
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
| [`ccshelf diff`](#ccshelf-diff) | Compare two resolved profiles |
| [`ccshelf dry-run`](#ccshelf-dry-run) | Print the exact claude command a run would execute |
| [`ccshelf edit`](#ccshelf-edit) | Open a personal profile in $VISUAL or $EDITOR |
| [`ccshelf ls`](#ccshelf-ls) | List the profiles of all sources |
| [`ccshelf new`](#ccshelf-new) | Create a user or project profile |
| [`ccshelf run`](#ccshelf-run) | Start claude with a profile |
| [`ccshelf show`](#ccshelf-show) | Show a resolved profile: parents merged, MCP servers, closure |
| [`ccshelf trust`](#ccshelf-trust) | Review and trust a shared profile (or a project folder) |
| [`ccshelf doctor`](#ccshelf-doctor) | Check the org's catalog and profiles for overlap, staleness and policy conflicts |
| [`ccshelf recommend`](#ccshelf-recommend) | Suggest plugins and profiles for a project directory (rule based) |
| [`ccshelf search`](#ccshelf-search) | Search the plugin catalog of the org data repo |
| [`ccshelf catalog`](#ccshelf-catalog) | Set up the org data repo and build its plugin catalog |
| [`ccshelf catalog build`](#ccshelf-catalog-build) | Write catalog.json, CATALOG.md and the static site |
| [`ccshelf catalog init`](#ccshelf-catalog-init) | Set up a new org data repo, or add the missing pieces to an existing marketplace repo |
| [`ccshelf compile`](#ccshelf-compile) | Generate the profile-* bundle plugins from the profile manifests |
| [`ccshelf lint`](#ccshelf-lint) | Check the org data repo: marketplace, sidecars, ownership and profiles |
| [`ccshelf account`](#ccshelf-account) | Manage named Claude Code accounts (separate CLAUDE_CONFIG_DIR) |
| [`ccshelf account add`](#ccshelf-account-add) | Add an account and print the one-time steps to log in |
| [`ccshelf account ls`](#ccshelf-account-ls) | List the configured accounts |
| [`ccshelf account rm`](#ccshelf-account-rm) | Forget an account (its directory is left alone) |
| [`ccshelf completion`](#ccshelf-completion) | Print a shell completion script |
| [`ccshelf config`](#ccshelf-config) | Show and change the configuration file |
| [`ccshelf config edit`](#ccshelf-config-edit) | Edit config.toml in $VISUAL or $EDITOR, checked before it replaces the file |
| [`ccshelf config path`](#ccshelf-config-path) | Print the configuration file path in use |
| [`ccshelf config set`](#ccshelf-config-set) | Change one setting |
| [`ccshelf config show`](#ccshelf-config-show) | Print the effective configuration |
| [`ccshelf config source`](#ccshelf-config-source) | List, add, pin and remove profile sources |
| [`ccshelf config source add`](#ccshelf-config-source-add) | Add a profile source (git, dir or plugin) |
| [`ccshelf config source ls`](#ccshelf-config-source-ls) | List the configured profile sources |
| [`ccshelf config source pin`](#ccshelf-config-source-pin) | Change the pinned ref or the tracked branch of git source number n |
| [`ccshelf config source rm`](#ccshelf-config-source-rm) | Remove source number n |
| [`ccshelf config unset`](#ccshelf-config-unset) | Put one setting back to its default |
| [`ccshelf init`](#ccshelf-init) | Create the configuration file, optionally from an org data repo |
| [`ccshelf shell-init`](#ccshelf-shell-init) | Print shell functions cs-<profile> that run each profile |
| [`ccshelf update`](#ccshelf-update) | Update ccshelf to the latest release (verified), or roll back |
| [`ccshelf version`](#ccshelf-version) | Print the ccshelf version |

## ccshelf diff

Compare two profiles after their parents are merged: plugins, skills, MCP servers, environment variable names and session defaults. "+" marks what b adds to a and "-" what b lacks. Environment values and prompt text are never printed. The exit code is 0 whether or not they differ. --json has an "identical" field.

**Usage**

```text
ccshelf diff [profile-a] [profile-b] [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for diff |

## ccshelf dry-run

Run the whole pipeline of "run" (including the trust check) and print the exact claude command instead of starting it. The command writes the generated settings, MCP config and prompt files to the private cache, so the printed command is valid. It never prints environment values from profiles.

**Usage**

```text
ccshelf dry-run [profile] [-- claude args]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for dry-run |
| `--refresh` |  | check tracked branches for a new commit now, not only once per trust.branch_check_interval |

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
| `--refresh` |  | re-resolve git tags and branches on the remote instead of using the commits the trust lockfile pins |

## ccshelf new

Create a separate TOML file in your user profiles directory (default), or with --scope project in the Git root/.ccshelf/profiles (cwd outside Git). --from names a parent profile to extend (repeatable). With no content flags, a terminal offers a wizard and prints its equivalent flag command. Creation never enables or trusts project profiles. --yes confirms creation only.

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
| `--scope` | `string` | profile location: user or project (default "user") |
| `--skill-off` | `stringArray` | standalone skill to turn off (repeatable) |
| `--yes` |  | skip creation confirmation only (never accepts trust) |

## ccshelf run

Start claude with a profile: the plugins, skills and MCP servers it names.

ccshelf passes the arguments after the profile name (and after -- when no profile is given) to claude unchanged. Thus flags of ccshelf itself must come before the profile name: ccshelf run --account work sre --resume.

Without a profile name, a terminal gets a picker. Anything else exits with code 2. You must trust a profile from a shared source first (exit code 4 otherwise). --yes never accepts trust. Only an interactive confirmation of the printed closure or "ccshelf trust <profile> --accept <closure-hash>" accepts it.

A git source that tracks a branch runs the commit you trusted, with no network. At most once per trust.branch_check_interval (default 24h), ccshelf asks the remote for the head of the branch. If the head moved, a terminal shows what changed and asks. Without a terminal, or with --yes, ccshelf keeps the trusted commit and prints the command that reviews the update. --refresh checks now.

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
| `--refresh` |  | check tracked branches for a new commit now, not only once per trust.branch_check_interval |
| `--yes` |  | answer yes to confirmations other than trust (trust is never auto-accepted) |

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

Show what a profile's closure runs (MCP servers, prompts, plugins, environment names) and record it as trusted. In a terminal, the command asks you to confirm what it shows. For scripts, name exactly what you accept:

```text
ccshelf trust <profile> --accept <closure-hash>
```

"ccshelf trust <profile>" and "ccshelf show <profile>" print the hash. --yes does not exist here: trust is never accepted by default.

--project reviews the repository's .ccshelf folder (loaded only when trust.trust_project_profiles is true). With --project, --accept takes the folder hash. --revoke removes the records of a profile (or of the project folder).

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

## ccshelf doctor

Analyze the org data repo (--root, default the current directory): overlapping plugins, plugins in no profile, deprecated plugins in use, stale or missing review dates and owners, protected plugins that a profile masks, and plugins that need platform review.

The command skips the checks that need more input and lists them as skipped. It never passes them silently. These flags give the input:

```text
- --installed reads 'claude plugin list' (read only).
- --usage-file reads an OpenTelemetry JSONL export.
- --usage-api asks the Enterprise Analytics API. It needs the admin key in
  CCSHELF_ANALYTICS_KEY. This is the only network call.
- --skills-dir lists the standalone skills in a directory (read only). Each
  subdirectory with a SKILL.md is a skill. ccshelf never reads ~/.claude on
  its own.
- --policy reads the machine's managed Claude Code policy (read only, never
  bypassed) and prints the capability matrix.
```

With --policy, the command checks the needs of every profile against the policy that it could read. The needs are extra MCP servers, strict MCP, dropping user settings, a system prompt file, hiding claude.ai connectors, and the marketplaces of its plugins. The exit code is 3 when the policy blocks something a profile needs (POL002, POL005, POL006). A feature whose state is unknown is a warning, never a failure. A policy that exists but could not be read is a warning. It gives exit code 3 only with --strict.

The exit code is 1 for error findings, and when the directory is not an org data repo (its marketplace file cannot be read).

With --policy and no --root, a directory that is not an org data repo is not an error, because the managed policy belongs to the machine, not to a repository. Only the policy part runs: the capability matrix and the policy findings that do not need profiles. The command lists the checks that need the catalog as skipped. A developer who reaches the org through a git source can use it that way.

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

## ccshelf recommend

Look at a project directory (--dir, default the current directory) and suggest plugins and profiles of the org data repo (--root, default the current directory) whose relevance signals and when_to_use text match it. The rules are deterministic and there is no model call. The command reads only file names and a few small manifest files of the project.

Outside an org data repo (the marketplace file of ccshelf.toml cannot be read) and without --root, the command gets [catalog].remote_url of the user config over HTTPS when it is set. Otherwise it uses the catalog data of the organization's source from its local directory or verified git cache. Remote JSON can recommend profiles. Plugin recommendations need marketplace relevance rules from the org data repo.

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

## ccshelf search

Search the catalog of the org data repo (--root, default the current directory). Every word of the query must match a field (name, display name, tags, category, when_to_use, description or owner). Better matches come first. Equal matches sort by name.

Outside an org data repo and without --root, [catalog].remote_url in the user config takes precedence and retrieves catalog.json over HTTPS for this search. Otherwise search reads a configured org source from its local directory or verified git cache. A query that matches nothing in a real catalog is not an error.

**Usage**

```text
ccshelf search <query> [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for search |
| `--limit` | `int` | show at most this many matches (0 for all) (default 10) |

## ccshelf catalog

Set up the org data repo and build its plugin catalog

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
| [`ccshelf catalog init`](#ccshelf-catalog-init) | Set up a new org data repo, or add the missing pieces to an existing marketplace repo |

## ccshelf catalog build

Build the catalog from the org data repo and write it into --out (default dist/catalog, relative to the current directory):

```text
- catalog.json
- CATALOG.md
- the static site (index.html, app.js, style.css), unless --no-site
```

With --no-site, the command removes the files of an earlier site build from --out. The command writes nothing outside --out. The output is published files: directories 0755, files 0644.

--out must not:

```text
- be the repo root or contain it
- be inside the repo's source directories (profiles, bundles, catalog,
  plugins, .github and so on)
- pass through a symbolic link below the working directory or the repo root
```

A directory that is not an org data repo (its marketplace file cannot be read or parsed) is an error (exit 1), and the command writes nothing.

The catalog has no timestamp unless you give --timestamp, so the output is reproducible. The command prints lint findings to stderr. A repo with lint errors still produces its catalog (for previews), but the command exits 1.

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

## ccshelf catalog init

Bootstrap an org data repo in dir (default: --root, else the current directory). This is the organization's setup. "ccshelf init" is each developer's own.

Modes (detected, or forced with --mode):

```text
new    an empty or missing directory (a lone .git counts as empty).
adopt  a directory with content, such as a marketplace repo that has
       .claude-plugin/marketplace.json and/or plugins/. The command changes
       nothing that exists. It prints a plan and only creates the files
       that are missing. It reports an existing file as one of these:
         - skip-exists (and up to date when it already equals what the
           command would write)
         - needs-merge for CODEOWNERS, .gitattributes and .gitignore, with
           the lines to add. --write-suggestions saves them next to the
           file as <name>.ccshelf-suggested.
       The command never rewrites a marketplace.json. --force replaces the
       other differing files after it saves <file>.bak.
```

Generated files (a --no-<group> flag leaves out its group):

```text
- ccshelf.toml
- .claude-plugin/marketplace.json: a skeleton. In adopt mode, it lists the
  plugins found under plugins/.
- catalog/plugins/<name>.toml: a stub per plugin with the owner from
  CODEOWNERS or --owner, status experimental, and TODO(ccshelf)
  placeholders that ccshelf lint reports as warnings (CAT048).
- .github/CODEOWNERS
- .github/workflows/{validate,catalog,release}.yml
- README.md, .gitattributes and .gitignore
```

ccshelf has no built-in profiles. The command writes profiles/example.toml.sample, an all-comment sample, only with --example-profile.

--profiles-only sets up an org data repo that holds profiles (and prompts/ and mcp/registry.toml) but no plugin marketplace and no catalog. ccshelf.toml gets [catalog] enabled = false. The command writes ccshelf.toml, .github/CODEOWNERS, a README.md, .gitattributes, .gitignore and only the validate workflow (lint). It writes no marketplace.json, sidecars, catalog.yml or release.yml. These are usage errors with --profiles-only:

```text
- --marketplace-name, --owner and --sidecars stub
- a directory that already has a marketplace file
```

An existing ccshelf.toml must already have [catalog] enabled = false. Add it yourself, or use --force to replace the file. The command refuses --no-config unless the file has it.

The workflows call the ccshelf action pinned by full commit SHA. Pass --ccshelf-ref <40-hex SHA> and --ccshelf-version <vX.Y.Z> to pin it. A tag given as --ccshelf-ref sets the version only. Without them, the workflows contain a placeholder and fail with a clear message until you pin them.

Flags are the contract: you can give every value as a flag. In a terminal, the command asks for the missing values, shows the plan and prints the equivalent flag command. Without a terminal, a missing value is a usage error (exit 2) that names the flag. Writing needs --yes (or --dry-run to print the plan). The command commits, pushes and fetches nothing. --git-init only runs git init when the directory is not a repository yet.

**Usage**

```text
ccshelf catalog init [dir] [flags]
```

**Examples**

```text
ccshelf catalog init ./acme-claude --marketplace-name acme --org "Acme Corp" --platform-owners @acme/platform --yes
ccshelf catalog init . --platform-owners @acme/platform --dry-run
ccshelf catalog init ./acme-profiles --profiles-only --platform-owners @acme/platform --yes
ccshelf catalog init . --mode adopt --platform-owners @acme/platform --write-suggestions --yes
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--ccshelf-ref` | `string` | full 40-hex commit SHA of the ccshelf action to pin, or a release tag vX.Y.Z (sets the version only) |
| `--ccshelf-version` | `string` | ccshelf release tag the action installs, such as v0.1.0 |
| `--default-branch` | `string` | default branch the catalog workflow publishes from (default: read from an existing repository, else main) |
| `--dry-run` |  | print the plan and write nothing |
| `--example-profile` |  | also write profiles/example.toml.sample, an all-comment sample |
| `--force` |  | replace existing files that differ, after saving <file>.bak (never marketplace.json) |
| `--git-init` |  | run git init when the directory is not a git repository (nothing else of git) |
| `-h`, `--help` |  | help for init |
| `--marketplace-name` | `string` | marketplace name (lower case letters, digits and hyphens) |
| `--mode` | `string` | new or adopt (default: detected from the directory) |
| `--no-codeowners` |  | do not generate .github/CODEOWNERS |
| `--no-config` |  | do not generate ccshelf.toml |
| `--no-gitattributes` |  | do not generate .gitattributes |
| `--no-gitignore` |  | do not generate .gitignore |
| `--no-marketplace` |  | do not generate .claude-plugin/marketplace.json |
| `--no-readme` |  | do not generate README.md |
| `--no-sidecars` |  | do not generate catalog/plugins/<name>.toml (same as --sidecars none) |
| `--no-workflows` |  | do not generate the .github/workflows files |
| `--org` | `string` | display name of the organization (default: the marketplace name) |
| `--owner` | `string` | default owner of the plugin sidecars (default: the first platform owner) |
| `--platform-owners` | `strings` | CODEOWNERS owners of everything that runs code or shapes the catalog (@user, @org/team or an email address, repeatable) |
| `--profiles-only` |  | set up a repo of profiles only: no marketplace, sidecars, bundles or catalog (ccshelf.toml gets [catalog] enabled = false, and the command writes only the validate workflow) |
| `--quiet` |  | do not print the suggested lines of the files that need a merge (they stay in --json and in --write-suggestions) |
| `--runner-label` | `string` | fallback of runs-on in the workflows when the RUNNER_LABEL variable is unset (default ubuntu-latest) |
| `--sidecars` | `string` | sidecar files for the plugins found: stub or none (default "stub") |
| `--write-suggestions` |  | write <name>.ccshelf-suggested files for the files that need a merge |
| `--yes` |  | write without asking for confirmation (never accepts trust) |

## ccshelf compile

Compile every profile of the org data repo into a profile bundle: a plugin under bundles/profile-<name>/ whose dependencies are the profile's resolved plugins. The command skips profiles that resolve to no plugins (abstract bases).

The whole bundles/ tree is generated output. The command removes the files that no profile produces. With --check, the command writes nothing. It prints the differences and exits 1 when the committed bundles are stale.

**Usage**

```text
ccshelf compile [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--check` |  | write nothing, and exit 1 when the bundles are stale |
| `-h`, `--help` |  | help for compile |

## ccshelf lint

Check the org data repo (--root, default the current directory) against its ccshelf.toml: marketplace entries, catalog sidecars, taxonomy, review dates, CODEOWNERS coverage of hooks and MCP servers, profile manifests and their profile-* bundle entries.

Formats: text (default), json (same as the global --json: the common {"version","kind":"lint","data":{"summary","findings"}} envelope) and github (workflow annotations, one ::error/::warning/::notice line per finding). The exit code is 1 when there is any error finding (with --strict, also any warning).

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

## ccshelf config

Show the effective configuration, and add, pin or remove profile sources and change a few settings in place, without editing config.toml by hand. Every subcommand works with flags alone. In a terminal, "ccshelf config" alone opens a menu, and the command shows a change and asks you to confirm it (default no) before it writes it.

The command writes a change by encoding the file again. Comments and layout are lost, and the command keeps the previous file as config.toml.bak. When there is no terminal, a change that weakens a security setting needs --yes. These changes weaken a security setting:

```text
- turning off pinning
- trusting project profiles
- installing updates automatically
- adding a source that tracks a branch, is not pinned or has a path that is
  a variable
- changing claude.path or the update source
```

Nothing here fetches a source or records trust. Only "ccshelf init" creates the file.

Settings you can change with "config set": trust.on_change, trust.require_pin, trust.trust_project_profiles, trust.branch_check_interval, update.mode, update.interval, catalog.remote_url, default_account, ui.color, ui.interactive. Anything else (accounts, claude.path, the update source) has its own command or needs "ccshelf config edit".

**Usage**

```text
ccshelf config [flags]
ccshelf config [command]
```

**Examples**

```text
ccshelf config show
ccshelf config source add --git-url git@ghe.example.com:acme/claude-marketplace.git --ref v2026.10.1
ccshelf config source pin 1 --ref v2026.11.0
ccshelf config set update.mode notify
ccshelf config edit
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for config |

**Subcommands**

| Command | What it does |
|---|---|
| [`ccshelf config edit`](#ccshelf-config-edit) | Edit config.toml in $VISUAL or $EDITOR, checked before it replaces the file |
| [`ccshelf config path`](#ccshelf-config-path) | Print the configuration file path in use |
| [`ccshelf config set`](#ccshelf-config-set) | Change one setting |
| [`ccshelf config show`](#ccshelf-config-show) | Print the effective configuration |
| [`ccshelf config source`](#ccshelf-config-source) | List, add, pin and remove profile sources |
| [`ccshelf config unset`](#ccshelf-config-unset) | Put one setting back to its default |

## ccshelf config edit

Open a copy of config.toml (mode 0600, in the same folder) in the editor named by $VISUAL or $EDITOR (split on spaces, quotes group words, never run through a shell). When the editor exits, the command checks the copy like the real file. The copy replaces config.toml only if it is valid. Comments are kept, because the command writes the text as you saved it. It keeps the previous file as config.toml.bak. If the copy is invalid, the command prints the errors with line numbers, does not touch config.toml and keeps the copy so you can fix it. The command needs a terminal and an existing file (ccshelf init creates it).

--path prints the file name instead, for scripts and for editors you start yourself.

**Usage**

```text
ccshelf config edit [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for edit |
| `--path` |  | print the configuration file path instead of opening it |
| `--yes` |  | confirm the write after the check, but never accept trust |

## ccshelf config path

Print the path of config.toml (the --config file when given). The file need not exist.

**Usage**

```text
ccshelf config path [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for path |

## ccshelf config set

Change one setting of the configuration. The keys are: trust.on_change, trust.require_pin, trust.trust_project_profiles, trust.branch_check_interval, update.mode, update.interval, catalog.remote_url, default_account, ui.color, ui.interactive. Values:

```text
- true or false
- trust.on_change: prompt or fail
- update.mode: off, notify or install
- update.interval: a duration such as 24h (1h to one year)
- trust.branch_check_interval: a duration such as 24h (1h to one year)
- catalog.remote_url: an https URL
- default_account: an account name
- ui.color: auto, always or never
- ui.interactive: auto or never
```

The command refuses anything else. Accounts and sources have their own commands, and the rest needs "config edit".

Turning trust.require_pin off, trust.trust_project_profiles on, or update.mode to install weakens a security setting and needs --yes without a terminal.

**Usage**

```text
ccshelf config set [key] [value] [flags]
```

**Examples**

```text
ccshelf config set update.mode notify
ccshelf config set update.interval 48h
ccshelf config set trust.on_change fail
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for set |
| `--yes` |  | confirm the write (and a weakening change), but never accept trust |

## ccshelf config show

Print the sources (numbered from 1, with the numbers that config source pin and rm take), trust, update, catalog, accounts and ui settings. The command marks a value that the file does not set with (default). A missing file shows the defaults.

**Usage**

```text
ccshelf config show [flags]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for show |

## ccshelf config source

Change the [[sources]] of the configuration. Sources are numbered from 1 in the order of "config source ls". The order carries no meaning. The command fetches nothing and records no trust. ccshelf fetches shared profiles when you first use them, and they need your trust then.

**Usage**

```text
ccshelf config source [command]
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for source |

**Subcommands**

| Command | What it does |
|---|---|
| [`ccshelf config source add`](#ccshelf-config-source-add) | Add a profile source (git, dir or plugin) |
| [`ccshelf config source ls`](#ccshelf-config-source-ls) | List the configured profile sources |
| [`ccshelf config source pin`](#ccshelf-config-source-pin) | Change the pinned ref or the tracked branch of git source number n |
| [`ccshelf config source rm`](#ccshelf-config-source-rm) | Remove source number n |

## ccshelf config source add

Add one profile source with exactly one of these flags:

```text
- --git-url, with --ref or --branch, and --path for the folder inside the
  repository
- --dir, an absolute directory
- --plugin (name@marketplace), with --marketplace and --path
```

The command checks the source with the rules of the configuration file and refuses an exact duplicate. For a duplicate, use "config source pin" or "config source rm". The command fetches and trusts nothing.

In a terminal, without any of those flags, the command asks the questions.

A source with --branch follows a branch, which can move. A run still uses the commit you trusted, and every new commit needs your trust again. Adding one weakens a setting and needs --yes without a terminal. The same applies to a git source that is not pinned to a tag or full commit. That source is possible only while trust.require_pin is off.

**Usage**

```text
ccshelf config source add [flags]
```

**Examples**

```text
ccshelf config source add --git-url git@ghe.example.com:acme/claude-marketplace.git --ref v2026.10.1
ccshelf config source add --git-url git@ghe.example.com:acme/claude-marketplace.git --branch main --yes
ccshelf config source add --dir ~/team-profiles
ccshelf config source add --plugin org-profiles@acme --marketplace acme/claude-marketplace
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--branch` | `string` | git source: branch to track, instead of --ref |
| `--dir` | `string` | dir source: absolute directory of profiles |
| `--git-url` | `string` | git source: repository URL |
| `-h`, `--help` |  | help for add |
| `--marketplace` | `string` | plugin source: the marketplace source the plugin must come from (owner/repo or git URL) |
| `--path` | `string` | git or plugin source: folder inside the repository or plugin (git default: profiles) |
| `--plugin` | `string` | plugin source: name@marketplace |
| `--ref` | `string` | git source: tag or full commit id to pin to |
| `--yes` |  | confirm the write (and a weakening change), but never accept trust |

## ccshelf config source ls

List the [[sources]] with the numbers that config source pin and rm take. ccshelf always searches your personal profiles directory, and the list does not show it.

**Usage**

```text
ccshelf config source ls [flags]
```

**Aliases:** `ls`, `list`

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for ls |

## ccshelf config source pin

Change the tag or full commit id that git source number n (see config source ls) is pinned to, or make it track a branch with --branch. Give --ref or --branch, not both. A source can switch between a tag or commit and a branch. A branch can move, so a switch to a branch weakens a setting and needs --yes without a terminal. The next run that needs a profile from that source fetches the new ref. When its commit differs from the one you trusted, the profile needs your trust again. This command never records trust. Pinning to something that is not a tag or full commit is possible only while trust.require_pin is off, and it needs --yes without a terminal.

**Usage**

```text
ccshelf config source pin [n] [flags]
```

**Examples**

```text
ccshelf config source pin 1 --ref v2026.11.0
ccshelf config source pin 1 --branch main --yes
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--branch` | `string` | the branch to track, instead of --ref |
| `-h`, `--help` |  | help for pin |
| `--ref` | `string` | the new tag or full commit id |
| `--yes` |  | confirm the write (and a weakening change), but never accept trust |

## ccshelf config source rm

Remove the source with number n (see config source ls). Profiles from it no longer appear. The command keeps the trust records already made for its profiles as they are, and does not delete them.

**Usage**

```text
ccshelf config source rm [n] [flags]
```

**Aliases:** `rm`, `remove`

**Examples**

```text
ccshelf config source rm 2
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for rm |
| `--yes` |  | confirm the write, but never accept trust |

## ccshelf config unset

Put one setting back to its default (the same keys as config set). The command removes the key from the file where it can. Otherwise it writes the key with its default.

**Usage**

```text
ccshelf config unset [key] [flags]
```

**Examples**

```text
ccshelf config unset update.interval
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `-h`, `--help` |  | help for unset |
| `--yes` |  | confirm the write, but never accept trust |

## ccshelf init

Create config.toml and your personal profiles directory. With --git-url the org data repo becomes a profile source. Pin it with --ref (a tag or a full commit id), or track a branch with --branch. A branch can move: ccshelf runs the commit you trusted, and every new commit on the branch needs your trust again. With --dir another local profiles directory becomes a source. In a terminal, running without source, account or update values starts the full setup wizard. It shows a summary and asks before writing (default no). Partial flag runs do not prompt. --yes confirms writing only, never trust. The wizard prints an equivalent flag command at the end. Nothing is fetched here. ccshelf fetches profiles from a shared source when you first use them, and they need your trust then.

Automatic updates are off unless you turn them on:

```text
- --update-mode notify checks for a newer release once a day and prints one
  line when there is one.
- --update-mode install also installs it (same major version only).
```

The full wizard asks once.

**Usage**

```text
ccshelf init [flags]
```

**Examples**

```text
ccshelf init
ccshelf init --git-url git@ghe.example.com:acme/claude-marketplace.git --ref v2026.10.1
ccshelf init --git-url git@ghe.example.com:acme/claude-marketplace.git --branch main
ccshelf init --account-name work
```

**Flags**

| Flag | Value | Description |
|---|---|---|
| `--account-dir` | `string` | directory of that account (default ~/.claude-<name>) |
| `--account-name` | `string` | also create an account with this name (see: ccshelf account add) |
| `--branch` | `string` | branch the git source tracks, instead of --ref; every new commit needs trust again |
| `--dir` | `string` | absolute directory of profiles to add as a dir source |
| `--force` |  | replace an existing configuration file |
| `--git-url` | `string` | org data repo URL to add as a git source |
| `-h`, `--help` |  | help for init |
| `--path` | `string` | folder inside the repo that holds the profiles (default "profiles") |
| `--ref` | `string` | tag or full commit id the git source is pinned to |
| `--update-mode` | `string` | automatic update mode: off, notify or install (default: asked in the full wizard, otherwise off) |
| `--yes` |  | confirm writing in the full wizard (never accepts trust) |

## ccshelf shell-init

Print one function per profile (cs-<profile>) that runs "ccshelf run <profile>" with the remaining arguments passed through. Load it with:

```text
eval "$(ccshelf shell-init zsh)"          bash, zsh
ccshelf shell-init fish | source          fish
ccshelf shell-init pwsh | Out-String | Invoke-Expression
```

Only profiles from local directories are listed, so starting a shell never touches the network. Add others with --profile. For cmd.exe, --write-cmd-shims <dir> writes cs-<profile>.cmd files into a directory on PATH.

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

## ccshelf update

Download the newest ccshelf release, verify it and replace this binary.

The archive's SHA-256 must match checksums.txt of the same release. The command gets checksums.txt over HTTPS without credentials. If cosign is on PATH, the command also verifies the keyless signature of checksums.txt against the project's release workflow. A mismatch stops the update. With --require-signature, a missing cosign is an error. The command keeps the previous binary as <name>.old. --rollback restores it.

Nothing contacts the network unless you run this command or set [update] mode in config.toml (off by default). Without --force, the command does not replace these, and prints the correct command instead:

```text
- a copy installed by Homebrew, Scoop, WinGet, "go install" or a system package
- a development build
```

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
| `--dry-run` |  | show what the update would download, verify and replace, and change nothing |
| `--force` |  | also replace a package-managed or development build, or reinstall the same version (never installs an older release) |
| `-h`, `--help` |  | help for update |
| `--prerelease` |  | consider pre-releases when looking for the latest |
| `--require-signature` |  | fail (before anything else) unless cosign is available to verify the release signature |
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

# 07. How the launcher invokes Claude Code, what is shared, and combining with accounts

Design description. Stage 0 (macOS only) tested masking via `--settings`, `--setting-sources`, `--strict-mcp-config`, auth under those flags, and parallel runs. Everything else here is design intent or inference from the docs, marked as such. `ccprofiles` is the chosen command name.

## 1. Invocation flow: `ccprofiles run sre -- --resume`
The launcher is a compiler plus a process starter. It is never in the data path: it doesn't proxy or intercept the conversation.

1. **Resolve the profile** (pure file work): read config and the source list, find `sre`, follow `extends`, produce one resolved profile (plugins, skill overrides, MCP servers, session defaults).
2. **Look at the machine** (read-only):
   - `claude --version` for feature gating.
   - `claude plugin list --json` for installed plugin ids, install paths and scopes.
   - Managed-settings files and other policy signals.
   - Report profile plugins that aren't installed, with the install command. Never install silently.
3. **Compile into generated files**:
   - A settings file with `enabledPlugins` set to `false` for every installed plugin not in the profile (default-deny, regenerated each run so new installs don't leak), `true` for the profile's plugins (whether `true` is needed is untested), `skillOverrides` for standalone skills, and `env`/`model`/`effort` defaults.
   - An MCP config file when the profile uses `strict` (its servers, or an empty list).
   - Files are named by a hash of their content and written atomically into the launcher's cache dir, so concurrent runs share or never collide.
   ```json
   {
     "enabledPlugins": {
       "design-kit@acme": true, "sre-kit@acme": true,
       "seo-tools@acme": false, "frontend-design@claude-plugins-official": false
     },
     "skillOverrides": { "legacy-helper": "off" },
     "env": { "CCPROFILES_PROFILE": "sre" }
   }
   ```
4. **Build the command line**:
   ```
   claude --settings <cache>/settings-<hash>.json
          --strict-mcp-config --mcp-config <cache>/mcp-<hash>.json   # if strict
          --model opus --effort high                                  # if the profile sets them
          --resume                                                    # passthrough args
   ```
   With `inherit_user_settings = false` it adds `--setting-sources project,local` (drops the user layer; the launcher re-adds only what the profile sets). If policy blocks a flag, behavior follows `[policy] on_blocked` (`warn` drops that part, `fail` refuses). The system-prompt addition is passed as text via `--append-system-prompt` (a file-based flag was not verified).
5. **Start `claude` and step aside**: spawn with inherited stdin/stdout/stderr (a normal TTY), ignore Ctrl+C in the launcher (the child handles it), forward termination, wait, exit with Claude's exit code. Passthrough works for non-interactive use: `ccprofiles run sre -- -p "summarize this repo"`.
6. **Write nothing shared**: it never touches `~/.claude/settings.json`, `~/.claude.json` or the plugin cache (Claude Code rewrites its own state as usual).

Concurrency: two terminals produce two command lines pointing at two immutable files, so neither reads anything the other wrote (tested on macOS with three parallel sessions).

What breaks if Claude Code changes: the design depends on `--settings` honoring `enabledPlugins`/`skillOverrides`, on `--strict-mcp-config`, and on `claude plugin list --json`. Gate on `claude --version`, and run the real-`claude` init-event test in CI against new releases.

## 2. What is shared between profiles
The launcher never sets `CLAUDE_CONFIG_DIR`, so all profiles use the same `~/.claude` and `~/.claude.json`. Auth and parallel runs were tested; the rest is inferred from how `CLAUDE_CONFIG_DIR` is documented.

| Thing | Shared? | Notes |
|---|---|---|
| Auth (login) | Yes | Tested: OAuth works with the flags used. Log in once. |
| Projects and folder trust | Yes | Common per-project state in `~/.claude.json`. |
| Session history | Yes, mixed | Transcripts are per project, not tagged by profile; `/resume` lists sessions from all profiles. Resuming under a different profile loads that session with different plugins (untested). |
| Auto memory and CLAUDE.md | Yes | Per project, not per profile. Per-profile memory is not possible without a separate config dir (#91770 asks for it natively). **Decision: shared is fine.** |
| User settings | Yes by default | Permissions, hooks, model, statusline carry over because masking merges per key. With `inherit_user_settings = false` they are dropped; auth still works (tested). |
| Plugin install cache | Yes | One copy of each plugin; profiles only change what's enabled. Avoids the duplication and "corrupted" warnings of config-dir-per-profile. |
| claude.ai connector auth | Yes | Connected once; `strict` hides connectors for a session without affecting their auth. |

Not hidden by a profile: a repo's own `.claude/skills/`, `.claude/agents/` and `.claude/settings.json` still apply, and so does its `.mcp.json` unless the profile uses `strict`, which drops project `.mcp.json` servers too. Plugin masking outranks project settings (`--settings` has higher precedence).

A profile is a **loading filter, not a security or identity boundary**: profiles share credentials, history and memory.

## 3. Combining profiles with accounts (decided: document it)
Two independent axes:
- **Profile** = which plugins/skills/MCP are active (task-based). Launcher feature, shared state.
- **Account** = which Claude Code identity and data directory is used (work vs personal, different login). Done with a separate `CLAUDE_CONFIG_DIR`.

An account switch isolates credentials, user settings, installed plugins and marketplaces, history, memory and plugin cache. A profile then filters within that account.

### Three ways to combine, from least to most built-in
1. **Honor the environment (works from day one).** The launcher inherits the environment, and its own `claude plugin list --json` call must run under the same environment. So `CLAUDE_CONFIG_DIR=~/.claude-work ccprofiles run sre` just works, and so does any existing account switcher (e.g. quinnjr/claude-code-profiles, shell aliases) that sets that variable first. **Requirement:** the launcher must never unset or override an existing `CLAUDE_CONFIG_DIR`.
2. **Built-in accounts (nicer UX).** Config names accounts and the launcher sets the variable for the child:
   ```toml
   # ~/.config/ccprofiles/config.toml
   default_account = "work"            # top-level key; omit to use Claude Code's default dir

   [accounts.work]
   config_dir = "~/.claude-work"       # sets CLAUDE_CONFIG_DIR for the child
   [accounts.personal]
   config_dir = "~/.claude-personal"
   ```
   Usage: `ccprofiles run sre --account personal`. A profile may pin a default with the top-level `account = "work"` field; precedence is CLI flag, then profile field, then `default_account`, then Claude Code's default directory.
   `ccprofiles account add work` creates the dir and prints the one-time steps (run `claude` with the variable set and `/login` inside it). The launcher never copies, reads or moves credentials.
3. **Delegate to an existing tool.** Keep using a config-dir switcher and run `ccprofiles` inside it (way 1).

### What changes per account
- **Installed plugins and marketplaces are per config dir**, so each account must install its plugins (`/plugin marketplace add ...` and installs) separately, and the launcher's installed-plugin list is per account. A profile can list plugins that exist in one account but not another; the launcher reports "not installed in this account".
- **Plugin cache is duplicated per account.** Do not symlink `plugins/` between accounts: that triggers "corrupted" warnings (#82272, #85325). Accept the duplication, or evaluate `CLAUDE_CODE_PLUGIN_SEED_DIR` (a read-only pre-populated plugin dir for containers; plugins still must be enabled) as a way to dedupe. Untested.
- **Org profiles are shared across accounts**, because they come from the profile sources, not from the config dir. The `plugin` source type needs the data-only plugin installed in each account that uses it; `git` and `dir` sources work in any account.
- **Auth storage differs by OS**: macOS Keychain entry is keyed per config dir; Linux and Windows keep `.credentials.json` inside the dir. Either way each account has its own login.
- **Managed settings still apply to every account** (they are read from system paths).
- **Base preferences diverge**: the user layer (`settings.json`) is per account, while profile settings arrive through `--settings` and are common to all accounts.

### Unverified, to test
- `claude plugin list --json` under a non-default `CLAUDE_CONFIG_DIR` returns that dir's plugins (expected).
- Masking, `--strict-mcp-config` and `--setting-sources` behave the same under a second config dir.
- Whether a second login is needed to run the test: a human has to do the interactive `/login` in the second dir (e.g. in the prompt: `! CLAUDE_CONFIG_DIR=~/.claude-test claude`), then Stage 0 tests can be repeated against it.
- Seed-dir dedupe behavior.

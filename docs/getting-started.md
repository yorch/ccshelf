# Getting started

This page takes you from nothing to a running profile in about five minutes. A **profile** is a named set of plugins, standalone skills and MCP servers. `ccshelf run` starts `claude` with exactly that set, so each terminal can run a different one.

The examples use a fictional organization called `acme` and fictional plugin names. They are mockups of the format, not captured output. Replace them with your own marketplace and plugins.

## 1. Install ccshelf

Copy the install command for your platform from the [install section of the website](../site/index.html#install). There is one command for Linux and macOS (WSL counts as Linux) and one for Windows PowerShell. Both installers check the SHA-256 sum of the download before they install anything. They need no administrator rights. Then check the result:

```sh
ccshelf version
```

You can also build from source. The install section of the [README](../README.md) shows how.

## 2. Install the plugins in Claude Code

A profile chooses which installed plugins are active. It does not install them. All profiles share the plugins that are installed in your Claude Code configuration.

Inside Claude Code, add the marketplace once and install a plugin:

```text
/plugin marketplace add acme/plugins
/plugin install sre-kit@acme
```

`/plugin marketplace add <source>` adds a marketplace and `/plugin install name@marketplace` installs a plugin from it {V}. You can skip this step. Step 5 shows how ccshelf installs the plugins for you.

## 3. Create your first profile

```sh
ccshelf new sre-night --plugin sre-kit@acme
```

This writes a personal profile file. Add `--description`, `--model` or more `--plugin` flags to put more in it. In a terminal, `ccshelf new` with no flags asks for the values and prints the equivalent command.

Check what the profile does before you run it:

```sh
ccshelf ls
ccshelf show sre-night
ccshelf dry-run sre-night
```

- `ls` lists the profiles of every source.
- `show` prints the resolved plugins, MCP servers and the closure that a trust decision pins.
- `dry-run` prints the exact `claude` command and starts nothing.

## 4. Run it

```sh
ccshelf run sre-night
```

ccshelf starts `claude` with a generated settings file in its own cache directory. The plugins that the profile does not name are masked for that session. Other terminals, and plain `claude`, keep the full set. ccshelf never writes to `~/.claude`.

Put the flags of ccshelf before the profile name. Everything after the profile name goes to `claude` unchanged:

```sh
ccshelf run --account personal sre-night --resume
```

## 5. If a plugin is not installed

When a profile names a plugin that Claude Code does not list, `ccshelf run` prints a warning with the install command:

```text
warn: plugin sre-kit@acme is in the profile but not installed. Install it inside Claude Code with: /plugin install sre-kit@acme
```

In a terminal, it then asks whether to start anyway. You have two ways to fix this.

**Install the plugin yourself** with `/plugin install` inside Claude Code, as in step 2.

**Let ccshelf install it.** Add `--install-missing`:

```sh
ccshelf run --install-missing sre-night
```

ccshelf prints each command that it will run, then asks you to confirm. After you confirm, it runs `claude plugin install --scope user <id>` for every missing plugin. It then starts the profile. For a script, or to skip the question, add `--yes`:

```sh
ccshelf run --install-missing --yes sre-night
```

To install only some plugins, name them with `=`. Each one must be in the profile:

```sh
ccshelf run --install-missing=sre-kit@acme --yes sre-night
```

Rules of `--install-missing`:

- It installs only from a marketplace that you already added. It never adds a marketplace.
- It runs the install in ccshelf's cache directory and not in your project.
- It runs after the trust check, and it does not work with a profile from a project source.
- `--yes` installs plugins without a question. A plugin can run code through hooks and MCP servers, so use `--yes` only for profiles that you trust.
- Managed policy still applies, because Claude Code does the install. When Claude Code refuses a plugin, ccshelf prints a warning and starts as it does without the flag.
- `dry-run` has no `--install-missing` flag and never installs anything.

## 6. Use the profiles of your organization

An organization keeps its profiles in a git repository, the org data repo. Add it as a profile source:

```sh
ccshelf init --git-url git@ghe.example.com:acme/claude-marketplace.git --ref v2026.10.1
```

Then list the profiles and trust the one you want to use:

```sh
ccshelf ls
ccshelf trust sre
ccshelf run sre
```

`ccshelf trust sre` shows what the profile runs and asks you to confirm. In a script, you name exactly what you accept with `ccshelf trust sre --accept <closure-hash>`. `--yes` never accepts trust.

If your organization publishes a bundle for the profile, one command installs all of its plugins. Inside Claude Code, run `/plugin install profile-sre@acme`. A **profile bundle** is a generated plugin whose only content is the list of plugins of the profile. It does not activate anything. You still start the session with `ccshelf run sre`.

If you run an org data repo yourself, see `ccshelf catalog init` in the [command reference](reference/cli.md).

## 7. More than one Claude account

Each Claude Code account has its own configuration directory, so each account needs its own plugin installs. `ccshelf account add` creates an account and prints the one-time steps to log in. `ccshelf run --account <name>` starts a profile under that account. See the [launcher design](design/launcher.md) for the details.

## 8. Keep ccshelf up to date

```sh
ccshelf update
```

The command verifies the new release before it replaces the binary, and it can roll back. Automatic checks are off unless you turn them on.

## Where to go next

- [Command reference](reference/cli.md): every command, flag and exit code.
- [Launcher design](design/launcher.md): what a profile shares and what it changes.
- [Profiles](design/profiles.md): the profile file format and how trust works.
- [Security](design/security.md): the rules that ccshelf keeps, and what it never does.
- [Decision log](DECISIONS.md): why ccshelf works the way it does.

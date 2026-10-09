# ccshelf

Profiles and a plugin catalog for Claude Code. Unofficial: not affiliated with Anthropic.

**Status:** implemented and under adversarial review. Stable releases are available. The launcher, catalog, Action and starter template build for all six targets. The test suites pass on native macOS, Linux and Windows CI runners. Phase 0 evidence items (routing eval, adopt-or-build and bundle prototype) are still open. See the [roadmap](docs/design/roadmap.md). Start with [docs/README.md](docs/README.md) and the [decision log](docs/DECISIONS.md). The interactive report is `docs/report.html`, generated from the Markdown.

## Install

Install the latest release with the platform-specific script below, or build from source (Go 1.27 or newer):

```sh
go install github.com/yorch/ccshelf/cmd/ccshelf@latest
ccshelf --version
```

Or clone the repository and run `go build -o ccshelf ./cmd/ccshelf`.

The installers below fetch the latest release. Both installers:
- verify the download before they install anything.
- never need administrator rights.
- send no telemetry.
- write only into the install directory (created with any missing parent folders) and a private temporary directory. They remove the temporary directory afterwards.

"What the installer verifies" in [SECURITY.md](SECURITY.md#what-the-installer-verifies) gives every check and its limits.

**Linux and macOS** (amd64 and arm64, and WSL counts as Linux). Installs into `~/.local/bin`. `install.sh` needs `curl` (for example `apk add curl` or `apt install curl`):

```sh
curl -fsSL https://github.com/yorch/ccshelf/releases/latest/download/install.sh | sh
```

Pin a version, choose a directory, or require the signature check (options go after `sh -s --`):

```sh
curl -fsSL https://github.com/yorch/ccshelf/releases/latest/download/install.sh | sh -s -- --version v0.2.0 --bin-dir "$HOME/bin" --require-signature
```

Prefer to read it first? `curl -fsSLO https://github.com/yorch/ccshelf/releases/latest/download/install.sh`, read it, then `sh install.sh`. `sh install.sh --help` lists every option (`--base-url` for a mirror of the public release, such as one hosted on your GitHub Enterprise Server, `--dry-run`, `--quiet`, `--force`, `--cosign-identity`, `--cosign-issuer`).

**Windows** (amd64 and arm64, with Windows PowerShell 5.1 or PowerShell 7). Installs into `%LOCALAPPDATA%\Programs\ccshelf`:

```powershell
irm https://github.com/yorch/ccshelf/releases/latest/download/install.ps1 | iex
```

With options (`-Version`, `-BinDir`, `-BaseUrl`, `-RequireSignature`, `-AddToPath`, `-DryRun`, `-Force`). The `irm | iex` form leaves your session alone: it defines no variable or function and changes no preference. The script edits your user `PATH` only with `-AddToPath`:

```powershell
& ([scriptblock]::Create((irm https://github.com/yorch/ccshelf/releases/latest/download/install.ps1))) -Version v0.2.0 -AddToPath
```

**Verify by hand.** Follow "Verifying a release" in [SECURITY.md](SECURITY.md#verifying-a-release). Then extract the binary with `tar -xzf ccshelf_X.Y.Z_<os>_<arch>.tar.gz ccshelf` (on Windows, `Expand-Archive` the .zip). On Windows, compare `(Get-FileHash -Algorithm SHA256 <archive>).Hash` with the archive's line in `checksums.txt`.

**Notes.** The binaries are not notarized (macOS) or Authenticode-signed (Windows) yet. A file fetched by `curl` or the installers carries no quarantine mark, so Gatekeeper does not stop it. A browser download carries the mark, and macOS may ask you to allow it in System Settings (or run `xattr -d com.apple.quarantine ccshelf`). Windows SmartScreen may warn about a downloaded `.exe`.

**Uninstall.**
- Delete the binary: `rm ~/.local/bin/ccshelf`, or the directory you chose.
- On Windows, delete `%LOCALAPPDATA%\Programs\ccshelf`. If you used `-AddToPath`, also delete its entry in your user `PATH`.
- Optionally, delete ccshelf's own cache (`~/.cache/ccshelf`, or `%LOCALAPPDATA%\ccshelf` on Windows) and its configuration directory.

ccshelf never writes to `~/.claude`.

A reusable GitHub Action is also available ([action/](action/README.md)).

## Use

A **profile** is a named recipe of plugins, standalone skills and MCP servers. `ccshelf run` starts `claude` with exactly that set, so each terminal can run a different one.

```sh
ccshelf new sre-night --from base --plugin sre-kit@acme   # create a personal profile
ccshelf ls                                                # list profiles from every source
ccshelf show sre-night                                    # resolved plugins, MCP servers, trust closure
ccshelf dry-run sre-night                                 # print the exact claude command, run nothing
ccshelf run sre-night                                     # start claude with the profile
ccshelf run --account personal sre-night --resume         # ccshelf flags first, then claude's
```

Every command works with flags alone. On a terminal, ccshelf asks for missing values and prints the equivalent command. You must trust profiles from a shared source (a git repo of your organization) first. `ccshelf trust <profile>` shows what the profile does. Nothing is ever accepted automatically (`--yes` does not accept trust). Exit codes: 0 ok, 1 failure, 2 usage, 3 policy, 4 trust required, 130 interrupted.

### Update

```sh
ccshelf update --check     # current and latest version; exit 0 either way
ccshelf update             # verify (SHA-256, and cosign if installed), then replace this binary
ccshelf update --rollback  # restore the previous binary kept as ccshelf.old
ccshelf update --yes       # the scripted form: no question (the automatic update never acts in scripts)
```

Before it replaces anything, `ccshelf update` checks the archive's SHA-256 against `checksums.txt` of the same release, and the keyless signature when [cosign](https://docs.sigstore.dev/cosign/) is on `PATH`. `--require-signature` insists on the signature. "Network use and updates" in [SECURITY.md](SECURITY.md#network-use-and-updates) lists every check and what ccshelf sends.

`--force` never installs an older release than the one you run. `--allow-downgrade` does. ccshelf leaves these copies to their package manager and prints the command (`--force` overrides):
- a copy installed with Homebrew, Scoop, WinGet or `go install`.
- development builds.

Nothing checks for updates by itself unless you opt in:

```toml
# config.toml
[update]
mode = "notify"   # off (default) | notify (one line when a release exists) | install (see below)
interval = "24h"
```

`install` takes only a newer stable release of the same major version (for 0.x, the same minor). Both modes act only in an interactive terminal (stdin, stdout and stderr all terminals), so never in scripts, cron jobs, pipelines or when `CI` is set. `CCSHELF_NO_UPDATE_CHECK=1` turns them off. Details: [docs/design/update.md](docs/design/update.md).

For organizations, `ccshelf lint`, `compile`, `catalog build`, `search`, `recommend` and `doctor` work on the org data repo (see [examples/org-data-repo](examples/org-data-repo/README.md) for a starter template). The design is in [docs/](docs/README.md).

### Set up your org repo

`ccshelf catalog init` bootstraps the org data repo. It works on:
- a new repo in an empty directory.
- an existing marketplace repo (`.claude-plugin/marketplace.json` and/or `plugins/`). It retrofits this repo **without changing any existing file**.

It prints a plan (`create`, `skip-exists`, `needs-merge`). Then it writes `ccshelf.toml`, a marketplace skeleton, a sidecar stub per plugin, `CODEOWNERS`, the pinned `validate`, `catalog` and `release` workflows, a README and `.gitattributes`. There are no built-in profiles. `ccshelf lint` reports placeholders (`TODO(ccshelf)`) as warnings until you fill them in.

```sh
ccshelf catalog init ./acme-claude --marketplace-name acme --org "Acme Corp" \
  --platform-owners @acme/platform --dry-run          # print the plan, write nothing
ccshelf catalog init ./acme-claude --marketplace-name acme --org "Acme Corp" \
  --platform-owners @acme/platform --yes              # write it
ccshelf catalog init . --platform-owners @acme/platform --write-suggestions --yes   # adopt an existing repo
```

On a terminal, ccshelf asks for missing values. The design is in [docs/design/catalog-and-org-repo.md](docs/design/catalog-and-org-repo.md).

The project website lives in [site/](site/). It is static, and the build generates the /docs pages from the Markdown. `just site-check` checks it. A workflow deploys it to GitHub Pages automatically on every change to `main` (`.github/workflows/pages.yml`, see [site/README.md](site/README.md)).

Licensed under the [MIT License](LICENSE).

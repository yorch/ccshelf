# ccshelf

Profiles and a plugin catalog for Claude Code. Unofficial: not affiliated with Anthropic.

**Status:** implemented and under adversarial review; not yet released. The launcher, catalog, Action and starter template build for all six targets; tests pass on macOS and Linux, and Windows runs in CI and is best-effort until proven; Phase 0 evidence items (routing eval, adopt-or-build, bundle prototype, Linux/Windows runs) are still open, see the [roadmap](docs/design/roadmap.md). Start with [docs/README.md](docs/README.md) and the [decision log](docs/DECISIONS.md). The interactive report is `docs/report.html`, generated from the Markdown.

## Install

Not released yet. From source (Go 1.27 or newer):

```sh
go install github.com/yorch/ccshelf/cmd/ccshelf@latest   # module path is provisional
ccshelf --version
```

Releases will ship signed archives for macOS, Linux and Windows (amd64 and arm64) and a reusable GitHub Action ([action/](action/README.md)).

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

Every command works with flags alone; on a terminal, missing values are asked for and the equivalent command is printed. Profiles from a shared source (a git repo of your organization) must be trusted first: `ccshelf trust <profile>` shows what the profile does, and nothing is ever accepted automatically (`--yes` does not accept trust). Exit codes: 0 ok, 1 failure, 2 usage, 3 policy, 4 trust required, 130 interrupted.

### Update

```sh
ccshelf update --check     # current and latest version; exit 0 either way
ccshelf update             # verify (SHA-256, and cosign if installed), then replace this binary
ccshelf update --rollback  # restore the previous binary kept as ccshelf.old
ccshelf update --yes       # the scripted form: no question (the automatic update never acts in scripts)
```

The archive's SHA-256 must match `checksums.txt` of the same release; with [cosign](https://docs.sigstore.dev/cosign/) on `PATH` its keyless signature is checked too (`--require-signature` insists on it, and is checked before anything else). The signature must come from the release workflow of the repository this binary was built from, whatever `base_url` says; a fork that signs its own releases names itself in `cosign_identity_repo`. `--force` never installs an older release than the one you run; `--allow-downgrade` does. A copy installed with Homebrew, Scoop, WinGet or `go install`, and development builds, are left to their package manager (the command is printed; `--force` overrides). Nothing checks for updates by itself unless you opt in:

```toml
# config.toml
[update]
mode = "notify"   # off (default) | notify (one line when a release exists) | install (see below)
interval = "24h"
```

`install` takes only a newer stable release of the same major version (for 0.x, the same minor). Both modes act only in an interactive terminal (stdin, stdout and stderr all terminals), never in scripts, cron jobs or pipelines, never when `CI` is set, and `CCSHELF_NO_UPDATE_CHECK=1` turns them off. Details: [docs/design/update.md](docs/design/update.md).

For organizations, `ccshelf lint`, `compile`, `catalog build`, `search`, `recommend` and `doctor` work on the org data repo (see [examples/org-data-repo](examples/org-data-repo/README.md) for a starter template). The design is in [docs/](docs/README.md).

The project website (static; the /docs pages are generated from the Markdown at build time) lives in [site/](site/); it is checked by `make site-check` and is deployed to GitHub Pages only by hand (`.github/workflows/pages.yml`, see [site/README.md](site/README.md)).

Licensed under the [MIT License](LICENSE).

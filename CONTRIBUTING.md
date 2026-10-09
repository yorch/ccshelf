# Contributing to ccshelf

Thanks for helping. `ccshelf` is an unofficial, MIT-licensed tool for Claude Code (not affiliated with Anthropic). It has a launcher that starts `claude` with a named profile, and a catalog for an organization's plugin marketplace. Please read [AGENTS.md](AGENTS.md) (working rules and decided requirements) and [docs/README.md](docs/README.md) (overview and glossary) first. The [Code of Conduct](CODE_OF_CONDUCT.md) governs participation. Security problems go through [SECURITY.md](SECURITY.md), never a public issue.

## Development setup

You need Go (the version in `go.mod`), [`just`](https://github.com/casey/just) (1.15 or later), `bash`, and `python3` for the docs recipes. Nothing else. Install `just` with your package manager (`brew install just`, `winget install Casey.Just`, `apt install just` on Debian 13 or Ubuntu 24.04 and later) or a prebuilt binary from its releases page. On Windows the recipes run in Git Bash.

```sh
git clone https://github.com/yorch/ccshelf && cd ccshelf   # OWNER: update if the repository moves
just tools     # installs pinned golangci-lint, govulncheck, goreleaser and actionlint into $(go env GOPATH)/bin
just build     # dist/ccshelf
just ci        # everything CI runs, locally
```

`just` with no arguments lists every recipe. Useful ones: `just test-race`, `just cover` (HTML report plus a threshold), `just lint`, `just fmt`, `just cross` (all six targets), `just docs` and `just docs-check`, `just examples-check`. `just test` and `just test-race` take packages and flags (`just test ./internal/config -run Edit`). To override a variable, use `just version=1.2.3 build` or the environment (`VERSION=1.2.3 just build`).

## Repository layout

| Path | What lives there |
|---|---|
| `cmd/ccshelf/` | The `main` package and the cobra command tree: thin, no logic |
| `internal/` | All logic, one package per concern (profiles, settings, cache, trust, policy, catalog, ...) |
| `internal/version/` | Build identity set by `-ldflags` |
| `action/` | The reusable composite GitHub Action (a thin wrapper around the binary) |
| `examples/` | Fictional example org data repo and starter template. Never real org data (R5) |
| `docs/` | Design notes, the decision log, research, and the generated `report.html` |
| `scripts/` | CI and maintenance scripts (pin checking, link checking, coverage) |
| `.github/` | Workflows, templates, `CODEOWNERS`, Dependabot |

## Testing rules

- **Hermetic.** Tests use `t.TempDir()` and `t.Setenv` for `HOME`, `USERPROFILE`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `APPDATA` and `LOCALAPPDATA`. No network. Tests that run git set `GIT_CONFIG_GLOBAL` and `GIT_CONFIG_SYSTEM` to an empty file so your own git config cannot change results.
- **Never the real Claude Code.** No test reads or writes `~/.claude` or `~/.claude.json`, installs plugins, or calls the real `claude` binary. Use the fake `claude` test double. The only real-`claude` test is behind the `realclaude` build tag and runs in the nightly workflow.
- **Cross-platform.** Tests must pass on Linux, macOS and Windows (amd64 and arm64).
  - Use `filepath`, never hand-built path strings.
  - Do not rely on symlinks, `$TMPDIR` or a POSIX shell.
  - Skip OS-specific behavior with a clear reason.
- Table-driven tests, edge cases and error paths. Golden files under `testdata/` for textual output. Aim for 80% or more statement coverage on logic packages.
- Run `go test -race ./...` before you push.

## Security checklist (SR1 to SR5)

Anything that touches profiles, settings generation, the cache, trust, policy, CI or releases must keep these true. The pull request template asks you to tick them.

1. **Closed schemas.** Unknown keys are errors. Generated settings use only the allowlist. A profile can never write `permissions`, `hooks`, auth or endpoint settings.
2. **Trust the resolved closure, pinned by commit SHA.**
   - Non-personal sources need explicit trust.
   - Project sources are off by default and never shadow another source.
   - Non-interactive runs fail closed, and `--yes` never accepts trust.
3. **No shadowing, protected controls.** Names cannot collide across sources. Protected plugins and MCP servers are never masked.
4. **Private, verified local artifacts.**
   - Directories 0700, files 0600, exclusive create, no-follow.
   - Re-hash before reuse.
   - Paths confined to their root with no `..` and no symlink escapes.
   - No secrets on disk or in argv.
   - Redact values in output.
5. **Hardened CI and releases.** See "Workflow rules" below.

Also: never bypass or probe managed policy by trial, no telemetry, and no network access except where a feature explicitly needs it. If a change would weaken any of this, open an issue and discuss it first.

## Code style

- `gofmt` and `gofumpt` clean (`just fmt`), `go vet` and `golangci-lint` clean (`.golangci.yml`).
- Every exported identifier documented. Every package has a `doc.go`.
- Wrap errors with context: `fmt.Errorf("reading profile %q: %w", name, err)`. No panics for expected conditions.
- `context.Context` is the first parameter of anything that runs a process or blocks on I/O.
- No `fmt.Print*` outside `cmd/` and `internal/ui`. No `log.Fatal`. A `depguard` rule against `os/exec` guards the packages that must not run processes.
- Dependencies: the standard library plus `cobra`, `go-toml/v2`, `golang.org/x/term` and `golang.org/x/sys`. Discuss a new dependency before you add it.
- Commands must work with flags alone. Prompts and pickers are an optional front-end, used only on a TTY (R6).

## Commit and pull request style

Every commit message and pull request title uses [Conventional Commits](https://www.conventionalcommits.org/) (`type(scope)!: description`). Maintainers squash-merge, so **the pull request title becomes the commit subject and the changelog entry**. [AGENTS.md](AGENTS.md#commits-and-pull-requests) has the types, the title rules and the pull request rules. Its ["Releasing"](AGENTS.md#releasing) section says which types cause a release.

The `pr-title` check (its own workflow, `pr-title.yml`, required next to `ci-ok`) applies the title rules, including a ban on invisible characters. It re-runs when you edit the title. It also rejects a description with the release-please markers that [docs/design/release.md](docs/design/release.md#pull-request-titles-d-37) lists.

## Documentation rules

- Follow [AGENTS.md "Documentation conventions"](AGENTS.md#documentation-conventions): the docs layout, the decision log, the glossary terms, the `{V}` `{R}` `{U}` markers, mockup labels and the generated `docs/report.html`.
- `just docs` rebuilds the report (`python3 docs/build_report.py`). CI runs `python3 docs/build_report.py --check`.
- Write messages, help text and docs in the plain style of [AGENTS.md](AGENTS.md#writing-style-asd-ste100): short active sentences, no semicolons, one name for one thing, and no lost hedges or conditions.

## Workflow rules (SR5)

- Pin every `uses:` to a **full 40-character commit SHA** with a trailing `# vX.Y.Z` comment. After you add or bump an action by tag, run `scripts/pin-actions.sh` (needs `gh`). Otherwise `scripts/check-pins.sh` fails CI. Dependabot keeps pins current.
- Every workflow has:
  - `permissions: {}` at the top.
  - explicit per-job grants (`contents: read` by default).
  - `timeout-minutes` and `concurrency` everywhere.
  - `persist-credentials: false` on checkouts that do not push.
- Never put untrusted data (PR titles, branch names, tags, commit messages) inside `${{ }}` in a `run:` script. Pass it through `env:` and quote it.
- **`ci.yml` runs only the jobs a pull request needs.** The `changes` job (`scripts/ci_changes.py`) maps changed paths to jobs. Some events and paths run everything. When you add a new file or directory, add a rule to `scripts/ci_changes.py`. Otherwise a unit test fails. Details: [docs/design/release.md](docs/design/release.md#ci-job-selection).
- Build and test use `pull_request`, never `pull_request_target`. Secrets exist only on protected environments (`release`, `nightly`) and are never sent to forks.
- Prefer plain shell calling `go`, `gh` and the project's own binary over third-party actions (R2). The allowed third-party actions are the ones already in use.

## Releasing

Releases are cut from a **release pull request** that a bot keeps up to date. Nobody tags by hand. [docs/design/release.md](docs/design/release.md) has the design: the versioning rules, how to force a version, hotfixes, the token and signing analysis, the failure runbook and the one-time setup. The maintainer steps:

1. On every push to `main`, the `release-please` workflow opens or updates the pull request `chore(main): release X.Y.Z`. It contains the next version (`.release-please-manifest.json`) and the generated `CHANGELOG.md`. The `dispatch` job starts `ci.yml` and `pr-title.yml` on that branch, so the required checks `ci-ok` and `pr-title` appear on it.
   - Review the version and the changelog like any pull request.
   - Make sure `docs/report.html` is current on `main`.
   - Merge it with the usual approvals.
2. Merging creates the tag `vX.Y.Z` and a **draft** GitHub release. Then `dispatch` starts the `release` workflow with that tag as its ref, so the signing identity stays `release.yml@refs/tags/vX.Y.Z`.
3. The `verify` job checks the tag and CI (the design note lists each check). It also checks that `go.mod` is tidy and the report is up to date.
4. The `release` job checks the draft and waits for approval of the protected **`release` environment**. Configure that environment to require **two maintainers** (with "prevent self-review") and to allow deployments only from `v*` tags (see "Required repository rulesets"). Dry runs use the separate `snapshot` job, which has no environment, no signing and a read-only token.
5. goreleaser:
   1. builds the six targets (`-trimpath`, `CGO_ENABLED=0`, commit-timestamped).
   2. writes `checksums.txt` and an SBOM per archive (syft).
   3. uploads them to the draft.
   4. appends the verification footer to the notes written by release-please.
   5. publishes the release.

   The release also uploads the end-user installers, `scripts/install.sh` and `scripts/install.ps1`, and lists them in `checksums.txt`. The settings are `release.extra_files` and `checksum.extra_files` in `.goreleaser.yaml`. Keep the two lists identical. cosign signs the checksums file keyless (`checksums.txt.sigstore.json`), and `actions/attest-build-provenance` attaches a SLSA build provenance attestation.
6. **Optional publishers** run only if their secret exists on the `release` environment, and never for pre-releases. Otherwise the workflow skips each one with a notice:

   | Secret | Publishes to | Token needs |
   |---|---|---|
   | `HOMEBREW_TAP_TOKEN` | Homebrew cask in `yorch/homebrew-tap` | contents write on that repository |
   | `SCOOP_BUCKET_TOKEN` | Scoop manifest in `yorch/scoop-bucket` | contents write on that repository |
   | `WINGET_TOKEN` | WinGet pull request to `microsoft/winget-pkgs` from the `yorch/winget-pkgs` fork | contents write on the fork and pull request creation |

   Use fine-grained tokens scoped to exactly those repositories.
7. Before you announce the release:
   - Verify the published release as a user would. Follow "Verifying a release" in [SECURITY.md](SECURITY.md).
   - Run the documented one-liners from the README (`install.sh | sh`, and `install.ps1` on Windows) against it in a scratch directory (`--bin-dir`/`-BinDir`).

   A change to `scripts/install.sh` or `install.ps1` is a security-sensitive change. Keep their tests green (see [AGENTS.md "Installers"](AGENTS.md#installers)), and update "What the installer verifies" in SECURITY.md. The macOS and Windows binaries are not notarized or signed yet, so Gatekeeper and SmartScreen may warn. Targets without a native CI runner are noted as built but not natively tested.

If a release fails or turns out bad, follow [Failure and rollback](docs/design/release.md#failure-and-rollback). Before the first release, follow the [one-time setup and bootstrap runbook](docs/design/release.md#one-time-maintainer-setup-and-bootstrap-runbook) and create the rulesets and the `release` environment below.

The repository owner appears in `.goreleaser.yaml`, `.github/ISSUE_TEMPLATE/config.yml`, `CODEOWNERS` (team handle), `SECURITY.md` and this file, each marked `OWNER`.

## Required repository rulesets

The release signature identity is `https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/tags/<tag>`. release-please creates tags with the workflow's `GITHUB_TOKEN`, so a maintainer no longer types a tag. These protect the identity:

- A tag is only accepted when it names the version the merged release pull request wrote into the manifest.
- The tag must point at a commit on `main` with green CI, whose draft release targets that commit.
- Above all, **two reviewers approve the `release` environment** before anything is signed.

Configure these rulesets (Settings > Rules) on the tool repository before the first release:

- **Tags `v*`**: block updates (a tag never moves) and block deletion (bypass list: maintainers only, for the yank and squatted-tag cases in the design note). Do **not** restrict creation: the built-in Actions identity that release-please uses cannot be put on a ruleset bypass list {U}, so restricting creation would stop the release flow. If you want creation restricted to maintainers and the bot, use the hardened variant below. GitHub rulesets cannot require signed tags {U}. The tags created here are lightweight and unsigned.
- **Branch `main`**:
  - Required status checks `ci-ok` and `pr-title`. `pr-title` is a second, separate workflow so that title edits do not re-run the matrix. `ci-ok` accepts skipped jobs, so path-gated jobs never block a merge.
  - Require pull requests with **code-owner review** and **two approvals**. The release pull request is subject to the same rule.
  - Block force pushes and deletion.

  The bot opens the release pull request, and it gets its CI from the `dispatch` job, not from a `pull_request` event. Enable "Require approval of the most recent reviewable push" and keep CODEOWNERS review. Reason: "Allow GitHub Actions to create and approve pull requests" also lets workflow tokens submit approvals.
- **Environment `release`**:
  - Deployment tags limited to `v*`.
  - **Two required reviewers** with **"prevent self-review"**.
  - The optional publisher secrets live here and nowhere else.

**Hardened variant (optional, adds one long-lived key).**
1. Create a GitHub App with contents, pull requests and actions write on this repository.
2. Store its private key as a secret on a protected environment.
3. Pass its token to `release-please-action` (`token:`).
4. Add the App to the tag ruleset's bypass list, and restrict creation of `v*` to it and the maintainers.

Pull requests and tags created with an App token trigger workflows natively, so the `dispatch` job becomes unnecessary. The tag-push trigger of `release.yml` would be needed again.

The release workflow also fails when the tagged commit is not an ancestor of `main` (`git merge-base --is-ancestor` after a full fetch, plus a GitHub compare API check). This backs up the rulesets but does not replace them.

## Embedded checksums for the Action

After each release the `pins` job opens a pull request `chore: pin checksums for vX.Y.Z` that appends the six archive hashes to `action/pins.txt`. Review the lines against the signed `checksums.txt` and merge. The job needs no environment, and you can re-run it safely. [action/README.md](action/README.md) says how the job gets CI, how the optional `PINS_PR_TOKEN` works (scope it to this repository), and how adopters use the pins.

## Nightly smoke test

`.github/workflows/nightly.yml` installs Claude Code with npm and runs `go test -tags realclaude ./internal/e2e/...` in an empty `HOME`. It runs only if `ANTHROPIC_API_KEY` is set on the protected `nightly` environment (main only) and otherwise skips with a notice. A failure opens or updates one issue labeled `nightly-failure`.

## GitHub Enterprise Server notes

The tool and its workflows are designed to the lowest common denominator (R2): no hard-coded hosts in logic, CLI first and workflow second, and few third-party actions.

- **Mirror the Action and the binary.** Follow "GitHub Enterprise Server and offline use" in [action/README.md](action/README.md). It covers the mirror of `yorch/ccshelf`, the asset layout for `base-url`, SHA pins and the offline checks.
- **Third-party actions.** The workflows here use `actions/checkout`, `actions/setup-go`, `actions/upload-artifact`, `actions/download-artifact` and a few others. GHE Server administrators must mirror or allow them (or use GitHub Connect). Where that is not possible, build the binary with plain `go build` and call it directly. Every `ccshelf` command works outside Actions.
- **Attestations.** Build provenance attestations have limited support on GHE Server. Use one of these instead (see [SECURITY.md](SECURITY.md)):
  - the cosign bundle with `cosign verify-blob` and a mirrored trusted root (this is not offline without it).
  - the SHA-256 pin.
- **Version skew.** GHE Server lags github.com. Avoid newer Actions syntax in the Action and the org data repo templates, or make it optional. The minimum supported Server version is not yet known.
- **Marketplace sources** for GHE hosts should use git URLs. See [docs/design/platform.md](docs/design/platform.md).

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).

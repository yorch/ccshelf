# Contributing to ccshelf

Thanks for helping. `ccshelf` is an unofficial, MIT-licensed tool for Claude Code (not affiliated with Anthropic): a launcher that starts `claude` with a named profile, and a catalog for an organization's plugin marketplace. Please read [AGENTS.md](AGENTS.md) (working rules and decided requirements) and [docs/README.md](docs/README.md) (overview and glossary) first. Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md). Security problems go through [SECURITY.md](SECURITY.md), never a public issue.

## Development setup

You need Go (the version in `go.mod`), `make`, `bash`, and `python3` for the docs targets. Nothing else.

```sh
git clone https://github.com/yorch/ccshelf && cd ccshelf   # OWNER: update if the repository moves
make tools     # installs pinned golangci-lint, govulncheck, goreleaser and actionlint into $(go env GOPATH)/bin
make build     # dist/ccshelf
make ci        # everything CI runs, locally
```

`make help` lists every target. Useful ones: `make test-race`, `make cover` (HTML report plus a threshold), `make lint`, `make fmt`, `make cross` (all six targets), `make docs` and `make docs-check`, `make examples-check`.

## Repository layout

| Path | What lives there |
|---|---|
| `cmd/ccshelf/` | The `main` package and the cobra command tree: thin, no logic |
| `internal/` | All logic, one package per concern (profiles, settings, cache, trust, policy, catalog, ...) |
| `internal/version/` | Build identity set by `-ldflags` |
| `action/` | The reusable composite GitHub Action (a thin wrapper around the binary) |
| `examples/` | Fictional example org data repo and starter template; never real org data (R5) |
| `docs/` | Design notes, the decision log, research, and the generated `report.html` |
| `scripts/` | CI and maintenance scripts (pin checking, link checking, coverage) |
| `.github/` | Workflows, templates, `CODEOWNERS`, Dependabot |

## Testing rules

- **Hermetic.** Tests use `t.TempDir()` and `t.Setenv` for `HOME`, `USERPROFILE`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `APPDATA` and `LOCALAPPDATA`. No network. Tests that run git set `GIT_CONFIG_GLOBAL` and `GIT_CONFIG_SYSTEM` to an empty file so your own git config cannot change results.
- **Never the real Claude Code.** No test reads or writes `~/.claude` or `~/.claude.json`, installs plugins, or calls the real `claude` binary. Use the fake `claude` test double. The only real-`claude` test is behind the `realclaude` build tag and runs in the nightly workflow.
- **Cross-platform.** Tests must pass on Linux, macOS and Windows (amd64 and arm64). Use `filepath`, never hand-built path strings; do not rely on symlinks, `$TMPDIR` or a POSIX shell; skip OS-specific behavior with a clear reason.
- Table-driven tests, edge cases and error paths; golden files under `testdata/` for textual output. Aim for 80% or more statement coverage on logic packages.
- Run `go test -race ./...` before you push.

## Security checklist (SR1 to SR5)

Anything that touches profiles, settings generation, the cache, trust, policy, CI or releases must keep these true; the pull request template asks you to tick them.

1. **Closed schemas.** Unknown keys are errors. Generated settings use only the allowlist; a profile can never write `permissions`, `hooks`, auth or endpoint settings.
2. **Trust the resolved closure, pinned by commit SHA.** Non-personal sources need explicit trust; project sources are off by default and never shadow another source; non-interactive runs fail closed and `--yes` never accepts trust.
3. **No shadowing, protected controls.** Names cannot collide across sources; protected plugins and MCP servers are never masked.
4. **Private, verified local artifacts.** Directories 0700, files 0600, exclusive create, no-follow; re-hash before reuse; paths confined to their root with no `..` and no symlink escapes; no secrets on disk or in argv; redact values in output.
5. **Hardened CI and releases.** See "Workflow rules" below.

Also: never bypass or probe managed policy by trial, no telemetry, and no network access except where a feature explicitly needs it. If a change would weaken any of this, open an issue and discuss it first.

## Code style

- `gofmt` and `gofumpt` clean (`make fmt`), `go vet` and `golangci-lint` clean (`.golangci.yml`).
- Every exported identifier documented; every package has a `doc.go`.
- Wrap errors with context: `fmt.Errorf("reading profile %q: %w", name, err)`. No panics for expected conditions.
- `context.Context` is the first parameter of anything that runs a process or blocks on I/O.
- No `fmt.Print*` outside `cmd/` and `internal/ui`; no `log.Fatal`. Packages that must not run processes are guarded by a `depguard` rule against `os/exec`.
- Dependencies: the standard library plus `cobra`, `go-toml/v2`, `golang.org/x/term` and `golang.org/x/sys`. Adding another needs a discussion first.
- Commands must work with flags alone; prompts and pickers are an optional front-end used only on a TTY (R6).

## Commit and pull request style

[Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:`, `test:`, `ci:`, `build:`, `refactor:`, `perf:`, `chore:`, `revert:`, with an optional scope and an optional `!` for a breaking change, for example `fix(settings): reject env names matching ANTHROPIC_*`. Maintainers squash-merge, so **the pull request title becomes the commit subject and the changelog entry**: the `pr-title` check (its own workflow, `pr-title.yml`, required next to `ci-ok`) rejects a title that is not in this form (lowercase description, no trailing period, at most 72 characters, no invisible characters; edit the title and the check re-runs). It also rejects a description that contains `BEGIN_COMMIT_OVERRIDE`, `BEGIN_NESTED_COMMIT` or a `Release-As:` line, because release-please reads those. Only `feat`, `fix`, `perf`, `revert` and breaking changes appear in the changelog and trigger a release; use `fix(deps):` for a dependency bump that fixes a vulnerability. Keep pull requests focused, fill in the template, and make sure `ci-ok` is green. The full rules are in [AGENTS.md](AGENTS.md#commits-and-pull-requests).

## Documentation rules

- Edit the **Markdown** under `docs/`; never edit `docs/report.html` by hand. Rebuild it with `python3 docs/build_report.py` (or `make docs`) and commit it in the same commit as the Markdown change. CI runs `python3 docs/build_report.py --check`.
- Record decisions, supersessions and open questions in `docs/DECISIONS.md` (never delete a row). Use the glossary terms consistently: profile, profile bundle, catalog, sidecar, marketplace, tool repo, org data repo, account.
- Mark factual claims about Claude Code with `{V}` verified, `{R}` reported or `{U}` unverified.
- Examples are fictional and labeled as mockups until the behavior exists.

## Workflow rules (SR5)

- Every `uses:` is pinned to a **full 40-character commit SHA** with a trailing `# vX.Y.Z` comment. After adding or bumping an action by tag, run `scripts/pin-actions.sh` (needs `gh`); `scripts/check-pins.sh` fails CI otherwise. Dependabot keeps pins current.
- `permissions: {}` at the top of every workflow, explicit per-job grants (`contents: read` by default), `timeout-minutes` and `concurrency` everywhere, and `persist-credentials: false` on checkouts that do not push.
- Never put untrusted data (PR titles, branch names, tags, commit messages) inside `${{ }}` in a `run:` script: pass it through `env:` and quote it.
- **`ci.yml` runs only the jobs a pull request needs.** The `changes` job (`scripts/ci_changes.py`) maps changed paths to jobs; skipped jobs count as passed in `ci-ok`, which always reports. Pushes to `main`, merge groups, manual dispatches, and pull requests that touch `.github/`, an unclassified file or the release config run everything. Adding a new file or directory means adding a rule to `scripts/ci_changes.py` (a unit test fails otherwise). Details: [docs/design/release.md](docs/design/release.md#ci-job-selection).
- Build and test use `pull_request`, never `pull_request_target`. Secrets exist only on protected environments (`release`, `nightly`) and are never sent to forks.
- Prefer plain shell calling `go`, `gh` and the project's own binary over third-party actions (R2); the allowed third-party actions are the ones already in use.

## Releasing

Releases are cut from a **release pull request** that a bot keeps up to date; nobody tags by hand. The design, the token and signing analysis and the failure runbook are in [docs/design/release.md](docs/design/release.md).

1. Pull requests are squash-merged with a Conventional Commits title. On every push to `main`, the `release-please` workflow reads the titles since the last release and opens or updates the pull request `chore(main): release X.Y.Z`, which contains the next version (`.release-please-manifest.json`) and the generated `CHANGELOG.md`. Before 1.0.0 a breaking change and a `feat` bump the minor version, a `fix`, `perf` or `revert` the patch. If only `docs`, `test`, `ci`, `build`, `refactor` or `chore` commits landed, there is nothing to release and no pull request.
2. The `dispatch` job starts `ci.yml` and `pr-title.yml` on that branch (a pull request opened with `GITHUB_TOKEN` does not trigger CI by itself), so the required checks `ci-ok` and `pr-title` appear on it. Review the version and the changelog like any pull request, make sure `docs/report.html` is current on `main`, and merge it with the usual approvals. To force a version (1.0.0, a correction) merge a releasable (`fix:` or `feat:` titled) pull request that sets `"release-as": "X.Y.Z"` in `release-please-config.json`, merge the release pull request, then remove `release-as` again (a `chore:` commit with a `Release-As:` footer does nothing: the release is skipped when no visible entry exists, and the footer is rejected in pull request descriptions). A hotfix is a `fix:` pull request followed by merging the next release pull request; it ships everything on `main`.
3. Merging creates the tag `vX.Y.Z` at the merge commit and a **draft** GitHub release with the changelog as its notes, and `dispatch` starts the `release` workflow with that tag as its ref, so the signing identity stays `release.yml@refs/tags/vX.Y.Z`. (A manual retry is `gh workflow run release.yml --ref vX.Y.Z -f dry-run=false`. A dry run, `gh workflow run release.yml`, builds a snapshot without signing or publishing.)
4. The `verify` job checks that the tag is a semantic version, points at a commit on `main`, equals the version in `.release-please-manifest.json` at that commit, that no branch has the tag's name, and that `ci.yml` succeeded for the commit (it waits for that run); it also checks that `go.mod` is tidy and the report is up to date. It runs with a read-only token, which cannot see draft releases, so the `release` job (after the approval below) checks that exactly one draft has the tag as `tag_name`, exactly one release is named like the tag, they are the same release, and the draft targets the tag's commit.
5. The `release` job waits for approval of the protected **`release` environment**, which must be configured to require **two maintainers** (with "prevent self-review") and to allow deployments only from `v*` tags (see "Required repository rulesets"). Dry runs use the separate `snapshot` job, which has no environment, no signing and a read-only token.
6. goreleaser builds the six targets (`-trimpath`, `CGO_ENABLED=0`, commit-timestamped), writes `checksums.txt`, an SBOM per archive (syft), uploads them to the draft, appends the verification footer to the notes written by release-please, and publishes the release. The two end-user installers, `scripts/install.sh` and `scripts/install.ps1`, are uploaded as release assets and listed in `checksums.txt` (`release.extra_files` and `checksum.extra_files` in `.goreleaser.yaml`; keep the two lists identical), so the signature covers them and `releases/latest/download/install.sh` always serves the script of the newest release. The checksums file is signed keyless with cosign (`checksums.txt.sigstore.json`), and `actions/attest-build-provenance` attaches a SLSA build provenance attestation.
7. **Optional publishers** run only if their secret exists on the `release` environment, and never for pre-releases. Each is skipped with a notice otherwise:

   | Secret | Publishes to | Token needs |
   |---|---|---|
   | `HOMEBREW_TAP_TOKEN` | Homebrew cask in `ccshelf/homebrew-tap` | contents write on that repository |
   | `SCOOP_BUCKET_TOKEN` | Scoop manifest in `ccshelf/scoop-bucket` | contents write on that repository |
   | `WINGET_TOKEN` | WinGet pull request to `microsoft/winget-pkgs` from the `ccshelf/winget-pkgs` fork | contents write on the fork and pull request creation |

   Use fine-grained tokens scoped to exactly those repositories.
8. Verify the published release as a user would, following "Verifying a release" in [SECURITY.md](SECURITY.md), and run the documented one-liners from the README (`install.sh | sh`, and `install.ps1` on Windows) against it in a scratch directory (`--bin-dir`/`-BinDir`) before announcing it. Changing `scripts/install.sh` or `install.ps1` is a security-sensitive change: keep `scripts/test_install.sh` (with `--mutants`) and `scripts/test_install.ps1` green and update "What the installer verifies" in SECURITY.md. The macOS and Windows binaries are not notarized or signed yet, so Gatekeeper and SmartScreen may warn; targets without a native CI runner are noted as built but not natively tested.

If a release fails or turns out bad, follow "Failure and rollback" in the design note: a failed job leaves the draft and the tag in place and can be re-run; a published bad release is yanked (back to a draft or deleted, tag kept) and fixed by the next patch.

**One-time setup** (before the first release; the full bootstrap runbook is in the design note): enable "Allow GitHub Actions to create and approve pull requests" under Settings > Actions > General; create the rulesets and the `release` environment below; set the squash-merge default message to "Pull request title" (the description must not reach `main`: release-please also reads `BEGIN_COMMIT_OVERRIDE` and `Release-As:` from it, and `pr-title` rejects them); require code-owner review and "Require approval of the most recent reviewable push" in the `main` ruleset, because the Actions setting also lets workflow tokens submit approvals. No secret is needed for the release flow itself. The first release is `0.1.0` (`initial-version`), and `bootstrap-sha` in `release-please-config.json` can shorten its changelog.

The repository owner appears in `.goreleaser.yaml`, `.github/ISSUE_TEMPLATE/config.yml`, `CODEOWNERS` (team handle), `SECURITY.md` and this file, each marked `OWNER`.

## Required repository rulesets

The release signature identity is `https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/tags/<tag>`. Since release-please creates tags with the workflow's `GITHUB_TOKEN`, a tag is no longer typed by a maintainer; what protects the identity is that a tag is only accepted when it names the version the merged release pull request wrote into the manifest, that it points at a commit on `main` with green CI whose draft release targets that commit, and above all that **two reviewers approve the `release` environment** before anything is signed. Configure these rulesets (Settings > Rules) on the tool repository before the first release:

- **Tags `v*`**: block updates (a tag never moves) and block deletion (bypass list: maintainers only, for the yank and squatted-tag cases in the design note). Do **not** restrict creation: the built-in Actions identity that release-please uses cannot be put on a ruleset bypass list {U}, so restricting creation would stop the release flow. If you want creation restricted to maintainers and the bot, use the hardened variant below. GitHub rulesets cannot require signed tags {U}; the tags created here are lightweight and unsigned.
- **Branch `main`**: required status checks `ci-ok` and `pr-title` (a second, separate workflow so that title edits do not re-run the matrix; `ci-ok` accepts skipped jobs, so path-gated jobs never block a merge); require pull requests with **code-owner review** and **two approvals** (the release pull request is subject to the same rule); block force pushes and deletion. The release pull request is opened by the bot and gets its CI from the `dispatch` job, not from a `pull_request` event. Enable "Require approval of the most recent reviewable push" and keep CODEOWNERS review: "Allow GitHub Actions to create and approve pull requests" also lets workflow tokens submit approvals.
- **Environment `release`**: deployment tags limited to `v*`; **two required reviewers** with **"prevent self-review"**; the optional publisher secrets live here and nowhere else.

**Hardened variant (optional, adds one long-lived key).** Create a GitHub App with contents, pull requests and actions write on this repository, store its private key as a secret on a protected environment, pass its token to `release-please-action` (`token:`), add the App to the tag ruleset's bypass list and restrict creation of `v*` to it and the maintainers. Pull requests and tags created with an App token trigger workflows natively, so the `dispatch` job becomes unnecessary (the tag-push trigger of `release.yml` would be needed again).

The release workflow also fails when the tagged commit is not an ancestor of `main` (`git merge-base --is-ancestor` after a full fetch, plus a GitHub compare API check), which backs up the rulesets but does not replace them. The `pins` job needs no environment; it starts `ci.yml` and `pr-title.yml` on its own branch with `workflow_dispatch` so `ci-ok` and `pr-title` report on its pull request, and it can be re-run safely. If you prefer CI to trigger natively, add a repository secret `PINS_PR_TOKEN` (GitHub App token or fine-grained PAT scoped to this repository) and the job uses it instead.

## Embedded checksums for the Action

After each release the `pins` job opens a pull request `chore: pin checksums for vX.Y.Z` that appends the six archive hashes to `action/pins.txt` (`<version> <os> <arch> <sha256>`). Review the lines against the signed `checksums.txt` and merge. Adopters pin the Action to a commit that contains the line for their version, which pins the binary without any signature service (see [action/README.md](action/README.md)).

## Nightly smoke test

`.github/workflows/nightly.yml` installs Claude Code with npm and runs `go test -tags realclaude ./internal/e2e/...` in an empty `HOME`. It runs only if `ANTHROPIC_API_KEY` is set on the protected `nightly` environment (main only) and otherwise skips with a notice. A failure opens or updates one issue labeled `nightly-failure`.

## GitHub Enterprise Server notes

The tool and its workflows are designed to the lowest common denominator (R2): no hard-coded hosts in logic, CLI first and workflow second, and few third-party actions.

- **Mirror the Action and the binary.** Mirror `yorch/ccshelf` (the repository with `action/`) onto your instance, for example with [`actions-sync`](https://github.com/actions/actions-sync), and pin it by full commit SHA. Mirror the release assets (`ccshelf_*` archives, `checksums.txt`, `checksums.txt.sigstore.json`) to an internal location; the Action must download from there (`base-url`) and verify the SHA-256. `cosign verify-blob` needs the Sigstore trusted root and is not offline; for an air-gapped instance use the `sha256` pin or `action/pins.txt`, or mirror the trusted root and pass it as `trusted-root`.
- **Third-party actions.** The workflows here use `actions/checkout`, `actions/setup-go`, `actions/upload-artifact`, `actions/download-artifact` and a few others. GHE Server administrators must mirror or allow them (or use GitHub Connect). Where that is not possible, build the binary with plain `go build` and call it directly; every `ccshelf` command works outside Actions.
- **Attestations.** Build provenance attestations have limited support on GHE Server; use the cosign bundle with `cosign verify-blob` and a mirrored trusted root (this is not offline without it) or the SHA-256 pin instead (see [SECURITY.md](SECURITY.md)).
- **Version skew.** GHE Server lags github.com: avoid newer Actions syntax in the Action and the data repo templates, or make it optional. The minimum supported Server version is not yet known.
- **Marketplace sources** for GHE hosts should use git URLs; see [docs/design/platform.md](docs/design/platform.md).

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).

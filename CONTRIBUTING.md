# Contributing to ccshelf

Thanks for helping. `ccshelf` is an unofficial, MIT-licensed tool for Claude Code (not affiliated with Anthropic): a launcher that starts `claude` with a named profile, and a catalog for an organization's plugin marketplace. Please read [AGENTS.md](AGENTS.md) (working rules and decided requirements) and [docs/README.md](docs/README.md) (overview and glossary) first. Participation is governed by the [Code of Conduct](CODE_OF_CONDUCT.md). Security problems go through [SECURITY.md](SECURITY.md), never a public issue.

## Development setup

You need Go (the version in `go.mod`), `make`, `bash`, and `python3` for the docs targets. Nothing else.

```sh
git clone https://github.com/ccshelf/ccshelf && cd ccshelf   # OWNER: update if the repository moves
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

[Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:`, `test:`, `ci:`, `chore:`, `refactor:`, with an optional scope, for example `fix(settings): reject env names matching ANTHROPIC_*`. The release changelog is generated from these messages. The **pull request title** follows the same format, because maintainers squash-merge and the title becomes the commit on `main`. Keep pull requests focused, fill in the template, and make sure `ci-ok` is green. The full rules are in [AGENTS.md](AGENTS.md#commits-and-pull-requests).

## Documentation rules

- Edit the **Markdown** under `docs/`; never edit `docs/report.html` by hand. Rebuild it with `python3 docs/build_report.py` (or `make docs`) and commit it in the same commit as the Markdown change. CI runs `python3 docs/build_report.py --check`.
- Record decisions, supersessions and open questions in `docs/DECISIONS.md` (never delete a row). Use the glossary terms consistently: profile, profile bundle, catalog, sidecar, marketplace, tool repo, org data repo, account.
- Mark factual claims about Claude Code with `{V}` verified, `{R}` reported or `{U}` unverified.
- Examples are fictional and labeled as mockups until the behavior exists.

## Workflow rules (SR5)

- Every `uses:` is pinned to a **full 40-character commit SHA** with a trailing `# vX.Y.Z` comment. After adding or bumping an action by tag, run `scripts/pin-actions.sh` (needs `gh`); `scripts/check-pins.sh` fails CI otherwise. Dependabot keeps pins current.
- `permissions: {}` at the top of every workflow, explicit per-job grants (`contents: read` by default), `timeout-minutes` and `concurrency` everywhere, and `persist-credentials: false` on checkouts that do not push.
- Never put untrusted data (PR titles, branch names, tags, commit messages) inside `${{ }}` in a `run:` script: pass it through `env:` and quote it.
- Build and test use `pull_request`, never `pull_request_target`. Secrets exist only on protected environments (`release`, `nightly`) and are never sent to forks.
- Prefer plain shell calling `go`, `gh` and the project's own binary over third-party actions (R2); the allowed third-party actions are the ones already in use.

## Releasing

Releases are cut by maintainers.

1. Make sure `main` is green (`ci-ok`) and `docs/report.html` is current. Optionally run the `release` workflow manually with `dry-run` left on: it builds a snapshot without signing or publishing.
2. Tag the commit on `main` and push the tag: `git tag -s v0.1.0 -m "v0.1.0" && git push origin v0.1.0`.
3. The `release` workflow verifies that the tag is a semantic version, points at a commit on `main`, that `ci.yml` succeeded for it, that `go.mod` is tidy and the report is up to date.
4. The `release` job waits for approval of the protected **`release` environment**, which must be configured to require **two maintainers** (with "prevent self-review") and to allow deployments only from `v*` tags (see "Required repository rulesets"). Dry runs use the separate `snapshot` job, which has no environment, no signing and a read-only token.
5. goreleaser builds the six targets (`-trimpath`, `CGO_ENABLED=0`, commit-timestamped), writes `checksums.txt`, an SBOM per archive (syft), and the release notes from the conventional-commit history. The checksums file is signed keyless with cosign (`checksums.txt.sigstore.json`), and `actions/attest-build-provenance` attaches a SLSA build provenance attestation.
6. **Optional publishers** run only if their secret exists on the `release` environment, and never for pre-releases. Each is skipped with a notice otherwise:

   | Secret | Publishes to | Token needs |
   |---|---|---|
   | `HOMEBREW_TAP_TOKEN` | Homebrew cask in `ccshelf/homebrew-tap` | contents write on that repository |
   | `SCOOP_BUCKET_TOKEN` | Scoop manifest in `ccshelf/scoop-bucket` | contents write on that repository |
   | `WINGET_TOKEN` | WinGet pull request to `microsoft/winget-pkgs` from the `ccshelf/winget-pkgs` fork | contents write on the fork and pull request creation |

   Use fine-grained tokens scoped to exactly those repositories.
7. Verify the published release as a user would, following "Verifying a release" in [SECURITY.md](SECURITY.md). The macOS and Windows binaries are not notarized or signed yet, so Gatekeeper and SmartScreen may warn; targets without a native CI runner are noted as built but not natively tested.

The repository owner appears in `.goreleaser.yaml`, `.github/ISSUE_TEMPLATE/config.yml`, `CODEOWNERS` (team handle), `SECURITY.md` and this file, each marked `OWNER`.

## Required repository rulesets

The release signature identity is `https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/tags/<tag>`, so whoever can create a `v*` tag, or point one at an unreviewed commit, can mint a validly signed binary. Configure these rulesets (Settings > Rules) on the tool repository before the first release:

- **Tags `v*`**: creation restricted to maintainers (bypass list: maintainers only); require signed tags (annotated and signed, `git tag -s`); block deletion; block updates (a tag never moves).
- **Branch `main`**: required status check `ci-ok`; require pull requests with **code-owner review** and **two approvals**; block force pushes and deletion.
- **Environment `release`**: deployment tags limited to `v*`; **two required reviewers** with **"prevent self-review"**; the optional publisher secrets live here and nowhere else.

The release workflow also fails when the tagged commit is not an ancestor of `main` (`git merge-base --is-ancestor` after a full fetch, plus a GitHub compare API check), which backs up the rulesets but does not replace them. The `pins` job needs no environment; if you want CI on its pull request, add a repository secret `PINS_PR_TOKEN` (GitHub App token or fine-grained PAT scoped to this repository), because pull requests opened with `GITHUB_TOKEN` do not trigger workflows.

## Embedded checksums for the Action

After each release the `pins` job opens a pull request `chore: pin checksums for vX.Y.Z` that appends the six archive hashes to `action/pins.txt` (`<version> <os> <arch> <sha256>`). Review the lines against the signed `checksums.txt` and merge. Adopters pin the Action to a commit that contains the line for their version, which pins the binary without any signature service (see [action/README.md](action/README.md)).

## Nightly smoke test

`.github/workflows/nightly.yml` installs Claude Code with npm and runs `go test -tags realclaude ./internal/e2e/...` in an empty `HOME`. It runs only if `ANTHROPIC_API_KEY` is set on the protected `nightly` environment (main only) and otherwise skips with a notice. A failure opens or updates one issue labeled `nightly-failure`.

## GitHub Enterprise Server notes

The tool and its workflows are designed to the lowest common denominator (R2): no hard-coded hosts in logic, CLI first and workflow second, and few third-party actions.

- **Mirror the Action and the binary.** Mirror `ccshelf/ccshelf` (the repository with `action/`) onto your instance, for example with [`actions-sync`](https://github.com/actions/actions-sync), and pin it by full commit SHA. Mirror the release assets (`ccshelf_*` archives, `checksums.txt`, `checksums.txt.sigstore.json`) to an internal location; the Action must download from there (`base-url`) and verify the SHA-256. `cosign verify-blob` needs the Sigstore trusted root and is not offline; for an air-gapped instance use the `sha256` pin or `action/pins.txt`, or mirror the trusted root and pass it as `trusted-root`.
- **Third-party actions.** The workflows here use `actions/checkout`, `actions/setup-go`, `actions/upload-artifact`, `actions/download-artifact` and a few others. GHE Server administrators must mirror or allow them (or use GitHub Connect). Where that is not possible, build the binary with plain `go build` and call it directly; every `ccshelf` command works outside Actions.
- **Attestations.** Build provenance attestations have limited support on GHE Server; use the cosign bundle with `cosign verify-blob` and a mirrored trusted root (this is not offline without it) or the SHA-256 pin instead (see [SECURITY.md](SECURITY.md)).
- **Version skew.** GHE Server lags github.com: avoid newer Actions syntax in the Action and the data repo templates, or make it optional. The minimum supported Server version is not yet known.
- **Marketplace sources** for GHE hosts should use git URLs; see [docs/design/platform.md](docs/design/platform.md).

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).

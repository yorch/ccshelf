# Release and versioning

How `ccshelf` is versioned, proposed, approved, signed and published, and why. Decided 2026-10-06 as D-36 (release model) and D-37 (pull request title enforcement). The user chose "release PR from conventional commits". The mechanics are in `.github/workflows/release-please.yml`, `.github/workflows/release.yml`, `release-please-config.json`, `.release-please-manifest.json` and `.goreleaser.yaml`. The day-to-day steps are in [CONTRIBUTING.md](../../CONTRIBUTING.md) ("Releasing"). The signing rules are in [security.md](security.md) (SR5) and [SECURITY.md](../../SECURITY.md).

Confidence markers: `{V}` verified (read the source or documentation at the version named, or ran it), `{R}` reported, `{U}` unverified. Nothing here has run as a real release yet (see "What is validated").

## Assessment of three other projects

Read only, from `~/code/ccpod`, `~/code/cc-analyzer` and `~/code/pi-harness-delegate` (workflows, `.changeset/`, `package.json`, `AGENTS.md`, `CONTRIBUTING.md`, changelogs). All three use Changesets with a version pull request.

| Topic | ccpod | cc-analyzer | pi-harness-delegate | ccshelf before this change |
|---|---|---|---|---|
| Scheme and pre-1.0 policy | semver, 0.7.1. Bump chosen per change file. No written pre-1.0 rule | semver, 0.22.1. Same. No written rule | semver, 0.8.0. Same. No written rule | semver from a hand-pushed tag. No policy written |
| How a change is recorded | a Changeset file per pull request. CI fails without one (`--empty` for no-bump) | same | same. Docs-only pull requests also need one, because `files` ships the README | the commit message (conventional commits), not enforced |
| Release proposal | `changesets/action` opens and updates `chore: version packages` (bump, `CHANGELOG.md`, lockfile) | same | same | none: a maintainer decided and tagged |
| Approval and cut | merging the version pull request. The same workflow then verifies, builds, attests, runs `gh release create --target <sha>` (tag created by the workflow) and calls `docker.yml` | same, no Docker. Five assets incl. a Windows exe | merging it runs `bun run release` (npm publish by OIDC trusted publishing), then tags and creates the release | `git tag -s`, push. Two reviewers approve the `release` job |
| Provenance and signing | build provenance attestation per binary. Checksums file. No cosign | same | npm provenance through OIDC. No long-lived npm token | cosign keyless signature of `checksums.txt`, SLSA attestation, SBOMs |
| Checks and permissions | CI jobs (typecheck, lint, test, build, changeset). The release workflow re-runs `bun run verify`. Contents, pull-requests, id-token, attestations write at the **workflow** level | same | same, plus a post-publish `latest` dist-tag check with a repair hint | `verify` job (tag, ancestry, CI, tidy, report, pins). Per-job permissions. Protected `release` environment |
| Secrets | only `GITHUB_TOKEN` | only `GITHUB_TOKEN` | only `GITHUB_TOKEN` (no npm token) | `GITHUB_TOKEN`. Optional `HOMEBREW_TAP_TOKEN`, `SCOOP_BUCKET_TOKEN`, `WINGET_TOKEN`, `PINS_PR_TOKEN` |
| Changelog and notes | `@changesets/changelog-github` (PR link, commit, thanks). The GitHub release uses `--generate-notes`, so notes and `CHANGELOG.md` differ | same | awk cuts the notes out of `CHANGELOG.md`, so they match | goreleaser grouped commit subjects by regexp into the release body only. No `CHANGELOG.md` |
| Version strings kept in sync | `package.json` is the one source. Website examples such as `CCPOD_VERSION=v0.2.0` go stale | `version.ts` imports `package.json`. Tag and file must agree | `package.json`. After a "ghost tag" incident, the tag is read from the publishing commit with `git show` | the tag is the version (ldflags). Nothing in Go to bump |
| Failure and rollback | idempotent: a complete release for the commit is a no-op, a tag on another commit fails, assets are compared with an exact list. No rollback written. Docker re-push by `gh workflow run docker.yml -f version=X` | same | `gh release view` is the idempotence gate. The dist-tag check prints `npm dist-tag add` commands | re-run the job. No runbook |
| What AGENTS.md says | never bump the version or push a `v*` tag by hand. A `cut-release` skill | same | no manual version bump, no `npm publish` | "Releases are cut by maintainers" |

Strengths worth taking:
- The release is a reviewable pull request.
- The same workflow verifies again instead of trusting a prior CI run.
- Idempotence by checking the release and its exact asset list.
- Release notes extracted from the changelog (pi-harness-delegate).
- Publish verification at the end.
- Tag created at the exact commit.
- Agent-facing "never tag by hand".

Gaps those projects share, which this design addresses:
- The version pull request is created with `GITHUB_TOKEN`, so CI never runs on it. ccpod's `AGENTS.md` says not to make CI required for it.
- The release notes can differ from the changelog.
- Permissions apply to the whole workflow.
- No environment approval, no keyless signature, no written pre-1.0 policy or rollback runbook.

What is not taken: Changeset files. They need a file per pull request and a Node toolchain. ccshelf is a Go repository and already requires conventional commit subjects, so the commit history is the record (D-36).

## Assessment of ccshelf before this change

- **Tag as the trust anchor.** The Action and the docs pin the cosign identity `.../release.yml@refs/tags/<tag>`. That identity was only as strong as "who can create a `v*` tag". The rulesets text asked for signed tags. I could not confirm that GitHub rulesets can require signed tags (they can require signed commits on branches) {U}, so that control may never have been enforceable.
- **No reviewable proposal.** The version and the notes were decided at tag time. Notes came from goreleaser's regexp groups over commit subjects, so a badly titled squash commit silently fell into "Other changes". The notes omitted `chore(deps)`, including a security fix in a dependency.
- **CI before tagging was manual.** `verify` required a successful `ci.yml` run for the tagged commit, so the maintainer had to wait before pushing the tag.
- **The `pins` pull request never got CI** (it is opened with `GITHUB_TOKEN`) unless the optional `PINS_PR_TOKEN` existed. So the required check `ci-ok` would stay pending.
- **Version strings.** `internal/version` takes `Version` from `-ldflags` (goreleaser `{{ .Version }}`, the tag without `v`). A plain `go build` reports `dev` and CI builds report `0.0.0-ci`. The Action's `version` input is required and has no default, so there is no stale default. `action/README.md` shows `v0.1.0` in examples next to a placeholder SHA, which is intentional. The `pins` job appends to `action/pins.txt` after each release. A `site/` directory is on another branch. It must read the version from the release (not hard-code it) when it lands.
- **What was already good** and is kept unchanged in effect: SHA-pinned actions, `permissions: {}`, the protected `release` environment with two reviewers, keyless cosign signing of `checksums.txt`, build provenance, SBOMs, the ancestry check against `main`, the snapshot dry run.

## Decision (D-36)

A bot-maintained **release pull request** derived from conventional commits, using `googleapis/release-please-action` v5.0.0, pinned by full commit SHA {V}. It carries the version bump and the generated `CHANGELOG.md`. Merging it creates the tag and a draft GitHub release, and the existing signed goreleaser pipeline publishes. We chose release-please over `semantic-release` (needs Node and a token with push rights, publishes directly with no reviewable pull request). We also chose it over a script of our own (we would re-implement version rules and changelog formatting).

```
 pull request  --squash-->  main  (subject = pull request title, checked by pr-title.yml)
                              |
                              v   push to main
                +--------------------------------+
                |  release-please.yml            |  job release-please (contents, pull-requests,
                |  release-please-action         |  issues write; no checkout)
                +--------------------------------+
                  |                    |
     commits since last release     release PR merged?
     are releasable (feat, fix,          |
     perf, revert, breaking)             v
                  |            tag vX.Y.Z at the merge commit (lightweight, created
                  v            with GITHUB_TOKEN) + DRAFT release with the changelog
     open or update             as its notes
     "chore(main): release X.Y.Z"        |
     (version + CHANGELOG.md)            v
                  |            job dispatch (actions write): POST workflow_dispatch
                  v            of release.yml with ref = the tag
     job dispatch: workflow_dispatch of       |
     ci.yml + pr-title.yml on the branch      v
     -> `ci-ok`, `pr-title` report    +--------------------------------------------+
                  |                   | release.yml  (github.ref = refs/tags/vX.Y.Z) |
     maintainers review and merge     |  verify (read-only): semver tag, ancestor of |
     (two approvals, code owner,      |   main, manifest == tag, no branch of that   |
     ci-ok, pr-title)                 |   name, ci.yml green for the commit          |
                                      |  release: environment `release` (2 reviewers)|
                                      |   draft checks (one draft, name == tag_name  |
                                      |   == tag, target == tag commit);             |
                                      |   goreleaser -> archives, checksums, SBOMs,  |
                                      |   cosign bundle -> un-draft the release;     |
                                      |   attest provenance; verify the signature    |
                                      |  pins: PR with the archive checksums + CI    |
                                      +--------------------------------------------+
```

## Versioning rules

- **Tag is the version.** Tags are `vX.Y.Z` (`include-v-in-tag`, no component in the tag). Nothing in Go or the docs is bumped. `-ldflags` read the tag. `.release-please-manifest.json` holds the last released version and is the second source that `verify` compares with the tag.
- **Pre-1.0 (0.x), chosen** (`bump-minor-pre-major: true`, `bump-patch-for-minor-pre-major: false`) {V: `DefaultVersioningStrategy`, release-please v17.6.0}:
  - A breaking change (`!` or a `BREAKING CHANGE:` footer) bumps the **minor**.
  - A `feat` bumps the **minor**.
  - `fix`, `perf` and `revert` bump the **patch**.

  So a minor bump means "read the changelog before upgrading", and a patch bump is safe. Breaking changes pre-1.0 are expected (the profile and catalog schemas may still change). The notes list them under "BREAKING CHANGES".
- **1.0.0** is a deliberate act: forced with the `release-as` setting (see "Forcing a version" below). Criteria belong in D-36's revisit trigger: the schemas, the Action inputs and the CLI flags are stable and the six targets have run natively.
- **First release.** The manifest says `0.0.0`, which release-please treats as "never released" {V: `manifest.ts`}. The first version is then the `initial-version` setting, **not** a bump: without `initial-version` it would be 1.0.0 {V: `BaseStrategy.initialReleaseVersion`}. The setting is `0.1.0`. The schema marks the `release-as` config key as deprecated, and the key is sticky (it applies to every later release until somebody removes it) {V: schema and `base.ts` v17.6.0}.
- **Forcing a version** (the first release, 1.0.0, or a correction). Two facts shape this {V: release-please v17.6.0 source}. First, a commit of a hidden type (`chore`, `docs`, ...) yields an empty changelog, and `BaseStrategy.buildReleasePullRequest` then skips the release (`changelogEmpty`, "No user facing commits found ... skipping"). So a `chore:` commit carrying `Release-As:` does **nothing**. Second, `release-as` in the package configuration wins over the commit-derived version. So:
  1. Open a pull request that sets `"release-as": "X.Y.Z"` for the `.` package in `release-please-config.json`. The pull request must itself be releasable: its title is `fix(release): ...` or `feat(release): ...` (a real change, or the title of a change that is in it anyway). Merge it. The release pull request now proposes `X.Y.Z`.
  2. Review and merge the release pull request as usual.
  3. Remove `release-as` again in a `chore(release): drop release-as` pull request, before merging anything releasable. Left in place, the sticky key forces the next release to the same version.

  A `Release-As:` footer in a commit message also works for a releasable commit, but this repository does not use it. The commit body reaches `main` only through the squash default, and `pr-title` rejects a pull request description carrying `Release-As:` (see "Pull request titles").
- **Changelog sections.** Shown: `feat` (Features), `fix` (Bug fixes), `perf` (Performance), `revert` (Reverts). Hidden: `docs`, `test`, `ci`, `build`, `refactor`, `chore`. release-please opens a release pull request only when at least one visible entry exists (see "Forcing a version") {V: `BaseStrategy.buildReleasePullRequest`}. I chose to hide the maintenance types rather than add a "Maintenance" section, because a docs-only or CI-only merge should not produce a release pull request. Consequences: **dependency bumps do not trigger a release** (Dependabot titles are `chore(deps)`). Ship a vulnerability fix in a dependency with a title such as `fix(deps): bump x to 1.2.3 (CVE-...)`. Security fixes are `fix(security): ...`. The old goreleaser "Security" group by regexp is gone.
- **No releasable commits** (only hidden types since the last release): no release pull request is opened or updated, nothing is tagged, nothing is built. An existing open release pull request stays as it is.
- **Hotfix.** A fix is an ordinary `fix:` pull request on `main`. The release pull request then shows the next patch. Merge it. It contains everything on `main` since the last release. This model cannot ship only a fix while unreleased `feat` commits sit on `main`. This is by design:
  - There are no release branches.
  - `verify` requires the tagged commit to be an ancestor of `main`.
  - SECURITY.md supports only the latest release and `main`.

  If the pending changes are not ready, revert them (`revert:`) first.
- **Pre-releases** (`vX.Y.Z-rc.1`) are not produced by this configuration. goreleaser and `verify` accept such a tag, and `prerelease: auto` marks it.

## Token, tag and signing chain

| Actor | Token and permissions | Can do | Cannot do |
|---|---|---|---|
| `release-please` job | `GITHUB_TOKEN`: contents, pull-requests, issues write. No checkout, no repository code runs | create and update the release branch and pull request, labels, the tag, the draft release | push to `main` (the ruleset requires pull requests), run anything that signs, reach secrets (none are passed) |
| `dispatch` job | `GITHUB_TOKEN`: actions write | start `ci.yml` and `release.yml` (`workflow_dispatch`) | change contents |
| `verify` job | contents read, actions read | read the repository, branches and CI runs (it cannot see draft releases: the API lists them only to tokens with push access) | write |
| `release` job | contents, id-token, attestations write, **after two reviewers approve the `release` environment, from a `v*` tag only** | check the draft release (see "Trust change" below), build, sign (keyless OIDC), verify the signature with cosign, attest, upload to the draft, publish it | run without approval |
| optional publishers | `HOMEBREW_TAP_TOKEN`, `SCOOP_BUCKET_TOKEN`, `WINGET_TOKEN` on the `release` environment | write to the tap, bucket and winget fork | anything else |
| `pins` job | contents, pull-requests, actions write | open the checksum pull request and run CI on its branch | merge it |

No personal access token and no GitHub App key is needed. The one place a token would otherwise be unavoidable is "GITHUB_TOKEN creates a tag or pull request and nothing runs". Two facts remove it {V: GitHub documentation on triggering workflows, and release-please-action's README warning}:
- Events created with `GITHUB_TOKEN` do not start workflows **except `workflow_dispatch` and `repository_dispatch`**.
- The REST endpoint for `workflow_dispatch` accepts a branch **or a tag** as `ref` {U: documented, not exercised yet}.

**Why not call `release.yml` as a reusable workflow from `release-please.yml`.** The Fulcio certificate identity is the `job_workflow_ref` of the workflow that runs the signing job {U: from the Sigstore GitHub issuer documentation}. For a reusable workflow, that is the called file at the *caller's* ref, here `refs/heads/main`. So every release would be signed as `release.yml@refs/heads/main`, and the "exact tag" identity that SECURITY.md and the Action rely on would be lost. Dispatching the workflow with the tag as `ref` keeps `github.ref = refs/tags/vX.Y.Z`. The `release` job's `cosign verify-blob` step proves the identity on every release.

**CI on bot-opened pull requests.** `ci.yml` and `pr-title.yml` have a `workflow_dispatch` trigger. After each release-please run that created or updated the release pull request, `dispatch` starts both on the branch named by release-please's `pr` output {V: the action's `pr` output}. That output is `headBranchName`, expected to be `release-please--branches--main`, and the job fails on an unexpected name. Check runs attach to the head commit of the branch, so the required checks `ci-ok` and `pr-title` show on the pull request {U: the usual behaviour of the checks API, confirm on the first release pull request}. In dispatch mode, `pr-title` reads the open pull request of the branch through the API. It applies the full rules with the real author, including the reserved release title. The `pins` job does the same for its branch. If the repository later adopts a GitHub App token, pull requests and tags trigger workflows natively, and these dispatches become redundant. To avoid two runs, the `release.yml` `push: tags` trigger would then have to be added again and the dispatch step removed.

**Trust change, stated plainly.**

- Before: a maintainer pushed a tag (the ruleset text asked for signed tags, probably not enforceable {U}). Two reviewers approved the environment. The job signed.
- Now: `github-actions[bot]` creates a lightweight, unsigned tag at the merge commit of a release pull request. Two maintainers and the code owner approved that pull request, and it passed `ci-ok`. `verify` accepts a tag only if all of these are true:
  - It is a semantic version.
  - It is an ancestor of `main`.
  - It is equal to the version in `.release-please-manifest.json` at that commit (so a tag cannot name a version no release pull request produced).
  - `ci.yml` succeeded for the commit.

  `verify` also refuses a branch of the same name as the tag (the dispatch API takes a short ref name). The `release` job, which can read drafts because it has `contents: write` (see the token table), requires all of these:
  - Exactly one draft with `tag_name` equal to the tag.
  - Exactly one release with `name` equal to the tag (goreleaser finds its draft by name).
  - The two are the same release.
  - No published release uses the tag.
  - The draft's `target_commitish` resolves to the commit the run started on.

  So a tag that somebody created first at another commit fails closed. These draft checks run after the environment approval. The two-reviewer approval of the `release` environment is unchanged. It is the control that matters most: nothing is signed or published without it.
- What the new design adds to the attack surface: the release-please action (third party, SHA-pinned, runs with `contents: write` and no repository checkout, no secrets) and the `GITHUB_TOKEN` permission to create tags. A compromised action could create a `v*` tag, but only at a commit already on `main` whose manifest equals the tag, and only once (the ruleset blocks updating a tag and lets only maintainers delete one). It still cannot sign.
- What it removes: any maintainer typing a version by hand, and an unsigned-but-required "signed tag" rule that could not be enforced.
- **Rulesets that follow:**
  - Tags `v*`: block updates and deletion. Do **not** restrict creation unless the bot can be put on the bypass list. The built-in Actions identity probably cannot {U}, but a dedicated GitHub App can. The hardened variant is a GitHub App (contents, pull requests, actions write, with the private key stored as a secret on a protected environment). release-please uses it as `token:`, it is on the tag ruleset's bypass list, and only it and maintainers can create tags. That adds one long-lived key, which is why it is not the default.
  - `main`: required status checks `ci-ok` and `pr-title`, pull requests with code-owner review and two approvals (the release pull request is subject to the same rule), no force push or deletion.
  - Environment `release`: tags `v*` only, two reviewers, prevent self-review.

## CI job selection

`ci.yml` starts with a `changes` job. It runs `scripts/ci_changes.py` (stdlib Python, tests in `scripts/test_ci_changes.py`, which run in the `docs` job) and publishes boolean outputs. The other jobs have `if: needs.changes.outputs.<name> == 'true'`. The workflow has **no workflow-level `paths:` filter**, so `ci-ok` always reports. `ci-ok` treats a skipped job as passed: only `failure` and `cancelled` fail it. That rule is this repository's own script. That a skipped job in `needs` does not skip `ci-ok` under `if: always()` is documented GitHub behavior {R}. So a skipped job never blocks a merge. `ci-ok` needs `changes` too: if the classifier itself fails, the check fails.

| Group | Paths | Jobs it selects |
|---|---|---|
| go | `**/*.go`, `go.mod`, `go.sum`, `cmd/`, `internal/`, `schema/`, `.golangci.yml`, `scripts/check-cover.sh` | `test` (with the smoke steps), `govulncheck`, `build`, the Go steps of `lint`, and also `examples`, `site`, `install-test` and `action-e2e` (they run the built binary) |
| installer | `scripts/install.sh`, `scripts/install.ps1`, `scripts/test_install.*`, `scripts/make-e2e-release.sh`, and `LICENSE` and `README.md` (the end-to-end archive contains them) | `install-test`, `install-mutants` |
| action | `action/` | `action-e2e` (includes the offline `action/test/run.sh`), `lint` pins and actionlint |
| docs | `docs/`, `site/`, root `*.md` (so `CHANGELOG.md`), `LICENSE`, `.gitignore`, `.release-please-manifest.json`, the docs, site, link and EOL scripts, `scripts/ci_changes.py` and its test | `site` |
| examples | `examples/`, `scripts/check-examples.sh` | `examples`. `examples/` also selects the Go group (the starter is golden output of `internal/scaffold`) and the pins and actionlint steps (it holds workflows) |
| workflows | `scripts/check-pins.sh`, `scripts/pin-actions.sh` (and `action/`, `examples/`) | the pins and actionlint steps of `lint` |
| mixed | `.goreleaser.yaml` (go, installer, action), `.gitattributes` and `.editorconfig` (go, docs, examples), `Makefile` (go, docs) | the union |

The `docs` job (report check, link check, EOL check, the classifier's tests) always runs: the EOL check covers every file in the tree and the whole job is cheap. The `site` job runs for go or docs changes because its CLI reference check builds the binary.

**Fail open.** Everything runs in each of these cases:
- The event is not `pull_request` (a push to `main`, a merge group, a `workflow_dispatch`).
- The changed files cannot be computed (the script validates the two SHAs and runs `git diff --name-only --no-renames base...head` on a full-history checkout), or the diff is empty.
- Anything under `.github/` changed (workflows, CODEOWNERS, Dependabot).
- `scripts/ci_changes.py` or `scripts/test_ci_changes.py` changed (a pull request must not gate itself).
- The pull request branch starts with `release-please--`. The branch name is passed as `HEAD_REF` through `env:`. On a fork it is attacker-controlled, and the only effect of a matching name is that more jobs run.
- A changed file matches no rule, which includes `release-please-config.json` and any new top-level file.

Fail-open is what keeps the release path intact. The `verify` job of `release.yml` waits for a successful `ci.yml` run for the tagged commit, which is the full push-to-`main` run. The dispatched run, also full, covers the release pull request. A unit test asserts that every tracked file is classified (or deliberately fails open), so a new file needs a rule in the same pull request.

**The release pull request.** It changes only `CHANGELOG.md` and `.release-please-manifest.json`, which alone would select just `site`. A native `pull_request` run can still happen on it, for example after a maintainer closes and reopens it or pushes to its branch, or if a GitHub App token is adopted. A cheap green `ci-ok` from that run could supersede a failed full dispatched run on the same commit. The `release-please--` fail-open rule above makes a native run as complete as the dispatched one, so the two cannot disagree. Nothing skips by branch name. One earlier run that sat in `action_required` is GitHub waiting for approval of a workflow run triggered by a bot-owned pull request {U: not reproduced here}.

**The first real pull request shows whether gating works.** The `changes` job uses `fetch-depth: 0` unconditionally. The form `cond && 0 || 1` always gives 1, because 0 is falsy in GitHub expressions {R}. That would make every diff fail and every pull request run everything. When the diff cannot be computed, the step summary says so in a warning line. A unit test documents the failure in a depth-1 clone.

The test matrix drops the two experimental legs (`ubuntu-24.04-arm`, `windows-11-arm`) on pull requests. They run on `push`, `merge_group` and `workflow_dispatch`. The classifier builds the matrix (`fromJSON`), because a job-level `if:` cannot read the `matrix` context {R}.

## Pull request titles (D-37)

Maintainers squash-merge, so the pull request title becomes the commit subject and the changelog entry. The workflow `pr-title.yml` runs `scripts/check_pr_title.py` (stdlib Python, tests in `scripts/test_check_pr_title.py`) on every pull request, including when the title or description is edited. Its job `pr-title` is a **second required check** next to `ci-ok`. It is a separate workflow so that an edit never re-runs the `ci.yml` matrix (whose `cancel-in-progress` would cancel runs). It has `permissions: {}` at the top, `contents: read` and `pull-requests: read` on the job, and no third-party action. `ci-ok` does not need `pr-title`.

- Format `type(scope)!: description`:
  - Types `feat fix docs test ci build refactor perf chore revert`.
  - Scope optional (lowercase letters, digits, `. _ / -`).
  - `!` optional, and it marks a breaking change.
  - The description starts with a lowercase letter or digit and has no trailing period.
  - The whole title is at most 72 characters (120 for Dependabot group titles).
- The title reaches the script through `env:` only, and the script prints it escaped. So a hostile title cannot inject a workflow command.
- release-please's own title is `chore(main): release X.Y.Z` (configured explicitly as `chore${scope}: release${component} ${version}`) and satisfies the rule. The `pins` pull request title is `chore: pin checksums for vX.Y.Z`.
- GitHub's default revert title (`Revert "feat: x"`) fails. Use `revert: x`.
- `pr-title` rejects titles with a control, format, surrogate, private-use or line/paragraph separator character (Unicode categories Cc, Cf, Cs, Co, Zl, Zp: U+2028, U+0085, U+202E, U+200B, U+FEFF, U+00AD, DEL, ...). So a changelog line cannot hide text or reorder it.
- `pr-title` accepts a title `chore(main): release ...` only from the release bot (`github-actions[bot]`, passed as `PR_AUTHOR`). Nobody else can open a pull request that looks like the release pull request.
- **The title is not the only input.** release-please v17.6.0 also reads the pull request **description** when it processes a merge commit {R: reported from the source, `splitMessages`, not re-checked here}:
  - A `BEGIN_COMMIT_OVERRIDE` / `BEGIN_NESTED_COMMIT` block replaces or adds changelog entries.
  - A `Release-As:` footer forces the version.
  - A paragraph that starts like a conventional commit (`feat: ...`) can become an extra commit.

  With the squash default "Pull request title and description", the description reaches `main`. So a contributor could inject changelog lines, a breaking-change bump or a forced version. Three layers:
  1. **Squash default "Pull request title"** (setup step 3): the description is not copied into the commit, so none of this reaches release-please. This is the real control.
  2. `pr-title` rejects a description that contains `BEGIN_COMMIT_OVERRIDE`, `BEGIN_NESTED_COMMIT` or a line starting `Release-As:` (any case, after quote or list marks), for everyone except the release bot. The description reaches the script through `env:` (`PR_BODY`), and the script never prints it. Maintainers force a version through `release-as` in the configuration (see "Forcing a version").
  3. The release pull request carries the reminder "Review every changelog entry and the version before merging" (`pull-request-footer`), and the two approvals plus the code owner are the last review.
  **Residual risk:** `pr-title` does not reject a description paragraph that merely looks like a commit (`feat: ...`, `BREAKING CHANGE: ...`), because that check would have false positives, for example in Dependabot release notes. A commit message edited by hand at merge time is outside every check here. Layer 1 neutralises both and layer 3 catches them, but nothing prevents them.
- With the squash default "Pull request title" (layer 1), the `!` of the title still marks a breaking change. A breaking change is therefore always visible in the title.

## Failure and rollback

| Situation | What happens | What to do |
|---|---|---|
| `release-please.yml` fails after the release pull request was merged | no tag yet | re-run the workflow. release-please is idempotent |
| `dispatch` cannot start `release.yml` | tag and draft release exist, nothing built | `gh workflow run release.yml --ref vX.Y.Z -f dry-run=false` |
| `verify` fails (CI red on the commit, manifest mismatch, a branch named like the tag) | nothing built | fix on `main` with a new pull request. The tag stays. If the tagged commit itself is bad, publish the next version instead (a tag never moves) |
| `release` job or goreleaser fails after the tag exists | the release is still a **draft**, the tag stays, nobody sees a half release. `replace_existing_artifacts` lets a retry overwrite a partial upload to the draft | re-run the failed job in the same run (the environment asks for approval again) or dispatch again. The "reset notes" step removes a duplicated verification footer |
| a step after publishing fails (attestation, signature check, artifact upload) | the release is public with archives and the signed checksums | re-running goreleaser is not possible (it only reuses a draft, so a published release is never touched, by design). If the signature or the provenance attestation is missing, treat it as a bad release (next row) |
| bad release (wrong binary, leaked data, broken signature) | users may have downloaded it | **Yank**: convert the GitHub release back to a draft (or delete it) and add a notice. Keep the tag (tags are never moved or reused, and release-please finds the last release by tag). Then publish a fixed **patch** through the normal release pull request. Delete the tag only if the release was never published, and then also set the manifest back by a pull request. Deleting a tag that release-please has recorded makes it fall back to the manifest version and can produce an oversized next changelog {U}. If "Immutable releases" is enabled for the repository, a published release cannot be edited, which blocks this yank but also blocks tampering {U}. The setting works with the draft flow, because assets are attached before publishing |
| `release` fails at "Draft release checks": the draft targets another commit than the tag (a **squatted tag**: somebody created `vX.Y.Z` first) | nothing signed or published | delete the tag through the maintainers' ruleset bypass (`git push origin --delete vX.Y.Z`), delete the draft release if it names the wrong commit, re-run `release-please.yml` (it recreates tag and draft at the merge commit) and then dispatch `release.yml` again. If the tag cannot be explained, treat it as a security incident |
| a step after publishing fails and the provenance attestation is missing (`gh attestation verify <archive> --repo OWNER/REPO` finds nothing), but the cosign bundle verifies | the release is public, signed, checksummed. Only the SLSA attestation is absent | the failed job cannot simply be re-run: `Draft release checks` refuses a tag whose release is already published, and goreleaser must not run again. The `release-dist` artifact of the run (kept 90 days: archives, `checksums.txt`, bundle, SBOMs) is the input for a re-attestation. But a re-attest mode in `release.yml` (download the artifact, compare its `checksums.txt` with the published asset, run only the attest step) is **not implemented** {U}. Until it is, state the gap in the release notes. If the cosign bundle is missing too, yank and publish the next patch |
| the `pins` job fails or is re-run | it is idempotent. It exits when `action/pins.txt` already has the version, reuses an existing branch and an open pull request, and restarts a leftover branch from `main` | re-run the job. Open the pull request by hand only if it keeps failing (copy the six lines from the signed `checksums.txt`) |
| a release whose target commit changes `.github/workflows/` relative to the default branch | {U} workflow-file changes may need a permission (`workflows`) that the `GITHUB_TOKEN` does not have, so release-please or the dispatch could fail | **watch item.** Avoid merging workflow changes while a release pull request is pending. Watch for it on the first release. If it happens, merge the workflow change first, let release-please regenerate the pull request, then release |
| the release pull request is stale or wrong | it is regenerated on every push to `main` | do not edit `CHANGELOG.md` by hand in it. A wrong entry cannot be reworded after the squash merge, so add a corrective commit, or correct the release notes after publishing by editing the GitHub release |
| `ci-ok` or `pr-title` never appears on the release pull request | the `dispatch` job failed or its check does not satisfy the ruleset | run `gh workflow run ci.yml --ref release-please--branches--main` and `gh workflow run pr-title.yml --ref release-please--branches--main`. If the ruleset does not accept the dispatched check, switch to the GitHub App token variant |

## Cadence and who can release

No fixed cadence. The maintainers cut a release when they merge the release pull request. They do this normally after a batch of `feat` or `fix` merges, and always for a security fix (a `fix(security):` merge makes the pull request appear within a minute). Only maintainers can merge it (the `main` ruleset), and only the two reviewers of the `release` environment can approve the publication. Agents never merge the release pull request unless the user asks (AGENTS.md).

## One-time maintainer setup and bootstrap runbook

Before the first release (everything under Settings of the tool repository):

1. **Actions > General > Workflow permissions:** enable "Allow GitHub Actions to create and approve pull requests". The default token permission can stay read-only: the workflows request what they need {V: release-please-action README}. **Side effect:** the same setting also lets a workflow token submit an approving review. Mitigate it in the `main` ruleset: require **code-owner review** (CODEOWNERS) from people, so a bot approval does not satisfy it, and enable **"Require approval of the most recent reviewable push"**. No workflow in this repository submits approvals.
2. **Rulesets and the environment `release`:** set up `main`, tags `v*` and the environment `release` as listed under "Rulesets that follow" in [Token, tag and signing chain](#token-tag-and-signing-chain). Optional publisher secrets live on the environment.
3. **Pull requests > squash merging:** allow squash merging only, with the default commit message **"Pull request title"** (not "title and description", and not "commit messages"). The description must not reach `main` (see "Pull request titles").
4. **Optional:** Settings > Code security > "Immutable releases" {U}.
5. Check `ci.yml` and `release.yml` are on the default branch (workflow_dispatch needs the file on the default branch to appear), then merge this change.

First release:

1. After the merge, `release-please.yml` runs on the push. History before the first release may be long. The changelog lists `feat`, `fix`, `perf` and `revert` entries from the last 500 commits (`commit-search-depth`) {V}. To start the notes later, add `"bootstrap-sha": "<full sha of the last commit to leave out>"` to `release-please-config.json` in a pull request before merging the release pull request. release-please ignores it after the first release, and you can then remove it {V}.
2. Open the pull request `chore(main): release 0.1.0`. Confirm `ci-ok` and `pr-title` appear (see the failure table if not), read the version and `CHANGELOG.md`, merge with two approvals. To force another version, follow "Forcing a version" (a releasable `fix(release):` pull request that sets `release-as`) before merging it.
3. Watch `release-please.yml` create the tag `v0.1.0` and the draft release, and `dispatch` start `release.yml`. Approve the `release` environment (two reviewers).
4. Verify as a user would (SECURITY.md, "Verifying a release"), then merge the `pins` pull request after comparing its lines with the signed `checksums.txt`.
5. Dry run any time without a tag: `gh workflow run release.yml` (defaults to `dry-run=true`: snapshot, no signing, no publishing).

## End-user installers (D-39)

Users install a release with `scripts/install.sh` (Linux and macOS, POSIX `sh`) or `scripts/install.ps1` (Windows, PowerShell 5.1 and 7). They fetch the scripts from the release itself: `releases/latest/download/install.sh` and `install.ps1`. Both scripts are release assets and are listed in `checksums.txt` (`checksum.extra_files` and `release.extra_files` in `.goreleaser.yaml`). So the cosign signature of `checksums.txt` covers them, and the `latest/download` URL serves the script of the newest published release {V: 2026-10-07, `curl -fsSL .../releases/latest/download/install.sh | sh` installed v0.2.0 on darwin/arm64 while v0.3.0 was still a draft}. A snapshot build lists both scripts next to the six archives in `dist/checksums.txt` {V}.

**Verification policy (decided with the user).** The installer always checks the SHA-256 of the archive against the same release's `checksums.txt`, with exactly one matching line. There is no option to skip it. The installer checks the cosign signature of `checksums.txt` when `cosign` is on `PATH`, and a failure is fatal. `--require-signature` makes a missing cosign fatal too. The signature check uses the exact identity `release.yml@refs/tags/<tag>` and the GitHub Actions issuer. Only `--cosign-identity` and `--cosign-issuer` override them, and they matter only for a release signed by someone else's workflow. A GitHub Enterprise Server mirror of the public release uses the default identity. A mirror's `latest` can name an older signed release, so pin `--version` when that matters. Without cosign the installer warns that the check detects corruption, not a hostile release. Rationale: the installer cannot assume cosign, but must not silently downgrade when it exists.

**Resolving "latest" without the GitHub API.** The installer fetches `<base>/latest/download/checksums.txt`, and the one archive named for the platform gives the version (`ccshelf_<version>_<os>_<arch>`). It then fetches everything else (checksums, bundle, archive) from `<base>/download/v<version>/`. So a release published in between cannot mix two releases, and there is no API rate limit or token.

**Hardening shared with the Action's installer** (`action/scripts/install.sh`):
- https only, with redirects restricted to https (`curl -q --proto =https --proto-redir =https`, `-q` first so `~/.curlrc` is ignored). Proxy and CA variables are honored, and TLS verification is never disabled. `install.sh` has no `wget` fallback, because `--https-only` does not cover redirects outside recursive mode, `~/.wgetrc` can disable certificate checks and busybox wget rejects the flags.
- Digest computed on stdin.
- A private temporary directory.
- Strict validation of every input.
- No `eval`, no downloaded text executed.

Added for an installer that writes into a user directory:
- The archive's entries are allowlisted before extraction (`ccshelf` plus optionally `LICENSE` and `README.md`, regular files only).
- Only the one binary is extracted, by streaming, under a size cap.
- The target directory must not be a symlink or junction. It must be owned by the user and not other-writable (Unix).
- An existing `ccshelf` that does not identify itself is replaced only with `--force`.
- The binary is copied next to its destination and renamed (on Windows an in-use binary is renamed to `ccshelf.exe.old` first).
- After the move, the target must be a regular file with the verified SHA-256.
- `--force` removes a symlink with that name before the move, and refuses a directory.
- An existing file is run to identify it only when it contains the text `ccshelf`, under a 5-second limit, and only where `timeout` exists.

The Windows script edits the user `PATH` only with `-AddToPath` (read unexpanded from the registry, written back as REG_EXPAND_SZ). It takes its parameters under `Ccshelf*` names, with the documented short names as aliases. It runs the installer in a child scope, so that `irm | iex` defines nothing and leaves the caller's variables, functions and preferences alone (tested).

**Tests.**
- `scripts/test_install.sh` is offline and hermetic. It uses a private `PATH` of symlinks to basic tools, local release trees through `file:///`, a fake `curl` whose calls are checked flag by flag and value by value, `cosign`, `uname`, `mktemp`, `timeout`, `head`, `tar`, `mv`. It tests size caps through a copy of the installer with tiny caps, and signals through a Python driver.
- Its mutation mode (`--mutants`) applies about seventy deliberate defects, each to a temp copy of the installer, and runs them in parallel. Each defect must fail the suite.
- `scripts/test_install.ps1` uses plain assertions, child processes under every available PowerShell, and Go-built fake executables.
- The `install-test` CI job runs both on Linux, macOS and Windows. It then builds the real binary and an archive for the runner (`scripts/make-e2e-release.sh`) and installs it through the real script, pinned and latest.

`install.sh`'s `https` code path is exercised through a fake `curl` only. `install.ps1`'s is exercised against a real local HTTPS server with a self-signed certificate (manual redirects, https-to-http refusal, redirect loop, size caps with and without a declared length, 404). That runs on Linux (trusted through `SSL_CERT_FILE`) and on the Windows runner (trusted through the certificate store). On macOS those tests are skipped, because a certificate cannot be trusted per process there. The maintainers' post-release check in CONTRIBUTING.md covers a real download from GitHub.

## What is validated

Run in the worktree:
- `actionlint` with shellcheck on every workflow.
- `scripts/check-pins.sh` (every `uses:` is a full SHA and its `# vX.Y.Z` comment resolves, including `googleapis/release-please-action` v5.0.0 at `45996ed`).
- `goreleaser check`.
- `goreleaser release --snapshot --clean --skip=publish,sign,sbom`.
- `python3 -I -m unittest discover -s scripts -p 'test_*.py'` (26 tests: title and description rules, the reserved release title, invisible Unicode, the length bounds).
- The draft-release checks of the `release` job, run against a stubbed `gh` with nine fixtures (good, two drafts, published, name mismatch, different releases, wrong target, branch-named target, none, unrelated releases).
- The config JSON checked against release-please's `schemas/config.json` at v17.6.0 (no jsonschema module, so a key-and-type check by hand).

Read, not run: release-please v17.6.0 source for versioning, initial version, empty changelog, forced tags and draft handling, and goreleaser v2.18.2 source and docs for `use_existing_draft`, `mode` and immutable releases.

Not validated (needs the real repository):
- A real release.
- That a read-only token sees no draft releases (taken from the REST documentation and the lead's finding, {R}).
- That the dispatch API would accept `ref=refs/tags/vX.Y.Z`. The REST and `gh workflow run --ref` documentation only say "branch or tag name", so the short name is used (see "Trust change").
- That the dispatched `ci.yml` run satisfies the required check.
- That a draft release plus `force-tag-creation` makes release-please find the previous release on the next run.
- That goreleaser publishes the draft that release-please created and appends to its notes as read in its source.
- The OIDC identity of a workflow dispatched on a tag (documented, not exercised).
- Whether GitHub lets the built-in Actions identity bypass a tag ruleset.

The release-please CLI was not run (it needs the network and a token for a repository that does not exist yet).

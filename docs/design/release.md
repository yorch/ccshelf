# Release and versioning

How `ccshelf` is versioned, proposed, approved, signed and published, and why. Decided 2026-10-06 as D-36 (release model) and D-37 (pull request title enforcement); the user chose "release PR from conventional commits". The mechanics are in `.github/workflows/release-please.yml`, `.github/workflows/release.yml`, `release-please-config.json`, `.release-please-manifest.json` and `.goreleaser.yaml`. The day-to-day steps are in [CONTRIBUTING.md](../../CONTRIBUTING.md) ("Releasing"); the signing rules are in [security.md](security.md) (SR5) and [SECURITY.md](../../SECURITY.md).

Confidence markers: `{V}` verified (read the source or documentation at the version named, or ran it), `{R}` reported, `{U}` unverified. Nothing here has run as a real release yet (see "What is validated").

## Assessment of three other projects

Read only, from `~/code/ccpod`, `~/code/cc-analyzer` and `~/code/pi-harness-delegate` (workflows, `.changeset/`, `package.json`, `AGENTS.md`, `CONTRIBUTING.md`, changelogs). All three use Changesets with a version pull request.

| Topic | ccpod | cc-analyzer | pi-harness-delegate | ccshelf before this change |
|---|---|---|---|---|
| Scheme and pre-1.0 policy | semver, 0.7.1; bump chosen per change file; no written pre-1.0 rule | semver, 0.22.1; same; no written rule | semver, 0.8.0; same; no written rule | semver from a hand-pushed tag; no policy written |
| How a change is recorded | a Changeset file per pull request; CI fails without one (`--empty` for no-bump) | same | same; docs-only pull requests also need one because `files` ships the README | the commit message (conventional commits), not enforced |
| Release proposal | `changesets/action` opens and updates `chore: version packages` (bump, `CHANGELOG.md`, lockfile) | same | same | none: a maintainer decided and tagged |
| Approval and cut | merging the version pull request; the same workflow then verifies, builds, attests, runs `gh release create --target <sha>` (tag created by the workflow) and calls `docker.yml` | same, no Docker; five assets incl. a Windows exe | merging it runs `bun run release` (npm publish by OIDC trusted publishing), then tags and creates the release | `git tag -s`, push; `release` job approved by two reviewers |
| Provenance and signing | build provenance attestation per binary; checksums file; no cosign | same | npm provenance through OIDC; no long-lived npm token | cosign keyless signature of `checksums.txt`, SLSA attestation, SBOMs |
| Checks and permissions | CI jobs (typecheck, lint, test, build, changeset); release workflow re-runs `bun run verify`; contents, pull-requests, id-token, attestations write at the **workflow** level | same | same, plus a post-publish `latest` dist-tag check with a repair hint | `verify` job (tag, ancestry, CI, tidy, report, pins); per-job permissions; protected `release` environment |
| Secrets | only `GITHUB_TOKEN` | only `GITHUB_TOKEN` | only `GITHUB_TOKEN` (no npm token) | `GITHUB_TOKEN`; optional `HOMEBREW_TAP_TOKEN`, `SCOOP_BUCKET_TOKEN`, `WINGET_TOKEN`, `PINS_PR_TOKEN` |
| Changelog and notes | `@changesets/changelog-github` (PR link, commit, thanks); the GitHub release uses `--generate-notes`, so notes and `CHANGELOG.md` differ | same | notes are cut out of `CHANGELOG.md` with awk, so they match | goreleaser grouped commit subjects by regexp into the release body only; no `CHANGELOG.md` |
| Version strings kept in sync | `package.json` is the one source; website examples such as `CCPOD_VERSION=v0.2.0` go stale | `version.ts` imports `package.json`; tag and file must agree | `package.json`; the tag is read from the publishing commit with `git show` after a "ghost tag" incident | the tag is the version (ldflags); nothing in Go to bump |
| Failure and rollback | idempotent: a complete release for the commit is a no-op, a tag on another commit fails, assets are compared with an exact list; no rollback written; Docker re-push by `gh workflow run docker.yml -f version=X` | same | `gh release view` is the idempotence gate; the dist-tag check prints `npm dist-tag add` commands | re-run the job; no runbook |
| What AGENTS.md says | never bump the version or push a `v*` tag by hand; a `cut-release` skill | same | no manual version bump, no `npm publish` | "Releases are cut by maintainers" |

Strengths worth taking: the release is a reviewable pull request; the same workflow re-verifies instead of trusting a prior CI run; idempotence by checking the release and its exact asset list; release notes extracted from the changelog (pi-harness-delegate); publish verification at the end; tag created at the exact commit; agent-facing "never tag by hand".

Gaps those projects share, which this design addresses: the version pull request is created with `GITHUB_TOKEN`, so CI never runs on it and ccpod's `AGENTS.md` says not to make CI required for it; the release notes can differ from the changelog; permissions are granted for the whole workflow; no environment approval, no keyless signature, no written pre-1.0 policy or rollback runbook.

What is not taken: Changeset files. They need a file per pull request and a Node toolchain; ccshelf is a Go repository and already requires conventional commit subjects, so the commit history is the record (D-36).

## Assessment of ccshelf before this change

- **Tag as the trust anchor.** The cosign identity `.../release.yml@refs/tags/<tag>` is pinned in the Action and the docs, and was only as strong as "who can create a `v*` tag". The rulesets text asked for signed tags; I could not confirm that GitHub rulesets can require signed tags (they can require signed commits on branches) {U}, so that control may never have been enforceable.
- **No reviewable proposal.** The version and the notes were decided at tag time. Notes came from goreleaser's regexp groups over commit subjects, so a badly titled squash commit silently fell into "Other changes". `chore(deps)` was filtered out, including a security fix in a dependency.
- **CI before tagging was manual.** `verify` required a successful `ci.yml` run for the tagged commit, so the maintainer had to wait before pushing the tag.
- **The `pins` pull request never got CI** (it is opened with `GITHUB_TOKEN`) unless the optional `PINS_PR_TOKEN` existed, so the required check `ci-ok` would stay pending.
- **Version strings.** `internal/version` takes `Version` from `-ldflags` (goreleaser `{{ .Version }}`, the tag without `v`); a plain `go build` reports `dev` and CI builds report `0.0.0-ci`. The Action's `version` input is required and has no default, so there is no stale default; `action/README.md` shows `v0.1.0` in examples next to a placeholder SHA, which is intentional. `action/pins.txt` is appended by the `pins` job after each release. A `site/` directory is on another branch; it must read the version from the release (not hard-code it) when it lands.
- **What was already good** and is kept unchanged in effect: SHA-pinned actions, `permissions: {}`, the protected `release` environment with two reviewers, keyless cosign signing of `checksums.txt`, build provenance, SBOMs, the ancestry check against `main`, the snapshot dry run.

## Decision (D-36)

A bot-maintained **release pull request** derived from conventional commits, using `googleapis/release-please-action` v5.0.0, pinned by full commit SHA {V}. It carries the version bump and the generated `CHANGELOG.md`; merging it creates the tag and a draft GitHub release, and the existing signed goreleaser pipeline publishes. release-please was chosen over `semantic-release` (needs Node and a token with push rights, publishes directly with no reviewable pull request) and over a script of our own (we would re-implement version rules and changelog formatting). Changesets was rejected above.

```
 pull request  --squash-->  main  (subject = pull request title, checked by ci.yml pr-title)
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
     job dispatch: workflow_dispatch         |
     of ci.yml on the release branch         v
     -> `ci-ok` reports on the PR     +--------------------------------------------+
                  |                   | release.yml  (github.ref = refs/tags/vX.Y.Z) |
     maintainers review and merge     |  verify: semver tag, ancestor of main,       |
     (two approvals, code owner,      |   manifest == tag, one draft release named   |
     ci-ok)                           |   like the tag, ci.yml green for the commit  |
                                      |  release: environment `release` (2 reviewers)|
                                      |   goreleaser -> archives, checksums, SBOMs,  |
                                      |   cosign bundle -> un-draft the release;     |
                                      |   attest provenance; verify the signature    |
                                      |  pins: PR with the archive checksums + CI    |
                                      +--------------------------------------------+
```

## Versioning rules

- **Tag is the version.** Tags are `vX.Y.Z` (`include-v-in-tag`, no component in the tag). Nothing in Go or the docs is bumped; `-ldflags` read the tag. `.release-please-manifest.json` holds the last released version and is the second source that `verify` compares with the tag.
- **Pre-1.0 (0.x), chosen:** a breaking change (`!` or a `BREAKING CHANGE:` footer) bumps the **minor**; a `feat` bumps the **minor**; `fix`, `perf` and `revert` bump the **patch** (`bump-minor-pre-major: true`, `bump-patch-for-minor-pre-major: false`) {V: `DefaultVersioningStrategy`, release-please v17.6.0}. So a minor bump means "read the changelog before upgrading", a patch bump is safe. Breaking changes pre-1.0 are expected (the profile and catalog schemas may still change) and are listed under "BREAKING CHANGES" in the notes.
- **1.0.0** is a deliberate act: a commit whose message body ends with the footer `Release-As: 1.0.0` (see below). Criteria belong in D-36's revisit trigger: the schemas, the Action inputs and the CLI flags are stable and the six targets have run natively.
- **First release.** The manifest says `0.0.0`, which release-please treats as "never released" {V: `manifest.ts`}, and the first version is then the `initial-version` setting, **not** a bump: without `initial-version` it would be 1.0.0 {V: `BaseStrategy.initialReleaseVersion`}. It is set to `0.1.0`. The `release-as` config key is deprecated in the schema and sticky (it must be removed after use), so forcing a version uses the `Release-As:` footer instead {V: schema and `DefaultVersioningStrategy`}.
- **Forcing a version** (the first release, 1.0.0, or a correction): put `Release-As: X.Y.Z` as a footer in the pull request description of any pull request (the squash commit body keeps it only when the repository's squash default is "Pull request title and description"), or push an empty commit with that footer through a pull request. release-please takes the newest such footer.
- **Changelog sections.** Shown: `feat` (Features), `fix` (Bug fixes), `perf` (Performance), `revert` (Reverts). Hidden: `docs`, `test`, `ci`, `build`, `refactor`, `chore`. A release pull request is only opened when at least one visible entry exists ("No user facing commits found ... skipping" otherwise {V: `BaseStrategy.buildReleasePullRequest`}). I chose to hide the maintenance types rather than add a "Maintenance" section, because a docs-only or CI-only merge should not produce a release pull request. Consequences: **dependency bumps do not trigger a release** (Dependabot titles are `chore(deps)`); ship a vulnerability fix in a dependency with a title such as `fix(deps): bump x to 1.2.3 (CVE-...)`. Security fixes are `fix(security): ...`; the old goreleaser "Security" group by regexp is gone.
- **No releasable commits** (only hidden types since the last release): no release pull request is opened or updated, nothing is tagged, nothing is built. An existing open release pull request stays as it is.
- **Hotfix.** A fix is an ordinary `fix:` pull request on `main`; the release pull request then shows the next patch; merge it. It contains everything on `main` since the last release. To ship only a fix while unreleased `feat` commits sit on `main` is not possible with this model, by design: there are no release branches, `verify` requires the tagged commit to be an ancestor of `main`, and SECURITY.md supports only the latest release and `main`. If the pending changes are not ready, revert them (`revert:`) first.
- **Pre-releases** (`vX.Y.Z-rc.1`) are not produced by this configuration. goreleaser and `verify` accept such a tag, and `prerelease: auto` marks it.

## Token, tag and signing chain

| Actor | Token and permissions | Can do | Cannot do |
|---|---|---|---|
| `release-please` job | `GITHUB_TOKEN`: contents, pull-requests, issues write; no checkout, no repository code runs | create and update the release branch and pull request, labels, the tag, the draft release | push to `main` (the ruleset requires pull requests); run anything that signs; reach secrets (none are passed) |
| `dispatch` job | `GITHUB_TOKEN`: actions write | start `ci.yml` and `release.yml` (`workflow_dispatch`) | change contents |
| `verify` job | contents read, actions read | read the repository, releases and CI runs | write |
| `release` job | contents, id-token, attestations write, **after two reviewers approve the `release` environment, from a `v*` tag only** | build, sign (keyless OIDC), attest, upload to the draft, publish it | run without approval |
| optional publishers | `HOMEBREW_TAP_TOKEN`, `SCOOP_BUCKET_TOKEN`, `WINGET_TOKEN` on the `release` environment | write to the tap, bucket and winget fork | anything else |
| `pins` job | contents, pull-requests, actions write | open the checksum pull request and run CI on its branch | merge it |

No personal access token and no GitHub App key is needed. The one place a token would otherwise be unavoidable is "GITHUB_TOKEN creates a tag or pull request and nothing runs". Two facts remove it {V: GitHub documentation on triggering workflows, and release-please-action's README warning}: events created with `GITHUB_TOKEN` do not start workflows **except `workflow_dispatch` and `repository_dispatch`**, and the REST endpoint for `workflow_dispatch` accepts a branch **or a tag** as `ref` {U: documented, not exercised yet}.

**Why not call `release.yml` as a reusable workflow from `release-please.yml`.** The Fulcio certificate identity is the `job_workflow_ref` of the workflow that runs the signing job {U: from the Sigstore GitHub issuer documentation}. For a reusable workflow that is the called file at the *caller's* ref, here `refs/heads/main`, so every release would be signed as `release.yml@refs/heads/main` and the "exact tag" identity that SECURITY.md and the Action rely on would be lost. Dispatching the workflow with the tag as `ref` keeps `github.ref = refs/tags/vX.Y.Z`, and `verify`'s `cosign verify-blob` step proves the identity on every release.

**CI on bot-opened pull requests.** `ci.yml` has a `workflow_dispatch` trigger. After each release-please run that created or updated the release pull request, `dispatch` starts `ci.yml` on `release-please--branches--main`. Check runs attach to the head commit of the branch, so the required check `ci-ok` shows on the pull request {U: the usual behaviour of the checks API; confirm on the first release pull request}. The `pins` job does the same for its branch. If a GitHub App token is later adopted, pull requests and tags trigger workflows natively and these dispatches become redundant (the `release.yml` `push: tags` trigger would then have to be re-added and the dispatch step removed, to avoid two runs).

**Trust change, stated plainly.**

- Before: a maintainer pushed a tag (the ruleset text asked for signed tags, probably not enforceable {U}); two reviewers approved the environment; the job signed.
- Now: `github-actions[bot]` creates a lightweight, unsigned tag at the merge commit of a release pull request that two maintainers and the code owner approved and that passed `ci-ok`. `verify` accepts a tag only if it is a semantic version, an ancestor of `main`, equal to the version in `.release-please-manifest.json` at that commit (so a tag cannot name a version no release pull request produced), has exactly one draft release named like the tag, and `ci.yml` succeeded for the commit. The two-reviewer approval of the `release` environment is unchanged, and it is the control that matters most: nothing is signed or published without it.
- What the new design adds to the attack surface: the release-please action (third party, SHA-pinned, runs with `contents: write` and no repository checkout, no secrets) and the `GITHUB_TOKEN` permission to create tags. A compromised action could create a `v*` tag, but only at a commit already on `main` whose manifest equals the tag, and only once (tags cannot be updated or deleted by the ruleset); it still cannot sign.
- What it removes: any maintainer typing a version by hand, and an unsigned-but-required "signed tag" rule that could not be enforced.
- **Rulesets that follow:** tags `v*`: block updates and deletion; do **not** restrict creation unless the bot can be put on the bypass list (the built-in Actions identity probably cannot {U}; a dedicated GitHub App can). The hardened variant is a GitHub App (contents, pull requests, actions write; private key stored as a secret on a protected environment) used as `token:` for release-please, added to the tag ruleset's bypass list, with creation restricted to it and maintainers. That adds one long-lived key, which is why it is not the default. `main`: required status check `ci-ok`, pull requests with code-owner review and two approvals (the release pull request is subject to the same rule), no force push or deletion. Environment `release`: tags `v*` only, two reviewers, prevent self-review.

## Pull request titles (D-37)

Maintainers squash-merge, so the pull request title becomes the commit subject and the changelog entry. `ci.yml` job `pr-title` runs `scripts/check_pr_title.py` (stdlib Python, tests in `scripts/test_check_pr_title.py`) on every pull request, including when the title is edited, and is part of `ci-ok`.

- Format `type(scope)!: description`; types `feat fix docs test ci build refactor perf chore revert`; scope optional (lowercase letters, digits, `. _ / -`); `!` optional and marks a breaking change; the description starts with a lowercase letter or digit, has no trailing period, the whole title is at most 72 characters (120 for Dependabot group titles).
- The title reaches the script through `env:` only and is printed escaped, so a hostile title cannot inject a workflow command.
- release-please's own title is `chore(main): release X.Y.Z` (configured explicitly as `chore${scope}: release${component} ${version}`) and satisfies the rule; the `pins` pull request title is `chore: pin checksums for vX.Y.Z`.
- GitHub's default revert title (`Revert "feat: x"`) fails; use `revert: x`.
- Setting to choose once: the repository's squash-merge default message. "Pull request title and description" keeps `BREAKING CHANGE:` and `Release-As:` footers from the description; "Pull request title" keeps only the title (the `!` still works).

## Failure and rollback

| Situation | What happens | What to do |
|---|---|---|
| `release-please.yml` fails after the release pull request was merged | no tag yet | re-run the workflow; release-please is idempotent |
| `dispatch` cannot start `release.yml` | tag and draft release exist, nothing built | `gh workflow run release.yml --ref vX.Y.Z -f dry-run=false` |
| `verify` fails (CI red on the commit, manifest mismatch) | nothing built | fix on `main` with a new pull request. The tag stays; if the tagged commit itself is bad, publish the next version instead (a tag never moves) |
| `release` job or goreleaser fails after the tag exists | the release is still a **draft**, the tag stays, nobody sees a half release; `replace_existing_artifacts` lets a retry overwrite a partial upload to the draft | re-run the failed job in the same run (the environment asks for approval again) or dispatch again; the "reset notes" step removes a duplicated verification footer |
| a step after publishing fails (attestation, signature check, artifact upload) | the release is public with archives and the signed checksums | re-running goreleaser is not possible (it only reuses a draft, so a published release is never touched, by design); if the signature or the provenance attestation is missing, treat it as a bad release (next row) |
| bad release (wrong binary, leaked data, broken signature) | users may have downloaded it | **Yank**: convert the GitHub release back to a draft (or delete it) and add a notice, keep the tag (tags are never moved or reused, and release-please finds the last release by tag), then publish a fixed **patch** through the normal release pull request. Delete the tag only if the release was never published, and then also set the manifest back by a pull request; deleting a tag that release-please has recorded makes it fall back to the manifest version and can produce an oversized next changelog {U}. If "Immutable releases" is enabled for the repository, a published release cannot be edited, which blocks this yank but also blocks tampering {U}; it works with the draft flow because assets are attached before publishing |
| the release pull request is stale or wrong | it is regenerated on every push to `main` | do not edit `CHANGELOG.md` by hand in it; a wrong entry cannot be reworded after the squash merge, so add a corrective commit, or correct the release notes after publishing by editing the GitHub release |
| `ci-ok` never appears on the release pull request | the `dispatch` job failed or its check does not satisfy the ruleset | run `gh workflow run ci.yml --ref release-please--branches--main`; if the ruleset does not accept the dispatched check, switch to the GitHub App token variant |

## Cadence and who can release

No fixed cadence. A release is cut when the maintainers merge the release pull request, normally after a batch of `feat` or `fix` merges, and always for a security fix (a `fix(security):` merge makes the pull request appear within a minute). Only maintainers can merge it (the `main` ruleset) and only the two reviewers of the `release` environment can approve the publication. Agents never merge the release pull request unless the user asks (AGENTS.md).

## One-time maintainer setup and bootstrap runbook

Before the first release (everything under Settings of the tool repository):

1. **Actions > General > Workflow permissions:** enable "Allow GitHub Actions to create and approve pull requests". The default token permission can stay read-only: the workflows request what they need {V: release-please-action README}.
2. **Rulesets:** `main` (required check `ci-ok`, pull requests, code-owner review, two approvals, block force push and deletion); tags `v*` (block update and deletion; see the trust section for creation). **Environment `release`:** deployment tags `v*`, two required reviewers, prevent self-review; optional publisher secrets live here.
3. **Pull requests > squash merging:** default message "Pull request title and description" (or at least the title).
4. **Optional:** Settings > Code security > "Immutable releases" {U}.
5. Check `ci.yml` and `release.yml` are on the default branch (workflow_dispatch needs the file on the default branch to appear), then merge this change.

First release:

1. After the merge, `release-please.yml` runs on the push. History before the first release may be long; the changelog lists `feat`, `fix`, `perf` and `revert` entries from the last 500 commits (`commit-search-depth`) {V}. To start the notes later, add `"bootstrap-sha": "<full sha of the last commit to leave out>"` to `release-please-config.json` in a pull request before merging the release pull request; it is ignored after the first release and can then be removed {V}.
2. Open the pull request `chore(main): release 0.1.0`. Confirm `ci-ok` appears (see the failure table if not), read the version and `CHANGELOG.md`, merge with two approvals. To force another version, merge a pull request whose description ends with `Release-As: X.Y.Z` first.
3. Watch `release-please.yml` create the tag `v0.1.0` and the draft release, and `dispatch` start `release.yml`. Approve the `release` environment (two reviewers).
4. Verify as a user would (SECURITY.md, "Verifying a release"), then merge the `pins` pull request after comparing its lines with the signed `checksums.txt`.
5. Dry run any time without a tag: `gh workflow run release.yml` (defaults to `dry-run=true`: snapshot, no signing, no publishing).

## What is validated

Run in the worktree: `actionlint` with shellcheck on every workflow; `scripts/check-pins.sh` (every `uses:` is a full SHA and its `# vX.Y.Z` comment resolves, including `googleapis/release-please-action` v5.0.0 at `45996ed`); `goreleaser check`; `goreleaser release --snapshot --clean --skip=publish,sign,sbom`; `python3 -I -m unittest discover -s scripts -p 'test_*.py'` (15 tests); the config JSON checked against release-please's `schemas/config.json` at v17.6.0 (no jsonschema module, so a key-and-type check by hand). Read, not run: release-please v17.6.0 source for versioning, initial version, empty changelog, forced tags and draft handling, and goreleaser v2.18.2 source and docs for `use_existing_draft`, `mode` and immutable releases.

Not validated (needs the real repository): a real release; that the dispatched `ci.yml` run satisfies the required check; that a draft release plus `force-tag-creation` makes release-please find the previous release on the next run; that goreleaser publishes the draft that release-please created and appends to its notes as read in its source; the OIDC identity of a workflow dispatched on a tag (documented, not exercised); whether GitHub lets the built-in Actions identity bypass a tag ruleset. The release-please CLI was not run (it needs the network and a token for a repository that does not exist yet).

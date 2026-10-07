# Security policy

`ccshelf` starts Claude Code with generated settings and reads profile and catalog data from sources you configure, so its security properties matter. Thank you for taking the time to report problems responsibly.

## Supported versions

`ccshelf` is pre-1.0. Only the **latest release** and the `main` branch receive security fixes. Once 1.0 ships, this section will list the supported minor versions.

## Reporting a vulnerability

Please **do not open a public issue** for a security problem.

Report it privately with GitHub Security Advisories: on the repository page choose **Security**, then **Report a vulnerability** (`https://github.com/yorch/ccshelf/security/advisories/new`). <!-- OWNER: update the URL if the repository moves -->

Include what you can: the version (`ccshelf --version`), your operating system, the smallest profile, config or workflow that reproduces the problem, and what you expected. Do not include real secrets or real organization data.

## What to expect

These are intentions for a small volunteer project, not contractual promises.

- Acknowledgement within **5 working days**.
- An initial assessment (accepted, needs more information, or not a vulnerability) within **10 working days**.
- For accepted reports: a fix or a mitigation plan, usually within **90 days**, and coordinated disclosure with you. We publish a GitHub Security Advisory and credit you unless you prefer to stay anonymous.

## Scope

In scope:

- **Profile closed schema (SR1).** Any way for a profile, an `extends` parent or an org config to make the generated settings contain a key outside the allowlist: `permissions`, `hooks`, `apiKeyHelper`, `*McpServers` allow lists, `disableAllHooks`, or env names such as `ANTHROPIC_*`, `*_PROXY`, `NODE_*`, `OTEL_*`, `CLAUDE_CODE_*`.
- **Trust (SR2, SR3).** Running or trusting content without explicit trust; trust that is not tied to the resolved closure and commit SHA; project sources trusted by default; name shadowing across sources; masking of protected plugins or MCP servers; any way to make `--yes` or a non-interactive run accept trust.
- **Cache and local files (SR4).** Predictable or world-readable files, symlink or path-traversal escapes (including `..` in profile paths), writes outside `ccshelf`'s own config and cache directories, secrets written to disk or visible in the process list, secrets leaking into `dry-run`, `show` or `doctor` output.
- **Managed policy.** Any way `ccshelf` bypasses, overrides or probes by trial a Claude Code managed policy.
- **CI and release supply chain (SR5).** Unpinned or mis-permissioned workflows, secret exposure to fork pull requests, injection through `${{ }}` in scripts, a release asset that does not match its signature or provenance, the reusable Action downloading an unverified binary.
- **Catalog site.** Cross-site scripting or script injection through catalog data, Markdown rendering that allows raw HTML or non-`http(s)` links, a weakened Content Security Policy.

Out of scope:

- Vulnerabilities in Claude Code itself (report them to Anthropic) or in third-party plugins and MCP servers that a profile merely names.
- Issues that need an attacker who already controls your account, your shell, or the machine, or who can already edit your own `~/.config/ccshelf/` files.
- Findings that depend on a source you explicitly marked as trusted behaving maliciously (that is what trust means), unless trust was granted without the required prompt or pinning.
- Denial of service by running the tool on absurdly large inputs you supplied yourself.
- Missing hardening headers on a static site you host yourself, except for the Content Security Policy that the generated catalog ships.

## Safe harbor

We will not pursue or support legal action against anyone who, in good faith, researches and reports a vulnerability in line with this policy: you test only against your own installation and accounts, you avoid accessing or modifying other people's data, you do not degrade services, and you give us a reasonable time to fix the issue before disclosing it. If in doubt, ask first through a private advisory. This policy does not authorize testing of GitHub, Anthropic or any third-party service.

## Threat model

The summary lives in [docs/design/security.md](docs/design/security.md): the closed profile schema, trust of the resolved closure pinned by commit SHA, no shadowing, private verified local artifacts and hardened CI and releases (requirements SR1 to SR5). It also states what the tool does **not** enforce: only Claude Code managed settings (`allowManagedHooksOnly`, `allowManagedPermissionRulesOnly`, `disableBypassPermissionsMode`) are real enforcement; `ccshelf` profiles are convenience, not a security boundary.

## Network use and updates

ccshelf makes no network call of its own accord, has no telemetry and sends no identifier. Its network use is: the `git` fetches you configure for profile sources (the `git` you have, with your credentials), and the **one optional exception, self-update**, which is off by default:

- `ccshelf update` (you run it) and, only if you set `[update] mode` to `notify` or `install` in `config.toml`, a periodic check (at most once per `interval`, default 24 hours, never in CI, never without an interactive terminal on standard input, output and error (scripts, cron jobs and pipelines use `ccshelf update --yes` if they want it), never when `CCSHELF_NO_UPDATE_CHECK` is set, never for `run`, `dry-run`, `update`, `shell-init`, `completion` or `version`). `notify` prints one line; `install` installs a newer stable release of the same major version only (for 0.x, the same minor), never a downgrade, and takes effect on the next run.
- What is sent: HTTPS `GET` requests for the release metadata of the repository the binary was built from (or the GitHub Enterprise Server in `[update] base_url`), and for an install the release's `checksums.txt`, archive and signature bundle. No credentials, cookies or machine or user identifier; the `User-Agent` is `ccshelf/<version>`.
- What is verified before anything is replaced: the archive's SHA-256 against `checksums.txt` of the same release; if `cosign` is on `PATH`, the keyless signature of `checksums.txt` against the exact identity of the release workflow at that tag, of the repository the binary was built from on github.com (never derived from `base_url`; a fork that signs its own releases must say so with `[update] cosign_identity_repo`, a trust decision) (a mismatch aborts; `--require-signature` makes cosign mandatory); the archive contents (one expected entry, no traversal or links, size caps); and that the new binary runs and reports the expected version. A binary installed by a package manager, `go install` or a system package, and development builds, are not replaced without `--force`. The previous binary is kept as `<name>.old` (`ccshelf update --rollback`; a backup that cannot be run needs `--force`). Redirects of a download are limited to the release host and GitHub's asset hosts, plus the exact hostnames you list in `[update] asset_hosts`.
- What is not verified: the build provenance attestation (see below), and without cosign the SHA-256 and the HTTPS channel are the only checks. Details and failure modes: [docs/design/update.md](docs/design/update.md).

## What the signing identity relies on

The exact identity `.../.github/workflows/release.yml@refs/tags/<version>` is only as trustworthy as what a `v*` tag can point at and who approves the `release` environment. Tags are no longer typed by a maintainer: the `release-please` workflow creates them with the workflow `GITHUB_TOKEN` (a lightweight, unsigned tag) when a release pull request, which needed two approvals including the code owner and a green `ci-ok`, is merged, and then dispatches the release workflow with the tag as its ref so the identity above is unchanged. The release workflow refuses a tag that is not a semantic version, whose commit is not an ancestor of `main`, whose version differs from `.release-please-manifest.json` at that commit, that shares its name with a branch, or whose commit has no green `ci.yml` run, and (in the job that holds the write token, which alone can see draft releases) one that has no single draft release named like the tag or whose draft targets another commit than the tag (a squatted tag); nothing is signed until two required reviewers (with "prevent self-review") approve the `release` environment, which only accepts `v*` tags. The tool repository must keep the rulesets listed under "Required repository rulesets" in [CONTRIBUTING.md](CONTRIBUTING.md): tags `v*` that cannot be updated and can be deleted only by a maintainer on the bypass list (for a yank or a squatted tag), `main` protected (required `ci-ok`, code-owner review, two approvals), and the `release` environment. The release pull request is derived from pull request titles, and release-please also reads the pull request description (`BEGIN_COMMIT_OVERRIDE`, `Release-As:`): the squash default must be "Pull request title", `pr-title` rejects those markers, and the release pull request must be reviewed (every entry and the version) like code; see "Pull request titles" in the design note. The trade-off, in short: a compromised release-please action could create a `v*` tag at a commit already on `main` that matches the manifest, but could not sign or publish it; creating tags is restricted by the checks above and the environment approval rather than by tag-creation rules (see [docs/design/release.md](docs/design/release.md) for the analysis and the optional GitHub App variant).

## Verifying a release

Every release publishes archives, `checksums.txt`, its keyless cosign bundle (`checksums.txt.sigstore.json`), an SBOM per archive, and a GitHub build provenance attestation. Replace `OWNER/REPO` with the repository the release came from (`yorch/ccshelf` for the public project). <!-- OWNER -->

1. Verify the signature on the checksums file (needs [cosign](https://docs.sigstore.dev/cosign/)). Use the **exact** identity of the release workflow at the tag you downloaded, not a prefix or regexp (a regexp also accepts a tag an attacker pushed):

   ```sh
   cosign verify-blob \
     --bundle checksums.txt.sigstore.json \
     --certificate-identity 'https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/tags/vX.Y.Z' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     checksums.txt
   ```

   `cosign verify-blob` needs the Sigstore trusted root and therefore network access (or a mirrored copy passed with `--trusted-root`): it is **not** an offline check.

2. Check the archive against the signed checksums:

   ```sh
   sha256sum --check --ignore-missing checksums.txt   # macOS: shasum -a 256 -c
   ```

3. Verify the build provenance attestation (needs the [GitHub CLI](https://cli.github.com/)):

   ```sh
   gh attestation verify ccshelf_<version>_<os>_<arch>.tar.gz --repo OWNER/REPO
   ```

On GitHub Enterprise Server, where attestations may not be available, mirror the release assets together with `checksums.txt` and `checksums.txt.sigstore.json` and use steps 1 and 2 with a mirrored Sigstore trusted root (`--trusted-root`). The fully offline path is a pinned SHA-256 of the archive: the `sha256` input of the Action, or the line for your version in `action/pins.txt` (pin the Action by a commit that contains it). Builds use `-trimpath` and `CGO_ENABLED=0` so they can be reproduced from the tagged source.

Binaries are not yet notarized (macOS) or Authenticode-signed (Windows); until they are, the checks above are how you establish authenticity.

## What the installer verifies

`scripts/install.sh` (Linux and macOS) and `scripts/install.ps1` (Windows) are published with every release (`releases/latest/download/install.sh` and `install.ps1`) and are listed in the signed `checksums.txt`, so you can verify the script itself the same way as an archive. Design and decision: D-39 in [docs/DECISIONS.md](docs/DECISIONS.md).

**Verified, and a failure stops the install with nothing written:**

- The archive's SHA-256 equals the one line for it in `checksums.txt`, fetched from the same release (a pinned tag; `latest` is resolved from `checksums.txt` itself, not the GitHub API, and then everything is fetched from that one tag). Zero, two or malformed lines are errors. This check cannot be switched off.
- When `cosign` is on `PATH`: the signature of `checksums.txt` against `checksums.txt.sigstore.json`, with the exact identity of this repository's release workflow at that tag (`--cosign-identity` and `--cosign-issuer` change it for GitHub Enterprise Server) and the GitHub Actions issuer. A bad signature, or a missing bundle while cosign is installed, is an error. `--require-signature` (`-RequireSignature`) makes a missing cosign an error as well.
- The archive holds only `ccshelf` (`ccshelf.exe`), `LICENSE` and `README.md`, each once, as regular files at the top level: absolute paths, `..`, directories, symlinks and hardlinks are rejected before anything is extracted. Only the one binary is streamed to a file the installer names, under a size cap.
- Downloads use https only (redirects too), with timeouts and size caps, into a private temporary directory (0700) that is removed on exit. `file:///` is accepted for local mirrors and tests. Every input is checked against a strict pattern; nothing downloaded is ever evaluated or piped into a shell.
- The target directory is not a symlink (junction on Windows), is owned by you and is not writable by others (Linux and macOS), and an existing file named `ccshelf` is replaced only if it identifies itself as ccshelf (or with `--force`). The binary is copied next to its destination and renamed into place.

**Not verified, so know the limits:**

- **Without cosign the checksum authenticates nothing by itself.** `checksums.txt` and the archive come from the same place, so someone who can alter the download location can alter both. The check then catches corruption and partial or mixed uploads, not a hostile release. The installer says so on every run; install cosign, or compare the archive's hash with a value you obtained another way.
- `curl ... | sh` runs a script fetched over https from the same release host. That is as trustworthy as that host and your TLS roots. Download the script first, read it, and check its hash against `checksums.txt` if you want more.
- cosign needs the Sigstore trusted root, so the signature check is not offline (see the mirrored `--trusted-root` note above; the installers do not pass it).
- The installers do not verify the GitHub build provenance attestation or the SBOMs (`gh attestation verify` does, see above), and they do not stop you from pinning an older release.
- The macOS and Windows binaries are not notarized or Authenticode-signed. Directory ownership and permission checks are not done on Windows, where the default directory is under your profile.
- The installer runs `ccshelf version` once after installing, and runs an existing `ccshelf` once (`version`) to see whether it may replace it.


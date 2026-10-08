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
- **Trust (SR2, SR3).** Any of these:
  - Running or trusting content without explicit trust.
  - Trust that is not tied to the resolved closure and commit SHA.
  - Project sources trusted by default.
  - Name shadowing across sources.
  - Masking of protected plugins or MCP servers.
  - Any way to make `--yes` or a non-interactive run accept trust.
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

We will not pursue or support legal action against anyone who, in good faith, researches and reports a vulnerability in line with this policy. In line with this policy means:

- You test only against your own installation and accounts.
- You avoid accessing or modifying other people's data.
- You do not degrade services.
- You give us a reasonable time to fix the issue before disclosing it.

If in doubt, ask first through a private advisory. This policy does not authorize testing of GitHub, Anthropic or any third-party service.

## Threat model

The summary lives in [docs/design/security.md](docs/design/security.md). It covers requirements SR1 to SR5:

- the closed profile schema.
- trust of the resolved closure pinned by commit SHA.
- no shadowing.
- private verified local artifacts.
- hardened CI and releases.

It also states what the tool does **not** enforce. Only Claude Code managed settings (`allowManagedHooksOnly`, `allowManagedPermissionRulesOnly`, `disableBypassPermissionsMode`) are real enforcement. `ccshelf` profiles are convenience, not a security boundary.

## Network use and updates

ccshelf makes no network call of its own accord, has no telemetry and sends no identifier. Its network use is:

- the `git` fetches you configure for profile sources (the `git` you have, with your credentials).
- the **one optional exception, self-update**, which is off by default.

Self-update works like this:

- `ccshelf update` runs when you run it. A periodic check runs only if you set `[update] mode` to `notify` or `install` in `config.toml`. The periodic check runs at most once per `interval` (default 24 hours), and it never runs:
  - in CI.
  - without an interactive terminal on standard input, output and error. Scripts, cron jobs and pipelines use `ccshelf update --yes` if they want it.
  - when `CCSHELF_NO_UPDATE_CHECK` is set.
  - for `run`, `dry-run`, `update`, `shell-init`, `completion` or `version`.
- `notify` prints one line. `install` installs a newer stable release of the same major version only (for 0.x, the same minor), never a downgrade. The new version takes effect on the next run.
- What ccshelf sends: HTTPS `GET` requests for the release metadata of the repository the binary was built from (or the GitHub Enterprise Server in `[update] base_url`). For an install, it also requests the release's `checksums.txt`, archive and signature bundle. It sends no credentials, cookies or machine or user identifier. The `User-Agent` is `ccshelf/<version>`.
- What ccshelf verifies before it replaces anything:
  - The archive's SHA-256 against `checksums.txt` of the same release.
  - If `cosign` is on `PATH`: the keyless signature of `checksums.txt`. ccshelf checks it against the exact identity of the release workflow at that tag, of the repository the binary was built from on github.com. This identity is never derived from `base_url`. A fork that signs its own releases must say so with `[update] cosign_identity_repo` (a trust decision). A mismatch aborts. `--require-signature` makes cosign mandatory.
  - The archive contents (one expected entry, no traversal or links, size caps).
  - That the new binary runs and reports the expected version.
- Without `--force`, ccshelf does not replace a binary installed by a package manager, `go install` or a system package, or a development build.
- ccshelf keeps the previous binary as `<name>.old` (`ccshelf update --rollback`). A backup that cannot be run needs `--force`.
- ccshelf limits the redirects of a download to the release host and GitHub's asset hosts, plus the exact hostnames you list in `[update] asset_hosts`.
- What ccshelf does not verify: the build provenance attestation (see below). Without cosign, the SHA-256 and the HTTPS channel are the only checks. Details and failure modes: [docs/design/update.md](docs/design/update.md).

## What the signing identity relies on

The exact identity `.../.github/workflows/release.yml@refs/tags/<version>` is only as trustworthy as what a `v*` tag can point at and who approves the `release` environment.

A maintainer no longer types tags. The `release-please` workflow creates them with the workflow `GITHUB_TOKEN` (a lightweight, unsigned tag) when a release pull request is merged. That pull request needed two approvals, including the code owner, and a green `ci-ok`. The workflow then dispatches the release workflow with the tag as its ref, so the identity above is unchanged.

The release workflow refuses a tag:

- that is not a semantic version.
- whose commit is not an ancestor of `main`.
- whose version differs from `.release-please-manifest.json` at that commit.
- that shares its name with a branch.
- whose commit has no green `ci.yml` run.
- that has no single draft release named like the tag, or whose draft targets another commit than the tag (a squatted tag).

Nothing is signed until two required reviewers (with "prevent self-review") approve the `release` environment. That environment only accepts `v*` tags.

The tool repository must keep the rulesets listed under "Required repository rulesets" in [CONTRIBUTING.md](CONTRIBUTING.md):

- tags `v*` that cannot be updated and can be deleted only by a maintainer on the bypass list (for a yank or a squatted tag).
- `main` protected (required `ci-ok`, code-owner review, two approvals).
- the `release` environment.

release-please derives the release pull request from pull request titles, but it also reads the pull request description. "Pull request titles" in [docs/design/release.md](docs/design/release.md) gives the controls against that. The main one is the squash default "Pull request title".

The trade-off, in short: a compromised release-please action could create a `v*` tag at a commit already on `main` that matches the manifest, but it could not sign or publish it. Tag creation is restricted by the checks above and the environment approval, not by tag-creation rules (see [docs/design/release.md](docs/design/release.md) for the analysis and the optional GitHub App variant).

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
   sha256sum --check --ignore-missing checksums.txt   # macOS: shasum -a 256 --check --ignore-missing checksums.txt
   ```

3. Verify the build provenance attestation (needs the [GitHub CLI](https://cli.github.com/)):

   ```sh
   gh attestation verify ccshelf_<version>_<os>_<arch>.tar.gz --repo OWNER/REPO
   ```

On GitHub Enterprise Server, attestations may not be available. There, mirror the release assets together with `checksums.txt` and `checksums.txt.sigstore.json`, and use steps 1 and 2 with a mirrored Sigstore trusted root (`--trusted-root`). The fully offline path is a pinned SHA-256 of the archive: the `sha256` input of the Action, or the line for your version in `action/pins.txt` (pin the Action by a commit that contains it). Builds use `-trimpath` and `CGO_ENABLED=0` so they can be reproduced from the tagged source.

Binaries are not yet notarized (macOS) or Authenticode-signed (Windows). Until they are, the checks above are how you establish authenticity.

## What the installer verifies

Every release publishes `scripts/install.sh` (Linux and macOS) and `scripts/install.ps1` (Windows) (`releases/latest/download/install.sh` and `install.ps1`). The signed `checksums.txt` lists them, so you can verify the script itself the same way as an archive. Design and decision: D-39 in [docs/DECISIONS.md](docs/DECISIONS.md).

**Verified, and a failure stops the install with nothing written:**

- The archive's SHA-256 equals the one line for it in `checksums.txt`, fetched from the same release. The release is a pinned tag. The installer resolves `latest` from `checksums.txt` itself, not the GitHub API, and then fetches everything from that one tag. Zero, two or malformed lines are errors. This check cannot be switched off.
- When `cosign` is on `PATH`: the signature of `checksums.txt` against `checksums.txt.sigstore.json`, with the exact identity of this repository's release workflow at that tag and the GitHub Actions issuer. `--cosign-identity` and `--cosign-issuer` change them, but only matter for a release you signed yourself. The installer checks a GitHub Enterprise Server mirror of the public release with the default identity. A bad signature, or a missing bundle while cosign is installed, is an error. `--require-signature` (`-RequireSignature`) makes a missing cosign an error as well.
- The archive holds only `ccshelf` (`ccshelf.exe`), `LICENSE` and `README.md`, each once, as regular files at the top level. The installer rejects absolute paths, `..`, directories, symlinks and hardlinks before it extracts anything. It streams only the one binary to a file that it names, under a size cap.
- Downloads use https only, redirects included. `install.sh` runs `curl -q --proto =https --proto-redir =https --fail --max-filesize`. `install.ps1` follows redirects by hand and only to https. Downloads have timeouts and size caps, and go into a private temporary directory (0700) that the installer removes on exit.
- `curl` is required. There is no `wget` fallback, because wget cannot pin https for redirects, would read `~/.wgetrc`, and busybox wget rejects the flags. `-q` makes curl ignore `~/.curlrc`. The installer honors proxy and CA environment variables and never turns TLS verification off.
- The installer accepts `file:///` for local mirrors and tests. It checks every input against a strict pattern. It never evaluates or pipes into a shell anything it downloads.
- The target directory:
  - is not a symlink (junction on Windows).
  - is owned by you and is not writable by others (Linux and macOS).
- The installer replaces an existing file named `ccshelf` only if it identifies itself as ccshelf, or with `--force`. `--force` replaces a file or a symlink with that name itself, never a directory.
- The installer copies the binary next to its destination and renames it into place. Afterwards it checks that what sits at the destination is a regular file (not a link or directory) with the SHA-256 of the verified binary, and fails otherwise.
- The installer creates a missing target directory with `mkdir -p`, so it creates missing parent folders too.

**Not verified, so know the limits:**

- **Without cosign the checksum authenticates nothing by itself.** `checksums.txt` and the archive come from the same place, so someone who can alter the download location can alter both. The check then catches corruption and partial or mixed uploads, not a hostile release. The installer says so on every run. Install cosign, or compare the archive's hash with a value you obtained another way.
- `curl ... | sh` runs a script fetched over https from the same release host. That is as trustworthy as that host and your TLS roots. Download the script first, read it, and check its hash against `checksums.txt` if you want more.
- cosign needs the Sigstore trusted root, so the signature check is not offline. See the mirrored `--trusted-root` note above. The installers do not pass it.
- The installers do not verify the GitHub build provenance attestation or the SBOMs (`gh attestation verify` does, see above). They do not stop you from pinning an older release.
- On Windows, the installer does not check directory ownership and permissions. There, the default directory is under your profile.
- The installer runs `ccshelf version` once after installing. To decide whether it may replace an existing `ccshelf`, it first reads the file without running it, and the file must contain the text `ccshelf`. Only then does it run `version`, under a 5-second limit. Where there is no `timeout` command (stock macOS), the text check alone decides. The installer never executes an existing file that does not contain that text.
- The installer trusts a mirror given with `--base-url` to serve the release you ask for. A mirror's `latest` can name an older release that carries a valid signature (a downgrade through the mirror). Pin `--version` to avoid it.
- On Windows, the installer changes the user `PATH` only with `-AddToPath`, through the registry. It reads the value unexpanded and writes it back as an expandable value, so `%VARIABLE%` entries survive. A `PATH` over 2047 characters earns a warning (some older programs cut it off).


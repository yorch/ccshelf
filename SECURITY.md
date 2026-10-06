# Security policy

`ccshelf` starts Claude Code with generated settings and reads profile and catalog data from sources you configure, so its security properties matter. Thank you for taking the time to report problems responsibly.

## Supported versions

`ccshelf` is pre-1.0. Only the **latest release** and the `main` branch receive security fixes. Once 1.0 ships, this section will list the supported minor versions.

## Reporting a vulnerability

Please **do not open a public issue** for a security problem.

Report it privately with GitHub Security Advisories: on the repository page choose **Security**, then **Report a vulnerability** (`https://github.com/ccshelf/ccshelf/security/advisories/new`). <!-- OWNER: update the URL if the repository moves -->

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

## Verifying a release

Every release publishes archives, `checksums.txt`, its keyless cosign bundle (`checksums.txt.sigstore.json`), an SBOM per archive, and a GitHub build provenance attestation. Replace `OWNER/REPO` with the repository the release came from (`ccshelf/ccshelf` for the public project). <!-- OWNER -->

1. Verify the signature on the checksums file (needs [cosign](https://docs.sigstore.dev/cosign/)):

   ```sh
   cosign verify-blob \
     --bundle checksums.txt.sigstore.json \
     --certificate-identity-regexp '^https://github.com/OWNER/REPO/' \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com \
     checksums.txt
   ```

2. Check the archive against the signed checksums:

   ```sh
   sha256sum --check --ignore-missing checksums.txt   # macOS: shasum -a 256 -c
   ```

3. Verify the build provenance attestation (needs the [GitHub CLI](https://cli.github.com/)):

   ```sh
   gh attestation verify ccshelf_<version>_<os>_<arch>.tar.gz --repo OWNER/REPO
   ```

On GitHub Enterprise Server, where attestations may not be available, mirror the release assets together with `checksums.txt` and `checksums.txt.sigstore.json` and use step 1 and 2, which work offline with the bundle. Builds use `-trimpath` and `CGO_ENABLED=0` so they can be reproduced from the tagged source.

Binaries are not yet notarized (macOS) or Authenticode-signed (Windows); until they are, the checks above are how you establish authenticity.

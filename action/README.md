# ccshelf GitHub Action

A composite action that installs a **verified** `ccshelf` release binary and runs one `ccshelf` command. It is a thin wrapper: all logic (`lint`, `compile --check`, `catalog build`) lives in the binary, so the same commands run locally or in any other CI. The action uses no third-party actions, only `bash`, `curl`, `tar` (or an unzip tool on Windows) and optionally `cosign`.

## Usage

Pin the action by its **full commit SHA**, never by a tag or a branch. Tags move; a SHA does not. Find the SHA of the release you want on the tool repo's Releases page and keep the human-readable tag in a comment:

```yaml
jobs:
  lint:
    runs-on: ubuntu-latest
    permissions:
      contents: read
    steps:
      - uses: actions/checkout@<full commit SHA>
        with:
          persist-credentials: false
      - uses: ccshelf/ccshelf/action@<full commit SHA>   # v0.1.0
        with:
          version: v0.1.0
          sha256: <sha-256 of ccshelf_v0.1.0_linux_amd64.tar.gz>   # strongest option, see below
          args: lint
```

### Inputs

| Input | Default | Meaning |
|---|---|---|
| `version` | required | Release tag to install, strict semver such as `v0.1.0` or `v0.1.0-rc.1`. Anything else is rejected. |
| `args` | empty | The subcommand and flags, split on spaces (no quoting, no shell expansion). Empty means install only. |
| `working-directory` | `.` | Relative directory in the workspace where the command runs. `..` and absolute paths are rejected. |
| `sha256` | empty | SHA-256 of the archive for this runner's OS and architecture, pinned by you. |
| `base-url` | `https://github.com/ccshelf/ccshelf/releases/download` | Where `<version>/` assets live. `https://` or `file://` only. |
| `verify-signature` | `true` | Verify the cosign signature of `checksums.txt` when no `sha256` is pinned. |
| `cosign-identity` | the release workflow of the tool repo | Regexp the signing certificate identity must match. |
| `cosign-oidc-issuer` | `https://token.actions.githubusercontent.com` | OIDC issuer the certificate must name. |

Outputs: `path` (the installed binary) and `version`. The binary's directory is also added to `PATH` for later steps.

### Examples

```yaml
# Lint the marketplace, sidecars and profiles
- uses: ccshelf/ccshelf/action@<full commit SHA>   # v0.1.0
  with: { version: v0.1.0, sha256: "<archive sha-256>", args: lint }

# Fail the pull request when committed bundles differ from what compile would write
- uses: ccshelf/ccshelf/action@<full commit SHA>   # v0.1.0
  with: { version: v0.1.0, sha256: "<archive sha-256>", args: compile --check }

# Build the static catalog
- uses: ccshelf/ccshelf/action@<full commit SHA>   # v0.1.0
  with: { version: v0.1.0, sha256: "<archive sha-256>", args: catalog build --out dist/catalog }
```

Install once and run several commands: give the first call no `args` and call `ccshelf` directly afterwards, or reuse `steps.<id>.outputs.path`.

Pinning `sha256` per OS and architecture in a matrix is easiest with a map in the workflow (`sha256: ${{ matrix.ccshelf_sha256 }}`). The hashes are in the release's `checksums.txt`.

## How verification works

The installer downloads `ccshelf_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows), `checksums.txt` and `checksums.txt.sigstore.json` from `<base-url>/<version>/` and checks, in this order:

1. **`sha256` is set (strongest).** The archive hash must equal your pinned value. Nothing else is trusted: not the download host, not `checksums.txt`, not a signing service. Protects against a compromised or rewritten release, a malicious mirror and a man in the middle. Costs you one value to update per release and platform.
2. **Otherwise, `verify-signature: true` (default).** `cosign verify-blob` checks `checksums.txt` against the Sigstore bundle, the certificate identity regexp and the OIDC issuer, then the archive hash must match its single line in `checksums.txt`. Protects against a tampered mirror or a swapped asset, as long as the release workflow identity is the one you expect. `cosign` must be on `PATH` (install it in an earlier step, itself pinned by SHA); if it is missing the step **fails** with instructions and never skips silently. The bundle is verified offline from the file, so it works for GHE Server mirrors if you copy the `.sigstore.json` file along with the archive.
3. **`verify-signature: false` without `sha256` (explicit opt-out).** The archive must still match `checksums.txt`, but that file is unauthenticated, so this only detects corruption. The job prints a loud warning annotation. Use it only where neither a pin nor cosign is possible.

On success the installer prints the verified SHA-256, extracts only the `ccshelf` binary (never other archive members) into a private directory under `$RUNNER_TEMP`, and writes the path to `$GITHUB_OUTPUT` and `$GITHUB_PATH`. Inputs are passed through `env:` and validated, never interpolated into shell text.

If you mirror releases under a different repository or a GHE host, set `cosign-identity` to match your own signing workflow, or rely on `sha256`.

## GitHub Enterprise Server and offline use

The action only needs the assets, so three approaches work. All keep the logic in the binary (R2).

1. **GitHub Connect** lets a GHE Server instance resolve `uses:` references to github.com actions. Enable it for the tool's owner only, and still pin by SHA.
2. **Mirror the action repository** with [`actions-sync`](https://github.com/actions/actions-sync) into an organization on the instance (for example `platform/ccshelf-action`) and use `uses: platform/ccshelf-action/action@<full commit SHA>`. Pin the SHA from the mirrored copy and review it.
3. **Mirror the release assets** to an internal URL (artifact registry, internal static host, or a release in an internal repo) with this layout, and set `base-url`:

   ```
   <base-url>/v0.1.0/ccshelf_v0.1.0_linux_amd64.tar.gz
   <base-url>/v0.1.0/checksums.txt
   <base-url>/v0.1.0/checksums.txt.sigstore.json
   ```

   ```yaml
   - uses: platform/ccshelf-action/action@<full commit SHA>
     with:
       version: v0.1.0
       base-url: https://artifacts.example.internal/ccshelf
       sha256: "<archive sha-256>"
       args: lint
   ```

   A `base-url` that starts with `file://` copies from a runner-local directory, which is how an air-gapped runner image or a test can supply the release. Downloads over `https://` do not follow redirects to other schemes. The action sends no credentials; if your mirror needs authentication, download the assets in an earlier step and use `file://`.

Notes: artifact attestations have limited support on GHE Server, which is why the signature is verified offline from the Sigstore bundle and why the `sha256` pin does not need any service at all. The Windows path (`.zip`, `ccshelf.exe`) is reviewed but not exercised by the offline tests.

## Tests

```
bash action/test/run.sh
```

Builds a fake release and runs `scripts/install.sh` through `file://`: pin success and failure, tampered archives, missing or duplicate checksum lines, invalid versions and unsafe inputs, fail-closed behavior without `cosign`, a fake `cosign` that succeeds and fails, and the opt-out warning. No network and no real `cosign`. The script runs on macOS and Linux (bash 3.2 and newer); on Windows it skips, because the tests build `tar.gz` archives.

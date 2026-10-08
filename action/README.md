# ccshelf GitHub Action

A composite action that installs a **verified** `ccshelf` release binary and runs one `ccshelf` command. It is a thin wrapper. All logic (`lint`, `compile --check`, `catalog build`) lives in the binary, so the same commands run locally or in any other CI. The action uses no third-party actions, only `bash`, `curl`, `tar` (or an unzip tool on Windows) and optionally `cosign`.

## Usage

Pin the action by its **full commit SHA**, never by a tag or a branch. Tags move. A SHA does not. Find the SHA of the release you want on the tool repo's Releases page and keep the human-readable tag in a comment:

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
      - uses: yorch/ccshelf/action@<full commit SHA>   # v0.1.0
        with:
          version: v0.1.0
          sha256: <sha-256 of ccshelf_0.1.0_linux_amd64.tar.gz>   # offline pin, see below; optional when the pinned commit's pins.txt has the line
          args: lint
```

### Inputs

| Input | Default | Meaning |
|---|---|---|
| `version` | required | Release tag to install, strict semver such as `v0.1.0` or `v0.1.0-rc.1`. Anything else is rejected. |
| `args` | empty | The subcommand and flags, split on spaces (no quoting, no shell expansion). Empty means install only. |
| `working-directory` | `.` | Relative directory in the workspace where the command runs. `..` and absolute paths are rejected. |
| `sha256` | empty | SHA-256 of the archive for this runner's OS and architecture, pinned by you. Wins over `pins.txt`. |
| `base-url` | `https://github.com/yorch/ccshelf/releases/download` | Where `<version>/` assets live (the directory keeps the leading `v`). `https://`, or `file:///<abs path>` (`file://C:/...` on Windows), only. |
| `verify-signature` | `true` | Verify the cosign signature of `checksums.txt` when neither `sha256` nor a `pins.txt` line applies. Needs the Sigstore trusted root: not offline. |
| `cosign-identity` | empty: `https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/<version>` | **Exact** certificate identity (`--certificate-identity`). |
| `cosign-identity-regexp` | empty | Explicit opt-in: a regexp (escape dots) used instead of the exact identity. Exclusive with `cosign-identity`. |
| `trusted-root` | empty | Path to a Sigstore trusted root file your organization mirrors (`--trusted-root`). |
| `cosign-oidc-issuer` | `https://token.actions.githubusercontent.com` | OIDC issuer the certificate must name. |

Outputs: `path` (the installed binary) and `version`. The action also adds the binary's directory to `PATH` for later steps.

### Examples

```yaml
# Lint the marketplace, sidecars and profiles
- uses: yorch/ccshelf/action@<full commit SHA>   # v0.1.0
  with: { version: v0.1.0, sha256: "<archive sha-256>", args: lint }

# Fail the pull request when committed bundles differ from what compile would write
- uses: yorch/ccshelf/action@<full commit SHA>   # v0.1.0
  with: { version: v0.1.0, sha256: "<archive sha-256>", args: compile --check }

# Build the static catalog
- uses: yorch/ccshelf/action@<full commit SHA>   # v0.1.0
  with: { version: v0.1.0, sha256: "<archive sha-256>", args: catalog build --out dist/catalog }
```

To install once and run several commands, give the first call no `args`. Then call `ccshelf` directly, or reuse `steps.<id>.outputs.path`.

Pinning `sha256` per OS and architecture in a matrix is easiest with a map in the workflow (`sha256: ${{ matrix.ccshelf_sha256 }}`). The hashes are in the release's `checksums.txt`. Archive names have no leading `v` (`ccshelf_0.1.0_linux_amd64.tar.gz`, `.zip` on Windows) while the release path keeps it (`.../download/v0.1.0/`).

## How verification works

The installer downloads `ccshelf_<version without v>_<os>_<arch>.tar.gz` (`.zip` on Windows) from `<base-url>/<version>/` and checks it, in this order:

1. **`sha256` input (strongest, fully offline).** The archive hash must equal your pinned value. Nothing else is trusted: not the download host, not `checksums.txt`, not a signing service.
2. **`pins.txt` embedded in the action (offline).** [`action/pins.txt`](pins.txt) holds one `<version> <os> <arch> <sha256>` line per release archive (the version keeps its `v`). If a line matches, the archive must equal it, and cosign is not needed. Because the file is part of the action, **pinning the action by a commit SHA that contains the line for your version also pins the binary.** If there is no matching line, the installer falls through to step 3. A malformed `pins.txt` fails the step.
3. **`verify-signature: true` (default).** `cosign verify-blob` checks `checksums.txt` against the Sigstore bundle with the **exact** certificate identity `https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/<version>` and the OIDC issuer. Then the archive hash must match its single line in `checksums.txt`. `cosign` must be on `PATH` (install it in an earlier step, itself pinned by SHA). If it is missing, the step **fails** and never skips silently. **This is not an offline check:** `cosign verify-blob` needs the Sigstore trusted root, which it fetches over the network unless you give it a copy with the `trusted-root` input. The only fully offline paths are steps 1 and 2.
4. **`verify-signature: false` without a pin (explicit opt-out).** The archive must still match `checksums.txt`, but that file is unauthenticated, so this only detects corruption. The job prints a loud warning annotation.

Why an exact identity: a regexp such as `.../release.yml@refs/tags/v.*` accepts any tag, including one an attacker pushed. The exact form ties the signature to the tag you asked for. This only helps if the tool repository restricts who can create `v*` tags and which commits they point at. See "Required repository rulesets" in `CONTRIBUTING.md`.

### Embedded checksums and the release flow

After each successful release, the `pins` job of `release.yml` appends the six lines for the new version to `action/pins.txt` and opens a pull request titled `chore: pin checksums for vX.Y.Z`. A maintainer reviews it (compare with the signed `checksums.txt`) and merges it. Pull requests created with the default `GITHUB_TOKEN` do not start other workflows. So the job starts `ci.yml` and `pr-title.yml` on the branch itself (`workflow_dispatch`), and the required checks `ci-ok` and `pr-title` report on the pull request. The repository can have a secret `PINS_PR_TOKEN` (a GitHub App token or a fine-grained PAT with contents and pull request write access). If it does, the job uses it and CI starts natively.

Adopters:

1. Pick the commit of the action that contains the `pins.txt` line for the version you want (the pins pull-request merge commit or any later one).
2. Pin `uses:` to that full SHA.
3. Set `version:` to that release.

Older commits do not know newer versions and fall through to cosign.

On success, the installer:

1. prints the verified SHA-256.
2. extracts only the `ccshelf` binary (never other archive members) into a private directory under `$RUNNER_TEMP`.
3. writes the path to `$GITHUB_OUTPUT` and `$GITHUB_PATH`.

The action passes inputs through `env:`. It rejects inputs that contain control characters, and validates the others. It never interpolates them into shell text.

If you mirror releases under a different repository or a GHE host, set `cosign-identity` to the exact identity of your own signing workflow, or rely on `sha256`.

## GitHub Enterprise Server and offline use

The action only needs the assets, so three approaches work. All keep the logic in the binary (R2).

1. **GitHub Connect** lets a GHE Server instance resolve `uses:` references to github.com actions. Enable it for the tool's owner only, and still pin by SHA.
2. **Mirror the action repository** with [`actions-sync`](https://github.com/actions/actions-sync) into an organization on the instance (for example `platform/ccshelf-action`) and use `uses: platform/ccshelf-action/action@<full commit SHA>`. Pin the SHA from the mirrored copy and review it.
3. **Mirror the release assets** to an internal URL (artifact registry, internal static host, or a release in an internal repo) with this layout, and set `base-url`:

   ```
   <base-url>/v0.1.0/ccshelf_0.1.0_linux_amd64.tar.gz
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

   A `base-url` that starts with `file:///` copies from a runner-local directory, which is how an air-gapped runner image or a test can supply the release. Downloads over `https://` do not follow redirects to other schemes. The action sends no credentials. If your mirror needs authentication, download the assets in an earlier step and use `file://`.

Notes: artifact attestations have limited support on GHE Server. The signature check reads the Sigstore bundle from the file, but it still needs the Sigstore trusted root (mirror it and pass `trusted-root`). So on an air-gapped instance, use the `sha256` pin or `pins.txt`, which need no service at all.

## Tests

```
bash action/test/run.sh
```

The suite builds a fake release and runs `scripts/install.sh` through `file:///`. It covers:

- pin success and failure.
- `pins.txt` hits, mismatches, fall-through and malformed lines.
- tampered archives.
- missing or duplicate checksum lines.
- invalid versions and unsafe inputs (including newlines).
- hashing in a path with a backslash (a fake `sha256sum` that emulates GNU's prefix).
- fail-closed behavior without `cosign`.
- a fake `cosign` that succeeds and fails (exact identity, regexp opt-in, trusted root).
- the opt-out warning.
- the Windows zip path.

A separate check compares the archive names the script requests with the `name_template` in `.goreleaser.yaml` and with `test/testdata/goreleaser-snapshot-checksums.txt`, the checksum file of a real `goreleaser --snapshot` run. No network and no real `cosign`. CI runs the suite on Linux, macOS and Windows (Git Bash). The suite skips the zip case on Windows because Git Bash cannot build zip files.

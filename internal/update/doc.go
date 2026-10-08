// Package update implements "ccshelf update" and the opt-in automatic update:
// discovering the latest release, downloading and verifying it, and replacing
// the running binary.
//
// The package is the one place that makes optional network calls on the
// user's behalf (SECURITY.md lists it as the single exception to "no network
// unless asked"). Nothing here runs unless the user ran "ccshelf update" or
// set [update] mode in config.toml. The default is off.
//
// # Trust model
//
//   - Release metadata and assets are fetched over HTTPS only, without any
//     credential, from the repository the binary was released from
//     ([version.Repo]) on github.com or on the GitHub Enterprise Server named
//     by [update] base_url. Redirects stay on the same host or on GitHub's
//     release-asset hosts, at most five of them.
//   - The archive's SHA-256 must equal its line in checksums.txt of the same
//     release. If cosign is on PATH, the keyless signature of checksums.txt
//     (checksums.txt.sigstore.json) must also verify against the identity
//     https://<host>/<repo>/.github/workflows/release.yml@refs/tags/<tag> and
//     the GitHub Actions OIDC issuer. A failure aborts the update. With
//     RequireSignature a missing cosign is an error too.
//   - Only the ccshelf (ccshelf.exe) entry of the archive is extracted, with
//     traversal, links, unknown entries and oversize rejected, into a file next
//     to the running binary. The file must report the expected version before
//     it replaces anything.
//   - The previous binary is kept as <name>.old for rollback.
//
// Every effect that touches the outside world (HTTP client, executable path,
// cosign lookup, the replace step, the clock) is a field of [Updater] so that
// tests are hermetic.
package update

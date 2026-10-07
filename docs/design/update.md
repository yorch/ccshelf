# Updating ccshelf

How `ccshelf update` replaces the binary, what it verifies, and the opt-in automatic update. Decided 2026-10-06 as D-40. The code is `internal/update` (mechanics) and `internal/cli/updatecmd` (command, flags, prompts, hooks); the release side it relies on is in [release.md](release.md).

Confidence markers: `{V}` verified (ran it or read the source at the version named), `{R}` reported, `{U}` unverified. GitHub behavior marked `{V}` was checked against the public `cli/cli` releases on 2026-10-07 with `curl`, once, by hand; it is not part of any test (tests never reach the real network).

## Principles

1. **Nothing contacts the network unless the user asked.** The only triggers are running `ccshelf update` and, if the user opted in, `[update] mode` in `config.toml`. The default is `off`. This is the one optional network call of the tool besides the git fetch of configured profile sources (see [SECURITY.md](../../SECURITY.md)).
2. **Always verify, never trust the transport alone.** SHA-256 against `checksums.txt` of the same release, and the keyless cosign signature of `checksums.txt` whenever cosign is installed.
3. **Never replace what another tool owns.** A package-managed or development build is refused (with the right command) unless `--force`.
4. **Fail closed and leave the old binary alone.** Every check runs before the binary is touched; the previous one is kept for rollback.
5. **R6 holds.** `ccshelf update` works with flags alone; a terminal gets a confirmation and the equivalent flag command.

## The command

```
ccshelf update [--check] [--version vX.Y.Z] [--prerelease] [--dry-run]
               [--require-signature] [--rollback] [--yes] [--force] [--allow-downgrade]
```

| Flag | Meaning |
|---|---|
| `--check` | Print the current version, the latest one and the release notes URL. Exit 0 whether or not an update exists. With `--json`, the envelope kind `update`. |
| `--version vX.Y.Z` | Install that release. An older one needs `--allow-downgrade` (exit 2 otherwise). Cannot be combined with `--prerelease`. |
| `--prerelease` | Let the search for "latest" include pre-releases. |
| `--dry-run` | Show what would be downloaded, verified and replaced. Changes nothing, downloads nothing (the release metadata is still fetched). |
| `--require-signature` | Fail unless cosign is on `PATH` (see below). Checked before anything is downloaded. |
| `--rollback` | Restore the previous binary. No network. |
| `--yes` | Skip the confirmation only. There is no trust decision in an update, so unlike `trust --yes` nothing is being accepted that a flag could not name. |
| `--force` | Also replace a package-managed or development build, and reinstall the running version. |
| `--allow-downgrade` | See `--version`. |

Exit codes follow [cli.md](cli.md): 0 success (including "already up to date" and `--check`), 1 failure (network, verification, disk, package-managed, development build; nothing was replaced, and the message carries a hint), 2 usage (a downgrade without the flag, a bad `--version`, a missing `--yes` without a terminal, flag combinations that make no sense), 130 interrupted.

Interactive flow on a terminal: `ccshelf 0.1.0 -> 0.2.0`, the release notes URL and the path that will be replaced, then a plain yes/no confirmation (default no; no typed word, because nothing risky is being accepted), then the line `Equivalent: ccshelf update --version v0.2.0 --yes`. Without a terminal and without `--yes`, it exits 2 naming `--yes`; when there is nothing to do ("up to date") no confirmation is needed and it exits 0.

## Flow

```
  discover       GET <api>/repos/<repo>/releases/latest   (or /releases?per_page=30 with --prerelease,
                 or /releases/tags/<tag> with --version); no credentials
     |
  judge          current vs target (semver, v prefix, pre-release rules); up to date? downgrade?
                 install method; development build; resolve the executable (symlinks)
     |
  gate           refuse package-managed / development build unless --force;
                 directory writable?  no: print the exact elevated command;
                 --require-signature without cosign: refuse
     |
  confirm        --yes, or the terminal prompt
     |
  lock           <cache>/update.lock, created exclusively; a second update fails with exit 1
     |
  download       <web>/<repo>/releases/download/<tag>/checksums.txt, then the archive
                 (caps, redirect policy, SHA-256 computed while writing)
     |
  verify         cosign verify-blob of checksums.txt if cosign is on PATH (failure = stop),
                 then the archive's SHA-256 must equal its single line in checksums.txt
     |
  extract        only the ccshelf / ccshelf.exe entry, into a private file in the SAME directory
                 as the running binary; fsync; mode of the old binary (0755 if it was not executable)
     |
  smoke test     run the new file with `version --json`: it must be ccshelf and report the target version
     |
  replace        Unix: hard-link the old binary to <name>.old, then one atomic rename over it.
                 Windows: rename the running exe to <name>.old, rename the new file into place.
```

Discovery, download and extraction use stdlib only (`net/http`, `archive/tar`, `archive/zip`, `crypto/sha256`); the only process started is cosign and the version check of the downloaded file, both from `internal/update`, the one package besides the launcher and the git helpers allowed to import `os/exec` (`.golangci.yml`, depguard).

## Where releases come from

`internal/version.Repo` (default `yorch/ccshelf`, set by `-ldflags` in `.goreleaser.yaml` and the Makefile) names the repository. With no `[update] base_url` the source is github.com: API `https://api.github.com`, downloads `https://github.com/<repo>/releases/download/<tag>/<asset>`.

For a **GitHub Enterprise Server** (R2) set `base_url = "https://ghe.example.com"` (API `https://ghe.example.com/api/v3`) or `base_url = "https://ghe.example.com/owner/repo"` when the mirrored repository has another name. It is validated like the git URLs: https only, no user information or token, no query or fragment, no whitespace or control characters, a path only of the form `/owner/repo`. A plain-http loopback URL is accepted only in a binary built with `-tags e2eloopback` (below). Asset names are built from the validated version (`ccshelf_<version>_<os>_<arch>.tar.gz`, `.zip` on Windows, exactly the goreleaser `name_template`); the download URL is built from the base URL, never copied from the API response.

Unauthenticated access works for public repositories and is limited to 60 requests per hour per IP address {V} (`x-ratelimit-limit: 60`, resource `core`); the automatic path asks once per interval. A private GitHub Enterprise repository cannot be updated this way, by design: no credential is ever sent.

## Trust model and verification

| Check | What it protects against | Where |
|---|---|---|
| HTTPS only; no credentials, cookies or identifiers (`User-Agent: ccshelf/<version>` is the only identification) | Eavesdropping, leaking a token | `fetch.go` |
| Redirects: at most 5; https only; to the same host or, for github.com, to `objects.githubusercontent.com`, `release-assets.githubusercontent.com` or `github-releases.githubusercontent.com`; a target with credentials is refused | A hostile or compromised redirect to another host. github.com redirects a release download to `release-assets.githubusercontent.com` {V} | `RedirectPolicy` |
| Size caps: 2 MiB for API responses, 256 KiB for checksums and the signature bundle, 128 MiB for the archive, 256 MiB for the binary; a declared length over the cap is refused before reading; a body shorter than declared is an error | Memory and disk exhaustion, truncated downloads | `fetch.go`, `archive.go` |
| Time limits: 40 s for the discovery, 5 min per download, 90 s for cosign, 15 s for the version check; the automatic check has a hard 3 s | Hangs | constants in `update` |
| `checksums.txt` is parsed strictly: every line `<sha256>  <file>`, and **exactly one** line for the archive | A swapped or ambiguous list | `ParseChecksums` |
| SHA-256 of the archive equals that line (constant-time compare) | Corruption, a replaced asset | `VerifySHA256` |
| **cosign**, if on `PATH`: `cosign verify-blob --bundle checksums.txt.sigstore.json --certificate-identity https://<host>/<repo>/.github/workflows/release.yml@refs/tags/<tag> --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt`. The identity is exact, never a regular expression; the tag is the one being installed. A failure, or a missing bundle while cosign is present, stops the update. cosign is found only in absolute `PATH` entries (a `.` or empty entry is ignored) and runs with a scrubbed environment (`PATH`, home and temp directories, proxy and CA variables only) | A compromised release asset set, a build not made by the release workflow, a tag squatted by someone else | `cosign.go` |
| `--require-signature` | A user who wants the signature check to be mandatory | `Updater.Apply` |
| Archive shape: only the binary entry is extracted; `LICENSE` and `README.md` (which goreleaser adds) are tolerated and skipped; any other entry, a directory, a symlink or hard link, a path with a separator, `..`, a drive or an absolute path, a duplicate, an oversize entry, more than 16 entries: the update fails | Path traversal, link tricks, a bomb | `ExtractBinary` |
| The new file is run with `version --json` (scrubbed environment, `CCSHELF_NO_UPDATE_CHECK=1`) and must report the target version | A wrong-architecture or wrong-version build that would brick the install | `RunVersion` |

What is **not** verified: the GitHub build provenance attestation (`gh attestation verify` needs `gh` and the network; the manual steps are in [SECURITY.md](../../SECURITY.md)), and cosign needs the Sigstore trusted root, so with cosign installed the update also needs access to the Sigstore TUF mirror (the same limitation as the manual check). Without cosign the SHA-256 and the HTTPS channel are the root of trust: an attacker who can replace both `checksums.txt` and the archive on the release host defeats it. Install cosign and use `--require-signature` where that matters. Binaries are not notarized or Authenticode-signed yet.

## Install-method detection

A table in `internal/update/detect.go`, a pure function of the path(s) of the executable (as invoked and with symlinks resolved), the OS, `GOBIN`/`GOPATH`, the home directory and a container flag:

| Kind | Recognized by (any path, case-insensitive) | Command shown |
|---|---|---|
| homebrew (darwin, linux) | `/Cellar/`, `/Caskroom/`, `/linuxbrew/`, `/homebrew/` | `brew upgrade ccshelf` |
| scoop (windows) | `\scoop\apps\`, `\scoop\shims\`, `\scoop\persist\` | `scoop update ccshelf` |
| winget (windows) | `\Microsoft\WinGet\Packages\`, `\Microsoft\WinGet\Links\`, `\Microsoft\WindowsApps\` | `winget upgrade ccshelf.ccshelf` |
| go | directory equal to `GOBIN`, an entry of `GOPATH` plus `/bin`, or `~/go/bin` | `go install github.com/<repo>/cmd/ccshelf@latest` |
| system (darwin, linux) | directory `/usr/bin`, `/usr/sbin`, `/bin`, `/sbin`; paths under `/nix/store/`, `/snap/`, `/usr/lib/`, `/usr/libexec/` | update with the system package manager |
| container | a container marker (`/.dockerenv`, `/run/.containerenv`, `KUBERNETES_SERVICE_HOST`, `container`), checked last | pull or rebuild the image |
| manual | everything else (`/usr/local/bin`, `~/.local/bin`, the install script's directory, ...) | none: ccshelf may replace itself |

`/usr/local/bin` is deliberately manual: it is where people put a downloaded binary by hand. A **development build** (`dev`, an empty version, a Go pseudo-version, `+dirty`, anything that is not semver) is refused too. `--force` overrides both and says so in `--help`. A symlinked executable is followed only if the target's directory is owned by the current user (Unix) and writable; otherwise the update is refused.

When the directory is not writable the command prints the exact elevated command (`sudo /usr/local/bin/ccshelf update --yes --version v0.2.0`, or the Administrator-terminal form on Windows) and exits 1. ccshelf never elevates itself.

## Replacing the running binary

- **Unix.** The new file is created next to the executable (same directory, so the same file system) with mode 0600, filled, `fsync`ed, then given the old binary's permission bits (0755 if it was not executable). The old binary is hard-linked to `<name>.old` (a copy if linking fails), then one `rename(2)` puts the new file over the old name. At every instant the name is a complete binary; the running process keeps its old inode.
- **Windows** (build-tagged; compiled and vetted for amd64 and arm64, run only on a Windows CI runner). A running `.exe` cannot be overwritten or deleted but can be renamed: remove an old `<name>.old`, rename the running `ccshelf.exe` to `ccshelf.exe.old`, rename the new file into place, and rename the old one back if that fails. The new temporary file is named `.exe` so that it can be run for the smoke test. The `.old` file is the rollback copy and is kept; only stale temporary files (`.ccshelf-update-*` older than an hour) are removed on later runs.
- **Rollback.** `--rollback` first runs `<name>.old version --json`. It must be ccshelf; if it runs, a normal confirmation follows; if it cannot be run at all (another platform, damaged) it needs `--yes` (a typed `yes` on a terminal); if it runs but is not ccshelf it is refused whatever the flags. The swap keeps the replaced binary as the new `.old`, so a rollback can be undone with another `--rollback`. A rollback never touches the network.
- **Locking.** `update.lock` in the cache directory is created exclusively (mode 0600); a second `update` (or automatic install) fails immediately (exit 1, or silently in the automatic path) and a lock older than 10 minutes, left by a crash, is taken over.

## Automatic update (opt-in)

```toml
[update]
mode = "off"        # off (default) | notify | install
interval = "24h"    # Go duration, minimum 1h
base_url = ""       # GitHub Enterprise Server only (see above)
```

There is no `channel`: automatic updates follow stable releases only. `ccshelf init` asks once, on a terminal, only when `[update]` is absent: "Check for updates once a day and tell me when one exists?" (default no), prints the equivalent `--update-mode notify` flag, and writes the answer (`notify` or `off`), so it is not asked again. In flags-only mode `init` never changes it; `--update-mode off|notify|install` is the way to set it, and `init --force` keeps an existing `[update]` section.

When it acts, after a command that **succeeded**, for every command except `run`, `dry-run`, `update`, `shell-init`, `completion`, `version` and `--help`:

- nothing at all if `mode` is `off`, `CI` is non-empty, or `CCSHELF_NO_UPDATE_CHECK` is set to any non-empty value (the kill switch; it does not affect the explicit `ccshelf update`), or this is a development build;
- the check runs at most once per `interval`, with a **hard 3 s** overall timeout; any error ends in at most one warning line on a terminal and the next attempt waits for the interval (a failing network is not retried on every command); it never changes the command's exit code;
- `notify` asks only when someone can read the answer (a terminal), and prints one line to stderr: `ccshelf 0.2.0 is available (you have 0.1.0). Run: ccshelf update` (the package manager's command instead, for a package-managed copy), at most once per 24 hours;
- `install` runs the same verified flow, but only for a release **newer, stable, in the same major version** (for 0.x, the same minor, since a 0.x minor may break), never a downgrade, never a pre-release, never for a package-managed or development build, and only if the directory is writable. It has its own 60 s budget (an archive is downloaded) and prints a progress line on a terminal and one result line, `ccshelf updated to 0.1.1 (...); it takes effect the next time you run ccshelf`. The running process is not replaced. A new major (or a new 0.x minor) is only announced, like `notify`.
- `run` and `dry-run` (which `exec` claude and must stay fast) make **no network call**: in `notify` mode they print the cached notice from the state file, once per 24 hours; in `install` mode they print nothing.

State is `update-state.json` in the cache directory (0700 directory, 0600 file, written atomically, never followed through a symlink): `last_check`, `latest` and `notified_at`. No identifier of the user or machine is stored or sent. A corrupt, unknown-keyed, oversized or implausible file (a time in the future, a version that does not parse) is ignored and treated as a first run.

### What is sent

A `GET` of the release metadata (and, for an install or `update`, of `checksums.txt`, the archive and, with cosign, the signature bundle) over HTTPS with `User-Agent: ccshelf/<version>` and `Accept` headers. No cookies, no authentication, no machine or user identifier, no telemetry. The release host sees an IP address and the version, as for any download. With cosign installed, cosign itself contacts Sigstore.

## Failure modes

| Situation | Behavior |
|---|---|
| No network, timeout, HTTP 5xx | Exit 1, hint to check the connection; nothing changed. Automatic path: one warning on a terminal, retried after the interval. |
| HTTP 403 or 429 (rate limit) | Exit 1; hint that GitHub limits unauthenticated requests. |
| Release or asset missing for this platform | Exit 1 naming the file; nothing changed. |
| Checksum mismatch, bad or missing signature, bad archive, wrong version inside | Exit 1, `nothing was changed`. |
| Directory not writable | Exit 1 with the exact `sudo ...` (or Administrator) command. |
| Another update running | Exit 1 (silent for the automatic path). |
| Package manager or development build | Exit 1 with the command to run, or `--force`. |
| No `.old` for `--rollback` | Exit 1. |
| Downloaded file killed mid-way (crash) | The old binary is untouched; stale `.ccshelf-update-*` files are cleaned on a later update. |

## Tests

Unit tests are hermetic: an `httptest` server (`internal/update/updatetest`) serves real tar.gz and zip archives and a `checksums.txt`; the clock, the HTTP client, the executable path, cosign (a copy of the test binary that behaves like `cosign verify-blob`), the version check and the replace step are seams. They cover success, up to date, a newer major (manual yes, automatic refused), downgrade, wrong checksum, truncated, oversize, a redirect to another host, plain http, archive traversal/links/extra entries, a read-only directory, package-managed paths per OS, development builds, rollback, concurrent updates, the interval and throttle, corrupt state, `CI` and the kill switch, `notify` without a terminal, and the 3 s timeout. Both replace algorithms (atomic rename and rename-aside) run on every OS. Two mutation passes by hand over the security-relevant conditions (version rules, checksum, archive entries, lock, redirects, signature identity, CI and the kill switch, the notice throttle, the hooks, the usage checks, the plain-http guard, the state-file name check) each found survivors (a defensive length check, the publisher's pre-release flag, three usage checks that another failure masked, the in-command plain-http guard); every one was closed with a test, and the last pass left none.

The end-to-end tests (`internal/e2e/update_test.go`, in `go test ./...` on all three OSes) build the real binary twice, stamped 0.0.1 and 0.0.2 with `-ldflags`, serve the second one inside a fake release, run `ccshelf update --yes`, and assert that the binary on disk now prints 0.0.2, the `.old` backup is 0.0.1, `--rollback` restores it, a tampered release changes nothing, and the automatic `install` honors `CI` and the kill switch.

### How the e2e binary reaches a plain-http loopback server

Production code requires https. The e2e binaries are built with `-tags e2eloopback`, which compiles `config.LoopbackHTTPAllowed = true` (`internal/config/loopback_e2e.go`); a normal build compiles the constant `false` (`loopback_prod.go`) and a unit test asserts it. It is a compile-time constant, not an environment variable, flag, file or build-time string that a user or a release could set, so a release binary cannot be talked into it, and even when it is on, only an `http://` URL whose host is a loopback address is accepted. The alternative, a test CA injected at run time, would have needed a code path that reads an environment variable in production binaries; a build tag leaves no such path. The e2e test that uses the untagged binary proves it rejects an http `base_url`. The unit tests of the commands use `httptest.NewTLSServer` and the production https path, with the client trusting the test certificate.

## Open items

- Windows: the rename-aside replacement, the swap and the end-to-end run were not run on a real Windows machine by the author; the first CI run on Windows is the proof {U}.
- An automatic install blocks the command that triggered it for up to 60 s once per release; moving it to a detached helper was rejected because process spawning is confined to the launcher and the git helpers.
- Verifying the build provenance attestation is not automated.

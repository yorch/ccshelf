# Platform: GitHub, GHE and cross-platform support

Requirements R1 (macOS, Linux, Windows) and R2 (GHE Cloud and Server, GitHub Actions), with the design assessment for each. Only macOS has run the real `claude` (Stage 0). The ccshelf test suite, which uses a fake `claude`, passes on macOS and, in Docker (`golang:1.27`), on Linux arm64 and amd64 (amd64 emulated), as root and as a normal user {V}. Windows code compiles and vets but has never run on Windows.

## Stack and platform decisions
Decided by the user (2026-10-06): **Go** for the implementation; **GitHub / GitHub Enterprise (GHE) and GitHub Actions** for hosting and CI.

### What GHE + GitHub Actions imply (design assessment; not yet tested against the user's GHE)
| Area | Implication |
|---|---|
| Which GHE | **Both GHE Cloud and GHE Server must be supported (R2).** The flavor matters for Actions availability, Pages and egress, so the design assumes the lowest common denominator (see "Requirement R2" below). |
| CI lint/catalog action | Provide a **composite action** in this repo. Two ways to get the binary: download a pinned release asset from the tool's public GitHub Releases (or from an internal mirror of them, which GHE Server without internet access needs), or build from source with `actions/setup-go` (slower; works where release downloads are restricted). On GHE Server, third-party actions such as `setup-go` must be mirrored or allowed by the admin. Pin by full commit SHA (SR5). |
| Releases | `goreleaser` supports GitHub Enterprise endpoints (`github_urls`). Publish binaries for all six targets to the public tool repo's GitHub Releases; adopters can mirror them to an internal package registry, Homebrew tap or Scoop bucket. A mirror that lives in a private repo needs a token for downloads, which affects `brew`/`scoop` install steps. |
| Catalog hosting | GitHub Pages (access-controlled on GHE Cloud; available on GHE Server if enabled), or any internal static host. The CI job publishes `catalog.json` + HTML as a Pages artifact. Pages visibility must match who may see the plugin list. |
| Marketplace source type | Claude Code marketplace sources of type `github` target github.com; for GHE hosts a **git URL** source is likely required (`git@ghe.example.com:org/marketplace.git` or https). Needs verification against the marketplace docs and the user's auth (SSH keys/credential helper). Also affects `strictKnownMarketplaces` patterns. |
| Profile `git` source | Same: use git URLs for GHE; honor the user's existing git credential helper/SSH config rather than storing tokens. |
| Tags and bundles | Native bundle resolution uses git tags `<plugin>--v<version>`; CI should create them (`claude plugin tag`), which needs write permission for `GITHUB_TOKEN` on tags. |
| Usage data | Enterprise Analytics API / OTel exist independently of GitHub; the catalog job needs a secret for the Analytics API key if used. |
| Provenance | **Required on github.com releases (SR5):** keyless signatures (cosign), SLSA provenance and an SBOM. Artifact attestations have limited support on GHE Server, so ship offline-verifiable bundles for GHE Server mirrors. |

### Requirement R2: support both GHE Cloud and GHE Server (decided 2026-10-06)
Design to the lowest common denominator, so nothing assumes github.com or the newest Actions features.
1. **CLI first, workflow second.** All logic (`lint`, `compile`, `catalog build`) lives in the Go binary; the GitHub Actions workflow is a thin wrapper that calls it. The same binary then runs in any other CI (or locally) if Actions is restricted or a different runner is used.
2. **No hard-coded hosts.** GitHub API/Git/Pages/release URLs come from configuration or the Actions environment (`GITHUB_SERVER_URL`, `GITHUB_API_URL`), never from `github.com` literals. Test against a non-github.com host.
3. **Minimal Actions dependencies.** Prefer composite steps that call the binary and plain `git`/`gh`. Avoid third-party actions where practical; where unavoidable (`setup-go`, `upload-pages-artifact`), document that GHE Server admins must mirror/allow them (e.g. via `actions-sync` or GitHub Connect) and offer an alternative without them.
4. **Offline/air-gapped tolerant.** GHE Server may have no internet: the release binary must come from the internal instance, builds must not fetch from `github.com` at runtime, and the catalog site must be self-contained (no CDN fonts/scripts).
5. **Version skew.** GHE Server lags github.com. Avoid features that may not exist on older Server versions (newer Actions syntax, artifact attestations, some Pages options) or make them optional with a fallback. Record the minimum supported GHE Server version once known.
6. **Auth via the user's git setup.** Git operations shell out to `git` using the existing credential helper or SSH config; API calls (if any) use `GITHUB_TOKEN` in CI or `gh` locally. No tokens stored by the tool.
7. **Marketplace source types.** For GHE hosts use git URL sources; verify the `github` source type behavior against the marketplace docs for both flavors (open).
8. **Catalog hosting is pluggable.** Output a plain static directory. Publishing to Pages is one option (differences between Cloud and Server noted above), but any static host works.

Still to verify: how Claude Code's marketplace and plugin source types behave with GHE Cloud vs Server hosts (including data-residency domains), and the minimum GHE Server version the Actions workflows must support.


## Cross-platform requirement (macOS, Linux, Windows)
**Requirement R1:** the launcher, the catalog tooling and the CI lint must work on macOS (arm64, x64), Linux (x64, arm64; WSL counts as Linux) and native Windows 10/11 (x64, arm64), with the same behavior and the same profile files everywhere. Everything below is a design assessment; Stage 0 was run on macOS only, so Linux and Windows behavior is **not yet tested**.

### Facts from the docs (reported by a research agent; managed-settings registry details were truncated)
- Native Windows: `%USERPROFILE%\.local\bin\claude.exe` (PowerShell/CMD installer or WinGet), or npm (Node 22+). The shell tool is PowerShell, and Git Bash is optional (`CLAUDE_CODE_GIT_BASH_PATH`). WSL is a separate Linux install with its own `~/.claude`.
- Config: `~/.claude/` and `~/.claude.json` (`%USERPROFILE%` on Windows). Linux does not use XDG for Claude Code.
- Managed settings: macOS `/Library/Application Support/ClaudeCode/`, Linux `/etc/claude-code/`, Windows `C:\Program Files\ClaudeCode\`. Windows registry/MDM/GPO is mentioned but its details were not retrieved.
- Credentials: macOS Keychain (keyed per `CLAUDE_CONFIG_DIR`, file fallback; verified in the docs by the adversary's check); **Linux and Windows use `<config dir>/.credentials.json`**, with no locking documented.
- Paths in JSON settings/MCP configs: forward slashes everywhere, `~` and `${VAR}` expansion supported. `CLAUDE_CODE_PLUGIN_DIRS` uses `:` on Unix and `;` on Windows; `--plugin-dir` is repeated per path.
- No Claude Code temp-dir override; it uses OS defaults (`TMPDIR`, or `TEMP`/`TMP` on Windows).
- Symlinks on Windows need Developer Mode or admin. WinGet upgrades can fail while `claude.exe` is running.
- **Conflict to resolve:** the agent says MCP `npx` servers need no `cmd /c` wrapper on native Windows. I recall the MCP docs recommending `cmd /c npx ...` there. Treat as unverified until tested.

### How it changes the design
| Area | Unix-only assumption in earlier drafts | Change |
|---|---|---|
| Starting `claude` | `exec claude` everywhere | **Unix: `exec`** (replace the launcher process, so job control, Ctrl+Z, `fg`, resize, SIGHUP and exit codes behave exactly as for plain `claude`; a spawn-and-wait parent would hang the terminal when Claude Code suspends itself). **Windows: no `exec`**, so spawn a child with inherited stdio, ignore Ctrl+C in the launcher (the child shares the console), forward termination, return the child's exit code. Map signal deaths to 128+n. Test Ctrl+Z, `fg`, resize and SIGHUP on Unix. |
| Generated files | Per-process files under `$TMPDIR`, deleted on exit | With `exec` nobody can clean up afterwards, and `--settings` and related flags carry through to backgrounded, resumed and respawned sessions, so a launch-time cleanup can delete a file a live session still uses. Use **content-addressed files** (name = hash of the resolved config) in the launcher's cache dir. Identical content is reused, so concurrent runs never collide. Write atomically (temp file + rename; if the file already exists, skip the rename, which also avoids Windows failures on open files). **Garbage-collect by age only (weeks)** and skip paths referenced by running sessions (`claude agents --json --all`). Validate the generated JSON before launch (see [stage0.md](../research/stage0.md), "Corrections after the review round"). |
| Finding `claude` | `PATH` lookup | Use PATH lookup (handles `.exe`/`.cmd`), fall back to `~/.local/bin`, allow an override (config or env). npm installs on Windows give a `.cmd` shim, which needs care when spawning. |
| Paths in profile files | `~/...` and `/` | Accept `~` and forward slashes; normalize backslashes on read; always write forward slashes into generated JSON. Never build paths by string concatenation. |
| Launcher's own dirs | `~/.config/ccshelf/` | Config: `$XDG_CONFIG_HOME` or `~/.config/ccshelf` on macOS/Linux, `%APPDATA%\ccshelf` on Windows. Cache: `~/.cache/ccshelf` or `%LOCALAPPDATA%\ccshelf`. Docs examples show the Unix form. |
| Shell aliases | `alias cf=...` | Aliases don't exist in PowerShell/cmd. `ccshelf shell-init` emits bash/zsh/fish functions and PowerShell functions; cmd gets `.cmd` shims. |
| Symlinks | Not used | Keep it that way: never depend on symlinks (Windows needs Developer Mode). Copy or content-address instead. |
| MCP `npx` servers | `command = "npx"` | The MCP registry should allow a per-OS command override (e.g. `windows = ["cmd", "/c", "npx", ...]`) until the question above is settled. |
| Policy detection | Read managed-settings files | File paths are known per OS, but Windows registry/MDM and server-managed settings can't be read reliably. Make detection best-effort and also **detect by failure**: if `claude` exits 1 on a sideload flag, report "blocked by policy". |
| Credentials | "Keychain per config dir" | Not our concern as long as we never set `CLAUDE_CONFIG_DIR`. On Linux/Windows credentials are one shared file with no documented locking, so concurrent token refresh is **untested** there. |
| Encoding | Not discussed | Write UTF-8 without BOM and LF; read tolerant of BOM and CRLF. Windows PowerShell 5 `>` redirection writes UTF-16, which would break TOML/JSON; document it and reject non-UTF-8 input with a clear error. |
| Self-update | n/a | A running `.exe` can't be replaced on Windows. No self-update; ship through package managers (Homebrew, Scoop/WinGet, apt/deb later) and release downloads. |
| Distribution/trust | n/a | Unsigned binaries trigger macOS Gatekeeper (notarization) and Windows SmartScreen. Internal distribution needs signing or a package manager that handles it. |
| WSL | n/a | Treated as Linux. A Windows-native launcher and a WSL launcher are separate installs with separate configs; the launcher never crosses the boundary. Profile sources on a Windows path aren't visible from WSL unless configured. |
| Testing | macOS only so far | CI matrix on macos, ubuntu and windows runners. Use a **fake `claude`** test double (records its arguments and generated files) for unit and integration tests on every OS; keep the real-`claude` init-event test (Stage 0 method) opt-in because it needs authentication. |

### Supported targets for the first release (decided 2026-10-06)
All six: **darwin/arm64, darwin/amd64, linux/amd64, linux/arm64, windows/amd64, windows/arm64**. WSL counts as linux.
- **Build:** Go cross-compiles all six from one runner (`goreleaser`).
- **Test in CI:** run unit and fake-`claude` integration tests natively wherever a hosted runner exists (macOS arm64 and Intel, Ubuntu x64, Windows x64; Ubuntu arm64 and Windows arm64 runners are reportedly available for public repos, which needs verifying before committing to them). Targets without a native runner are at minimum compile-checked, and flagged "built, not natively tested" in the release notes until a runner is available.
- **Publish:** release archives for all six; Homebrew tap (macOS and Linux), Scoop and WinGet (Windows). Binaries need macOS notarization and Windows signing, or the package manager must cover that.
- **Not covered by CI:** the real-`claude` integration test stays opt-in (needs authentication), so Linux and Windows behavior of `claude` itself (credentials, `cmd /c` for `npx`, managed-settings paths) must be verified manually or in a self-hosted job before claiming support.

### Effect on the language choice
This strengthens **Go**: `os/exec`, build tags for platform differences, `filepath`, `os.UserConfigDir`/cache dirs, trivial cross-compilation, and `goreleaser` publishing to Homebrew, Scoop and WinGet. TypeScript with `bun --compile` remains possible, but Windows signal and console handling are the weaker spot. Shell scripts are out as an implementation (they're fine only as generated aliases).

### Open questions (new)
1. Do concurrent sessions refresh credentials safely on Linux/Windows (shared `.credentials.json`)?
2. Does masking via `--settings` behave identically on Windows (Stage 0 repeated there)?
3. Does the MCP `npx` command need `cmd /c` on native Windows?
4. How do Windows registry/MDM managed settings appear, and can they be detected?
5. ~~Which architectures are in scope~~: **decided, all six targets** (see "Supported targets"). Still open: which Linux distros and packaging formats beyond release archives.

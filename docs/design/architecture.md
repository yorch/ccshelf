# Architecture

Code layout of the tool repo, design principles and the Go stack. For the org data repo see [catalog-and-org-repo.md](catalog-and-org-repo.md).

## Code layout and principles
**Code layout of the tool repo** (the public repo; the org's private data repo is described in [catalog-and-org-repo.md](catalog-and-org-repo.md)). One Go module that builds one binary, split into three packages with no cross-dependencies except on `core`:

- `core/`: read `marketplace.json` and installed state (`claude plugin list --json` for installed plugins, `--available` when the catalog needs uninstalled ones), resolve sets. Pure, no side effects.
- `profiles/`: local per-terminal launcher. Depends on `core`.
- `catalog/`: metadata lint + static catalog site, runs in CI. Depends on `core`.

Separate releases. Git plus CI is the registry; no server or database. `internal/update` (with `internal/cli/updatecmd`) implements the verified self-update; see [update.md](update.md).

### Design principles
1. **Never write shared state** (`~/.claude/settings.json`, `~/.claude.json`, plugin cache). The launcher writes only its own generated files (content-addressed, in its own cache dir; see [platform.md](platform.md)) and starts `claude`. This is what makes concurrent terminals safe and what mcpick-style tools probably lack {U}.
2. **Respect org policy.** Detect `disableSideloadFlags`, managed `enabledPlugins`, `strictKnownMarketplaces`; report "blocked by policy". Never work around them.
3. **Prefer `--settings` (`enabledPlugins` masking + `skillOverrides`) over sideload flags**, since `--plugin-dir`, `--agents`, `--mcp-config` can be disabled by policy. Use sideload flags only when policy allows.
4. **Everything cheap to delete.** Anthropic is shipping into this space. The manifest and catalog conventions are the durable parts; the launcher should be thin enough to delete if native profiles (#91770) ship.
5. **Catalog is derived**, never a second source of truth: `marketplace.json` + per-plugin sidecar files + git data.
6. **Lead the pitch with routing quality and clutter**, not token cost.

## Go stack choices (proposed)
- Module layout: `core/`, `profiles/`, `catalog/` as Go packages in one module; one `ccshelf` binary with subcommands (project and command name `ccshelf`; see [project.md](project.md)). Cobra or a small stdlib-based CLI parser; stdlib `os/exec`, `encoding/json`, `html/template`, `embed`.
- TOML: a maintained library (`pelletier/go-toml/v2` or `BurntSushi/toml`; pick one that preserves useful error positions).
- JSON Schema validation for manifests: a Go validator library, with schemas in `schema/` shared with editors.
- Git access for `git` sources: shell out to the user's `git` (inherits credential helper/SSH config, important for GHE) rather than embedding a git library.
- Catalog frontend: generated static HTML plus one small vanilla JS file for filtering; data in `catalog.json`.
- Release: `goreleaser`; CI matrix on `macos-latest`, `ubuntu-latest`, `windows-latest` (see cross-platform section).
- Tests: golden files for generated settings, a fake `claude` binary built from `testdata/`, opt-in real-`claude` integration test.

## Repo layout
Tool repo (this one): `core/ profiles/ catalog/ schema/ action/ site/ examples/ docs/`.
Org data repo: see [catalog-and-org-repo.md](catalog-and-org-repo.md) (marketplace, plugins, `profiles/`, generated `bundles/`, `catalog/` sidecars, org config, CI). Built catalog output is not committed.

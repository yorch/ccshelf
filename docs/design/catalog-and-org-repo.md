# Catalog and the org data repo

How the tool repo and an adopting org's private data repo relate (R5), what each contains, and how the data repo is structured. The org data repo is called the "profile catalog repo" in conversation.

## Tool repo vs data repo (R5)
Decided 2026-10-06: the **tool is hosted in a public GitHub repo** (this one), and an adopting company stores its **profiles and catalog data in its own private GHE repo**. The tool never assumes the two live together.

| | Tool repo (public, github.com) | Org data repo (private, GHE Cloud or Server) |
|---|---|---|
| Holds | Go source, schemas, docs, release binaries, a reusable GitHub Action, example/fictional profiles, a starter template | The org's `marketplace.json`, plugins, `profiles/*.toml`, generated profile bundles (`bundles/`), org config. The built catalog (`catalog.json`, site) is produced in CI and not committed |
| Contains org data? | Never. Examples are fictional. | Yes |
| Released how | Public GitHub Releases (goreleaser); package managers; adopters may mirror internally | Not released; consumed by the tool |
| Changes by | Open-source contributors | The org's platform team |

Consequences:
- **Reusable CI:** the data repo's workflow calls the public tool, either `uses: <owner>/ccshelf/action@<full commit SHA>` or a step that downloads a pinned release binary. GHE Cloud can use public actions directly; **GHE Server needs GitHub Connect or a mirror** (e.g. `actions-sync`), or the binary-download variant (also mirrorable to an internal registry). Both variants must be documented; the logic stays in the binary (R2).
- **Pin everything (SR5).** The data repo pins the tool by full commit SHA (never a moving tag), the Action embeds the expected SHA-256 of the binary it downloads, and releases are signed with provenance, because the tool runs in the org's CI and on developers' machines.
- **Starter template** (layout below): ship a template/example data repo (`examples/org-data-repo/`, possibly also a GitHub template repository) with a sample `marketplace.json`, `profiles/`, a CI workflow and a catalog publish recipe, so adopting takes minutes.
- **Configuration lives with the adopter, not in the tool:** profile sources (`dir`/`git`, later `plugin`), the catalog metadata schema location and lint rules come from the org's `ccshelf.toml` in the data repo and the user's own `~/.config/ccshelf/config.toml`, with sane defaults. The tool repo never needs to know about a particular org.
- **Catalog hosting is the adopter's choice** (R2): the tool outputs a plain static directory; the starter template shows GitHub Pages and an internal static host. We don't pick one for the org.
- **Telemetry:** none by default. An open-source tool that runs in corporate CI must not phone home.
- **Security reporting, license and contribution docs** live in the public repo (R4).





### What the tool repo contains
The tool repo holds **no real org definitions**, but it does hold **fictional example ones**.
- **Code:** the Go source for `core/`, `profiles/` and `catalog/`, built into one binary.
- **Schemas:** JSON Schemas for profiles, sidecars and the org config (they define what is valid, not any actual profile or sidecar).
- **Catalog site templates:** the HTML, CSS and JS that render a catalog from data.
- **Reusable GitHub Action** and the release and packaging config (goreleaser, Homebrew, Scoop, WinGet).
- **Tests and fixtures:** the fake `claude` test double, golden files, and fixtures that simulate managed policy.
- **Docs:** the research notes, `AGENTS.md`, the license, security policy and contribution guide.
- **Starter template** (`examples/org-data-repo/`): a complete but fictional org data repo (sample `marketplace.json`, a couple of example plugins, profiles such as `frontend` and `sre`, sidecars, org config and CI workflows). Adopters copy it to start, and it doubles as a test fixture.

Never in the tool repo: a real org's plugins, profiles, sidecars, marketplace, taxonomy or MCP server definitions; org-specific URLs, team names or secrets.

Two choices: (1) **no built-in default profiles**: roles like "frontend" are org choices and a shipped default invites arguments about what belongs in it, so the examples show the format and nothing more; (2) a **public demo catalog** built from the fictional starter data and published (for example on GitHub Pages) is optional, to show what the catalog looks like.

---

## Structure of the org data repo


This describes the **private repo an adopting organization keeps** (R5): its marketplace, profiles and catalog data. The public tool repo (this one) ships a starter template of it in `examples/org-data-repo/` once code starts. Everything here is a design proposal except where marked decided; names are provisional. The `ccshelf` commands shown don't exist yet.

## Principles
1. **Native first.** `marketplace.json` and plugin folders stay exactly what Claude Code expects, so `/plugin` works with the repo unchanged.
2. **Hand-written data is small and reviewable; everything else is derived.** Catalog site and `catalog.json` are built in CI and not committed.
3. **Ownership is routable.** Review rules must be expressible in `CODEOWNERS` (which works per file, not per JSON entry).
4. **Pinned and releasable.** Consumers pin a tag of this org data repo for profile sources; plugins are tagged `<plugin>--v<version>` for native dependency resolution.
5. **Works for small and large registries.** ~50 plugins is the design point; a single-file mode exists for tiny registries.

## Layout
```
acme-claude-marketplace/                 # private repo on GHE (Cloud or Server)
├── .claude-plugin/
│   └── marketplace.json                 # native; hand-written; plugin list + description/category/tags/author
├── plugins/                             # plugins whose source lives in this org data repo
│   ├── design-kit/
│   │   ├── .claude-plugin/plugin.json
│   │   ├── skills/  agents/  hooks/
│   │   └── README.md
│   └── sre-kit/ ...
├── bundles/                             # GENERATED, committed: one dependency-only plugin per profile
│   ├── profile-frontend/.claude-plugin/plugin.json
│   └── profile-sre/.claude-plugin/plugin.json
├── profiles/                            # hand-written profile manifests (see [profiles.md](profiles.md) for the format)
│   ├── base.toml
│   ├── frontend.toml
│   └── sre.toml
├── mcp/
│   └── registry.toml                    # MCP server definitions that profiles reference by name
├── prompts/                             # files referenced by profiles (append_system_prompt_file)
├── catalog/
│   ├── plugins/                         # one sidecar per plugin: catalog-only metadata (hand-written)
│   │   ├── design-kit.toml
│   │   └── sre-kit.toml
│   ├── taxonomy.toml                    # allowed categories and tags, curated by the platform team
│   └── site/                            # optional overrides: title, logo, intro text, templates
├── ccshelf.toml                  # org config: required fields, lint rules, catalog settings
├── .github/
│   ├── CODEOWNERS
│   ├── pull_request_template.md
│   └── workflows/
│       ├── validate.yml                 # on PR
│       ├── tag-plugins.yml              # on merge: tag changed plugin versions
│       ├── catalog.yml                  # on merge to main: build and publish the catalog
│       └── release.yml                  # manual or scheduled: tag the repo for consumers to pin
├── docs/                                # contributor guide: add a plugin, metadata fields, deprecation
└── README.md                            # links to the catalog site and the contributor guide
```
Not in the repo: the built catalog (`dist/`, published as an artifact or to a static host) and `catalog.json` (also built). They can be reproduced from the files above.

## What each part is
| Path | Written by | Committed? | Purpose |
|---|---|---|---|
| `.claude-plugin/marketplace.json` | Humans (platform-reviewed) | Yes | Native plugin list. Fields Claude Code reads: `name`, `source`, `description`, `category`, `tags`, `version`, `dependencies`, `relevance`, `author`. |
| `plugins/<name>/` | Plugin authors | Yes | Plugin source for in-repo plugins. Plugins hosted elsewhere use a git `source` in `marketplace.json` and have no folder here. |
| `bundles/profile-<name>/` | `ccshelf compile` | Yes (checked for drift) | Dependency-only plugins so a profile's plugins install natively. Must be in git for the marketplace to serve them. |
| `profiles/*.toml` | Humans | Yes | Profile manifests: plugins, standalone-skill off-lists, MCP, session defaults. |
| `mcp/registry.toml` | Humans | Yes | Named MCP server definitions. Security-sensitive: commands and env are code execution on developer machines. |
| `catalog/plugins/<name>.toml` | Plugin authors | Yes | Catalog-only metadata, one file per plugin (see below). |
| `catalog/taxonomy.toml` | Platform team | Yes | Allowed categories and tags, so the catalog's facets stay clean. |
| `ccshelf.toml` | Platform team | Yes | Which sidecar fields are required, lint severity, catalog title and the marketplace files that feed the catalog. Users configure their own profile sources in `~/.config/ccshelf/config.toml`; `ccshelf init` can write that from the data repo's URL (proposed). |
| `dist/`, `catalog.json` | CI | No | Built catalog. |

## Where catalog metadata lives: a sidecar per plugin (decided 2026-10-06)
The earlier convention put `owner`, `status`, `when_to_use`, etc. into each marketplace entry's free-form `metadata`. That has a problem: with ~50 plugins in one `marketplace.json`, **`CODEOWNERS` can't route review per entry**, so the decided "authors write, platform team reviews" model can't be enforced by file ownership. A sidecar file per plugin fixes this: a PR that touches `catalog/plugins/*.toml` requires platform review, while plugin source stays with the author's team.

- `marketplace.json` keeps only what Claude Code reads (plus `category` and `tags`, which are native; whether the `/plugin` UI searches them is unverified).
- The catalog is **derived from** `marketplace.json` + sidecars + git data (last change, contributors). Nothing is duplicated into `metadata`.
- Plugins hosted outside this org data repo get a sidecar too; the sidecar directory is uniform, not colocated with plugin source.
- **Small registries** (for example under 20 plugins) can opt into **single-file mode** (`ccshelf.toml`: `metadata_source = "marketplace"`), reading the same fields from each entry's `metadata` object, so one file is enough.

Sidecar example (`catalog/plugins/sre-kit.toml`; fields are provisional):
```toml
owner = "@acme/sre"                  # team accountable for this plugin (required)
status = "active"                    # active | experimental | deprecated (required)
when_to_use = ["incident response", "postmortems"]   # phrases for search and "what should I use"
avoid_when = ["routine deploy checks"]
overlaps_with = ["ops-helper"]       # plugins a reader might confuse this with
superseded_by = ""                   # required when status = deprecated
review_by = "2027-03-01"             # when the entry must be re-confirmed; `doctor` flags stale ones
support = "#sre-help"                # where to ask for help
docs = "https://wiki.example/sre-kit"
```

## Generated bundles
`ccshelf compile` writes `bundles/profile-<name>/.claude-plugin/plugin.json` (name, version, `dependencies`) from `profiles/<name>.toml`. Because marketplaces serve files from git, these are committed; CI runs `ccshelf compile --check` and fails the PR if a committed bundle differs from what would be generated. The `marketplace.json` entry for each bundle (name, `source: ./bundles/profile-frontend`, category `profile`) is written by hand once; `lint` checks that every profile has an entry and every bundle entry has a profile. (Alternative: generate those entries into `marketplace.json`; rejected for now because mixing generated and hand-written entries in one JSON file invites merge conflicts.)

## Org config (`ccshelf.toml`)
```toml
[lint]
require = ["owner", "status"]        # sidecar fields every plugin needs
require_when_deprecated = ["superseded_by"]
max_review_age_days = 180
taxonomy = "catalog/taxonomy.toml"

[catalog]
title = "Acme plugin catalog"
metadata_source = "sidecar"          # sidecar | marketplace (single-file mode)
marketplaces = [".claude-plugin/marketplace.json"]   # more than one marketplace may feed one catalog

[profiles]
dir = "profiles"
mcp_registry = "mcp/registry.toml"
```

## Ownership and review (`.github/CODEOWNERS`)
```
# platform team reviews everything that shapes what people see and what runs on their machines
/.claude-plugin/marketplace.json   @acme/platform
/catalog/                          @acme/platform
/profiles/                         @acme/platform
/mcp/                              @acme/platform
/ccshelf.toml               @acme/platform
/bundles/                          @acme/platform
/.github/                          @acme/platform   # workflows and CODEOWNERS themselves; without this a plugin team can edit them
/plugins/*/hooks/                  @acme/platform   # hooks and MCP in plugins are code on developer machines
/plugins/*/.mcp.json               @acme/platform

# each team owns its plugin source
/plugins/design-kit/               @acme/web
/plugins/sre-kit/                  @acme/sre
```
`ccshelf catalog init` writes this file for a new repo (see below). `ccshelf lint` can check that every `plugins/<name>/` folder has a `CODEOWNERS` line and that the sidecar `owner` matches it. Note that listing several owners on one `CODEOWNERS` line requires approval from any one of them; requiring both teams needs branch-protection rules or rulesets.

## CI workflows (thin wrappers around the pinned tool)
| Workflow | Trigger | Steps |
|---|---|---|
| `validate.yml` | pull request | `claude plugin validate .`; `ccshelf lint`; `ccshelf compile --check`; `ccshelf catalog build` and upload the preview as an artifact |
| `tag-plugins.yml` | merge to main | for each plugin whose version changed, create tag `<plugin>--v<version>` (`claude plugin tag`); needs tag-write permission |
| `catalog.yml` | merge to main | `ccshelf catalog build`; publish to the org's chosen host (Pages or any static host) |
| `release.yml` | manual or scheduled | create a repo tag (for example `v2026.10.1`); consumers pin profile sources to it |

All steps call the same pinned binary from the public tool repo (R2, R5): `uses: <owner>/ccshelf/action@<full commit SHA>` or a pinned release download whose SHA-256 is verified (SR5).

**Workflow security (SR5).** `permissions: {}` at the top of every workflow, granted per job (`contents: write` only for tagging; `pages` and `id-token` only for the catalog). Secrets (for example an Analytics API key) live in a protected environment deployable only from `main`, and are not sent to pull requests from forks. Never interpolate `${{ }}` values from plugin names, versions, descriptions or sidecars into shell: pass them through `env:` and validate (for example a semver pattern). Require code-owner review and at least two approvals through a ruleset, and dismiss stale reviews. The catalog build renders sidecar and marketplace text with `textContent` and a strict CSP, Markdown with raw HTML off and only `http` and `https` links, and the publish step fails unless Pages visibility is private or internal. PR preview artifacts are built from untrusted content and are not published.

## Setting up the data repo: `ccshelf catalog init` (decided 2026-10-06, D-41)
`ccshelf catalog init [dir]` bootstraps a **new** org data repo in an empty directory, or retrofits an **existing** marketplace repo (one that has `.claude-plugin/marketplace.json` and/or `plugins/`) without changing anything that is already there. It is the organization's one-time setup; `ccshelf init` stays each developer's own configuration. The behavior below is implemented and covered by unit, golden and end-to-end tests.

### Modes and the plan
The mode is detected (`new`: a missing or empty directory, a lone `.git` counts as empty; `adopt`: anything else) and can be forced with `--mode new|adopt` (`new` on a directory with content is a usage error). The command always builds a **plan** first and prints it, one line per file, then (without `--dry-run`) writes it:

| Action | Meaning |
|---|---|
| `create` | The file does not exist; it is written (mode 0644, directories 0755, LF endings, no timestamps). |
| `skip-exists` | The file exists and is left alone. The reason says `up to date` when it already equals what would be written, else `exists; left alone` (or `never rewritten` for `marketplace.json`). |
| `needs-merge` | `.github/CODEOWNERS` (or the CODEOWNERS that GitHub reads from the root or `docs/`), `.gitattributes` or `.gitignore` exists and lacks generated rules or lines. The file is not touched; the lines to add are in the plan and, with `--write-suggestions`, in `<file>.ccshelf-suggested` next to it. For CODEOWNERS the check is semantic (an existing catch-all `*` owned by a platform owner already covers the platform paths) and the suggestion tells you to put a missing catch-all at the **top** of the file, because GitHub applies the last matching rule. |
| `overwrite` | Only with `--force`: the old bytes are first saved as `<file>.bak` (an existing `.bak` stops the run), then the file is replaced atomically. `marketplace.json` is never rewritten, even with `--force`. |

Adopt mode never edits, appends to or deletes an existing file; it creates only missing files, and the second run with the same flags changes nothing and says `nothing to do`. A re-run without the value flags works as well, because the values are read back from the existing `marketplace.json` and `ccshelf.toml`; a re-run after adding plugins creates their sidecars and suggests their CODEOWNERS lines (that is how the generated files are kept up to date: edit freely, run it again, merge the suggestions).

### What it generates
```
ccshelf.toml                          lint settings ([lint] platform_owners from --platform-owners), catalog title, [protect] as comments
.claude-plugin/marketplace.json       a skeleton; in adopt mode it lists the plugins found under plugins/*/.claude-plugin/plugin.json
catalog/plugins/<name>.toml           a stub per plugin (see below); --sidecars none or --no-sidecars skips them
.github/CODEOWNERS                    the platform-owned paths of the starter template and one team rule per plugin
.github/workflows/validate.yml        pull requests: lint, compile --check, catalog preview
.github/workflows/catalog.yml         merge to the default branch: build and publish to GitHub Pages
.github/workflows/release.yml         manual or monthly: create the repository tag consumers pin to
README.md                             what the repo is, how to add a plugin, profiles and catalog
.gitattributes, .gitignore            LF endings (the compile drift check compares bytes); dist/ and suggestion files
profiles/example.toml.sample          only with --example-profile: an all-comment sample, never a profile
```
Every group has a `--no-<group>` flag. There are **no built-in default profiles** (R5): roles are the organization's choice, and the one sample is commented out and carries a `.sample` suffix so that `ccshelf` never loads it. No organization name, host or data is in the templates.

### Placeholders and the lint result
A sidecar stub has the owner (the owner the existing CODEOWNERS names for the plugin directory, else `--owner`, else the first platform owner), `status = "experimental"` (an unreviewed entry should not claim `active`) and `when_to_use = ["TODO(ccshelf): ..."]`. The fields that `lint.require` demands and that can hold a placeholder (`avoid_when`, `support`) get one; the ones that cannot (`review_by`, `overlaps_with`, `superseded_by`, `docs`) are left out, so lint reports them as errors (CAT013) exactly where work is needed. A marketplace entry whose `plugin.json` has no description gets `TODO(ccshelf): describe what <name> does`. `ccshelf lint` reports every remaining marker as warning **CAT048**, so the result for a freshly generated repo is deterministic: **no errors; warnings only CAT048 (one per placeholder) and, in adopt mode, whatever the existing files already had or the not yet merged CODEOWNERS suggestions leave uncovered (CAT044, CAT045, CAT046)**. A new repo without plugins lints with no findings at all, also with `--strict`.

### The ccshelf pin in the workflows
The workflows call `yorch/ccshelf/action` by full commit SHA (SR5). `--ccshelf-ref` takes the 40-character commit SHA, or a release tag `vX.Y.Z` (which sets only the `version:` input; a tag can never be the pin); `--ccshelf-version` sets the tag when the ref is a SHA. Without a complete pin the workflows contain the all-zero SHA, a `TODO(ccshelf)` comment and a first step that prints an error annotation and exits 1, so a pull request fails with a clear message instead of an obscure "action not found"; the command prints the same as a TODO. `--runner-label` sets the fallback of `runs-on: ${{ vars.RUNNER_LABEL || '<label>' }}`. The workflows contain no host name besides the action reference, and no user value reaches a `run:` block: the only values written into YAML are the pin and the runner label, both restricted to characters that need no quoting.

### Safety
- Writes are confined to the target directory through `os.Root`; a symbolic link is never followed or written through (the target, an intermediate directory and a file are all checked; a link at a generated path is reported and left alone, and `--force` does not replace it); `..` in the argument, the file system root, the user's home directory and any directory that is or lies inside the ccshelf tool repository are refused; `.git` is never touched.
- A file is created exclusively and atomically (a temporary file next to it, then a hard link that fails when the name exists, falling back to a check and a rename on file systems without links), so a file that appears after the plan is never replaced; an overwrite re-reads the file and refuses when it changed since the plan.
- Every value is validated with a strict pattern (marketplace name `^[a-z0-9][a-z0-9-]{0,63}$`, owners `@user`, `@org/team` or an email address, no control, invisible or bidirectional characters) and written through an encoder of its format: TOML strings and JSON through encoders, Markdown by escaping every punctuation character; the display name is the only free text. Existing files are read with a size limit and never through a link.
- It runs no process (only `--git-init` runs `git init`, through the same hardened git helper as the catalog git data; the repository is never committed to, fetched from or pushed) and makes no network call.

### Single source of the templates
The templates live in `internal/scaffold/templates/` and are embedded into the binary, so they are the one source of the workflows, `.gitattributes` and `.gitignore`. The starter under `examples/org-data-repo/` keeps its own copies of exactly those files (so it stays a complete, copyable repository) and a test in `internal/scaffold` fails when they drift: `go test ./internal/scaffold -run Example -update` regenerates them with the fictional `acme` parameters. The rest of the starter (plugins, profiles, CODEOWNERS with several teams, `ccshelf.toml` with `[protect]`, the README) is hand-written and is only checked by `scripts/check-examples.sh`. This is less fragile than generating the embedded copy from the example (an embed cannot reach outside its package, so that needs a copy step and a second drift check) or than an overlay inside the generator that models the example-only content.

## Consumption
- **Plugins:** users add the marketplace natively (`extraKnownMarketplaces` or `/plugin marketplace add <git URL>`), and bundles install profile plugin sets in one step.
- **Profiles:** users configure a `git` source pointing at this org data repo at a pinned tag, or a local clone as a `dir` source (see [profiles.md](profiles.md), "Profile sources and sharing"). The `plugin` source (profiles shipped inside a plugin) comes later.
- **Catalog:** the published site, and `ccshelf search` or `ccshelf doctor` reading the same data locally.

## Variants
- **Plugins in other repos:** `source` entries point at external git repos; sidecars still live here; `lint` can't inspect external plugin contents beyond the marketplace entry.
- **Several teams, several marketplaces:** each team keeps its own marketplace repo; one central catalog build lists multiple `marketplaces`.
- **Public marketplaces:** nothing prevents a public data repo; then R4 applies to the data as well.

## Open questions
1. ~~Sidecar versus in-entry metadata~~: **decided, sidecar per plugin** (single-file mode stays available for small registries).
2. ~~Where committed bundles live~~: **decided, a separate `bundles/` directory** (makes "generated" obvious).
3. Whether the catalog should also commit a human-readable index (for example `CATALOG.md`) so the repo is browsable without the site.
4. How to treat plugins that bundle MCP servers or hooks in `lint`: an extra review path may be warranted because they run code.
5. Release cadence and naming for repo tags that consumers pin.

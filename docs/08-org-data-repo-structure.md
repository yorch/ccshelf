# 08. Structure of the org data repo (profiles and catalog)

This describes the **private repo an adopting organization keeps** (R5): its marketplace, profiles and catalog data. The public tool repo (this one) ships a starter template of it in `examples/org-data-repo/` once code starts. Everything here is a design proposal except where marked decided; names are provisional. The `cprof` commands shown don't exist yet (`cprof` is the chosen command name for the project `claude-profile`).

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
├── profiles/                            # hand-written profile manifests (see 04 for the format)
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
├── claude-profile.toml                  # org config: required fields, lint rules, catalog settings
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
| `bundles/profile-<name>/` | `cprof compile` | Yes (checked for drift) | Dependency-only plugins so a profile's plugins install natively. Must be in git for the marketplace to serve them. |
| `profiles/*.toml` | Humans | Yes | Profile manifests: plugins, standalone-skill off-lists, MCP, session defaults. |
| `mcp/registry.toml` | Humans | Yes | Named MCP server definitions. Security-sensitive: commands and env are code execution on developer machines. |
| `catalog/plugins/<name>.toml` | Plugin authors | Yes | Catalog-only metadata, one file per plugin (see below). |
| `catalog/taxonomy.toml` | Platform team | Yes | Allowed categories and tags, so the catalog's facets stay clean. |
| `claude-profile.toml` | Platform team | Yes | Which sidecar fields are required, lint severity, catalog title and the marketplace files that feed the catalog. Users configure their own profile sources in `~/.config/claude-profile/config.toml`; `cprof init` can write that from the data repo's URL (proposed). |
| `dist/`, `catalog.json` | CI | No | Built catalog. |

## Where catalog metadata lives: a sidecar per plugin (decided 2026-10-06)
The earlier convention put `owner`, `status`, `when_to_use`, etc. into each marketplace entry's free-form `metadata`. That has a problem: with ~50 plugins in one `marketplace.json`, **`CODEOWNERS` can't route review per entry**, so the decided "authors write, platform team reviews" model can't be enforced by file ownership. A sidecar file per plugin fixes this: a PR that touches `catalog/plugins/*.toml` requires platform review, while plugin source stays with the author's team.

- `marketplace.json` keeps only what Claude Code reads (plus `category` and `tags`, which are native; whether the `/plugin` UI searches them is unverified).
- The catalog is **derived from** `marketplace.json` + sidecars + git data (last change, contributors). Nothing is duplicated into `metadata`.
- Plugins hosted outside this org data repo get a sidecar too; the sidecar directory is uniform, not colocated with plugin source.
- **Small registries** (for example under 20 plugins) can opt into **single-file mode** (`claude-profile.toml`: `metadata_source = "marketplace"`), reading the same fields from each entry's `metadata` object, so one file is enough.

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
`cprof compile` writes `bundles/profile-<name>/.claude-plugin/plugin.json` (name, version, `dependencies`) from `profiles/<name>.toml`. Because marketplaces serve files from git, these are committed; CI runs `cprof compile --check` and fails the PR if a committed bundle differs from what would be generated. The `marketplace.json` entry for each bundle (name, `source: ./bundles/profile-frontend`, category `profile`) is written by hand once; `lint` checks that every profile has an entry and every bundle entry has a profile. (Alternative: generate those entries into `marketplace.json`; rejected for now because mixing generated and hand-written entries in one JSON file invites merge conflicts.)

## Org config (`claude-profile.toml`)
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
/claude-profile.toml               @acme/platform
/bundles/                          @acme/platform

# each team owns its plugin source
/plugins/design-kit/               @acme/web
/plugins/sre-kit/                  @acme/sre
```
`cprof lint` can check that every `plugins/<name>/` folder has a `CODEOWNERS` line and that the sidecar `owner` matches it. Note that listing several owners on one `CODEOWNERS` line requires approval from any one of them; requiring both teams needs branch-protection rules or rulesets.

## CI workflows (thin wrappers around the pinned tool)
| Workflow | Trigger | Steps |
|---|---|---|
| `validate.yml` | pull request | `claude plugin validate .`; `cprof lint`; `cprof compile --check`; `cprof catalog build` and upload the preview as an artifact |
| `tag-plugins.yml` | merge to main | for each plugin whose version changed, create tag `<plugin>--v<version>` (`claude plugin tag`); needs tag-write permission |
| `catalog.yml` | merge to main | `cprof catalog build`; publish to the org's chosen host (Pages or any static host) |
| `release.yml` | manual or scheduled | create a repo tag (for example `v2026.10.1`); consumers pin profile sources to it |

All steps call the same pinned binary from the public tool repo (R2, R5): `uses: <owner>/claude-profile/action@<pin>` or a pinned release download.

## Consumption
- **Plugins:** users add the marketplace natively (`extraKnownMarketplaces` or `/plugin marketplace add <git URL>`), and bundles install profile plugin sets in one step.
- **Profiles:** users configure a `git` source pointing at this org data repo at a pinned tag, or a local clone as a `dir` source (see 04, "Profile sources and sharing"). The `plugin` source (profiles shipped inside a plugin) comes later.
- **Catalog:** the published site, and `cprof search` or `cprof doctor` reading the same data locally.

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

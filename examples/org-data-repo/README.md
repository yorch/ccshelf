# Acme Claude marketplace (starter template)

A complete, **fictional** org data repo for [ccshelf](https://github.com/ccshelf/ccshelf): a Claude Code marketplace, profiles, catalog metadata and CI for a made-up organization called Acme. Copy it to start your own. It also serves as a test fixture for the tool, so everything in it must stay valid.

Everything here is invented: the `acme` org, the teams `@acme/platform`, `@acme/web`, `@acme/sre` and `@acme/seo`, the `*.example` hosts, the MCP packages and the partner plugin. There are no built-in default profiles in ccshelf; the profiles here only show the format.

## Adopt it

1. **Copy** this directory into a new private repository (GitHub.com, GHE Cloud or GHE Server).
2. **Rename `acme`**: the marketplace `name` in `.claude-plugin/marketplace.json`, every `name@acme` plugin id in `profiles/*.toml` and `ccshelf.toml`, the teams in `.github/CODEOWNERS` and in `owner` fields, the title in `ccshelf.toml`, and the `*.example` URLs. Keep the marketplace name and the `@<marketplace>` suffix consistent.
3. **Replace the example plugins** with your own (see `docs/CONTRIBUTING.md`) and delete the ones you do not need, with their sidecars, profile references and bundle entries.
4. **Pin the tool.** The workflows call `ccshelf/ccshelf/action@0000000000000000000000000000000000000000`, an all-zero placeholder. Replace it in `validate.yml` and `catalog.yml` with the **full 40-character commit SHA** of the ccshelf release you chose (keep the tag in a comment), set `version:` to the same release, and set the repository variable `CCSHELF_SHA256_LINUX_AMD64` to the archive hash from that release's `checksums.txt` (archive names have no leading `v`: `ccshelf_0.1.0_linux_amd64.tar.gz`). Pick an action commit that already contains the `action/pins.txt` line for your version and the binary is pinned by the commit alone. Without the variable and a pins line the action verifies the cosign signature instead, which needs `cosign` on the runner and network access to the Sigstore trusted root (it is not offline). See `action/README.md` in the tool repo, including the GHE Server mirror options.
5. **Configure the users' sources.** Each developer adds this repo as a `git` profile source in `~/.config/ccshelf/config.toml`, pinned to a tag created by `release.yml`, and adds the marketplace natively in Claude Code. The tool itself never learns about your org.
6. **Protect the repo**: branch protection or a ruleset that requires code-owner review and at least two approvals, tag protection for `v*` (block updates and deletion), and the `github-pages` environment limited to `main`. `.github/` is owned by the platform team so plugin teams cannot edit workflows. Do not restrict tag creation unless `release.yml` creates the tag with a GitHub App token and the App is on the ruleset's bypass list: the built-in `github-actions` identity probably cannot be put there (unverified), so a restricted ruleset would reject the tag. Scheduled runs of that workflow work on the default branch only.

## Layout

```
.claude-plugin/marketplace.json    native marketplace; hand-written
plugins/<name>/                    plugin source: .claude-plugin/plugin.json, skills/, hooks/, .mcp.json, README.md
bundles/profile-<name>/            GENERATED dependency-only plugins, one per bundled profile
profiles/*.toml                    profile manifests (closed schema)
mcp/registry.toml                  named MCP server definitions that profiles reference
prompts/frontend.md                text a profile appends to the system prompt
catalog/plugins/<name>.toml        one sidecar per plugin: catalog-only metadata
catalog/taxonomy.toml              allowed categories and tags
ccshelf.toml                       org configuration: lint rules, catalog settings, protected plugins
.github/                           CODEOWNERS, pull request template, workflows
docs/CONTRIBUTING.md               how to add a plugin
```

The example set: `design-kit` (web), `sre-kit` (SRE, with a hook), `seo-tools` (SEO, with an `.mcp.json`), `release-notes` (platform, **deprecated**, superseded by `docs-writer`), `docs-writer` (platform), `audit-logger` (platform, **protected**), `partner-linter` (external git source, no folder here) and the generated bundles `profile-frontend`, `profile-sre` and `profile-seo`.

`audit-logger` is listed under `[protect]` in `ccshelf.toml`, so profiles can never mask it. Audit controls must not depend on what a profile proposes in a pull request.

## Generated and hand-written

| Path | Written by | Committed | Checked by |
|---|---|---|---|
| `.claude-plugin/marketplace.json` (including the `profile-*` entries) | Humans | Yes | `ccshelf lint`, `claude plugin validate` |
| `plugins/<name>/` | Plugin authors | Yes | `ccshelf lint` |
| `profiles/*.toml`, `mcp/registry.toml`, `prompts/*.md` | Humans (platform-reviewed) | Yes | `ccshelf lint` |
| `catalog/plugins/*.toml`, `catalog/taxonomy.toml`, `ccshelf.toml` | Humans (platform-reviewed) | Yes | `ccshelf lint` |
| `bundles/profile-<name>/.claude-plugin/plugin.json` | `ccshelf compile` | Yes | `ccshelf compile --check` (drift) |
| `dist/catalog` (site and `catalog.json`) | CI | No | built on every pull request, published on merge |

Profiles with no plugins get no bundle (the compiler skips them): `base` is an abstract parent with no plugins, so it has none. Bundles exist for `frontend`, `sre` and `seo`.

A bare name in a bundle's `dependencies` resolves in the bundle's own marketplace. A plugin from another marketplace must be written as an object, `{"name": "...", "marketplace": "..."}`, and `marketplace.json` must list that marketplace in `allowCrossMarketplaceDependenciesOn`. The three bundles here only depend on `acme` plugins, so they need neither.

`.gitattributes` forces LF for JSON, TOML, Markdown and YAML: the drift check compares bytes, so CRLF from a Windows `autocrlf` clone would otherwise count as drift.

### Bundle file format

`ccshelf compile` writes, and `compile --check` compares byte for byte, a file with exactly these properties:

- Two keys only, `dependencies` and `name`, in that (sorted) order. No `version` (the commit SHA is the version), no description.
- `name` is `profile-<profile name>`.
- `dependencies` is the sorted, de-duplicated list of the plugin names in the profile's resolved `plugins.include` (the union over `extends`), without the `@<marketplace>` suffix, which is the marketplace the bundle itself is served from.
- JSON with 2-space indentation, one array element per line, UTF-8, LF line endings and a single trailing newline.

For `profile-sre` that is:

```json
{
  "dependencies": [
    "partner-linter",
    "sre-kit"
  ],
  "name": "profile-sre"
}
```

### Conventions in this example

- Profile `append_system_prompt_file` paths are relative to the **repository root** (`prompts/frontend.md`) and must stay inside it.
- The MCP registry has one `[servers.<name>]` table per server (the schema is closed: there is no `description` key, use a comment) with `type` (`stdio` or `http`), and either `command` and `args` or `url`; `[servers.<name>.windows]` overrides `command` and `args` on Windows (`cmd /c npx`).
- `ccshelf.toml` holds `platform_owners` under `[lint]` and the protected plugin ids under `[protect] plugins`.
- Generated bundles have no sidecar: their owner and status come from the profile. Plugins from external repositories (`partner-linter`) get a sidecar like any other.
- `CODEOWNERS` uses last-match-wins: a catch-all `* @acme/platform` first, then the team rules, then the platform rules for hooks, `.mcp.json` and plugin manifests (`/plugins/*/.claude-plugin/`, last, because a manifest can declare hooks and MCP servers inline). `ccshelf lint` (CAT042) errors when a plugin that declares hooks, MCP or LSP servers inline in `plugin.json`, in a declared config path or in `.lsp.json` is not owned by a `platform_owners` entry, and warns about scripts those configs run through `${CLAUDE_PLUGIN_ROOT}`; keep the last rule so a plugin team cannot add them without platform review. CAT045 checks the same for workflows, `CODEOWNERS`, `ccshelf.toml`, profiles, bundles, the catalog and the MCP registry.
- `partner-linter` is pinned with both `ref` and a `sha`. The 40-hex value in this template is fictional but well formed; replace it with the real commit.
- Plugin repository tags `<plugin>--v<version>` are not created here: there is no `tag-plugins.yml` until you use dependency version ranges.

## GitHub Enterprise Server

The workflows are written for the lowest common denominator.

- **Runners.** Set the repository variable `RUNNER_LABEL` to your runner label; it defaults to `ubuntu-latest`.
- **Actions.** Mirror every action the workflows use (`actions/checkout`, `actions/upload-artifact`, `actions/upload-pages-artifact`, `actions/deploy-pages` and the ccshelf action) onto the instance with [`actions-sync`](https://github.com/actions/actions-sync), or enable GitHub Connect, and keep the SHA pins (re-pin them to the mirrored commits).
- **`upload-artifact`.** GHES versions before 3.13 only support `actions/upload-artifact` v3.2.2 (the artifact backend differs). Either pin that mirrored version, or set the variable `UPLOAD_PREVIEW` to `false` to skip the optional preview upload in `validate.yml`.
- **Pages.** `catalog.yml` publishes with GitHub Pages; where Pages or `deploy-pages` is unavailable, replace the two Pages steps with an upload of `dist/catalog` to any internal static host.
- **Release assets.** Mirror the ccshelf release assets (archive, `checksums.txt`, `checksums.txt.sigstore.json`, under a `v<version>/` directory) to an internal URL and pass it as `base-url:` to the ccshelf action. Pin `sha256` or use a `pins.txt` commit so no Sigstore access is needed.
- **Claude validation.** Set the variable `CLAUDE_VALIDATE` to `true` to run `claude plugin validate .` in `validate.yml`; GHES runners may not be able to install Claude Code, which is why it is off by default.

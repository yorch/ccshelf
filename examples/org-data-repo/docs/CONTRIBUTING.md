# Contributing a plugin

How to add a plugin to this marketplace. Five steps.

1. **Create the plugin.** Add `plugins/<name>/` with `.claude-plugin/plugin.json` (`name`, `version`, `description`, `author`), a `README.md`, and your `skills/<skill>/SKILL.md` files (each starts with `name` and `description` frontmatter). Use kebab-case names. If the plugin has hooks or an `.mcp.json`, say in the README what they run and why: the platform team reviews them because they execute on developer machines. Do not put hooks or MCP servers inline in `plugin.json`; use `hooks/hooks.json` and `.mcp.json` so ownership rules apply.
   Plugins hosted in another repository skip this step and use a git `source` in step 2.
2. **List it in `.claude-plugin/marketplace.json`.** Add an entry with `name`, `source` (`./plugins/<name>`), a description of at least 30 characters, a `category` and `tags` that exist in `catalog/taxonomy.toml`, and `author`. Optionally add a `relevance` block so Claude Code suggests the plugin in the right directories.
3. **Write the sidecar `catalog/plugins/<name>.toml`.** Required: `owner` (your team) and `status` (`active`, `experimental` or `deprecated`). Strongly recommended: `when_to_use`, `avoid_when`, `overlaps_with` (plugins people might confuse yours with), `review_by` (a date within 180 days), `support` and `docs`. A deprecated plugin also needs `superseded_by`.
4. **Add ownership.** Add `/plugins/<name>/ @acme/<your-team>` to `.github/CODEOWNERS`, **above** the platform rules for `hooks/` and `.mcp.json` (the last matching line wins). The sidecar `owner` must match.
5. **Open a pull request.** CI runs `ccshelf lint`, `ccshelf compile --check` and a catalog preview. Fix what they report, then request review. The platform team reviews the marketplace entry, the sidecar and any hooks or MCP configuration; your team reviews the plugin source.

To make the plugin part of a role, add it to the `plugins.include` list of the relevant profile in `profiles/` and run `ccshelf compile` to regenerate the bundles, then commit the result.

To retire a plugin, set `status = "deprecated"` and `superseded_by` in its sidecar, note the replacement in the marketplace description, and remove it from profiles. Delete it after the grace period.

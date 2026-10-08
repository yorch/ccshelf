# Example workflows

Illustrative only. Nothing is built yet: the command is `ccshelf`, the outputs are mockups, and plugin names are fictional (`@official` stands for the official marketplace) of the intended behavior. Behavior marked *(tested)* was confirmed in Stage 0 (see [stage0.md](../research/stage0.md)); the rest is design intent. The manifest fields used below are explained in [profiles.md](profiles.md).

## 1. Daily use: two terminals, two profiles
You work on a React app in the morning and debug production in the afternoon, and sometimes both at once.

```
# terminal 1
$ ccshelf run frontend
profile: frontend (extends base)  plugins: 6 on / 10 masked  mcp: figma  connectors: none
claude> ...

# terminal 2, at the same time
$ ccshelf run sre
profile: sre (extends base)       plugins: 6 on / 10 masked  mcp: pagerduty, grafana
claude> ...
```
- Each run writes a generated settings file (content-addressed, in the launcher's own cache dir, so concurrent runs never collide) and starts `claude --settings <file>`, passing back its exit code. The same on macOS, Linux and Windows by design (only macOS has been tested). Nothing shared (`~/.claude/settings.json`, `~/.claude.json`, plugin cache) is modified, so the two terminals can't affect each other. *(masking and parallel runs tested)*
- Plugins not in the profile are masked with `enabledPlugins:false`. The list comes from `claude plugin list --json` (cached for up to five minutes and re-read when installed plugins or managed settings change), so a plugin you installed yesterday doesn't leak into the profile. *(masking tested on macOS)*
- Extra arguments pass through: `ccshelf run sre -- --model opus`.

## 2. See exactly what will happen before running
```
$ ccshelf dry-run frontend
claude --settings ~/.cache/ccshelf/settings-9f3a1c27.json --mcp-config ~/.cache/ccshelf/mcp-5be02d41.json

$ ccshelf show frontend
resolved from: base -> frontend
plugins on  (6): design-kit@acme, playwright@acme, frontend-design@official, ...
plugins off (10): sre-kit@acme, seo-tools@acme, ...
skills off  (2): legacy-helper, old-notes            # standalone skills only
mcp: figma    claude.ai connectors: none
est. context saved vs. no profile: ~2.1k tokens, 41 fewer tools   # illustrative; Stage 0 measured ~2.4k tokens for dropping the whole user layer and ~0.5k for masking 14 skills
```
`dry-run` prints the exact command so you can trust it, debug it, or copy it into a shell alias. `show` prints the resolved closure including parents. Token estimates are rough: Stage 0 measured small savings (~9%), so the pitch is clutter and routing, not tokens.

## 3. Make a personal profile that builds on an org profile
```
$ ccshelf new my-frontend --from frontend
created ~/.config/ccshelf/profiles/my-frontend.toml
$ ccshelf edit my-frontend          # add a plugin, drop one, set model = "opus"
$ ccshelf diff frontend my-frontend
+ plugins.include: my-experiments@me
- plugins.include: playwright@acme
$ ccshelf run my-frontend
```
Personal profiles live outside the repo and can `extends` org profiles. A `plugins.exclude` beats a plugin include and a `skills.off` beats `skills.name_only`, at any level of the chain, so a child cannot re-include what a parent excluded.

## 4. New hire onboarding
1. They open the internal catalog page (linked from the README) and pick the role: "Backend engineer".
2. The page shows the `backend` profile, its plugins with owner and status, and `when_to_use` hints.
3. They install the role bundle once through native `/plugin` (the bundle plugin `profile-backend` pulls in its dependencies).
4. They run `ccshelf run backend`. If a listed plugin isn't installed, the launcher says which one and prints the install command; it doesn't install anything silently:
   ```
   $ ccshelf run backend
   missing: audit-kit@acme  ->  /plugin install audit-kit@acme
   ```

## 5. "What should I use for X?"
```
$ ccshelf search "incident postmortem"
sre-kit@acme        active   owner @sre        "Runbooks, incident timeline, postmortem templates"
postmortem-lite@acme deprecated -> sre-kit    (superseded_by)
$ ccshelf recommend            # rule-based: cwd, files, relevance signals; no LLM
In ./infra (terraform files found): sre, platform
```

`search` and `recommend` read the org repo in the current directory (or `--root`); otherwise they read, offline, the commit of the configured org git source that the trust lockfile pins (or the newest verified cached checkout) and say so. `doctor --policy` works without an org repo and then checks only the policy.
The same data renders on the catalog page: facets by category/tag/team/status, an overlap view built from `overlaps_with`, and a "new or changed since last tag" list.

## 6. Publishing a plugin to the internal marketplace (platform owner)
1. Add the plugin to `marketplace.json` with `author`, `category`, `tags`, and create its sidecar `catalog/plugins/<name>.toml` with `owner`, `status`, `when_to_use`, optionally `overlaps_with`. The sidecar needs platform review via `CODEOWNERS`.
2. Open a PR. CI runs `ccshelf lint` (plus `claude plugin validate`). Missing `owner` or `status` fails the build with a clear message:
   ```
   FAIL  catalog/plugins/x.toml: owner missing
   ```
3. On merge, CI runs `ccshelf compile` (regenerates the `profile-*` bundle plugins) and `ccshelf catalog build` (regenerates the static site and `catalog.json`).
4. Optionally add a `relevance` block so Claude Code itself suggests the plugin in the right directories.

## 7. Retiring a plugin
1. In the plugin's sidecar, set `status = "deprecated"` and `superseded_by = "new-plugin"`.
2. CI flags every profile that still includes it (`ccshelf doctor`) so owners can migrate.
3. The catalog shows a deprecated badge and the replacement. Runs of an affected profile print a warning (`run`, `show` and `dry-run` warn once per deprecated plugin, with the replacement; sidecars are read from the org repo or from the pinned git source).
4. After the grace period, remove it from `marketplace.json`, using the native `renames` mapping where applicable.

## 8. Housekeeping with `doctor`
```
$ ccshelf doctor
overlap:      sre-kit@acme and ops-helper@acme share 9 of 12 tags
unused:       seo-tools@acme not active in any profile and 0 skill_activated events in 30 days
deprecated:   postmortem-lite@acme still in profiles: sre
stale owners: 3 plugins have no review_by in the last 180 days
policy:       none detected on this machine
```
Usage counts need OTel (`OTEL_LOG_TOOL_DETAILS=1` for real names) or the Enterprise Analytics API; without them that line is skipped.

## 9. Running under org policy
```
$ ccshelf run frontend
policy: disableSideloadFlags is set (managed). --mcp-config is blocked, so servers a profile would add are skipped.
  - plugin masking via --settings: allowed
  - claude.ai connectors and other MCP servers: hidden through settings keys (disableClaudeAiConnectors, deniedMcpServers)
  - managed force-enabled plugins cannot be masked: audit-logger@acme
profile applied with limits (on_blocked = "warn"). Use on_blocked = "fail" to refuse instead.
```
The launcher reports what policy prevents and never tries to work around it. This part is design intent: policy detection is untested, and Stage 0 found no managed policy on the test machine.

## 10. Shell shortcuts for speed
```
# bash / zsh
alias cf='ccshelf run frontend --'

# PowerShell
function cf { ccshelf run frontend -- @args }
```
`ccshelf shell-init <bash|zsh|fish|pwsh>` could print these for the profiles you use most (cmd would get `.cmd` shims). A statusline hook can show the active profile from `$CCSHELF_PROFILE`, which the launcher exports.

## 11. Sharing profiles centrally
Platform owner publishes, everyone else consumes.
1. The org data repo contains `profiles/*.toml`. CI runs `ccshelf lint` on every PR, and the platform team tags a release (for example `v2026.10.1`) when profiles change.
2. A user (or `ccshelf init` with the repo URL, proposed) adds the data repo as a `git` source pinned to that tag in `~/.config/ccshelf/config.toml`. A team could also ship a config for it in a project repo. (A `plugin` source, with profiles shipped inside a data-only plugin, is planned for a later release.)
3. `ccshelf ls` shows profiles from all sources, labeled by origin (personal, project, org). A personal profile with the same name wins.
4. When the pinned tag moves and a profile adds an MCP command or env value, the launcher shows the diff and asks before accepting: `ccshelf trust sre`. Accepted hashes are stored in a lockfile.
```
$ ccshelf ls
frontend   org       active   React, CSS and accessibility work
sre        org       active   Incident response and observability
my-sre     personal  active   (extends sre)

$ ccshelf run sre
sre changed since you last accepted it:
  + mcp.servers: pagerduty-ro (command: npx -y @acme/pagerduty-mcp)
accept? [y/N]
```
The launcher only loads org profiles from sources the org already trusts (an allowlisted marketplace, or a pinned git ref). See "Profile sources and sharing" in [profiles.md](profiles.md).

## 12. Interactive and scripted use of the same command
In a terminal, with no arguments, `ccshelf` offers a picker; with flags it never asks anything.
```
$ ccshelf
? Run which profile?  (type to filter)
> frontend   React, CSS and accessibility work
  sre        Incident response and observability
  seo        Content and analytics
Equivalent: ccshelf run frontend

$ ccshelf new sre-night
? Based on: sre
? Plugins to add (space to select): pagerduty-tools@acme, grafana-helper@acme
Equivalent: ccshelf new sre-night --from sre --plugin pagerduty-tools@acme --plugin grafana-helper@acme

# the same thing in a script or CI: no prompts, errors name what is missing
$ ccshelf new sre-night --from sre --plugin pagerduty-tools@acme --no-interactive
error: nothing to add; give --plugin or --skill-off (exit 2)
```
Trust is never auto-accepted: `ccshelf trust sre --accept <hash>` is the scripted form, and `--yes` does not apply to it.

## Not covered by any workflow yet
- Installing missing plugins automatically (deliberately not done).
- Switching profiles inside a running session (a profile is fixed at launch).
- Teams with different profiles per repo (the direnv approach can set a default profile per directory).

# 06. Example workflows

Illustrative only. Nothing is built yet: the command name `cprof` is a placeholder, and the outputs are mockups of the intended behavior. Behavior marked *(tested)* was confirmed in Stage 0 (see 05); the rest is design intent. The manifest fields used below are explained in 04.

## 1. Daily use: two terminals, two profiles
You work on a React app in the morning and debug production in the afternoon, and sometimes both at once.

```
# terminal 1
$ cprof run frontend
profile: frontend (extends base)  plugins: 6 on / 14 masked  mcp: figma  connectors: none
claude> ...

# terminal 2, at the same time
$ cprof run sre
profile: sre (extends base)       plugins: 5 on / 15 masked  mcp: pagerduty, grafana
claude> ...
```
- Each run writes a generated settings file (content-addressed, in the launcher's own cache dir, so concurrent runs never collide) and starts `claude --settings <file>`, passing back its exit code. The same on macOS, Linux and Windows. Nothing shared (`~/.claude/settings.json`, `~/.claude.json`, plugin cache) is modified, so the two terminals can't affect each other. *(masking and parallel runs tested)*
- Plugins not in the profile are masked with `enabledPlugins:false`. The list is regenerated from `claude plugin list --json` on every launch, so a plugin you installed yesterday doesn't leak into the profile. *(masking tested)*
- Extra arguments pass through: `cprof run sre -- --model opus`.

## 2. See exactly what will happen before running
```
$ cprof dry-run frontend
claude --settings /tmp/cprof/48213/settings.json --strict-mcp-config --mcp-config /tmp/cprof/48213/mcp.json

$ cprof show frontend
resolved from: base -> frontend
plugins on  (6): design-kit@acme, playwright@acme, frontend-design@official, ...
plugins off (14): sre-kit@acme, seo-tools@acme, ...
skills off  (2): legacy-helper, old-notes            # standalone skills only
mcp: figma    claude.ai connectors: none
est. context saved vs. no profile: ~2.1k tokens, 41 fewer tools
```
`dry-run` prints the exact command so you can trust it, debug it, or copy it into a shell alias. `show` prints the resolved closure including parents. Token estimates are rough: Stage 0 measured small savings (~9%), so the pitch is clutter and routing, not tokens.

## 3. Make a personal profile that builds on an org profile
```
$ cprof new my-frontend --from frontend
created ~/.config/claude-profile/profiles/my-frontend.toml
$ cprof edit my-frontend          # add a plugin, drop one, set model = "opus"
$ cprof diff frontend my-frontend
+ plugins.include: my-experiments@me
- plugins.include: playwright@acme
$ cprof run my-frontend
```
Personal profiles live outside the repo and can `extends` org profiles. Later `off` / `exclude` wins over an earlier include.

## 4. New hire onboarding
1. They open the internal catalog page (linked from the README) and pick the role: "Backend engineer".
2. The page shows the `backend` profile, its plugins with owner and status, and `when_to_use` hints.
3. They install the role bundle once through native `/plugin` (the bundle plugin `profile-backend` pulls in its dependencies).
4. They run `cprof run backend`. If a listed plugin isn't installed, the launcher says which one and prints the install command; it doesn't install anything silently.

## 5. "What should I use for X?"
```
$ cprof search "incident postmortem"
sre-kit@acme        active   owner @sre        "Runbooks, incident timeline, postmortem templates"
postmortem-lite@acme deprecated -> sre-kit    (superseded_by)
$ cprof recommend            # rule-based: cwd, files, relevance signals; no LLM
In ./infra (terraform files found): sre, platform
```
The same data renders on the catalog page: facets by category/tag/team/status, an overlap view built from `overlaps_with`, and a "new or changed since last tag" list.

## 6. Publishing a plugin to the internal marketplace (platform owner)
1. Add the plugin to `marketplace.json` with `author`, `category`, `tags`, and create its sidecar `catalog/plugins/<name>.toml` with `owner`, `status`, `when_to_use`, optionally `overlaps_with`. The sidecar needs platform review via `CODEOWNERS`.
2. Open a PR. CI runs `cprof lint` (plus `claude plugin validate`). Missing `owner` or `status` fails the build with a clear message.
3. On merge, CI runs `cprof compile` (regenerates the `profile-*` bundle plugins) and `cprof catalog build` (regenerates the static site and `catalog.json`).
4. Optionally add a `relevance` block so Claude Code itself suggests the plugin in the right directories.

## 7. Retiring a plugin
1. In the plugin's sidecar, set `status = "deprecated"` and `superseded_by = "new-plugin"`.
2. CI flags every profile that still includes it (`cprof doctor`) so owners can migrate.
3. The catalog shows a deprecated badge and the replacement. Runs of an affected profile print a warning.
4. After the grace period, remove it from `marketplace.json`, using the native `renames` mapping where applicable.

## 8. Housekeeping with `doctor`
```
$ cprof doctor
overlap:      sre-kit@acme and ops-helper@acme share 9 of 12 tags
unused:       seo-tools@acme not active in any profile and 0 skill_activated events in 30 days
deprecated:   postmortem-lite@acme still in profiles: sre
stale owners: 3 plugins have no reviewBy in the last 180 days
policy:       none detected on this machine
```
Usage counts need OTel (`OTEL_LOG_TOOL_DETAILS=1` for real names) or the Enterprise Analytics API; without them that line is skipped.

## 9. Running under org policy
```
$ cprof run frontend
policy: disableSideloadFlags is set (managed). --mcp-config is blocked.
  - plugin masking via --settings: allowed
  - claude.ai connectors: cannot be masked (needs --strict-mcp-config)
  - managed force-enabled plugins cannot be masked: audit-logger@acme
profile applied with limits (on_blocked = "warn"). Use on_blocked = "fail" to refuse instead.
```
The launcher reports what policy prevents and never tries to work around it. This part is design intent: policy detection is untested, and Stage 0 found no managed policy on the test machine.

## 10. Shell shortcuts for speed
```
# bash / zsh
alias cf='cprof run frontend --'

# PowerShell
function cf { cprof run frontend -- @args }
```
`cprof shell-init <bash|zsh|fish|pwsh>` could print these for the profiles you use most (cmd would get `.cmd` shims). A statusline hook can show the active profile from `$CPROF_PROFILE`, which the launcher exports.

## 11. Sharing profiles centrally
Platform owner publishes, everyone else consumes.
1. The marketplace repo contains `profiles/*.toml`. CI runs `cprof lint` and publishes them as a data-only plugin, `org-profiles@acme`.
2. A user installs it once through `/plugin` and adds it as a source in `~/.config/claude-profile/config.toml` (`type = "plugin"`). A team could also ship that config in a project repo.
3. `cprof ls` shows profiles from all sources, labeled by origin (personal, project, org). A personal profile with the same name wins.
4. When the plugin updates and a profile adds an MCP command or env value, the launcher shows the diff and asks before accepting: `cprof trust sre`. Accepted hashes are stored in a lockfile.
```
$ cprof ls
frontend   org       active   React, CSS and accessibility work
sre        org       active   Incident response and observability
my-sre     personal  active   (extends sre)

$ cprof run sre
sre changed since you last accepted it:
  + mcp.servers: pagerduty-ro (command: npx -y @acme/pagerduty-mcp)
accept? [y/N]
```
The launcher only loads org profiles from sources the org already trusts (an allowlisted marketplace, or a pinned git ref). See "Profile sources and sharing" in 04.

## Not covered by any workflow yet
- Installing missing plugins automatically (deliberately not done).
- Switching profiles inside a running session (a profile is fixed at launch).
- Teams with different profiles per repo (the direnv approach can set a default profile per directory).

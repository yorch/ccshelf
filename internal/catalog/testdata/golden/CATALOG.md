# Acme plugin catalog

ccshelf generates this file from the marketplace, the sidecar files and the plugin directories. Do not edit it by hand.

**5 plugins**: 3 active, 1 experimental, 1 deprecated. 2 need platform review (hooks or MCP servers).

## data

| Plugin | Description | Owner | Status | When to use | Docs |
|---|---|---|---|---|---|
| `data-tools` (needs platform review) | SQL review skills and a read-only warehouse MCP server for analysts. | `@acme/data` | experimental | reviewing SQL; exploring the warehouse |  |

## design

| Plugin | Description | Owner | Status | When to use | Docs |
|---|---|---|---|---|---|
| `design-kit` | Design review helpers: color palettes, CSS review and UI critique agents. | `@acme/web` | active | reviewing a design handoff; choosing a color palette | [docs](https://wiki.example.com/design-kit) |

## integrations

| Plugin | Description | Owner | Status | When to use | Docs |
|---|---|---|---|---|---|
| `figma-bridge` | Reads Figma frames and turns them into component stubs \(hosted in its own repository\). | `@acme/web` | active | turning Figma frames into component stubs | [docs](https://wiki.example.com/figma-bridge) |

## observability

| Plugin | Description | Owner | Status | When to use | Docs |
|---|---|---|---|---|---|
| `sre-kit` (needs platform review) | Incident response workflows and postmortem templates for on-call engineers. | `@acme/sre` | active | incident response; postmortems | [docs](https://wiki.example.com/sre-kit) |

## Deprecated

| Plugin | Use instead | Description |
|---|---|---|
| `ops-helper` | `sre-kit` | Older runbook helpers, replaced by sre-kit. |

## Overlapping plugins

Plugins a reader might confuse. Check the "when to use" and "avoid when" notes before picking one.

- `design-kit` overlaps with `figma-bridge`. Avoid when: backend-only changes
- `figma-bridge` overlaps with `design-kit`
- `ops-helper` overlaps with `sre-kit`
- `sre-kit` overlaps with `ops-helper`. Avoid when: routine deploy checks

## Needs platform review

These plugins ship code that runs on developers' machines.

- `data-tools`: MCP servers
- `sre-kit`: hooks

## Profiles

| Profile | Description | Owner | Status | When to use |
|---|---|---|---|---|
| `frontend` | Frontend engineering | `@acme/web` | active | building web UI |
| `sre` | Site reliability engineering | `@acme/sre` | active | on call |

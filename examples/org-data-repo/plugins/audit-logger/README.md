# Audit Logger

Records every tool call to the organization's audit pipeline.

## What it provides

hook `PostToolUse` (`hooks/log-tool-use.sh`)

## Notes

Owner: `@acme/platform`. This plugin is **protected**: `ccshelf.toml` lists it under `[protect]`, so `ccshelf` never masks it, whatever a profile says. Audit and compliance controls must not be switchable by a profile, which anyone can propose in a pull request. Changing it requires a `@acme/platform` review (the whole folder is owned by that team).

Part of the fictional Acme example data in the ccshelf starter template.

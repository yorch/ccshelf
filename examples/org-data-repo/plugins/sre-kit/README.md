# SRE Kit

Incident and postmortem helpers for on-call engineers, plus a hook that warns before running commands against a production context.

## What it provides

`incident-timeline`, `postmortem-draft`; hook `PreToolUse` on `Bash` (`hooks/check-prod-context.sh`)

## Notes

Owner: `@acme/sre`. The hook runs code on developer machines, so `/plugins/*/hooks/` is owned by `@acme/platform` in `.github/CODEOWNERS`.

Part of the fictional Acme example data in the ccshelf starter template.

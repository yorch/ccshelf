#!/usr/bin/env bash
# check-pins.sh: fail if any `uses:` in a workflow or composite action is not
# pinned to a full 40-character commit SHA (SR5). Local actions (./path) and
# docker:// references are handled: local ones are allowed, docker:// must be
# pinned by digest (@sha256:...).
#
# Usage: scripts/check-pins.sh [file ...]
# Default files: .github/workflows/*.yml, .github/workflows/*.yaml, action/action.yml
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

files=("$@")
if [ ${#files[@]} -eq 0 ]; then
  shopt -s nullglob
  files=(.github/workflows/*.yml .github/workflows/*.yaml action/action.yml action/action.yaml)
fi

status=0
checked=0
for f in "${files[@]}"; do
  [ -f "$f" ] || continue
  lineno=0
  while IFS= read -r line || [ -n "$line" ]; do
    lineno=$((lineno + 1))
    # Match `uses: value` (optionally `- uses:`), ignoring comment-only lines.
    case "$line" in
      *[[:space:]]uses:*|uses:*) ;;
      *) continue ;;
    esac
    stripped="${line#"${line%%[![:space:]]*}"}"
    case "$stripped" in '#'*) continue ;; esac
    ref="${line#*uses:}"
    ref="${ref%%#*}"
    ref="$(printf '%s' "$ref" | tr -d '"'"'"' ' | tr -d '[:space:]')"
    [ -n "$ref" ] || continue
    checked=$((checked + 1))
    case "$ref" in
      ./*) continue ;;
      docker://*@sha256:????????????????????????????????????????????????????????????????) continue ;;
      docker://*)
        echo "$f:$lineno: docker action is not pinned by digest: $ref" >&2
        status=1
        continue
        ;;
    esac
    sha="${ref##*@}"
    if [ "$sha" = "$ref" ] || ! printf '%s' "$sha" | grep -Eq '^[0-9a-f]{40}$'; then
      echo "$f:$lineno: not pinned to a full commit SHA: $ref" >&2
      status=1
      continue
    fi
    if ! printf '%s' "$line" | grep -Eq '#[[:space:]]*v?[0-9]+(\.[0-9]+)*'; then
      echo "$f:$lineno: pinned SHA lacks a trailing '# vX.Y.Z' comment: $ref" >&2
      status=1
    fi
  done <"$f"
done

if [ "$status" -eq 0 ]; then
  echo "check-pins: ok ($checked uses: lines checked)"
fi
exit "$status"

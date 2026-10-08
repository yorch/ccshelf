#!/usr/bin/env bash
# check-examples.sh: run a built ccshelf binary against examples/org-data-repo:
# lint, compile --check and catalog build into a temporary directory. Fails on
# any non-zero exit (an error finding makes ccshelf exit non-zero).
#
# Usage: scripts/check-examples.sh [path-to-ccshelf]
#   default binary: dist/ccshelf (or dist/ccshelf.exe)
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
root="$(pwd)"

bin="${1:-}"
if [ -z "$bin" ]; then
  for c in dist/ccshelf dist/ccshelf.exe; do
    if [ -x "$c" ]; then bin="$c"; break; fi
  done
fi
[ -n "$bin" ] && [ -x "$bin" ] || { echo "check-examples: ccshelf binary not found (run just build)" >&2; exit 1; }
case "$bin" in /*) ;; *) bin="$root/$bin" ;; esac

example="$root/examples/org-data-repo"
[ -d "$example" ] || { echo "check-examples: $example not found" >&2; exit 1; }

work="$(mktemp -d)"
trap '/bin/rm -rf "$work"' EXIT
# Hermetic: the tool must not read the caller's real configuration.
export HOME="$work/home" XDG_CONFIG_HOME="$work/xdg-config" XDG_CACHE_HOME="$work/xdg-cache"
export USERPROFILE="$HOME" APPDATA="$work/appdata" LOCALAPPDATA="$work/localappdata"
mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME" "$APPDATA" "$LOCALAPPDATA"

cd "$example"
echo "== ccshelf lint"
"$bin" lint
echo "== ccshelf compile --check"
"$bin" compile --check
echo "== ccshelf catalog build"
"$bin" catalog build --out "$work/catalog"
[ -n "$(ls -A "$work/catalog" 2>/dev/null)" ] || { echo "check-examples: catalog output is empty" >&2; exit 1; }
echo "check-examples: ok"

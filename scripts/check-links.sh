#!/usr/bin/env bash
# check-links.sh: verify that relative links and anchors-free file targets in
# Markdown files exist. No network access; external (http, https, mailto)
# links are not fetched. Needs only bash, grep and sed.
#
# Usage: scripts/check-links.sh [file ...]
# Default: every tracked-looking *.md under the repo root, excluding dist/.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

files=("$@")
if [ ${#files[@]} -eq 0 ]; then
  while IFS= read -r f; do files+=("$f"); done < <(find . -name '*.md' -not -path './dist/*' -not -path './.git/*' -not -path './node_modules/*' | sort)
fi

status=0
count=0
for f in "${files[@]}"; do
  [ -f "$f" ] || continue
  dir="$(dirname "$f")"
  in_fence=0
  lineno=0
  while IFS= read -r line || [ -n "$line" ]; do
    lineno=$((lineno + 1))
    case "$line" in
      '```'*|'~~~'*) in_fence=$((1 - in_fence)); continue ;;
    esac
    [ "$in_fence" -eq 0 ] || continue
    # Strip inline code spans so examples inside backticks are not checked.
    scan="$(printf '%s' "$line" | sed -E 's/`[^`]*`//g')"
    # Extract markdown link targets: ](target)
    while IFS= read -r target; do
      [ -n "$target" ] || continue
      target="${target%% *}" # drop optional "title"
      case "$target" in
        http://*|https://*|mailto:*|'#'*|'<'*) continue ;;
      esac
      path="${target%%#*}"
      path="${path%%\?*}"
      [ -n "$path" ] || continue
      count=$((count + 1))
      case "$path" in
        /*) resolved=".$path" ;;
        *) resolved="$dir/$path" ;;
      esac
      if [ ! -e "$resolved" ]; then
        echo "$f:$lineno: broken link: $target" >&2
        status=1
      fi
    done < <(printf '%s' "$scan" | grep -oE '\]\([^)]+\)' | sed -E 's/^\]\(//; s/\)$//' || true)
  done <"$f"
done

if [ "$status" -eq 0 ]; then
  echo "check-links: ok ($count relative links checked in ${#files[@]} files)"
fi
exit "$status"

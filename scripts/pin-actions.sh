#!/usr/bin/env bash
# pin-actions.sh: rewrite `uses: owner/repo@<tag>` lines to
# `uses: owner/repo@<40-hex sha> # <tag>` (SR5). Lines already pinned to a
# SHA are left alone. Requires only bash, awk and an authenticated `gh`
# (set GH_HOST for GitHub Enterprise Server).
#
# Usage: scripts/pin-actions.sh [file ...]
# Default files: .github/workflows/*.yml, .github/workflows/*.yaml, action/action.yml and
# examples/**/.github/workflows/*.yml
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

command -v gh >/dev/null 2>&1 || { echo "pin-actions: gh is required" >&2; exit 1; }

files=("$@")
if [ ${#files[@]} -eq 0 ]; then
  shopt -s nullglob
  files=(.github/workflows/*.yml .github/workflows/*.yaml action/action.yml action/action.yaml)
  while IFS= read -r f; do
    files+=("$f")
  done < <(find examples -path '*/.github/workflows/*' \( -name '*.yml' -o -name '*.yaml' \) -type f 2>/dev/null | sort)
fi

resolve() { # owner/repo ref -> sha
  local repo="$1" ref="$2" sha
  sha="$(gh api "repos/$repo/commits/$ref" --jq .sha)"
  if ! printf '%s' "$sha" | grep -Eq '^[0-9a-f]{40}$'; then
    echo "pin-actions: could not resolve $repo@$ref" >&2
    return 1
  fi
  printf '%s' "$sha"
}

for f in "${files[@]}"; do
  [ -f "$f" ] || continue
  tmp="$(mktemp)"
  changed=0
  while IFS= read -r line || [ -n "$line" ]; do
    out="$line"
    # indent, optional "- ", "uses:", spaces, owner/repo[/path]@ref, optional comment
    if [[ "$line" =~ ^([[:space:]]*(-[[:space:]]+)?uses:[[:space:]]+)([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)(/[^@[:space:]]*)?@([^[:space:]#]+)([[:space:]]*#.*)?$ ]]; then
      prefix="${BASH_REMATCH[1]}"
      repo="${BASH_REMATCH[3]}"
      subpath="${BASH_REMATCH[4]:-}"
      ref="${BASH_REMATCH[5]}"
      if ! [[ "$ref" =~ ^[0-9a-f]{40}$ ]]; then
        sha="$(resolve "$repo" "$ref")"
        out="${prefix}${repo}${subpath}@${sha} # ${ref}"
        changed=$((changed + 1))
        echo "pin-actions: $f: $repo$subpath@$ref -> ${sha:0:12}" >&2
      fi
    fi
    printf '%s\n' "$out" >>"$tmp"
  done <"$f"
  if [ "$changed" -gt 0 ]; then
    cat "$tmp" >"$f"
  fi
  /bin/rm -f "$tmp"
done
echo "pin-actions: done"

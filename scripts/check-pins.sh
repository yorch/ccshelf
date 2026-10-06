#!/usr/bin/env bash
# check-pins.sh: fail if any `uses:` in a workflow or composite action is not
# pinned to a full 40-character commit SHA (SR5), or if its trailing `# vX.Y.Z`
# comment does not resolve to that SHA. Local actions (./path) are allowed;
# docker:// references must be pinned by digest (@sha256:...).
#
# Block style (`- uses: a/b@sha # v1.2.3`) and flow style (`- { uses: a/b@sha, with: {} }`)
# are both handled.
#
# The comment-to-SHA check asks the GitHub API through `gh` (GH_HOST selects a GHES
# instance). It is skipped with a notice when `gh` is missing, the network is
# unavailable, or CHECK_PINS_OFFLINE=1. A comment that names only a major version
# (`# v4`) is checked as a warning, because major tags move.
#
# The one allowed placeholder is the all-zero SHA in files under examples/, where the
# starter template cannot know a released commit yet.
#
# Usage: scripts/check-pins.sh [file ...]
# Default files: .github/workflows/*.yml, .github/workflows/*.yaml, action/action.yml
#   and examples/**/.github/workflows/*.yml (and .yaml).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

files=("$@")
if [ ${#files[@]} -eq 0 ]; then
  shopt -s nullglob
  files=(.github/workflows/*.yml .github/workflows/*.yaml action/action.yml action/action.yaml)
  while IFS= read -r f; do
    files+=("$f")
  done < <(find examples -path '*/.github/workflows/*' \( -name '*.yml' -o -name '*.yaml' \) -type f 2>/dev/null | sort)
fi

ZERO_SHA=0000000000000000000000000000000000000000
status=0
checked=0
verified=0
warnings=0
online=1
if [ "${CHECK_PINS_OFFLINE:-0}" = "1" ] || ! command -v gh >/dev/null 2>&1; then
  online=0
fi
skip_notice_printed=0
cache_dir="$(mktemp -d)"
trap '/bin/rm -rf "$cache_dir"' EXIT

notice_offline() {
  if [ "$skip_notice_printed" -eq 0 ]; then
    echo "check-pins: notice: comment-to-SHA verification skipped (gh missing, offline or CHECK_PINS_OFFLINE=1)" >&2
    skip_notice_printed=1
  fi
}

# resolve owner/repo tag -> prints the commit SHA; returns 0 resolved, 1 not found, 2 unavailable.
resolve() {
  local repo="$1" tag="$2" key out err
  key="$cache_dir/$(printf '%s@%s' "$repo" "$tag" | tr '/@' '__')"
  if [ -f "$key" ]; then
    out="$(cat "$key")"
    case "$out" in
      NOTFOUND) return 1 ;;
      UNAVAILABLE) return 2 ;;
      *) printf '%s' "$out"; return 0 ;;
    esac
  fi
  if out="$(gh api "repos/$repo/commits/$tag" --jq .sha 2>"$cache_dir/err")"; then
    if printf '%s' "$out" | grep -Eq '^[0-9a-f]{40}$'; then
      printf '%s' "$out" >"$key"
      printf '%s' "$out"
      return 0
    fi
  fi
  err="$(cat "$cache_dir/err" 2>/dev/null || true)"
  case "$err" in
    *"404"* | *"Not Found"* | *"No commit found"*)
      printf 'NOTFOUND' >"$key"
      return 1
      ;;
  esac
  printf 'UNAVAILABLE' >"$key"
  return 2
}

for f in "${files[@]}"; do
  [ -f "$f" ] || continue
  lineno=0
  while IFS= read -r line || [ -n "$line" ]; do
    lineno=$((lineno + 1))
    # Match `uses:` anywhere in the line (block or flow style), ignoring comment-only lines.
    case "$line" in
      *uses:*) ;;
      *) continue ;;
    esac
    stripped="${line#"${line%%[![:space:]]*}"}"
    case "$stripped" in '#'*) continue ;; esac
    code="${line%%#*}" # drop the trailing comment
    case "$code" in *uses:*) ;; *) continue ;; esac
    ref="${code#*uses:}"
    ref="${ref%%,*}" # flow style: stop at the next key
    ref="${ref%%\}*}"
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
    if [ "$sha" = "$ZERO_SHA" ]; then
      case "$f" in
        examples/*) continue ;;
        *)
          echo "$f:$lineno: the all-zero placeholder SHA is only allowed in examples/: $ref" >&2
          status=1
          continue
          ;;
      esac
    fi
    comment=""
    if [[ "$line" =~ \#[[:space:]]*(v?[0-9]+(\.[0-9]+)*[0-9A-Za-z.+-]*) ]]; then
      comment="${BASH_REMATCH[1]}"
    fi
    if [ -z "$comment" ]; then
      echo "$f:$lineno: pinned SHA lacks a trailing '# vX.Y.Z' comment: $ref" >&2
      status=1
      continue
    fi
    # Verify that the comment names a tag that resolves to the pinned SHA.
    if [ "$online" -eq 0 ]; then
      notice_offline
      continue
    fi
    name="${ref%@*}"
    repo="$(printf '%s' "$name" | cut -d/ -f1-2)"
    rc=0
    got="$(resolve "$repo" "$comment")" || rc=$?
    case "$rc" in
      0)
        if [ "$got" = "$sha" ]; then
          verified=$((verified + 1))
        elif [[ "$comment" =~ ^v?[0-9]+(\.[0-9]+)?$ ]]; then
          echo "$f:$lineno: warning: '# $comment' (a moving major tag) now resolves to ${got:0:12}, not the pinned ${sha:0:12}" >&2
          warnings=$((warnings + 1))
        else
          echo "$f:$lineno: '# $comment' resolves to $got in $repo, but the pinned SHA is $sha" >&2
          status=1
        fi
        ;;
      1)
        echo "$f:$lineno: '# $comment' is not a tag or ref of $repo" >&2
        status=1
        ;;
      *)
        online=0
        notice_offline
        ;;
    esac
  done <"$f"
done

if [ "$status" -eq 0 ]; then
  echo "check-pins: ok ($checked uses: lines checked, $verified comments verified against the API, $warnings warnings)"
fi
exit "$status"

#!/usr/bin/env bash
# check-cover.sh: print per-package statement coverage from a Go coverage
# profile and fail when the total is below a threshold OR any single package is
# below the per-package floor (unless it is on the exceptions list).
#
# Usage: scripts/check-cover.sh [profile] [min-percent]
#   profile      default coverage.out
#   min-percent  total threshold; default $COVER_MIN or 70
# Environment:
#   COVER_PKG_MIN     per-package floor, default 70
#   COVER_EXCEPTIONS  file listing packages exempt from the floor, one import-path suffix
#                     per line ("#" comments allowed); default scripts/cover-exceptions.txt.
#                     Each entry must say why. The total gate still applies.
# With GITHUB_STEP_SUMMARY set, a Markdown table is appended to it.
set -euo pipefail

profile="${1:-coverage.out}"
min="${2:-${COVER_MIN:-70}}"
pkg_min="${COVER_PKG_MIN:-70}"
exceptions_file="${COVER_EXCEPTIONS:-$(dirname "${BASH_SOURCE[0]}")/cover-exceptions.txt}"

is_exception() { # pkg: true when the package (module-relative or full path) is listed
  [ -f "$exceptions_file" ] || return 1
  local line
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line%%#*}"
    line="$(printf '%s' "$line" | tr -d '[:space:]')"
    [ -n "$line" ] || continue
    case "$1" in "$line" | */"$line") return 0 ;; esac
  done <"$exceptions_file"
  return 1
}

[ -f "$profile" ] || { echo "check-cover: $profile not found" >&2; exit 1; }

report="$(awk '
  NR == 1 { next }
  {
    split($1, a, ":")
    file = a[1]
    n = split(file, parts, "/")
    pkg = parts[1]
    for (i = 2; i < n; i++) pkg = pkg "/" parts[i]
    stmts = $2
    total[pkg] += stmts
    all += stmts
    if ($3 > 0) { covered[pkg] += stmts; allcov += stmts }
  }
  END {
    for (p in total) printf "%s %d %d\n", p, covered[p], total[p]
    printf "TOTAL %d %d\n", allcov, all
  }' "$profile" | sort)"

summary_target="${GITHUB_STEP_SUMMARY:-}"
if [ -n "$summary_target" ]; then
  { echo "### Coverage"; echo; echo "| Package | Coverage |"; echo "|---|---|"; } >>"$summary_target"
fi

total_pct=0
below=""
while read -r pkg cov tot; do
  [ -n "$pkg" ] || continue
  if [ "$tot" -gt 0 ]; then
    pct="$(awk -v c="$cov" -v t="$tot" 'BEGIN { printf "%.1f", 100 * c / t }')"
  else
    pct="0.0"
  fi
  printf '%-60s %6s%%\n' "$pkg" "$pct"
  if [ -n "$summary_target" ]; then
    echo "| \`$pkg\` | $pct% |" >>"$summary_target"
  fi
  if [ "$pkg" = "TOTAL" ]; then
    total_pct="$pct"
  elif awk -v t="$pct" -v m="$pkg_min" 'BEGIN { exit !(t + 0 < m + 0) }' && ! is_exception "$pkg"; then
    below="$below $pkg(${pct}%)"
  fi
done <<<"$report"

if awk -v t="$total_pct" -v m="$min" 'BEGIN { exit !(t + 0 < m + 0) }'; then
  echo "check-cover: total ${total_pct}% is below the ${min}% threshold" >&2
  exit 1
fi
if [ -n "$below" ]; then
  echo "check-cover: packages below the ${pkg_min}% per-package floor:$below" >&2
  echo "check-cover: add tests, or list the package with a reason in $exceptions_file" >&2
  exit 1
fi
echo "check-cover: total ${total_pct}% meets the ${min}% threshold and every package meets ${pkg_min}% (or is excepted)"

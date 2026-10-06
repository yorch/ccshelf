#!/usr/bin/env bash
# check-cover.sh: print per-package statement coverage from a Go coverage
# profile and fail when the total is below a threshold.
#
# Usage: scripts/check-cover.sh [profile] [min-percent]
#   profile      default coverage.out
#   min-percent  default $COVER_MIN or 70
# With GITHUB_STEP_SUMMARY set, a Markdown table is appended to it.
set -euo pipefail

profile="${1:-coverage.out}"
min="${2:-${COVER_MIN:-70}}"

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
  if [ "$pkg" = "TOTAL" ]; then total_pct="$pct"; fi
done <<<"$report"

if awk -v t="$total_pct" -v m="$min" 'BEGIN { exit !(t + 0 < m + 0) }'; then
  echo "check-cover: total ${total_pct}% is below the ${min}% threshold" >&2
  exit 1
fi
echo "check-cover: total ${total_pct}% meets the ${min}% threshold"

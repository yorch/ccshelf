#!/usr/bin/env bash
# check-eol.sh: fail on CRLF line endings or a missing final newline. Looks at
# text files known to git (or, outside a git work tree, every file) with a
# known text extension; testdata/ is skipped because goldens may differ on purpose.
#
# Usage: scripts/check-eol.sh
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  list="$(git ls-files --cached --others --exclude-standard)"
else
  list="$(find . -type f -not -path './.git/*' -not -path './dist/*' | sed 's|^\./||')"
fi

status=0
while IFS= read -r f; do
  [ -f "$f" ] || continue
  case "$f" in
    *.go|*.md|*.yml|*.yaml|*.toml|*.json|*.sh|*.py|*.html|*.txt|Makefile|.gitignore|.gitattributes|.editorconfig|CODEOWNERS|LICENSE) ;;
    *) continue ;;
  esac
  case "$f" in testdata/*|*/testdata/*) continue ;; esac
  if grep -q $'\r' "$f"; then
    echo "$f: contains CR (CRLF) line endings" >&2
    status=1
  fi
  if [ -s "$f" ] && [ -n "$(tail -c1 "$f")" ]; then
    echo "$f: missing final newline" >&2
    status=1
  fi
done <<<"$list"

[ "$status" -ne 0 ] || echo "check-eol: ok"
exit "$status"

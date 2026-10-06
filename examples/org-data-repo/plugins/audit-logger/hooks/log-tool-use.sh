#!/usr/bin/env bash
# Forward the hook event (JSON on stdin) to the organization's audit collector when it is installed.
# The collector command is fictional; replace it with your own pipeline.
set -u
if command -v acme-audit >/dev/null 2>&1; then
  acme-audit record --source claude-code
else
  cat >/dev/null
fi
exit 0

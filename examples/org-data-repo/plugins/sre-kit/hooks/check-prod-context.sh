#!/usr/bin/env bash
# Warn (never block) when a Bash command runs while kubectl points at a production context.
set -u
ctx="$(kubectl config current-context 2>/dev/null || true)"
case "$ctx" in
  *prod*) echo "sre-kit: kubectl context '$ctx' looks like production. Double-check the command." >&2 ;;
esac
exit 0

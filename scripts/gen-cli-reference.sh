#!/usr/bin/env bash
# gen-cli-reference.sh: build ccshelf and generate docs/reference/cli.md from its real --help output.
#
# Usage:
#   scripts/gen-cli-reference.sh             print the Markdown to stdout
#   scripts/gen-cli-reference.sh --write     write docs/reference/cli.md
#   scripts/gen-cli-reference.sh --check     fail if the committed file differs (CI)
#
# Needs: bash, Go (see go.mod), python3. CCSHELF_BIN skips the build and uses the given binary.
# The binary is only asked for --help, in a throwaway HOME with an empty PATH: no claude is run and
# no real configuration is read (the Python script builds the child's environment from scratch).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
mode=${1:-print}
case "$mode" in
  print) flag=() ;;
  --write | --check) flag=("$mode") ;;
  *) echo "usage: $0 [--write|--check]" >&2; exit 2 ;;
esac

work=$(mktemp -d)
trap '/bin/rm -rf "$work"' EXIT
bin=${CCSHELF_BIN:-}
if [ -z "$bin" ]; then
  bin=$work/ccshelf
  go build -trimpath -o "$bin" ./cmd/ccshelf
fi
python3 -I scripts/gen_cli_reference.py --bin "$bin" ${flag[@]+"${flag[@]}"}

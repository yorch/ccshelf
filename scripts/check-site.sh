#!/usr/bin/env bash
# check-site.sh: static checks for the project website: no external URLs, links and fragments
# resolve, alt text, unique ids, required meta tags and a strict CSP, no inline handlers, page
# weight, and for the documentation pages a skip link, one h1, a navigation listing every page and a
# consistent search index. Python 3 standard library only; the logic is in check_site.py.
#
# The website is built (the docs pages are generated from the Markdown and are not committed), so by
# default this builds it into dist/site with scripts/build-site.sh, which validates the built output.
#
# Usage:
#   scripts/check-site.sh               build dist/site and validate it (SITE_URL overrides the address)
#   scripts/check-site.sh <dir>         validate an existing directory as it is
#   scripts/check-site.sh --built <dir> same, and require that no deploy placeholder is left
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
if [ $# -eq 0 ]; then
  exec bash scripts/build-site.sh dist/site "${SITE_URL:-https://site.example.test/ccshelf}"
fi
exec python3 -I scripts/check_site.py "$@"

#!/usr/bin/env bash
# check-site.sh: static checks for the project website (site/): no external URLs, links and
# fragments resolve, alt text, unique ids, required meta tags and a strict CSP, no inline
# handlers, page weight. Python 3 standard library only; the logic is in check_site.py.
#
# Usage: scripts/check-site.sh [site_dir]    (default: site)
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
exec python3 -I scripts/check_site.py "${1:-site}"

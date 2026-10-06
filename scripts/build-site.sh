#!/usr/bin/env bash
# build-site.sh: build the deployable website: copy site/ to an output directory, render the
# Markdown documentation into <out>/docs (scripts/build_docs.py) and fill in the deploy-time values.
#
#   scripts/build-site.sh <out_dir> <site_url> [--repo-url <url>]
#
# <site_url> is the public address of the site without a trailing slash, for example
# https://example.github.io/ccshelf (GitHub Pages: the base_url output of actions/configure-pages).
# It replaces the __SITE_URL__ placeholder (canonical, og:url, social image) and becomes the
# <base href> of 404.html, so the 404 page works on nested paths. --repo-url swaps the repository
# address declared in index.html (the href of id="repo") for the public home before publishing; it is
# replaced only where it starts an attribute value, never in visible text or in docs/search-index.js
# (build_docs.py refuses to write the address into the index).
# The committed site/ is left untouched, the generated pages are never committed, and the result is
# validated with check_site.py --built. The repository address of the docs pages comes from the same
# id="repo" link, so one --repo-url swap covers the home page and the docs.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

usage() { echo "usage: $0 <out_dir> <site_url> [--repo-url <url>]" >&2; exit 2; }
[ $# -ge 2 ] || usage
out=$1
site_url=${2%/}
shift 2
repo_url=""
if [ $# -gt 0 ]; then
  [ "$1" = "--repo-url" ] && [ $# -eq 2 ] || usage
  repo_url=${2%/}
fi

url_re='^https://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._-]+)*$'
[[ "$site_url" =~ $url_re ]] || { echo "build-site: site_url must be an https URL without query or fragment: $site_url" >&2; exit 2; }
if [ -n "$repo_url" ]; then
  [[ "$repo_url" =~ $url_re ]] || { echo "build-site: repo-url must be a plain https URL: $repo_url" >&2; exit 2; }
fi
case "$out" in "" | / | . | .. | site | site/) echo "build-site: refusing out_dir '$out'" >&2; exit 2 ;; esac

/bin/rm -rf "$out"
mkdir -p "$out"
cp -R site/. "$out/"

old_repo=$(sed -n 's/.*<a id="repo" href="\([^"]*\)".*/\1/p' site/index.html | head -n 1)
[ -n "$old_repo" ] || { echo "build-site: no id=\"repo\" link in site/index.html" >&2; exit 1; }

# the repository address as a sed pattern (dots and the like are literal)
old_re=$(printf '%s' "$old_repo" | sed 's/[][\.*^$|\/&]/\\&/g')

python3 -I scripts/build_docs.py --out "$out"

while IFS= read -r f; do
  tmp=$f.tmp
  sed -e "s|__SITE_URL__|$site_url|g" \
      -e "s|<!--SITE_BASE-->|<base href=\"$site_url/\">|" "$f" >"$tmp"
  if [ -n "$repo_url" ]; then
    # only inside an attribute value (href="...", content="...", data-...="...") and in the visible
    # `git clone <repo>` command of the install steps; never in other text
    sed -e "s|=\"$old_re|=\"$repo_url|g" -e "s|git clone $old_re|git clone $repo_url|g" "$tmp" >"$f"
    /bin/rm -f "$tmp"
  else
    mv "$tmp" "$f"
  fi
done < <(find "$out" -name '*.html' | sort)
python3 -I scripts/check_site.py --built "$out"

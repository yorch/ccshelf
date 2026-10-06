#!/usr/bin/env bash
# build-site.sh: copy site/ to an output directory and fill in the deploy-time values.
#
#   scripts/build-site.sh <out_dir> <site_url> [--repo-url <url>]
#
# <site_url> is the public address of the site without a trailing slash, for example
# https://example.github.io/ccshelf (GitHub Pages: the base_url output of actions/configure-pages).
# It replaces the __SITE_URL__ placeholder (canonical, og:url, social image) and becomes the
# <base href> of 404.html, so the 404 page works on nested paths. --repo-url swaps the repository
# address declared in index.html (the href of id="repo") for the public home before publishing.
# The committed site/ is left untouched, and the result is validated with check_site.py.
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

for f in "$out"/*.html; do
  tmp=$f.tmp
  sed -e "s|__SITE_URL__|$site_url|g" \
      -e "s|<!--SITE_BASE-->|<base href=\"$site_url/\">|" "$f" >"$tmp"
  if [ -n "$repo_url" ]; then
    sed -e "s|$old_repo|$repo_url|g" "$tmp" >"$f"
    /bin/rm -f "$tmp"
  else
    mv "$tmp" "$f"
  fi
done
python3 -I scripts/check_site.py --built "$out"

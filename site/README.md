# ccshelf website

A static site: `index.html`, `404.html`, `assets/`. No build step, no framework, no external requests (a strict Content-Security-Policy `<meta>` allows only the site itself). It works from `file://` and from a project subpath such as `/ccshelf/`, because every URL is relative.

## Preview

```sh
python3 -m http.server 8000 --directory site    # then open http://localhost:8000/
```

Or open `site/index.html` directly.

## Check

```sh
make site-check              # the validator's own tests, then the validator on site/
bash scripts/check-site.sh   # only the validator
```

`scripts/check_site.py` fails on external URLs, broken links and fragments, missing alt text or image sizes, duplicate ids, a missing or weak CSP, inline handlers and more. CI runs it on every pull request.

## The loadout demo is captured output

`assets/demo-data.js` holds the commands and settings files that the hero demo shows. They are printed by the real `ccshelf dry-run` against the fictional example org in `examples/org-data-repo`, with the repository's fake `claude` (never the real one), in a throwaway HOME. The static `frontend` example in `index.html` mirrors it for visitors without JavaScript. After a change to the generator, the example profiles or the settings schema, regenerate:

```sh
scripts/regen-site-demo.sh --write   # rewrites assets/demo-data.js
scripts/regen-site-demo.sh --check   # fails if it is stale
```

then update the static `frontend` block and the commit label in `index.html` to match.

## Addresses

- **Repository link.** The href of `<a id="repo">` in `index.html` is `REPO_URL`; every other repository link starts with it (the validator enforces that). Today it is the private working repository. Change it to the public home before publishing: edit it in one find-and-replace in `index.html`, or pass `--repo-url` to `scripts/build-site.sh` at deploy time.
- **Site address.** `canonical`, `og:url` and the social image use the placeholder `__SITE_URL__`, and `404.html` has a `<!--SITE_BASE-->` marker. `scripts/build-site.sh <out> <site_url>` fills them in on a copy and validates the result; the committed files stay valid for `file://`.

## Publishing (manual, gated)

The site is **not** published automatically (decision D-34). `.github/workflows/pages.yml` runs only on `workflow_dispatch`. One-time setup:

1. Settle the gates: employer approval to open-source (O-11), the public GitHub home (O-08) and the module path (D-25).
2. Enable Pages: Settings > Pages > Source "GitHub Actions". For a private repository this needs a plan that includes Pages for private repositories, and the site is then visible only to people with access.
3. Run the `pages` workflow from the Actions tab. Pass `repo_url` if `site/index.html` still points at the working repository.

To deploy on every change to `site/` later, restore the `push` trigger shown in the comment at the top of `pages.yml`.

# ccshelf website

A static site: `index.html`, `404.html`, `assets/`, plus a `/docs` section that is **generated** from the repository's Markdown at build time (`scripts/build_docs.py`, Python standard library only, no static-site generator). No framework, no external requests (a strict Content-Security-Policy `<meta>` allows only the site itself). It works from `file://` and from a project subpath such as `/ccshelf/`, because every URL is relative.

The committed `site/` is only the hand-written part. Its links to `docs/` resolve in the built output, so build before you preview or check.

## Build and preview

```sh
make site-build                                   # builds dist/site (gitignored) and validates it
python3 -m http.server 8000 --directory dist/site # then open http://localhost:8000/
```

Or open `dist/site/index.html` directly. `make docs-site` renders only the docs pages into `dist/site/docs` (no validation), for quick iteration on the Markdown or on `assets/docs.css`.

## The documentation section

`scripts/build_docs.py` renders `docs/README.md`, `DECISIONS.md`, `design/*.md`, `research/*.md` and the generated `reference/cli.md`: a grouped sidebar (a menu button on narrow screens), a per-page table of contents, heading anchors, previous and next links, copy buttons on code blocks, tables that scroll sideways, the confidence markers as labelled marks, and a search box that reads the generated `docs/search-index.js` (no network; it works from `file://`). Without JavaScript the sidebar and every link still work. Styles are in `assets/docs.css` (built on the tokens of `site.css`), behavior in `assets/docs.js` and `assets/docs-search.js`.

- **Links.** Links between notes become links between the generated pages. A link to another repository file or folder becomes `REPO_URL/blob/main/<path>` (`tree/` for a folder) and fails the build if the target is missing. External links are live only for the repository and `code.claude.com`; any other address in the research notes is shown as text.
- **Not rendered.** The three interactive widgets of `docs/report.html` need a script, which the CSP forbids; the page shows a note that points to the report in the repository. `report.html` itself is not copied.
- **New notes.** Add a new `docs/**/*.md` file to `PAGES` in `scripts/build_docs.py`, or the build fails.
- **The command reference** (`docs/reference/cli.md`) is generated from the real binary: `scripts/gen-cli-reference.sh --write` regenerates it and CI runs `--check`.

## Check

```sh
make site-check              # unit tests of the validator and the docs builder, then build dist/site and validate it
bash scripts/check-site.sh   # build dist/site and validate it (no unit tests)
bash scripts/check-site.sh --built <dir>   # validate an existing directory
```

`scripts/check_site.py` fails on external URLs, broken links and fragments, missing alt text or image sizes, duplicate ids, a missing or weak CSP, inline handlers and more. For the generated docs pages it also checks a skip link, one `<h1>`, a navigation that lists every page, a search index that matches the pages, and orphan pages. CI runs it on every pull request, on the built output.

## The loadout demo is captured output

`assets/demo-data.js` holds the commands and settings files that the hero demo shows. They are printed by the real `ccshelf dry-run` against the fictional example org in `examples/org-data-repo`, with the repository's fake `claude` (never the real one), in a throwaway HOME. The static `frontend` example in `index.html` mirrors it for visitors without JavaScript. After a change to the generator, the example profiles or the settings schema, regenerate:

```sh
scripts/regen-site-demo.sh --write   # rewrites assets/demo-data.js
scripts/regen-site-demo.sh --check   # fails if it is stale
```

then update the static `frontend` block and the commit label in `index.html` to match.

What the page says under the terminal is one line: real `ccshelf dry-run` output, the commit, the fictional `acme` org, the fake `claude`. The longer account lives here: cache paths in the captured output are shortened to `~/.cache/ccshelf/`, the profiles come from `examples/org-data-repo`, and nothing on the page runs `ccshelf`; the script only replays the captured data.

## Addresses

- **Repository link.** The href of `<a id="repo">` in `index.html` is `REPO_URL`; every other repository link starts with it (the validator enforces that). Today it is the private working repository. Change it to the public home before publishing: edit it in one find-and-replace in `index.html`, or pass `--repo-url` to `scripts/build-site.sh` at deploy time.
- **Site address.** `canonical`, `og:url` and the social image use the placeholder `__SITE_URL__`, and `404.html` has a `<!--SITE_BASE-->` marker. `scripts/build-site.sh <out> <site_url>` fills them in on a copy and validates the result (it also renders the docs pages into `<out>/docs` first; those are never committed).

## Publishing

The site is published automatically (decision D-45, which supersedes D-34). `.github/workflows/pages.yml` runs on every push to `main` that changes `site/`, `docs/`, the site build scripts or the workflow itself, and on `workflow_dispatch` (Actions tab > pages > Run workflow; pass `repo_url` only if `site/index.html` should link somewhere else). Pull requests never deploy; the `site` job in `ci.yml` builds and validates the site for them.

One-time setup: Settings > Pages > Source "GitHub Actions". The live site is https://yorch.github.io/ccshelf/. To stop automatic deploys, remove the `push` trigger from `pages.yml` and keep `workflow_dispatch`.

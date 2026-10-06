#!/usr/bin/env python3
"""Render docs/**/*.md to static HTML pages of the website (standard library only).

Usage:
  python3 -I scripts/build_docs.py --out <site_out_dir> [--repo-url <url>]

Writes <out>/docs/**/*.html and <out>/docs/search-index.js. The stylesheet and scripts are the
committed files of site/assets (docs.css, docs.js, docs-search.js), which scripts/build-site.sh copies.
Normally run by scripts/build-site.sh; the deployable output is dist/site.

The Markdown parser is the one of docs/build_report.py (imported, not copied), so the report and the
website read the same subset: headings, paragraphs, lists (including task lists), GFM tables, fenced
code, inline code, bold, italic, strikethrough, links, blockquotes and the {V} {R} {U} markers. The
three report widgets (`<!-- widget: name -->`) need a script and inline styles, which the site's
Content-Security-Policy forbids: each is replaced by a note that points to the generated report.

Link rules (every relative URL, so the pages work from file:// and under a subpath such as /ccshelf/):
  * a link to another documentation .md file becomes a link to its generated page (with #fragment);
  * a link to site/index.html becomes the site home page;
  * a link to any other repository file or folder becomes REPO_URL/blob/main/<path> (tree/ for a
    folder), and fails the build when the target does not exist;
  * http(s) links are kept only for REPO_URL and the documentation hosts that check_site.py
    allows; any other external link is shown as its text followed by the address in <code>, not
    as a link, so the site links out to nowhere else.
REPO_URL is the href of the id="repo" link in site/index.html (the one source of truth); scripts/
build-site.sh swaps it for the public home at deploy time with the rest of the pages.
"""
from __future__ import annotations

import argparse
import html
import importlib.util
import json
import os
import posixpath
import re
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.normpath(os.path.join(HERE, ".."))
DOCS_DIR = os.path.join(ROOT, "docs")
SITE_DIR = os.path.join(ROOT, "site")
SITE_URL_PLACEHOLDER = "__SITE_URL__"
BRANCH = "main"


def _load(name: str, path: str):
    spec = importlib.util.spec_from_file_location(name, path)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


# The report's parser (parse_blocks, split_row, slug, esc) and the site checker's host allowlist.
br = _load("ccshelf_build_report", os.path.join(DOCS_DIR, "build_report.py"))
cs = _load("ccshelf_check_site", os.path.join(HERE, "check_site.py"))

# (group, nav label, markdown file relative to docs/, output path relative to docs/, description or "")
PAGES: list[tuple[str, str, str, str, str]] = [
    ("Start", "Overview", "README.md", "index.html",
     "Design notes, decision records and research for ccshelf, published from the Markdown in the repository. Not a user manual; not released yet."),
    ("Reference", "Command reference", "reference/cli.md", "reference/cli.html",
     "Every ccshelf command, flag and exit code, generated from the help output of the real binary."),
    ("Design", "Architecture", "design/architecture.md", "design/architecture.html",
     "Code layout of the tool repo, the design principles and the Go stack behind ccshelf."),
    ("Design", "Launcher", "design/launcher.md", "design/launcher.html",
     "How the ccshelf launcher starts claude with a generated settings file, what profiles share, accounts, and the known hazards."),
    ("Design", "Profiles", "design/profiles.md", "design/profiles.html",
     "Where profiles come from, how a shared profile is trusted by its resolved closure, and the profile manifest format."),
    ("Design", "CLI", "design/cli.md", "design/cli.html",
     "The ccshelf command set and the rule that every command works with flags alone, with prompts only as an optional front end."),
    ("Design", "Catalog and org repo", "design/catalog-and-org-repo.md", "design/catalog-and-org-repo.html",
     "How the public tool repo and an organization's private data repo relate, and how the data repo is structured."),
    ("Design", "Security", "design/security.md", "design/security.html",
     "The security requirements SR1 to SR5 and the managed-policy model: a closed profile schema, pinned trust and no bypass of policy."),
    ("Design", "Platform", "design/platform.md", "design/platform.html",
     "GitHub.com, GHE Cloud and GHE Server, GitHub Actions, and macOS, Linux and Windows support: the design and what has been tested."),
    ("Design", "Workflows", "design/workflows.md", "design/workflows.html",
     "Example workflows for ccshelf. Illustrative mockups with fictional plugin names, not captured output."),
    ("Design", "Project", "design/project.md", "design/project.html",
     "Open source under MIT, and the history and checks behind the name ccshelf."),
    ("Design", "Roadmap", "design/roadmap.md", "design/roadmap.html",
     "The staged plan: Phase 0 evidence, the MVP, deferred work and non-goals."),
    ("Decisions", "Decision log", "DECISIONS.md", "decisions.html",
     "The decision log of ccshelf: what was decided, why, with what confidence and when to revisit it."),
    ("Research", "Context", "research/context.md", "research/context.html",
     "The two use cases behind ccshelf and how the research was run."),
    ("Research", "Landscape", "research/landscape.md", "research/landscape.html",
     "Evidence of the problems, the native Claude Code mechanisms, existing tools and the naming findings."),
    ("Research", "Options and reviews", "research/options-and-reviews.md", "research/options-and-reviews.html",
     "The solution options considered for ccshelf and the adversarial reviews of them."),
    ("Research", "Stage 0 experiments", "research/stage0.md", "research/stage0.html",
     "The Stage 0 macOS experiments with Claude Code settings masking and the corrections made after review."),
    ("Research", "Adopt or build", "research/adopt-or-build.md", "research/adopt-or-build.html",
     "Evaluation of two existing tools against the gaps ccshelf targets, and the recommendation to build."),
    ("Research", "Routing eval protocol", "research/routing-eval-protocol.md", "research/routing-eval-protocol.html",
     "The fixed protocol and success criterion of the Phase 0.1 routing evaluation."),
]

# Markdown placed under the home page's title: what the sections are, and an honest status.
INTRO_MD = """\
These pages are the project's **design notes, decision records and research**, published from the Markdown files in the repository. They are **not a user manual**. ccshelf is implemented and under review but **not released**: there are no packages to install yet, nothing here promises behavior, and where the notes and the code disagree, the code and its tests win. For the build steps and a short command overview, see the [website home page](../site/index.html).

What each section is:

- **Start** is this page: the overview, the glossary and the requirements.
- **Reference** is the [command reference](reference/cli.md), generated from the real binary's `--help` output and checked in CI, so it cannot drift from the code.
- **Design** is the current design, one file per topic: how the launcher works, the profile format, the security model, platform support and the roadmap. It is kept up to date.
- **Decisions** is the [decision log](DECISIONS.md): every decision, supersession and open question, with date, evidence, confidence and a trigger to revisit. Rows are never deleted.
- **Research** holds dated findings. They are updated only to correct them, so they can be older than the design.
"""

LEGEND = (
    '<aside class="legend" aria-label="Confidence markers">\n'
    '<p class="legend-h">Confidence markers</p>\n'
    "<p>Claims about Claude Code behavior carry a marker saying how they were checked:</p>\n"
    "<ul>\n"
    '<li>%s <strong>Verified</strong>: in the official docs, with <code>gh</code>, or by running the CLI.</li>\n'
    '<li>%s <strong>Reported</strong>: by a research agent, not independently re-checked.</li>\n'
    '<li>%s <strong>Unverified</strong>: inferred, or seen only in a search snippet.</li>\n'
    "</ul>\n</aside>"
)
MARKERS = {"V": "verified", "R": "reported", "U": "unverified"}
ID_ROW = re.compile(r"^[A-Z]{1,3}-\d+$")
NW_CELL = re.compile(r"^([A-Z]-\d+|\d{4}-\d{2}-\d{2})$")
# ids the page template itself uses; headings and rows never take them.
RESERVED_IDS = {
    "main", "docnav", "docs-q", "docs-list", "docs-status", "docs-results", "dsearch", "theme-toggle", "toc", "top", "repo",
}
SECTION_TEXT_CAP = 420
ROW_TEXT_CAP = 320


class BuildError(Exception):
    pass


def esc(s: str) -> str:
    return html.escape(s, quote=False)


def attr(s: str) -> str:
    return html.escape(s, quote=True)


def marker_html(letter: str) -> str:
    word = MARKERS[letter]
    return '<span class="cm cm-%s" title="%s"><span class="sr">%s</span></span>' % (letter, word, word)


def strip_markers(text: str) -> str:
    return re.sub(r"\s*\{[VRU]\}", "", text)


def html_text(fragment: str) -> str:
    """Plain text of an HTML fragment (marker marks and tags dropped, entities decoded)."""
    fragment = re.sub(r'<span class="cm [^"]*"[^>]*><span class="sr">[^<]*</span></span>', "", fragment)
    fragment = re.sub(r"</?(?:code|strong|em|a|s|span)\b[^>]*>", "", fragment)  # inline tags do not split words
    fragment = re.sub(r"<[^>]+>", " ", fragment)
    return re.sub(r"\s+", " ", html.unescape(fragment)).strip()


def clip(text: str, cap: int) -> str:
    if len(text) <= cap:
        return text
    cut = text[:cap].rsplit(" ", 1)[0]
    return cut.rstrip(" ,;:(") + "..."


def read_repo_url(site_dir: str) -> str:
    with open(os.path.join(site_dir, "index.html"), encoding="utf-8") as f:
        text = f.read()
    m = re.search(r'<a\s+id="repo"\s+href="([^"]+)"', text)
    if not m:
        raise BuildError('site/index.html has no <a id="repo" href="..."> declaring REPO_URL')
    return m.group(1).rstrip("/")


class Page:
    def __init__(self, group: str, label: str, md: str, out: str, desc: str) -> None:
        self.group, self.label, self.md, self.out, self.desc = group, label, md, out, desc
        self.title = label
        self.html = ""
        self.toc: list[tuple[int, str, str]] = []
        self.sections: list[list] = []  # [anchor, heading, text]
        self.rows: list[list] = []  # [anchor, heading, text]
        self.ids: set[str] = set()
        self.first_text = ""
        self.h1_html = ""
        self.wide = False

    @property
    def site_path(self) -> str:
        return "docs/" + self.out


class Builder:
    def __init__(self, docs_dir: str = DOCS_DIR, repo_root: str = ROOT, site_dir: str = SITE_DIR,
                 pages: list[tuple[str, str, str, str, str]] | None = None, repo_url: str | None = None,
                 intro_md: str | None = INTRO_MD) -> None:
        self.docs_dir, self.repo_root = docs_dir, repo_root
        self.repo_url = (repo_url or read_repo_url(site_dir)).rstrip("/")
        self.pages = [Page(*p) for p in (PAGES if pages is None else pages)]
        self.by_md = {os.path.normpath(p.md): p for p in self.pages}
        self.intro_md = intro_md
        self.code_n = 0

    # ------------------------------------------------------------ paths and links
    def rel(self, from_site_path: str, to_site_path: str) -> str:
        return posixpath.relpath(to_site_path, posixpath.dirname(from_site_path) or ".")

    def external_ok(self, url: str) -> bool:
        if url == self.repo_url or url.startswith(self.repo_url + "/") or url.startswith(self.repo_url + "#"):
            return True
        m = re.match(r"^https://([^/:?#]+)", url)
        return bool(m) and m.group(1) in cs.OUTBOUND_DOC_HOSTS

    def repo_link(self, repo_path: str, frag: str) -> str:
        full = os.path.join(self.repo_root, repo_path)
        if os.path.isdir(full):
            kind, repo_path = "tree", repo_path.rstrip("/") + "/"
        elif os.path.isfile(full):
            kind = "blob"
        else:
            raise BuildError("link to a missing repository path: %s" % repo_path)
        return "%s/%s/%s/%s%s" % (self.repo_url, kind, BRANCH, repo_path, "#" + frag if frag else "")

    def resolve(self, url: str, cur: Page) -> tuple[str, str | None]:
        """Return ("href", href) / ("external", href) / ("xurl", url) / ("text", None)."""
        if re.match(r"^https?://", url):
            return ("external", url) if self.external_ok(url) else ("xurl", url)
        if re.match(r"^[a-z][a-z0-9+.-]*:", url, re.I):
            return ("text", None)
        path, _, frag = url.partition("#")
        if not path:
            return ("href", "#" + frag)
        target = posixpath.normpath(posixpath.join(posixpath.dirname(cur.md), path))
        page = self.by_md.get(os.path.normpath(target))
        if page is not None:
            href = self.rel(cur.site_path, page.site_path)
            return ("href", href + ("#" + frag if frag else ""))
        repo_path = posixpath.normpath(posixpath.join("docs", target))
        if repo_path.startswith("..") or repo_path.startswith("/"):
            raise BuildError("docs/%s: link leaves the repository: %s" % (cur.md, url))
        if repo_path == "site/index.html":
            return ("href", self.rel(cur.site_path, "index.html") + ("#" + frag if frag else ""))
        try:
            return ("external", self.repo_link(repo_path, frag))
        except BuildError as e:
            raise BuildError("docs/%s: %s" % (cur.md, e)) from e

    # ------------------------------------------------------------ inline
    def inline(self, text: str, cur: Page) -> str:
        store: list[str] = []

        def stash_html(fragment: str) -> str:
            store.append(fragment)
            return "\x00%d\x00" % (len(store) - 1)

        def stash(m: re.Match) -> str:
            return stash_html("<code>" + esc(m.group(2).strip()) + "</code>")

        text = text.replace("\x00", "")  # the placeholder delimiter never comes from the source
        text = re.sub(r"(`+)(.+?)\1(?!`)", stash, text)
        text = esc(text)

        def link(m: re.Match) -> str:
            label, url = m.group(1), html.unescape(m.group(2))
            kind, href = self.resolve(url, cur)
            if kind == "text":
                return label
            if kind == "xurl":
                shown = re.sub(r"^https?://", "", url).rstrip("/")
                if label.replace("\x00", "") == "" or html.unescape(re.sub(r"\x00\d+\x00", "", label)).strip() in (url, shown):
                    return stash_html('<code class="xurl">%s</code>' % esc(shown))
                return label + " " + stash_html('<span class="xurl">(<code>%s</code>)</span>' % esc(shown))
            ext = ' rel="external noopener"' if kind == "external" else ""
            # The finished tags are stashed: the emphasis and marker passes below must never rewrite
            # text inside an href (a "**" or "{V}" in a URL), only the label between the tags.
            return stash_html('<a href="%s"%s>' % (attr(href), ext)) + label + stash_html("</a>")

        text = re.sub(r"\[([^\]]+)\]\(([^)\s]+)\)", link, text)
        text = re.sub(r"\*\*(.+?)\*\*", r"<strong>\1</strong>", text)
        text = re.sub(r"(?<![\*\w])\*(?!\s)(.+?)(?<!\s)\*(?![\*\w])", r"<em>\1</em>", text)
        text = re.sub(r"~~(.+?)~~", r"<s>\1</s>", text)
        text = re.sub(r"\{([VRU])\}", lambda m: marker_html(m.group(1)), text)
        return re.sub(r"\x00(\d+)\x00", lambda m: store[int(m.group(1))], text)

    # ------------------------------------------------------------ blocks
    def unique_id(self, base: str, page: Page) -> str:
        n, cand = 2, base
        while cand in page.ids or cand in RESERVED_IDS:
            cand = "%s-%d" % (base, n)
            n += 1
        page.ids.add(cand)
        return cand

    def item_blocks(self, blocks: list, cur: Page) -> str:
        if blocks and blocks[0][0] == "p":
            first = self.inline(blocks[0][1], cur)
            rest = self.blocks(blocks[1:], cur)
            return first + ("\n" + rest if rest else "")
        return self.blocks(blocks, cur)

    def blocks(self, blocks: list, cur: Page) -> str:
        out: list[str] = []
        for b in blocks:
            k = b[0]
            if k == "p":
                out.append("<p>%s</p>" % self.inline(b[1], cur))
            elif k == "h":
                out.append(self.heading(b, cur))
            elif k == "code":
                self.code_n += 1
                cid = "code-%d" % self.code_n
                lang = b[1] if re.match(r"^[\w+-]+$", b[1] or "") else ""
                out.append(
                    '<div class="cmd"><pre><code id="%s"%s>%s</code></pre>'
                    '<button type="button" class="copy" data-copy="%s" hidden>Copy</button></div>'
                    % (cid, ' class="language-%s"' % attr(lang) if lang else "", esc(b[2]), cid))
            elif k == "hr":
                out.append("<hr>")
            elif k == "quote":
                out.append("<blockquote>%s</blockquote>" % self.blocks(b[1], cur))
            elif k == "widget":
                out.append(
                    '<p class="widget-note">An interactive chart belongs here. It needs an inline script, which this site\'s security policy blocks, '
                    "so it is only in the generated report: open <code>docs/report.html</code> from a clone of the repository. "
                    "The same figures are given in the text.</p>")
            elif k == "table":
                out.append(self.table(b, cur))
            elif k == "list":
                tag = "ol" if b[1] else "ul"
                items = []
                for task, inner_blocks in b[2]:
                    inner = self.item_blocks(inner_blocks, cur)
                    if task is None:
                        items.append("<li>%s</li>" % inner)
                    else:
                        items.append('<li class="task"><input type="checkbox" disabled%s aria-label="%s"><div>%s</div></li>'
                                     % (" checked" if task else "", "done" if task else "not done", inner))
                out.append("<%s>\n%s\n</%s>" % (tag, "\n".join(items), tag))
        return "\n".join(out)

    def heading(self, b: tuple, cur: Page) -> str:
        level, raw = b[1], b[2].strip()
        if level == 1:
            level = 2  # one h1 per page: the title
        plain = html_text(self.inline(strip_markers(raw), cur))
        hid = self.unique_id(br.slug(strip_markers(raw)), cur)
        if level in (2, 3):
            cur.toc.append((level, hid, plain))
            cur.sections.append([hid, plain, ""])
        return ('<h%d id="%s">%s<a class="anchor" href="#%s" aria-label="Link to this section: %s">#</a></h%d>'
                % (level, hid, self.inline(raw, cur), hid, attr(plain), level))

    def table(self, b: tuple, cur: Page) -> str:
        head, rows = b[1], b[2]
        ncol = len(head)
        id_rows = bool(rows) and all(ID_ROW.match(r[0].strip()) for r in rows if r and r[0].strip()) and ncol > 1
        if ncol >= 5:
            cur.wide = True
        thead = "".join('<th scope="col">%s</th>' % self.inline(c, cur) for c in head)
        body = []
        for r in rows:
            r = (r + [""] * ncol)[:ncol]
            rid = ""
            if id_rows and ID_ROW.match(r[0].strip()):
                key = self.unique_id(r[0].strip().lower(), cur)
                rid = ' id="%s"' % key
                text = [html_text(self.inline(c, cur)) for c in r]
                lead = text[2] if ncol > 2 and text[2] else text[1]
                rest = [t for i, t in enumerate(text[1:], 1) if t and t != lead]
                cur.rows.append([key, "%s: %s" % (r[0].strip(), clip(lead, 70)), clip(" ".join(rest), ROW_TEXT_CAP)])
            cells = "".join(
                "<td%s>%s</td>" % (' class="nw"' if NW_CELL.match(c.strip()) else "", self.inline(c, cur)) for c in r)
            body.append("<tr%s>%s</tr>" % (rid, cells))
        label = "Table: " + ", ".join(strip_markers(html_text(self.inline(c, cur))) for c in head[:3]) + (", ..." if ncol > 3 else "")
        table = '<table><thead><tr>%s</tr></thead><tbody>\n%s\n</tbody></table>' % (thead, "\n".join(body))
        return '<div class="tablewrap" role="region" aria-label="%s" tabindex="0">%s</div>' % (attr(label), table)

    # ------------------------------------------------------------ pages
    def render_page(self, p: Page) -> None:
        path = os.path.join(self.docs_dir, p.md)
        try:
            with open(path, encoding="utf-8") as f:
                text = f.read()
        except OSError as e:
            raise BuildError("cannot read %s: %s" % (p.md, e)) from e
        if SITE_URL_PLACEHOLDER in text:
            raise BuildError("docs/%s contains %s, which scripts/build-site.sh reserves for the site address" % (p.md, SITE_URL_PLACEHOLDER))
        blocks = br.parse_blocks(text.replace("\r\n", "\n").split("\n"))
        title_raw = p.label
        if blocks and blocks[0][0] == "h" and blocks[0][1] == 1:
            title_raw = blocks[0][2].strip()
            blocks = blocks[1:]
        p.title = html_text(self.inline(strip_markers(title_raw), p))
        p.h1_html = self.inline(title_raw, p)
        p.sections = [["", "", ""]]
        self.code_n = 0
        parts: list[str] = []
        if p.out == "index.html":
            if self.intro_md:
                frag = self.blocks(br.parse_blocks(self.intro_md.split("\n")), p)
                parts.append(frag)
                p.sections[0][2] = html_text(frag)
            parts.append(LEGEND % (marker_html("V"), marker_html("R"), marker_html("U")))
        for b in blocks:
            if b[0] == "h":
                parts.append(self.heading(b, p))  # opens a new search section
                continue
            before = len(p.rows)
            frag = self.table(b, p) if b[0] == "table" else self.blocks([b], p)
            parts.append(frag)
            if len(p.rows) == before:  # a table whose rows are indexed one by one is not repeated in the section
                sec = p.sections[-1]
                sec[2] = (sec[2] + " " + html_text(frag)).strip() if len(sec[2]) < SECTION_TEXT_CAP * 2 else sec[2]
        p.html = "\n".join(x for x in parts if x)
        for sec in p.sections:
            sec[2] = clip(sec[2], SECTION_TEXT_CAP)
        p.first_text = ""
        for b in blocks:
            if b[0] == "p":
                t = html_text(self.inline(strip_markers(b[1]), p))
                if t:
                    p.first_text = t
                    break
        if not p.desc:
            p.desc = clip(p.first_text, 158) or "Documentation page of ccshelf."

    def nav_html(self, cur: Page) -> str:
        groups: list[tuple[str, list[Page]]] = []
        for p in self.pages:
            if not groups or groups[-1][0] != p.group:
                groups.append((p.group, []))
            groups[-1][1].append(p)
        out = ['<nav id="docnav" class="docnav" aria-label="Documentation">']
        for g, ps in groups:
            out.append('<p class="navgrp">%s</p>\n<ul>' % esc(g))
            for p in ps:
                href = self.rel(cur.site_path, p.site_path)
                here = ' aria-current="page"' if p is cur else ""
                out.append('<li><a href="%s"%s>%s</a></li>' % (attr(href), here, esc(p.label)))
            out.append("</ul>")
        out.append("</nav>")
        return "\n".join(out)

    def toc_html(self, p: Page) -> str:
        if len(p.toc) < 2:
            return ""
        base = min(lvl for lvl, _h, _t in p.toc)  # levels are relative to the page's shallowest heading
        out = ['<nav class="toc" aria-label="On this page"><details class="toc-d"%s><summary>On this page</summary>' % ("" if p.wide else " open"), "<ul>"]
        open_sub = False
        for i, (lvl, hid, text) in enumerate(p.toc):
            sub = lvl > base and i > 0  # an entry deeper than the page's top level, with a parent entry before it
            if sub and not open_sub:
                out.append("<ul>")  # always inside the <li> of the entry before it: the first entry is never a sub entry
                open_sub = True
            elif not sub and open_sub:
                out.append("</ul></li>")
                open_sub = False
            elif not sub and i > 0:
                out.append("</li>")
            out.append('<li><a href="#%s">%s</a></li>' % (hid, esc(text)) if sub
                       else '<li><a href="#%s">%s</a>' % (hid, esc(text)))
        out.append("</ul></li>" if open_sub else "</li>")
        out.append("</ul></details></nav>")
        return "\n".join(out)

    def crumbs_html(self, p: Page) -> str:
        home = self.pages[0]
        items = ['<li><a href="%s">Docs</a></li>' % attr(self.rel(p.site_path, home.site_path))] if p is not home else []
        if p is home:
            return '<nav class="crumbs" aria-label="Breadcrumb"><ol><li aria-current="page">Docs</li></ol></nav>'
        items.append("<li>%s</li>" % esc(p.group))
        items.append('<li aria-current="page">%s</li>' % esc(p.label))
        return '<nav class="crumbs" aria-label="Breadcrumb"><ol>%s</ol></nav>' % "".join(items)

    def pager_html(self, p: Page) -> str:
        i = self.pages.index(p)
        cells = []
        if i > 0:
            q = self.pages[i - 1]
            cells.append('<a class="prev" rel="prev" href="%s"><span>Previous</span>%s</a>' % (attr(self.rel(p.site_path, q.site_path)), esc(q.label)))
        if i + 1 < len(self.pages):
            q = self.pages[i + 1]
            cells.append('<a class="next" rel="next" href="%s"><span>Next</span>%s</a>' % (attr(self.rel(p.site_path, q.site_path)), esc(q.label)))
        return '<nav class="pager" aria-label="Previous and next pages">%s</nav>' % "".join(cells) if cells else ""

    def page_html(self, p: Page) -> str:
        r = lambda to: attr(self.rel(p.site_path, to))  # noqa: E731
        docs_root = posixpath.relpath("docs", posixpath.dirname(p.site_path) or ".")
        data_root = "" if docs_root == "." else docs_root + "/"
        title = "ccshelf documentation" if p.out == "index.html" else "%s: ccshelf docs" % p.label
        og_title = attr(title)
        canonical = "%s/%s" % (SITE_URL_PLACEHOLDER, p.site_path)
        src_href = attr(self.repo_link("docs/" + p.md, ""))
        toc = self.toc_html(p)
        return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; font-src 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'">
<meta name="color-scheme" content="light dark">
<meta name="referrer" content="no-referrer">
<meta name="theme-color" content="#eef0ec" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="#111513" media="(prefers-color-scheme: dark)">
<title>{esc(title)}</title>
<meta name="description" content="{attr(p.desc)}">
<link rel="canonical" href="{canonical}">
<meta property="og:type" content="article">
<meta property="og:site_name" content="ccshelf">
<meta property="og:url" content="{canonical}">
<meta property="og:title" content="{og_title}">
<meta property="og:description" content="{attr(p.desc)}">
<meta property="og:image" content="{SITE_URL_PLACEHOLDER}/assets/og.png">
<meta property="og:image:width" content="1200">
<meta property="og:image:height" content="630">
<meta property="og:image:alt" content="A shelf of labelled plugin items with two of them lifted into a loadout.">
<link rel="icon" href="{r("assets/favicon.svg")}" type="image/svg+xml">
<link rel="stylesheet" href="{r("assets/site.css")}">
<link rel="stylesheet" href="{r("assets/docs.css")}">
<script src="{r("assets/site.js")}"></script>
<script src="{r("assets/docs.js")}"></script>
<script src="{r("assets/docs-search.js")}"></script>
</head>
<body class="docs-page">
<a class="skip" href="#main">Skip to content</a>

<header class="bar">
  <div class="wrap bar-in docs-bar">
    <a class="mark" href="{r("index.html")}" aria-label="ccshelf, site home">
      <svg class="mark-ico" width="28" height="28" viewBox="0 0 28 28" aria-hidden="true" focusable="false"><rect class="m-a" x="3" y="9" width="5" height="11" rx="1"/><rect class="m-b" x="10" y="4" width="5" height="16" rx="1"/><rect class="m-a" x="17" y="9" width="5" height="11" rx="1"/><rect class="m-board" x="1" y="21.5" width="26" height="3" rx="1"/></svg>
      <span>ccshelf</span>
    </a>
    <nav class="nav" aria-label="Site">
      <ul>
        <li><a href="{r("index.html")}">Home</a></li>
        <li><a href="{r("docs/index.html")}" aria-current="true">Docs</a></li>
        <li><a href="{attr(self.repo_url)}" rel="external noopener">Source</a></li>
      </ul>
    </nav>
    <div class="dsearch" id="dsearch" role="search" data-root="{data_root}" hidden>
      <label class="sr" for="docs-q">Search the documentation</label>
      <input id="docs-q" type="search" role="combobox" aria-autocomplete="list" aria-haspopup="true" autocomplete="off" spellcheck="false" enterkeyhint="search" placeholder="Search the docs" aria-keyshortcuts="/" aria-controls="docs-list" aria-expanded="false">
      <kbd class="dkey" aria-hidden="true">/</kbd>
      <div class="dresults" id="docs-results" hidden>
        <ul id="docs-list"></ul>
      </div>
    </div>
    <p class="sr" id="docs-status" role="status"></p>
    <button class="theme" type="button" id="theme-toggle" hidden>Theme: light</button>
  </div>
</header>

<div class="wrap docs">
{self.nav_html(p)}
<main id="main" class="dmain{" dmain-wide" if p.wide else ""}">
{self.crumbs_html(p)}
<h1>{p.h1_html}</h1>
<div class="dbody">
{toc}
<article class="doc">
{p.html}
</article>
</div>
{self.pager_html(p)}
</main>
</div>

<footer class="foot" id="source">
  <div class="wrap foot-in">
    <p>These notes are published from the Markdown in <a href="{attr(self.repo_url)}" rel="external noopener">the ccshelf repository</a>: <a href="{src_href}" rel="external noopener">view the source of this page</a>. Unofficial: not affiliated with Anthropic. Open source under the MIT License.</p>
    <p class="fine">Design notes and decision records, not a user manual. ccshelf is not released yet. Plugin names, teams and the <code>acme</code> organization in examples are fictional.</p>
  </div>
</footer>
</body>
</html>
"""

    # ------------------------------------------------------------ whole site
    def check_sources(self) -> None:
        listed = {os.path.normpath(p.md) for p in self.pages}
        found = set()
        for d, _dirs, files in os.walk(self.docs_dir):
            for f in files:
                if f.endswith(".md"):
                    found.add(os.path.normpath(os.path.relpath(os.path.join(d, f), self.docs_dir)))
        for m in sorted(found - listed):
            raise BuildError("docs/%s is not listed in PAGES (scripts/build_docs.py): add it or it never reaches the site" % m)
        for m in sorted(listed - found):
            raise BuildError("PAGES lists docs/%s, which does not exist" % m)
        outs = [p.out for p in self.pages]
        if len(outs) != len(set(outs)):
            raise BuildError("two pages share an output path")

    def search_index(self) -> str:
        pages = []
        for p in self.pages:
            secs = [[a, h, x] for a, h, x in p.sections if a or x]
            secs += p.rows
            pages.append({"u": p.out, "t": p.title, "g": p.group, "s": secs})
        data = {"v": 1, "pages": pages}
        return "window.CCSHELF_DOCS_INDEX = " + json.dumps(data, ensure_ascii=True, separators=(",", ":")) + ";\n"

    def build(self, out_dir: str) -> list[str]:
        self.check_sources()
        for p in self.pages:
            self.render_page(p)
        written = []
        for p in self.pages:
            dest = os.path.join(out_dir, "docs", *p.out.split("/"))
            os.makedirs(os.path.dirname(dest), exist_ok=True)
            with open(dest, "w", encoding="utf-8", newline="\n") as f:
                f.write(self.page_html(p))
            written.append(dest)
        index = self.search_index()
        if self.repo_url in index:
            # build-site.sh swaps the repository address only inside HTML attributes, so the index must not carry it
            raise BuildError("the search index would contain the repository address %s: reword the page text that mentions it" % self.repo_url)
        dest = os.path.join(out_dir, "docs", "search-index.js")
        with open(dest, "w", encoding="utf-8", newline="\n") as f:
            f.write(index)
        written.append(dest)
        return written


def main(argv: list[str]) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n", 1)[0])
    ap.add_argument("--out", required=True, help="the site output directory (docs/ is created inside it)")
    ap.add_argument("--repo-url", default=None, help="override REPO_URL (default: the id=repo link of site/index.html)")
    args = ap.parse_args(argv)
    try:
        written = Builder(repo_url=args.repo_url).build(args.out)
    except BuildError as e:
        print("build-docs: %s" % e, file=sys.stderr)
        return 1
    print("build-docs: wrote %d files under %s" % (len(written), os.path.join(args.out, "docs")))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))

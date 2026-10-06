#!/usr/bin/env python3
"""Static checks for the project website in site/ (standard library only).

Run as: python3 -I scripts/check_site.py [site_dir]

Fails (exit 1) on: external URLs other than the declared REPO_URL and documented outbound
documentation links; broken relative links and missing fragment ids; images without alt text or
dimensions; duplicate ids; a missing lang, title, description, viewport or Content-Security-Policy;
a permissive CSP; inline event handlers, inline style attributes, inline <script> and <style>
(the CSP forbids them); external URLs in CSS and JavaScript; and a page whose own size plus its
local subresources is over 1 MB.

REPO_URL is the href of the element with id="repo" in index.html: the single place the
repository address lives. It is the only external prefix allowed in src/href values besides
OUTBOUND_DOC_HOSTS (anchor hrefs only).
"""
from __future__ import annotations

import os
import re
import sys
from html.parser import HTMLParser
from urllib.parse import unquote, urlsplit

MAX_PAGE_BYTES = 1_000_000
# Documentation hosts that may be linked with <a href> (never loaded as a resource).
OUTBOUND_DOC_HOSTS = {"code.claude.com"}
URL_ATTRS = {
    "a": ("href",), "area": ("href",), "link": ("href",), "script": ("src",), "img": ("src", "srcset"),
    "source": ("src", "srcset"), "audio": ("src",), "video": ("src", "poster"), "iframe": ("src",),
    "embed": ("src",), "track": ("src",), "input": ("src",), "form": ("action",), "object": ("data",),
}
META_URL_KEYS = ("image", "url", "image:src")
EVENT_ATTR = re.compile(r"^on[a-z]+$", re.I)
CSS_URL = re.compile(r"url\(\s*(?:\"([^\"]*)\"|'([^']*)'|([^)\s]*))\s*\)", re.I)
CSS_IMPORT = re.compile(r"@import\s+(?:url\(\s*)?(?:\"([^\"]*)\"|'([^']*)')", re.I)
JS_URL = re.compile(r"(?:https?:)?//[A-Za-z0-9.-]+\.[A-Za-z]{2,}[^\s'\"`)]*")
JS_NET = re.compile(r"\b(fetch|XMLHttpRequest|WebSocket|EventSource|sendBeacon|importScripts)\b|\bimport\s*\(")


class Page(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.ids: list[str] = []
        self.links: list[tuple[str, str, str]] = []  # (tag, attr, value)
        self.errors: list[str] = []
        self.lang = ""
        self.title = ""
        self.in_title = False
        self.meta: dict[str, str] = {}
        self.csp = ""
        self.has_viewport = False
        self.imgs: list[dict[str, str | None]] = []
        self.inline_script = 0
        self.inline_style_tag = 0
        self._script_src = False
        self._in_style = False

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        a = {k.lower(): (v if v is not None else "") for k, v in attrs}
        if tag == "html":
            self.lang = a.get("lang", "")
        if tag == "title":
            self.in_title = True
        if "id" in a:
            self.ids.append(a["id"])
        if tag == "a" and "name" in a:
            self.ids.append(a["name"])
        for k in a:
            if EVENT_ATTR.match(k):
                self.errors.append(f"inline event handler {k}= on <{tag}>")
        if "style" in a:
            self.errors.append(f"inline style attribute on <{tag}> (blocked by the CSP)")
        if tag == "meta":
            name = (a.get("name") or a.get("property") or "").lower()
            if name:
                self.meta[name] = a.get("content", "")
            if name == "viewport":
                self.has_viewport = True
            if a.get("http-equiv", "").lower() == "content-security-policy":
                self.csp = a.get("content", "")
            if name.rsplit(":", 1)[-1] in META_URL_KEYS and a.get("content"):
                self.links.append(("meta", name, a["content"]))
        if tag == "script":
            if "src" not in a:
                self.inline_script += 1
        if tag == "style":
            self.inline_style_tag += 1
        if tag == "img":
            self.imgs.append({"alt": a.get("alt"), "width": a.get("width"), "height": a.get("height"), "src": a.get("src")})
        for attr in URL_ATTRS.get(tag, ()):
            if attr in a:
                if attr == "srcset":
                    for part in a[attr].split(","):
                        u = part.strip().split()
                        if u:
                            self.links.append((tag, attr, u[0]))
                else:
                    self.links.append((tag, attr, a[attr]))

    def handle_endtag(self, tag: str) -> None:
        if tag == "title":
            self.in_title = False

    def handle_data(self, data: str) -> None:
        if self.in_title:
            self.title += data


def read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


def parse(path: str) -> Page:
    p = Page()
    with open(path, encoding="utf-8") as f:
        p.feed(f.read())
    return p


def is_external(u: str) -> bool:
    s = urlsplit(u)
    return bool(s.scheme) or u.startswith("//")


def check_csp(csp: str, where: str, errs: list[str]) -> None:
    if not csp:
        errs.append(f"{where}: missing <meta http-equiv=\"Content-Security-Policy\">")
        return
    if "default-src" not in csp:
        errs.append(f"{where}: CSP has no default-src")
    for bad in ("'unsafe-inline'", "'unsafe-eval'", "http:", "https:", "*"):
        # '*' only counts as a source token, not inside other characters.
        toks = csp.replace(";", " ").split()
        if bad in toks:
            errs.append(f"{where}: CSP allows {bad}")
    if re.search(r"https?://", csp):
        errs.append(f"{where}: CSP names an external host")


def resolve_local(page_dir: str, ref: str, root: str) -> str | None:
    """Return the file a relative reference points to (inside root), or None if missing."""
    path = unquote(urlsplit(ref).path)
    if path == "":
        return None
    target = os.path.normpath(os.path.join(page_dir, path))
    if os.path.commonpath([os.path.abspath(target), os.path.abspath(root)]) != os.path.abspath(root):
        return None
    if os.path.isdir(target):
        target = os.path.join(target, "index.html")
    return target if os.path.isfile(target) else None


def check_site(root: str) -> list[str]:
    errs: list[str] = []
    if not os.path.isdir(root):
        return [f"{root}: not a directory"]
    pages = sorted(
        os.path.join(d, f) for d, _, fs in os.walk(root) for f in fs if f.endswith(".html")
    )
    if not pages:
        return [f"{root}: no .html files"]
    index = os.path.join(root, "index.html")
    if index not in pages:
        errs.append(f"{root}: no index.html")

    parsed: dict[str, Page] = {}
    for pg in pages:
        parsed[os.path.abspath(pg)] = parse(pg)

    repo_url = ""
    if index in pages:
        m = re.search(r'<a\b[^>]*\bid="repo"[^>]*\bhref="([^"]+)"|<a\b[^>]*\bhref="([^"]+)"[^>]*\bid="repo"', read(index))
        if m:
            repo_url = (m.group(1) or m.group(2)).rstrip("/")
        else:
            errs.append("index.html: no element with id=\"repo\" declaring REPO_URL")
        if repo_url and not repo_url.startswith("https://"):
            errs.append(f"index.html: REPO_URL must be https, got {repo_url}")

    def external_ok(tag: str, attr: str, u: str) -> bool:
        if repo_url and (u == repo_url or u.startswith(repo_url + "/")) and tag == "a":
            return True
        if tag == "a" and attr == "href":
            s = urlsplit(u)
            return s.scheme == "https" and s.hostname in OUTBOUND_DOC_HOSTS
        return False

    for pg in pages:
        rel = os.path.relpath(pg, root)
        p = parsed[os.path.abspath(pg)]
        size = os.path.getsize(pg)
        if not p.lang.strip():
            errs.append(f"{rel}: <html> has no lang")
        if not p.title.strip():
            errs.append(f"{rel}: missing <title>")
        if not p.meta.get("description", "").strip():
            errs.append(f"{rel}: missing meta description")
        if not p.has_viewport:
            errs.append(f"{rel}: missing meta viewport")
        check_csp(p.csp, rel, errs)
        for e in p.errors:
            errs.append(f"{rel}: {e}")
        if p.inline_script:
            errs.append(f"{rel}: inline <script> (blocked by the CSP)")
        if p.inline_style_tag:
            errs.append(f"{rel}: inline <style> (blocked by the CSP)")
        seen: set[str] = set()
        for i in p.ids:
            if i in seen:
                errs.append(f"{rel}: duplicate id \"{i}\"")
            seen.add(i)
        for im in p.imgs:
            if im["alt"] is None:
                errs.append(f"{rel}: <img src=\"{im['src']}\"> has no alt attribute")
            if not im["width"] or not im["height"]:
                errs.append(f"{rel}: <img src=\"{im['src']}\"> lacks width and height")
        page_dir = os.path.dirname(os.path.abspath(pg))
        weight = size
        counted: set[str] = set()
        for tag, attr, val in p.links:
            val = val.strip()
            if val == "":
                errs.append(f"{rel}: empty {attr} on <{tag}>")
                continue
            if is_external(val):
                if not external_ok(tag, attr, val):
                    errs.append(f"{rel}: external URL not allowed in <{tag} {attr}>: {val}")
                continue
            frag = urlsplit(val).fragment
            if val.startswith("#"):
                target_page = p
                tpath = pg
            else:
                tpath = resolve_local(page_dir, val, root)
                if tpath is None:
                    errs.append(f"{rel}: broken relative link <{tag} {attr}>: {val}")
                    continue
                target_page = parsed.get(os.path.abspath(tpath))
                if tag != "a" and tpath not in counted:
                    counted.add(tpath)
                    weight += os.path.getsize(tpath)
            if frag and target_page is not None and unquote(frag) not in target_page.ids and frag != "top":
                errs.append(f"{rel}: missing fragment id #{frag} in {os.path.relpath(tpath, root)} (from {val})")
        if weight > MAX_PAGE_BYTES:
            errs.append(f"{rel}: page weight {weight} bytes exceeds {MAX_PAGE_BYTES}")

    for d, _, fs in os.walk(root):
        for f in fs:
            path = os.path.join(d, f)
            rel = os.path.relpath(path, root)
            if f.endswith(".css"):
                errs.extend(check_css(path, rel, root))
            elif f.endswith(".js"):
                errs.extend(check_js(path, rel))
            elif f.endswith(".svg"):
                errs.extend(check_svg(path, rel))
    return errs


def check_css(path: str, rel: str, root: str) -> list[str]:
    errs: list[str] = []
    text = re.sub(r"/\*.*?\*/", "", read(path), flags=re.S)
    refs = [next(g for g in m.groups() if g is not None) for m in CSS_URL.finditer(text) if any(g is not None for g in m.groups())]
    refs += [next(g for g in m.groups() if g is not None) for m in CSS_IMPORT.finditer(text)]
    for r in refs:
        r = r.strip()
        if not r:
            continue
        if is_external(r) or r.startswith("data:"):
            errs.append(f"{rel}: external or data URL in CSS: {r}")
        elif r.startswith("#"):
            continue
        elif resolve_local(os.path.dirname(path), r, root) is None:
            errs.append(f"{rel}: broken url() in CSS: {r}")
    return errs


def check_js(path: str, rel: str) -> list[str]:
    errs: list[str] = []
    text = read(path)
    for m in JS_URL.finditer(text):
        errs.append(f"{rel}: URL literal in JavaScript: {m.group(0)}")
    for m in JS_NET.finditer(text):
        errs.append(f"{rel}: network API in JavaScript: {m.group(0)}")
    return errs


def check_svg(path: str, rel: str) -> list[str]:
    errs: list[str] = []
    text = read(path)
    for m in re.finditer(r"(?:href|src)\s*=\s*[\"']([^\"']+)[\"']", text):
        if is_external(m.group(1)):
            errs.append(f"{rel}: external reference in SVG: {m.group(1)}")
    if re.search(r"<script\b", text, re.I):
        errs.append(f"{rel}: <script> in SVG")
    return errs


def main(argv: list[str]) -> int:
    root = argv[1] if len(argv) > 1 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "site")
    root = os.path.normpath(root)
    errs = check_site(root)
    for e in errs:
        print(f"check-site: {e}", file=sys.stderr)
    if errs:
        print(f"check-site: {len(errs)} problem(s)", file=sys.stderr)
        return 1
    print("check-site: ok")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

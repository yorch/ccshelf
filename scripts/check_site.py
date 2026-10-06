#!/usr/bin/env python3
"""Static checks for the project website in site/ (standard library only).

Run as: python3 -I scripts/check_site.py [--built] [site_dir]

Fails (exit 1) on:
  * external URLs other than the declared REPO_URL (on <a href> and <link rel> only) and the
    documented outbound documentation hosts (<a href> only), in any URL-carrying attribute, meta
    refresh, CSS url()/image-set()/@import (escapes decoded), inline SVG, SVG files and JavaScript
    string literals; non-https schemes (javascript:, data:, blob:, ...) everywhere;
  * broken relative links, links that escape the site directory, and missing fragment ids;
  * images without alt text or with missing or non-numeric width/height; <input type=image>
    without alt; duplicate ids (including <a name>);
  * a missing lang, title, description, viewport or Content-Security-Policy; a CSP that allows
    anything but 'none' and 'self', has no default-src, or appears after an element that loads a
    resource; inline event handlers, inline style attributes, inline <script> and <style>,
    <script> in SVG, network APIs and eval in JavaScript;
  * Open Graph, Twitter and canonical URLs that are not under the one site address, a <base>
    outside that address, and a page whose own size plus its local subresources is over 1 MB.

REPO_URL is the href of the element with id="repo" in index.html: the single place the repository
address lives. Every other link to it must start with it.

SITE_URL is filled in at deploy time (scripts/build-site.sh). In the committed site the canonical,
og:url, og:image and twitter:image values start with the placeholder __SITE_URL__ and 404.html has
a <!--SITE_BASE--> marker. With --built (the output of build-site.sh) no placeholder may remain.
"""
from __future__ import annotations

import os
import re
import sys
from html.parser import HTMLParser
from urllib.parse import unquote, urlsplit

MAX_PAGE_BYTES = 1_000_000
PLACEHOLDER = "__SITE_URL__"
BASE_MARKER = "<!--SITE_BASE-->"
# Documentation hosts that may be linked with <a href> (never loaded as a resource).
OUTBOUND_DOC_HOSTS = {"code.claude.com"}
# Every attribute that can carry a URL, whatever the tag (including SVG's xlink:href).
URL_ATTRS = {
    "href", "xlink:href", "src", "srcset", "imagesrcset", "poster", "data", "action", "formaction",
    "ping", "cite", "background", "manifest", "longdesc", "usemap", "codebase", "icon",
}
# Tags that start loading something when parsed: the CSP must come before the first of them.
LOADER_TAGS = {"script", "link", "img", "iframe", "embed", "object", "audio", "video", "source", "track", "image", "use"}
META_URL_NAMES = {"og:url", "og:image", "og:image:url", "og:image:secure_url", "twitter:image", "twitter:image:src", "og:video", "og:audio"}
EVENT_ATTR = re.compile(r"^on[a-z]+$", re.I)
CSS_COMMENT = re.compile(r"/\*.*?\*/", re.S)
CSS_ESCAPE = re.compile(r"\\([0-9a-fA-F]{1,6})[ \t\r\n\f]?|\\(.)", re.S)
CSS_URL = re.compile(r"url\(\s*(?:\"([^\"]*)\"|'([^']*)'|([^)\s]*))\s*\)", re.I)
CSS_IMPORT = re.compile(r"@import\s+(?:\"([^\"]*)\"|'([^']*)')", re.I)
CSS_IMAGE_SET = re.compile(r"(?:-webkit-)?image-set\(((?:[^()]|\([^()]*\))*)\)", re.I)
CSS_STRING = re.compile(r"\"([^\"]*)\"|'([^']*)'")
JS_URL = re.compile(r"\b(?:https?|wss?|ftp|file):\s*//|[\"'`]\s*//[A-Za-z0-9-]+\.[A-Za-z]{2,}", re.I)
JS_NET = re.compile(
    r"\b(fetch|XMLHttpRequest|WebSocket|EventSource|sendBeacon|importScripts|eval)\b|\bimport\s*\(|\bnew\s+Function\b|\bdocument\.write\b"
)
ALLOWED_CSP_TOKENS = {"'none'", "'self'"}


class Page(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.ids: list[str] = []
        self.refs: list[tuple[str, str, str, dict[str, str]]] = []  # (tag, attr, value, all attrs)
        self.errors: list[str] = []
        self.lang = ""
        self.title = ""
        self.in_title = False
        self.meta: dict[str, str] = {}
        self.meta_urls: list[tuple[str, str]] = []
        self.csp: list[str] = []
        self.csp_late = False
        self.loaded_before_csp = False
        self.has_viewport = False
        self.imgs: list[dict[str, str | None]] = []
        self.inline_script = 0
        self.inline_style_tag = 0
        self.canonical = ""
        self.bases: list[str] = []
        self.repo_href = ""

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
        if tag == "a" and a.get("id") == "repo":
            self.repo_href = a.get("href", "")
        for k in a:
            if EVENT_ATTR.match(k):
                self.errors.append(f"inline event handler {k}= on <{tag}>")
        if "style" in a:
            self.errors.append(f"inline style attribute on <{tag}> (blocked by the CSP)")
        if tag == "meta":
            http_equiv = a.get("http-equiv", "").lower()
            name = (a.get("name") or a.get("property") or "").lower()
            if name:
                self.meta[name] = a.get("content", "")
            if name == "viewport":
                self.has_viewport = True
            if http_equiv == "content-security-policy":
                self.csp.append(a.get("content", ""))
                if self.loaded_before_csp:
                    self.csp_late = True
            if http_equiv == "refresh":
                self.errors.append(f"<meta http-equiv=refresh> is not allowed: {a.get('content', '')}")
            if name in META_URL_NAMES and a.get("content"):
                self.meta_urls.append((name, a["content"].strip()))
        if tag == "base":
            self.bases.append(a.get("href", ""))
        if tag == "link" and "canonical" in a.get("rel", "").lower().split():
            self.canonical = a.get("href", "").strip()
        if tag in LOADER_TAGS and not self.csp:
            self.loaded_before_csp = True
        if tag == "script" and "src" not in a:
            self.inline_script += 1
        if tag == "style":
            self.inline_style_tag += 1
        if tag == "img":
            self.imgs.append({"alt": a.get("alt"), "width": a.get("width"), "height": a.get("height"), "src": a.get("src")})
        if tag == "input" and a.get("type", "").lower() == "image":
            self.imgs.append({"alt": a.get("alt"), "width": "1", "height": "1", "src": a.get("src"), "input": "1"})
        for attr, val in a.items():
            if attr not in URL_ATTRS:
                continue
            if attr in ("srcset", "imagesrcset"):
                for part in val.split(","):
                    u = part.strip().split()
                    if u:
                        self.refs.append((tag, attr, u[0], a))
            else:
                self.refs.append((tag, attr, val, a))

    def handle_startendtag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        self.handle_starttag(tag, attrs)

    def handle_endtag(self, tag: str) -> None:
        if tag == "title":
            self.in_title = False

    def handle_data(self, data: str) -> None:
        if self.in_title:
            self.title += data


def read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


def parse_text(text: str) -> Page:
    p = Page()
    p.feed(text)
    p.close()
    return p


def is_external(u: str) -> bool:
    return bool(urlsplit(u).scheme) or u.startswith("//") or u.startswith("\\\\")


def check_csp(policies: list[str], where: str, errs: list[str]) -> None:
    if not policies:
        errs.append(f"{where}: missing <meta http-equiv=\"Content-Security-Policy\">")
        return
    for csp in policies:
        directives: dict[str, list[str]] = {}
        for d in csp.split(";"):
            toks = d.split()
            if toks:
                directives.setdefault(toks[0].lower(), toks[1:])
        if "default-src" not in directives:
            errs.append(f"{where}: CSP has no default-src")
        for name, toks in directives.items():
            for t in toks:
                if t.lower() not in ALLOWED_CSP_TOKENS:
                    errs.append(f"{where}: CSP {name} allows {t} (only 'none' and 'self' are accepted)")


def resolve_local(page_dir: str, ref: str, root: str) -> str | None:
    """Return the file a relative reference points to (inside root), or None if missing or escaping."""
    path = unquote(urlsplit(ref).path)
    if path == "":
        return None
    real_root = os.path.realpath(root)
    target = os.path.realpath(os.path.join(page_dir, path))
    if os.path.commonpath([target, real_root]) != real_root:
        return None
    if os.path.isdir(target):
        target = os.path.join(target, "index.html")
    return target if os.path.isfile(target) else None


def strip_js_comments(text: str) -> str:
    """Remove // and /* */ comments while leaving string and template literals intact.

    Regex literals that contain an unescaped-looking // are not understood; the site's script
    has none and the checker would fail loudly (a false positive), never silently.
    """
    out: list[str] = []
    i, n = 0, len(text)
    while i < n:
        c = text[i]
        if c in "'\"`":
            j = i + 1
            while j < n and text[j] != c:
                j += 2 if text[j] == "\\" else 1
            out.append(text[i : j + 1])
            i = j + 1
        elif text.startswith("//", i):
            while i < n and text[i] != "\n":
                i += 1
        elif text.startswith("/*", i):
            j = text.find("*/", i + 2)
            i = n if j < 0 else j + 2
        else:
            out.append(c)
            i += 1
    return "".join(out)


def css_unescape(text: str) -> str:
    def sub(m: re.Match[str]) -> str:
        if m.group(1):
            try:
                return chr(int(m.group(1), 16))
            except (ValueError, OverflowError):
                return ""
        return m.group(2)

    return CSS_ESCAPE.sub(sub, text)


def css_refs(text: str) -> list[str]:
    text = css_unescape(CSS_COMMENT.sub("", text))
    refs: list[str] = []
    for m in CSS_URL.finditer(text):
        refs.append(next(g for g in m.groups() if g is not None))
    for m in CSS_IMPORT.finditer(text):
        refs.append(next(g for g in m.groups() if g is not None))
    for m in CSS_IMAGE_SET.finditer(text):
        for s in CSS_STRING.finditer(m.group(1)):
            refs.append(next(g for g in s.groups() if g is not None))
    # Any other string that looks like an absolute URL (for example in content:) is refused too.
    for s in CSS_STRING.finditer(text):
        v = next(g for g in s.groups() if g is not None)
        if re.match(r"(?:[a-zA-Z][a-zA-Z0-9+.-]*:)?//", v):
            refs.append(v)
    return refs


def check_css_text(text: str, rel: str, root: str, base_dir: str) -> list[str]:
    errs: list[str] = []
    for r in css_refs(text):
        r = r.strip()
        if not r or r.startswith("#"):
            continue
        if is_external(r):
            errs.append(f"{rel}: external or data URL in CSS: {r}")
        elif resolve_local(base_dir, r, root) is None:
            errs.append(f"{rel}: broken url() in CSS: {r}")
    return errs


def check_js(path: str, rel: str) -> list[str]:
    errs: list[str] = []
    text = strip_js_comments(read(path))
    for m in JS_URL.finditer(text):
        errs.append(f"{rel}: URL literal in JavaScript: {m.group(0).strip()}")
    for m in JS_NET.finditer(text):
        errs.append(f"{rel}: network or code-evaluation API in JavaScript: {m.group(0)}")
    return errs


def check_svg(path: str, rel: str, root: str) -> list[str]:
    errs: list[str] = []
    text = read(path)
    p = parse_text(text)
    for e in p.errors:
        errs.append(f"{rel}: {e}")
    if re.search(r"<script\b", text, re.I):
        errs.append(f"{rel}: <script> in SVG")
    if re.search(r"<foreignObject\b", text, re.I):
        errs.append(f"{rel}: <foreignObject> in SVG")
    for _tag, _attr, val, _ in p.refs:
        v = val.strip()
        if v.startswith("#") or not v:
            continue
        if is_external(v):
            errs.append(f"{rel}: external reference in SVG: {v}")
        elif resolve_local(os.path.dirname(path), v, root) is None:
            errs.append(f"{rel}: broken reference in SVG: {v}")
    for m in re.finditer(r"<style\b[^>]*>(.*?)</style>", text, re.S | re.I):
        errs.extend(check_css_text(m.group(1), rel, root, os.path.dirname(path)))
    return errs


def check_site(root: str, built: bool = False) -> list[str]:
    errs: list[str] = []
    if not os.path.isdir(root):
        return [f"{root}: not a directory"]
    pages = sorted(os.path.join(d, f) for d, _, fs in os.walk(root) for f in fs if f.endswith(".html"))
    if not pages:
        return [f"{root}: no .html files"]
    index = os.path.join(root, "index.html")
    if index not in pages:
        errs.append(f"{root}: no index.html")

    parsed: dict[str, Page] = {}
    for pg in pages:
        parsed[os.path.realpath(pg)] = parse_text(read(pg))

    ip = parsed.get(os.path.realpath(index))
    repo_url = ""
    site_base = ""
    if ip is not None:
        if ip.repo_href:
            repo_url = ip.repo_href.rstrip("/")
            if not repo_url.startswith("https://"):
                errs.append(f"index.html: REPO_URL must be https, got {repo_url}")
        else:
            errs.append("index.html: no element with id=\"repo\" declaring REPO_URL")
        if not ip.canonical:
            errs.append("index.html: missing <link rel=\"canonical\">")
        site_base = ip.canonical.rstrip("/")
        if site_base and not (site_base == PLACEHOLDER or site_base.startswith("https://")):
            errs.append(f"index.html: canonical must be https or the {PLACEHOLDER} placeholder: {ip.canonical}")

    def repo_ok(u: str) -> bool:
        return bool(repo_url) and (u == repo_url or u.startswith(repo_url + "/") or u.startswith(repo_url + "#"))

    def external_ok(tag: str, attr: str, u: str, attrs: dict[str, str]) -> bool:
        if tag == "a" and attr == "href":
            if repo_ok(u):
                return True
            s = urlsplit(u)
            return s.scheme == "https" and s.hostname in OUTBOUND_DOC_HOSTS
        if tag == "link" and attr == "href" and attrs.get("rel"):
            return repo_ok(u)
        return False

    for pg in pages:
        rel = os.path.relpath(pg, root)
        p = parsed[os.path.realpath(pg)]
        text = read(pg)
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
        if p.csp_late:
            errs.append(f"{rel}: the CSP <meta> comes after an element that loads a resource")
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
                kind = "input type=image" if im.get("input") else "img"
                errs.append(f"{rel}: <{kind} src=\"{im['src']}\"> has no alt attribute")
            for dim in ("width", "height"):
                v = im[dim]
                if not v or not v.strip().isdigit() or int(v) <= 0:
                    errs.append(f"{rel}: <img src=\"{im['src']}\"> needs a numeric {dim}")

        if built:
            if PLACEHOLDER in text or BASE_MARKER in text:
                errs.append(f"{rel}: deploy placeholder left in the built site")
        elif p.bases:
            errs.append(f"{rel}: <base> is only added at deploy time, not committed")
        for b in p.bases:
            if not (site_base and site_base != PLACEHOLDER and (b == site_base + "/" or b.startswith(site_base + "/"))):
                errs.append(f"{rel}: <base href> must be under the site address ({site_base or 'unknown'}): {b}")

        # Canonical and social URLs: all under the one site address (https, or the placeholder).
        for name, val in p.meta_urls + ([("canonical", p.canonical)] if p.canonical else []):
            if site_base:
                if not (val == site_base or val.startswith(site_base + "/")):
                    errs.append(f"{rel}: {name} is not under the site address {site_base}: {val}")
                    continue
                local = val[len(site_base):]
                if local.startswith("/") and "." in os.path.basename(local) and resolve_local(root, local.lstrip("/"), root) is None:
                    errs.append(f"{rel}: {name} points to a file that is not in the site: {local}")
            elif not (val.startswith("https://") or val.startswith(PLACEHOLDER)):
                errs.append(f"{rel}: {name} must be absolute (https or {PLACEHOLDER}): {val}")

        page_dir = os.path.dirname(os.path.abspath(pg))
        weight = size
        counted: set[str] = set()
        for tag, attr, val, attrs in p.refs:
            val = val.strip()
            if val == "":
                errs.append(f"{rel}: empty {attr} on <{tag}>")
                continue
            if attr == "ping":
                errs.append(f"{rel}: ping attribute on <{tag}> is not allowed: {val}")
                continue
            if tag == "link" and attr == "href" and "canonical" in attrs.get("rel", "").lower().split():
                continue  # checked above against the site address
            if tag == "base":
                continue
            if is_external(val):
                if not external_ok(tag, attr, val, attrs):
                    errs.append(f"{rel}: external URL not allowed in <{tag} {attr}>: {val}")
                continue
            frag = urlsplit(val).fragment
            if val.startswith("#"):
                target_page, tpath = p, pg
            else:
                tpath = resolve_local(page_dir, val, root)
                if tpath is None:
                    errs.append(f"{rel}: broken relative link <{tag} {attr}>: {val}")
                    continue
                target_page = parsed.get(os.path.realpath(tpath))
                if tag != "a" and tpath not in counted:
                    counted.add(tpath)
                    weight += os.path.getsize(tpath)
            if frag and target_page is not None and unquote(frag) not in target_page.ids:
                errs.append(f"{rel}: missing fragment id #{frag} in {os.path.relpath(tpath, root)} (from {val})")
        if weight > MAX_PAGE_BYTES:
            errs.append(f"{rel}: page weight {weight} bytes exceeds {MAX_PAGE_BYTES}")

    for d, _, fs in os.walk(root):
        for f in fs:
            path = os.path.join(d, f)
            rel = os.path.relpath(path, root)
            if f.endswith(".css"):
                errs.extend(check_css_text(read(path), rel, root, os.path.dirname(path)))
            elif f.endswith(".js"):
                errs.extend(check_js(path, rel))
            elif f.endswith(".svg"):
                errs.extend(check_svg(path, rel, root))
    return errs


def main(argv: list[str]) -> int:
    built = "--built" in argv[1:]
    args = [a for a in argv[1:] if a != "--built"]
    root = args[0] if args else os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "site")
    errs = check_site(os.path.normpath(root), built=built)
    for e in errs:
        print(f"check-site: {e}", file=sys.stderr)
    if errs:
        print(f"check-site: {len(errs)} problem(s)", file=sys.stderr)
        return 1
    print("check-site: ok")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

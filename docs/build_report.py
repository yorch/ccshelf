#!/usr/bin/env python3
"""Build docs/report.html from the Markdown notes.

Usage:
  python3 docs/build_report.py           write docs/report.html
  python3 docs/build_report.py --check   exit 1 if docs/report.html is out of date

Standard library only. The Markdown is the source of truth; never edit report.html by hand.
Inputs: the .md files listed in TABS, docs/report/template.html, docs/report/style.css,
docs/report/widgets/*.html (inserted where a Markdown file has `<!-- widget: name -->`).
Markdown subset: headings, paragraphs, lists (incl. task lists), GFM tables, fenced code,
inline code, bold, italic, strikethrough, links, blockquotes, and the {V} {R} {U} markers.
"""
import hashlib
import html
import json
import os
import re
import sys

DOCS = os.path.dirname(os.path.abspath(__file__))

# (id, group, label, markdown file, options)
TABS = [
    ("overview", "Start", "Overview", "README.md", {"skip_sections": ["Glossary"], "hero": True}),
    ("decisions", "Start", "Decisions", "DECISIONS.md", {}),
    ("roadmap", "Start", "Roadmap", "design/roadmap.md", {}),
    ("glossary", "Start", "Glossary", "README.md", {"only_section": "Glossary"}),
    ("architecture", "Design", "Architecture", "design/architecture.md", {}),
    ("launcher", "Design", "Launcher", "design/launcher.md", {}),
    ("profiles", "Design", "Profiles", "design/profiles.md", {}),
    ("cli", "Design", "CLI", "design/cli.md", {}),
    ("catalog", "Design", "Catalog and org repo", "design/catalog-and-org-repo.md", {}),
    ("security", "Design", "Security", "design/security.md", {}),
    ("platform", "Design", "Platform", "design/platform.md", {}),
    ("release", "Design", "Release", "design/release.md", {}),
    ("update", "Design", "Updating", "design/update.md", {}),
    ("project", "Design", "Project", "design/project.md", {}),
    ("workflows", "Design", "Workflows", "design/workflows.md", {"chips": True}),
    ("context", "Research", "Context", "research/context.md", {}),
    ("landscape", "Research", "Landscape", "research/landscape.md", {}),
    ("options", "Research", "Options and reviews", "research/options-and-reviews.md", {}),
    ("stage0", "Research", "Stage 0", "research/stage0.md", {}),
    ("instr0", "Research", "Stage 0: instructions", "research/instructions-stage0.md", {}),
    ("style0", "Research", "Stage 0: output style", "research/output-style-stage0.md", {}),
    ("adopt", "Research", "Adopt or build", "research/adopt-or-build.md", {}),
    ("routing", "Research", "Routing eval protocol", "research/routing-eval-protocol.md", {}),
]
# Markdown file (relative to docs/) -> tab id, used to rewrite links between notes
FILE_TO_TAB = {}
for _id, _g, _l, _f, _o in TABS:
    if "only_section" not in _o:
        FILE_TO_TAB[_f] = _id


def esc(s):
    return html.escape(s, quote=False)


def slug(s):
    s = re.sub(r"<[^>]+>", "", s)
    s = re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")
    return s or "x"


# ---------------------------------------------------------------- inline

def resolve_link(url, cur_file):
    """Return an href for a link, or None to render as plain text."""
    if re.match(r"^(https?|mailto):", url):
        return url
    path, _, _frag = url.partition("#")
    if not path:
        return None
    target = os.path.normpath(os.path.join(os.path.dirname(cur_file), path))
    tab = FILE_TO_TAB.get(target)
    return "#" + tab if tab else None


def inline(text, cur_file):
    store = []

    def stash(m):
        store.append("<code>" + esc(m.group(2).strip()) + "</code>")
        return "\x00%d\x00" % (len(store) - 1)

    text = re.sub(r"(`+)(.+?)\1(?!`)", stash, text)
    text = esc(text)

    def link(m):
        label, url = m.group(1), html.unescape(m.group(2))
        href = resolve_link(url, cur_file)
        if href is None:
            return label
        ext = href.startswith("http")
        return '<a href="%s"%s>%s</a>' % (html.escape(href, quote=True), ' rel="noopener"' if ext else "", label)

    text = re.sub(r"\[([^\]]+)\]\(([^)\s]+)\)", link, text)
    text = re.sub(r"\*\*(.+?)\*\*", r"<strong>\1</strong>", text)
    text = re.sub(r"(?<![\*\w])\*(?!\s)(.+?)(?<!\s)\*(?![\*\w])", r"<em>\1</em>", text)
    text = re.sub(r"~~(.+?)~~", r"<s>\1</s>", text)
    text = re.sub(r"\{([VRU])\}", r'<span class="\1">\1</span>', text)
    return re.sub(r"\x00(\d+)\x00", lambda m: store[int(m.group(1))], text)


# ---------------------------------------------------------------- blocks

LIST_RE = re.compile(r"^(\s*)([-*]|\d+[.)])\s+(.*)$")
TABLE_SEP = re.compile(r"^\s*\|?[\s:\-|]+\|?\s*$")


def split_row(line):
    line = line.strip()
    if line.startswith("|"):
        line = line[1:]
    if line.endswith("|") and not line.endswith("\\|"):
        line = line[:-1]
    cells, cur, tick = [], [], 0
    i = 0
    while i < len(line):
        ch = line[i]
        if ch == "`":
            j = i
            while j < len(line) and line[j] == "`":
                j += 1
            n = j - i
            cur.append(line[i:j])
            tick = 0 if tick == n else (n if tick == 0 else tick)
            i = j
            continue
        if ch == "\\" and i + 1 < len(line) and line[i + 1] == "|":
            cur.append("|")
            i += 2
            continue
        if ch == "|" and tick == 0:
            cells.append("".join(cur).strip())
            cur = []
        else:
            cur.append(ch)
        i += 1
    cells.append("".join(cur).strip())
    return cells


def is_block_start(lines, i):
    l = lines[i]
    return (
        l.startswith("```")
        or re.match(r"^#{1,6}\s", l)
        or LIST_RE.match(l)
        or (l.lstrip().startswith("|") and i + 1 < len(lines) and TABLE_SEP.match(lines[i + 1]) and "-" in lines[i + 1])
        or re.match(r"^\s*<!--\s*widget:", l)
        or re.match(r"^(-{3,}|\*{3,})\s*$", l)
        or l.startswith(">")
    )


def parse_blocks(lines):
    out, i, n = [], 0, len(lines)
    while i < n:
        line = lines[i]
        if not line.strip():
            i += 1
            continue
        m = re.match(r"^\s*<!--\s*widget:\s*([\w-]+)\s*-->\s*$", line)
        if m:
            out.append(("widget", m.group(1)))
            i += 1
            continue
        if line.startswith("```"):
            lang, j, buf = line[3:].strip(), i + 1, []
            while j < n and not lines[j].startswith("```"):
                buf.append(lines[j])
                j += 1
            out.append(("code", lang, "\n".join(buf)))
            i = j + 1
            continue
        m = re.match(r"^(#{1,6})\s+(.*)$", line)
        if m:
            out.append(("h", len(m.group(1)), m.group(2).strip()))
            i += 1
            continue
        if re.match(r"^(-{3,}|\*{3,})\s*$", line):
            out.append(("hr",))
            i += 1
            continue
        if line.startswith(">"):
            buf = []
            while i < n and lines[i].startswith(">"):
                buf.append(re.sub(r"^>\s?", "", lines[i]))
                i += 1
            out.append(("quote", parse_blocks(buf)))
            continue
        if line.lstrip().startswith("|") and i + 1 < n and TABLE_SEP.match(lines[i + 1]) and "-" in lines[i + 1]:
            head = split_row(line)
            i += 2
            rows = []
            while i < n and lines[i].lstrip().startswith("|"):
                rows.append(split_row(lines[i]))
                i += 1
            out.append(("table", head, rows))
            continue
        m = LIST_RE.match(line)
        if m:
            base = len(m.group(1))
            ordered = m.group(2)[0].isdigit()
            items = []
            while i < n:
                m = LIST_RE.match(lines[i])
                if not m or len(m.group(1)) != base:
                    break
                content_col = base + len(m.group(2)) + 1
                buf = [m.group(3)]
                i += 1
                while i < n:
                    l = lines[i]
                    if not l.strip():
                        j = i
                        while j < n and not lines[j].strip():
                            j += 1
                        if j < n:
                            ind = len(lines[j]) - len(lines[j].lstrip())
                            nm = LIST_RE.match(lines[j])
                            if ind > base and not (nm and len(nm.group(1)) <= base):
                                buf.extend([""] * (j - i))
                                i = j
                                continue
                        break
                    ind = len(l) - len(l.lstrip())
                    if ind > base:
                        buf.append(l[min(ind, content_col):])
                        i += 1
                        continue
                    if not LIST_RE.match(l) and not is_block_start(lines, i):
                        buf.append(l.strip())  # lazy continuation
                        i += 1
                        continue
                    break
                task = None
                tm = re.match(r"^\[( |x|X)\]\s+(.*)$", buf[0])
                if tm:
                    task = tm.group(1).lower() == "x"
                    buf[0] = tm.group(2)
                items.append((task, parse_blocks(buf)))
            out.append(("list", ordered, items))
            continue
        buf = [line.strip()]
        i += 1
        while i < n and lines[i].strip() and not is_block_start(lines, i):
            buf.append(lines[i].strip())
            i += 1
        out.append(("p", " ".join(buf)))
    return out


# ---------------------------------------------------------------- render

class Ctx:
    def __init__(self, tab_id, cur_file, widgets):
        self.tab_id, self.cur_file, self.widgets = tab_id, cur_file, widgets
        self.used = set()


def render_item_blocks(blocks, ctx):
    if blocks and blocks[0][0] == "p":
        first = inline(blocks[0][1], ctx.cur_file)
        rest = render_blocks(blocks[1:], ctx, 0)
        return first + ("\n" + rest if rest else "")
    return render_blocks(blocks, ctx, 0)


def render_blocks(blocks, ctx, level_shift=0, chips=False):
    out = []
    for b in blocks:
        k = b[0]
        if k == "p":
            out.append("<p>%s</p>" % inline(b[1], ctx.cur_file))
        elif k == "h":
            lvl = b[1] + 1 + level_shift  # md ## -> h3
            lvl = min(lvl, 6)
            hid = ctx.tab_id + "-" + slug(b[2])
            n, base = 2, hid
            while hid in ctx.used:
                hid = "%s-%d" % (base, n)
                n += 1
            ctx.used.add(hid)
            out.append('<h%d id="%s">%s</h%d>' % (lvl, hid, inline(b[2], ctx.cur_file), lvl))
        elif k == "code":
            out.append("<pre><code>%s</code></pre>" % esc(b[2]))
        elif k == "hr":
            out.append("<hr>")
        elif k == "quote":
            out.append("<blockquote>%s</blockquote>" % render_blocks(b[1], ctx))
        elif k == "widget":
            frag = ctx.widgets.get(b[1])
            if frag is None:
                raise SystemExit("unknown widget %r in %s" % (b[1], ctx.cur_file))
            out.append(frag)
        elif k == "table":
            head = "".join("<th>%s</th>" % inline(c, ctx.cur_file) for c in b[1])
            body = []
            for r in b[2]:
                r = (r + [""] * len(b[1]))[: len(b[1])]
                body.append("<tr>%s</tr>" % "".join(
                    '<td%s>%s</td>' % (' class="nw"' if re.match(r"^([A-Z]-\d+|\d{4}-\d{2}-\d{2})$", c.strip()) else "", inline(c, ctx.cur_file))
                    for c in r))
            out.append("<table><thead><tr>%s</tr></thead><tbody>\n%s\n</tbody></table>" % (head, "\n".join(body)))
        elif k == "list":
            tag = "ol" if b[1] else "ul"
            items = []
            for task, blocks2 in b[2]:
                inner = render_item_blocks(blocks2, ctx)
                if task is None:
                    items.append("<li>%s</li>" % inner)
                else:
                    items.append('<li class="task"><label><input type="checkbox"%s> %s</label></li>' % (" checked" if task else "", inner))
            out.append("<%s>\n%s\n</%s>" % (tag, "\n".join(items), tag))
    return "\n".join(out)


def split_sections(blocks):
    """Split top-level blocks into (intro_blocks, [(h2_title, blocks)]) by md '##' headings."""
    intro, secs, cur = [], [], None
    for b in blocks:
        if b[0] == "h" and b[1] == 2:
            cur = (b[2], [])
            secs.append(cur)
        elif cur is None:
            intro.append(b)
        else:
            cur[1].append(b)
    return intro, secs


def render_tab(tab, widgets):
    tid, _grp, label, fname, opts = tab
    text = open(os.path.join(DOCS, fname), encoding="utf-8").read()
    blocks = parse_blocks(text.split("\n"))
    title = label
    if blocks and blocks[0][0] == "h" and blocks[0][1] == 1:
        title = blocks[0][2]
        blocks = blocks[1:]
    intro, secs = split_sections(blocks)
    ctx = Ctx(tid, fname, widgets)
    parts = []
    if opts.get("only_section"):
        sec = [s for s in secs if s[0] == opts["only_section"]]
        if not sec:
            raise SystemExit("section %r not found in %s" % (opts["only_section"], fname))
        title = sec[0][0]
        parts.append("<h2>%s</h2>" % inline(title, fname))
        parts.append(render_blocks(sec[0][1], ctx, -1))
    else:
        if opts.get("hero"):
            parts.append(widgets["hero"])
        else:
            parts.append("<h2>%s</h2>" % inline(title, fname))
        parts.append(render_blocks(intro, ctx))
        skip = set(opts.get("skip_sections", []))
        if opts.get("chips"):
            chips, panes = [], []
            for name, sb in secs:
                pid = "%s-pane-%d" % (tid, len(panes) + 1)
                chips.append('<button type="button" data-pane="%s">%s</button>' % (pid, inline(re.sub(r"^\d+\.\s*", "", name), fname)))
                panes.append('<div class="pane" id="%s"><h3 id="%s-h-%d">%s</h3>\n%s</div>' % (pid, tid, len(panes) + 1, inline(name, fname), render_blocks(sb, ctx)))
            parts.append('<div class="chips" data-chips>%s</div>' % "".join(chips))
            parts.extend(panes)
        else:
            for name, sb in secs:
                if name in skip:
                    continue
                parts.append(render_blocks([("h", 2, name)] + sb, ctx))
    return '<section id="%s">\n%s\n</section>\n' % (tid, "\n".join(p for p in parts if p))


def load_widgets():
    d = os.path.join(DOCS, "report", "widgets")
    return {f[:-5]: open(os.path.join(d, f), encoding="utf-8").read() for f in sorted(os.listdir(d)) if f.endswith(".html")}


def source_hash():
    h = hashlib.sha256()
    files = sorted({t[3] for t in TABS})
    for f in files:
        h.update(f.encode())
        h.update(open(os.path.join(DOCS, f), "rb").read())
    for f in ("report/template.html", "report/style.css"):
        h.update(open(os.path.join(DOCS, f), "rb").read())
    for f in sorted(os.listdir(os.path.join(DOCS, "report", "widgets"))):
        h.update(open(os.path.join(DOCS, "report", "widgets", f), "rb").read())
    h.update(open(os.path.abspath(__file__), "rb").read())
    return h.hexdigest()[:8]


def build():
    widgets = load_widgets()
    sections = "\n".join(render_tab(t, widgets) for t in TABS)
    tabs_json = json.dumps([{"id": t[0], "group": t[1], "label": t[2]} for t in TABS], ensure_ascii=False)
    tpl = open(os.path.join(DOCS, "report", "template.html"), encoding="utf-8").read()
    css = open(os.path.join(DOCS, "report", "style.css"), encoding="utf-8").read().rstrip()
    out = tpl.replace("/*STYLE*/", css).replace("/*HASH*/", source_hash()).replace("/*TABS*/", tabs_json).replace("/*SECTIONS*/", sections)
    return out


def main():
    out = build()
    path = os.path.join(DOCS, "report.html")
    if "--check" in sys.argv:
        cur = open(path, encoding="utf-8").read() if os.path.exists(path) else ""
        if cur != out:
            print("docs/report.html is out of date: run python3 docs/build_report.py", file=sys.stderr)
            sys.exit(1)
        print("docs/report.html is up to date")
        return
    open(path, "w", encoding="utf-8").write(out)
    print("wrote %s (%d bytes)" % (path, len(out)))


if __name__ == "__main__":
    main()

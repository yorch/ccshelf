#!/usr/bin/env python3
"""Unit tests for build_docs.py and gen_cli_reference.py. Run: python3 -I scripts/test_build_docs.py

Set UPDATE_GOLDEN=1 to rewrite scripts/testdata/build_docs_small.html after a deliberate change.
"""
import importlib.util
import os
import shutil
import tempfile
import unittest
from html.parser import HTMLParser

HERE = os.path.dirname(os.path.abspath(__file__))


def slurp(path, mode="rb"):
    with open(path, mode) as f:
        return f.read()


def load(name, fname):
    spec = importlib.util.spec_from_file_location(name, os.path.join(HERE, fname))
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


bd = load("build_docs_under_test", "build_docs.py")
gen = load("gen_cli_reference_under_test", "gen_cli_reference.py")
cs = load("check_site_for_docs_tests", "check_site.py")

REPO = "https://example.test/org/repo"
SMALL_PAGES = [
    ("Start", "Overview", "README.md", "index.html", "The overview."),
    ("Design", "Alpha", "design/alpha.md", "design/alpha.html", "Alpha page."),
    ("Research", "Beta", "research/beta.md", "research/beta.html", ""),
]
README = """# Small docs

Intro with a [design page](design/alpha.md#second-part), a [research page](research/beta.md), \
a [source file](../internal/thing.go), a [folder](../internal/pkg/), the [site](../site/index.html), \
[docs](https://code.claude.com/docs/en/overview), [other](https://other.example.test/x/y) and [repo](https://example.test/org/repo/issues).
"""
ALPHA = """# Alpha design

Claim one {V}. Claim two {R}. Claim three {U}.

## First part

Text with `code`, **bold**, *em* and ~~gone~~.

- [x] done task
- [ ] open task
- plain item

```sh
echo "a < b"
```

## Second part

| ID | Date | Decision |
|---|---|---|
| D-01 | 2026-10-06 | Do it |
| D-02 | 2026-10-07 | Do more |

### Sub heading

## Second part

Back to [the overview](../README.md).

<!-- widget: token-bars -->
"""
BETA = """# Beta research

Plain page with [a link to a section](#beta-heading) and no table.

## Beta heading

Done.
"""


class Tree(unittest.TestCase):
    def setUp(self):
        self.d = tempfile.mkdtemp()
        self.docs = os.path.join(self.d, "docs")
        for rel, text in (("README.md", README), ("design/alpha.md", ALPHA), ("research/beta.md", BETA)):
            self.write(os.path.join("docs", rel), text)
        self.write("internal/thing.go", "package x\n")
        os.makedirs(os.path.join(self.d, "internal", "pkg"))
        self.write("docs/report.html", "x")
        self.out = os.path.join(self.d, "out")

    def tearDown(self):
        shutil.rmtree(self.d, ignore_errors=True)

    def write(self, rel, text):
        p = os.path.join(self.d, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w", encoding="utf-8") as f:
            f.write(text)

    def builder(self, pages=None):
        return bd.Builder(docs_dir=self.docs, repo_root=self.d, pages=pages or SMALL_PAGES, repo_url=REPO, intro_md=None)

    def build(self, pages=None):
        b = self.builder(pages)
        b.build(self.out)
        return b

    def page(self, rel):
        with open(os.path.join(self.out, "docs", *rel.split("/")), encoding="utf-8") as f:
            return f.read()


class Links(Tree):
    def test_internal_markdown_links_become_pages_with_fragments(self):
        self.build()
        home = self.page("index.html")
        self.assertIn('href="design/alpha.html#second-part"', home)
        self.assertIn('href="research/beta.html"', home)
        self.assertIn('href="../design/alpha.html#second-part"'.replace("../", ""), home)
        self.assertIn('<a href="../index.html">the overview</a>', self.page("design/alpha.html"))

    def test_repo_files_and_folders_use_the_repo_url(self):
        self.build()
        home = self.page("index.html")
        self.assertIn('href="%s/blob/main/internal/thing.go" rel="external noopener"' % REPO, home)
        self.assertIn('href="%s/tree/main/internal/pkg/" rel="external noopener"' % REPO, home)

    def test_site_home_link_is_relative(self):
        self.build()
        self.assertIn('<a href="../index.html">site</a>', self.page("index.html"))

    def test_external_links_only_for_repo_and_doc_hosts(self):
        self.build()
        home = self.page("index.html")
        self.assertIn('href="https://code.claude.com/docs/en/overview" rel="external noopener"', home)
        self.assertIn('href="%s/issues"' % REPO, home)
        self.assertNotIn("other.example.test/x/y\"", home)
        self.assertIn('other <span class="xurl">(<code>other.example.test/x/y</code>)</span>', home)

    def test_missing_repo_target_fails_the_build(self):
        self.write("docs/design/alpha.md", ALPHA + "\n[gone](../../nowhere.go)\n")
        with self.assertRaises(bd.BuildError):
            self.build()

    def test_link_out_of_the_repository_fails(self):
        self.write("docs/README.md", "# T\n\n[x](../../../../etc/passwd)\n")
        with self.assertRaises(bd.BuildError):
            self.build()


class Rendering(Tree):
    def setUp(self):
        super().setUp()
        self.build()
        self.alpha = self.page("design/alpha.html")

    def test_one_h1_and_title(self):
        self.assertEqual(self.alpha.count("<h1>"), 1)
        self.assertIn("<h1>Alpha design</h1>", self.alpha)
        self.assertIn("<title>Alpha: ccshelf docs</title>", self.alpha)

    def test_heading_anchors_are_unique(self):
        self.assertIn('<h2 id="second-part">', self.alpha)
        self.assertIn('<h2 id="second-part-2">', self.alpha)
        self.assertIn('href="#first-part" aria-label="Link to this section: First part"', self.alpha)

    def test_markers_have_accessible_labels(self):
        for word in ("verified", "reported", "unverified"):
            self.assertIn('<span class="sr">%s</span>' % word, self.alpha)
        self.assertNotIn("{V}", self.alpha)

    def test_task_lists_are_read_only(self):
        self.assertIn('<input type="checkbox" disabled checked aria-label="done">', self.alpha)
        self.assertIn('<input type="checkbox" disabled aria-label="not done">', self.alpha)

    def test_code_blocks_have_copy_buttons_and_escape(self):
        self.assertIn('<button type="button" class="copy" data-copy="code-1" hidden>Copy</button>', self.alpha)
        self.assertIn("echo \"a &lt; b\"", self.alpha)

    def test_tables_scroll_in_a_wrapper_and_rows_get_ids(self):
        self.assertIn('<div class="tablewrap" role="region"', self.alpha)
        self.assertIn('<tr id="d-01">', self.alpha)
        self.assertIn('<td class="nw">2026-10-06</td>', self.alpha)

    def test_widgets_become_notes(self):
        self.assertIn('class="widget-note"', self.alpha)
        self.assertIn("%s/blob/main/docs/report.html" % REPO, self.alpha)
        self.assertNotIn("<script>", self.alpha)

    def test_nav_order_and_current_page(self):
        nav = self.alpha[self.alpha.index('<nav id="docnav"'): self.alpha.index("</nav>", self.alpha.index('<nav id="docnav"'))]
        self.assertLess(nav.index("Overview"), nav.index("Alpha"))
        self.assertLess(nav.index("Alpha"), nav.index("Beta"))
        self.assertIn('<a href="alpha.html" aria-current="page">Alpha</a>', nav)
        self.assertIn('<a href="../research/beta.html">Beta</a>', nav)

    def test_toc_lists_h2_and_nested_h3(self):
        toc = self.alpha[self.alpha.index('<nav class="toc"'):]
        toc = toc[: toc.index("</nav>")]
        self.assertIn('<a href="#first-part">First part</a>', toc)
        self.assertLess(toc.index("#second-part\""), toc.index("#sub-heading"))
        self.assertEqual(toc.count("<ul>"), toc.count("</ul>"))

    def test_toc_and_prev_next_and_breadcrumb(self):
        self.assertIn('rel="prev" href="../index.html"', self.alpha)
        self.assertIn('rel="next" href="../research/beta.html"', self.alpha)
        self.assertIn('<li>Design</li><li aria-current="page">Alpha</li>', self.alpha)

    def test_page_is_balanced_html(self):
        stack = []
        void = {"meta", "link", "input", "br", "hr", "img", "rect"}

        class P(HTMLParser):
            def handle_starttag(s, tag, attrs):
                if tag not in void:
                    stack.append(tag)

            def handle_startendtag(s, tag, attrs):
                pass

            def handle_endtag(s, tag):
                assert stack and stack[-1] == tag, (tag, stack[-3:])
                stack.pop()

        for rel in ("index.html", "design/alpha.html", "research/beta.html"):
            stack.clear()
            P().feed(self.page(rel))
            self.assertEqual(stack, [], rel)

    def test_search_index_lists_sections_and_rows(self):
        import json
        raw = slurp(os.path.join(self.out, "docs", "search-index.js"), "r")
        data = json.loads(raw[len(bd.cs.SEARCH_INDEX_PREFIX): -2])
        alpha = [p for p in data["pages"] if p["u"] == "design/alpha.html"][0]
        self.assertIn(["d-01", "D-01: Do it", "2026-10-06"], alpha["s"])
        self.assertTrue(any(s[0] == "first-part" for s in alpha["s"]))


class Structure(Tree):
    def test_a_markdown_file_missing_from_pages_fails(self):
        self.write("docs/design/extra.md", "# Extra\n")
        with self.assertRaises(bd.BuildError):
            self.build()

    def test_a_listed_page_without_a_file_fails(self):
        with self.assertRaises(bd.BuildError):
            self.build(SMALL_PAGES + [("Design", "Ghost", "design/ghost.md", "design/ghost.html", "")])

    def test_building_twice_gives_identical_bytes(self):
        self.build()
        first = {}
        for d, _, fs in os.walk(self.out):
            for f in fs:
                p = os.path.join(d, f)
                first[os.path.relpath(p, self.out)] = slurp(p)
        shutil.rmtree(self.out)
        self.build()
        second = {}
        for d, _, fs in os.walk(self.out):
            for f in fs:
                p = os.path.join(d, f)
                second[os.path.relpath(p, self.out)] = slurp(p)
        self.assertEqual(first, second)

    def test_golden_snapshot_of_a_small_page(self):
        self.build()
        got = self.page("research/beta.html")
        golden = os.path.join(HERE, "testdata", "build_docs_small.html")
        if os.environ.get("UPDATE_GOLDEN"):
            os.makedirs(os.path.dirname(golden), exist_ok=True)
            with open(golden, "w", encoding="utf-8", newline="\n") as f:
                f.write(got)
        with open(golden, encoding="utf-8") as f:
            self.assertEqual(got, f.read(), "run with UPDATE_GOLDEN=1 if the change is deliberate")


class RealDocs(unittest.TestCase):
    """The real Markdown builds, twice the same, and the result passes the site checker."""

    def build_into(self, out):
        shutil.copytree(os.path.join(bd.ROOT, "site"), out)
        bd.Builder().build(out)

    def test_real_docs_build_deterministically_and_pass_the_checker(self):
        with tempfile.TemporaryDirectory() as a, tempfile.TemporaryDirectory() as b:
            oa, ob = os.path.join(a, "o"), os.path.join(b, "o")
            self.build_into(oa)
            self.build_into(ob)
            self.assertEqual(cs.check_site(oa), [])
            for d, _, fs in os.walk(os.path.join(oa, "docs")):
                for f in fs:
                    pa = os.path.join(d, f)
                    pb = os.path.join(ob, os.path.relpath(pa, oa))
                    self.assertEqual(slurp(pa), slurp(pb), pa)

    def test_every_docs_markdown_file_is_a_page(self):
        b = bd.Builder()
        b.check_sources()


HELP = """Start claude with a profile.

Text that continues here
on a second line.

  ccshelf run --example   an indented example

Usage:
  ccshelf run [profile] [-- claude args]

Aliases:
  run, go

Examples:
  ccshelf run sre
  ccshelf run sre -- -p "x"

Available Commands:
  sub         A subcommand

Flags:
  -h, --help          help for run
      --limit int     show at most this many (default 10)
      --name string   a name

Global Flags:
      --json   machine-readable output

Use "ccshelf run [command] --help" for more information about a command.
"""


class CliReference(unittest.TestCase):
    def test_parse_help(self):
        h = gen.parse_help(HELP)
        self.assertEqual(h.usage, ["ccshelf run [profile] [-- claude args]"])
        self.assertEqual(h.aliases, ["run", "go"])
        self.assertEqual(h.examples, ["ccshelf run sre", 'ccshelf run sre -- -p "x"'])
        self.assertEqual(h.commands, [("sub", "A subcommand")])
        self.assertEqual(h.flags[1], ("", "--limit", "int", "show at most this many (default 10)"))
        self.assertEqual(h.flags[0][:2], ("-h", "--help"))
        self.assertEqual(h.global_flags, [("", "--json", "", "machine-readable output")])

    def test_unknown_section_or_flag_line_fails_loudly(self):
        with self.assertRaises(ValueError):
            gen.parse_help("x\n\nUsage:\n  a\n\nFlags:\n  garbage line\n")

    def test_description_becomes_paragraphs_and_code(self):
        md = "\n".join(gen.desc_blocks(gen.parse_help(HELP).desc))
        self.assertIn("Text that continues here on a second line.", md)
        self.assertIn("```text\nccshelf run --example   an indented example\n```", md)

    def test_paragraph_that_would_become_a_list_is_refused(self):
        with self.assertRaises(ValueError):
            gen.desc_blocks(["- not a list"])

    def test_flag_table_escapes_pipes(self):
        rows = gen.flag_table([("", "--x", "string", "a|b")])
        self.assertIn("a\\|b", rows[2])

    def test_exit_codes_come_from_the_source(self):
        codes = dict(gen.exit_codes())
        self.assertEqual(codes[0], "success")
        self.assertIn(130, codes)

    def test_environment_is_scrubbed(self):
        env = gen.scrub_env("/tmp/h")
        self.assertEqual(env["HOME"], "/tmp/h")
        self.assertNotIn("CLAUDE_CONFIG_DIR", env)
        self.assertEqual(env["PATH"], os.path.join("/tmp/h", "empty"))


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Unit tests for check_site.py. Run: python3 -I scripts/test_check_site.py

Each test starts from a site that passes, breaks one thing, and expects the matching complaint.
"""
import importlib.util
import os
import shutil
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("check_site", os.path.join(HERE, "check_site.py"))
cs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cs)

CSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; base-uri 'none'"
CSP_META = '<meta http-equiv="Content-Security-Policy" content="%s">' % CSP
GOOD = """<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="{csp}">
<meta name="referrer" content="no-referrer">
<title>Test site</title><meta name="description" content="A test.">
<link rel="canonical" href="__SITE_URL__/">
<meta property="og:url" content="__SITE_URL__/">
<meta property="og:image" content="__SITE_URL__/assets/a.svg">
<meta name="twitter:image" content="__SITE_URL__/assets/a.svg">
<link rel="stylesheet" href="assets/site.css"><script src="assets/site.js"></script></head>
<body><h1 id="top">Hi</h1><a href="#sec">Section</a><h2 id="sec">Sec</h2>
<a href="other.html#o">Other</a>
<img src="assets/a.svg" alt="An image" width="10" height="10">
<a id="repo" href="https://example.test/org/repo">repo</a>
<a href="https://example.test/org/repo/blob/main/x.md">doc</a>
<a href="https://code.claude.com/docs">docs</a>
</body></html>"""
OTHER = (
    GOOD.replace('id="top"', 'id="o"')
    .replace("<title>Test site", "<title>Other page")
    .replace('<link rel="canonical" href="__SITE_URL__/">', "")
    .replace('<a id="repo" href="https://example.test/org/repo">repo</a>', "")
)


class Base(unittest.TestCase):
    def setUp(self):
        self.d = tempfile.mkdtemp()
        os.makedirs(os.path.join(self.d, "assets"))
        self.write("index.html", GOOD.format(csp=CSP))
        self.write("other.html", OTHER.format(csp=CSP))
        self.write("assets/site.css", "body{background:url(a.svg)}")
        self.write("assets/site.js", "var x = 1;")
        self.write("assets/a.svg", '<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>')

    def tearDown(self):
        shutil.rmtree(self.d, ignore_errors=True)

    def write(self, rel, text):
        with open(os.path.join(self.d, rel), "w", encoding="utf-8") as f:
            f.write(text)

    def mutate(self, old, new, rel="index.html"):
        p = os.path.join(self.d, rel)
        with open(p, encoding="utf-8") as f:
            s = f.read()
        self.assertIn(old, s)
        self.write(rel, s.replace(old, new))

    def errs(self, built=False):
        return cs.check_site(self.d, built=built)

    def assertFails(self, needle, built=False):
        e = self.errs(built)
        self.assertTrue(any(needle in x for x in e), f"expected {needle!r} in {e}")

    def assertClean(self):
        self.assertEqual(self.errs(), [])


class Links(Base):
    def test_good_site_passes(self):
        self.assertClean()

    def test_external_src(self):
        self.mutate('<script src="assets/site.js">', '<script src="https://cdn.example.test/x.js">')
        self.assertFails("external URL not allowed")

    def test_protocol_relative(self):
        self.mutate('href="assets/site.css"', 'href="//cdn.example.test/x.css"')
        self.assertFails("external URL not allowed")

    def test_other_external_anchor(self):
        self.mutate("</body>", '<a href="https://evil.test/">x</a></body>')
        self.assertFails("external URL not allowed")

    def test_javascript_and_data_schemes(self):
        self.mutate("</body>", '<a href="javascript:alert(1)">x</a></body>')
        self.assertFails("external URL not allowed")
        self.mutate("</body>", '<a href="data:text/html,hi">y</a></body>')
        self.assertFails("data:text/html")

    def test_outbound_host_only_for_anchors(self):
        self.mutate("</body>", '<img src="https://code.claude.com/x.png" alt="x" width="1" height="1"></body>')
        self.assertFails("external URL not allowed")

    def test_author_credit_link(self):
        self.mutate("</body>", '<a href="https://github.com/yorch">Jorge Barnaby</a></body>')
        self.assertClean()
        # only the exact profile address, only on an anchor
        for bad in ("https://github.com/yorch/other", "http://github.com/yorch", "https://github.com/yorchx",
                    "https://github.com/yorch@evil.test"):
            self.setUp()
            self.mutate("</body>", f'<a href="{bad}">x</a></body>')
            self.assertFails("external URL not allowed")
        self.setUp()
        self.mutate("</body>", '<img src="https://github.com/yorch" alt="x" width="1" height="1"></body>')
        self.assertFails("external URL not allowed")

    def test_repo_prefix_boundary(self):
        self.mutate("</body>", '<a href="https://example.test/org/repo-evil/x">x</a></body>')
        self.assertFails("external URL not allowed")

    def test_repo_url_only_on_anchor_and_link(self):
        self.mutate("</body>", '<img src="https://example.test/org/repo/x.png" alt="x" width="1" height="1"></body>')
        self.assertFails("external URL not allowed in <img src>")
        self.mutate("</body>", '<form action="https://example.test/org/repo/x"></form></body>')
        self.assertFails("external URL not allowed in <form action>")

    def test_link_to_repo_host_must_start_with_repo_url(self):
        self.mutate("</body>", '<a href="https://example.test/org/other">x</a></body>')
        self.assertFails("external URL not allowed")

    def test_ping_attribute(self):
        self.mutate("</body>", '<a href="#sec" ping="https://t.example.test/p">x</a></body>')
        self.assertFails("ping attribute")

    def test_meta_refresh(self):
        self.mutate("</head>", '<meta http-equiv="refresh" content="0;url=https://evil.test/"></head>')
        self.assertFails("http-equiv=refresh")

    def test_srcset_external(self):
        self.mutate("</body>", '<img src="assets/a.svg" srcset="https://x.example.test/a.png 2x" alt="x" width="1" height="1"></body>')
        self.assertFails("<img srcset>")

    def test_inline_svg_image_and_use(self):
        self.mutate("</body>", '<svg><image href="https://x.example.test/a.png"/></svg></body>')
        self.assertFails("<image href>")
        self.mutate("</body>", '<svg><use xlink:href="https://x.example.test/s.svg#a"/></svg></body>')
        self.assertFails("<use xlink:href>")

    def test_broken_link(self):
        self.mutate("</body>", '<a href="missing.html">x</a></body>')
        self.assertFails("broken relative link")

    def test_path_escape_is_broken(self):
        self.mutate("</body>", '<a href="../secret.html">x</a></body>')
        self.assertFails("broken relative link")

    def test_path_escape_to_existing_file_is_broken(self):
        outside = os.path.join(os.path.dirname(self.d), os.path.basename(self.d) + "-outside.html")
        with open(outside, "w", encoding="utf-8") as f:
            f.write("x")
        self.addCleanup(os.remove, outside)
        self.mutate("</body>", '<a href="../%s">x</a></body>' % os.path.basename(outside))
        self.assertFails("broken relative link")

    def test_missing_fragment(self):
        self.mutate('href="#sec"', 'href="#nope"')
        self.assertFails("missing fragment id #nope")

    def test_top_fragment_is_not_exempt(self):
        self.mutate('<h1 id="top">', "<h1>")
        self.mutate("</body>", '<a href="#top">x</a></body>')
        self.assertFails("missing fragment id #top")

    def test_missing_fragment_other_page(self):
        self.mutate("other.html#o", "other.html#zzz")
        self.assertFails("missing fragment id #zzz")

    def test_anchor_name_counts_as_id_and_duplicates(self):
        self.mutate("</body>", '<a name="sec"></a></body>')
        self.assertFails('duplicate id "sec"')

    def test_duplicate_id(self):
        self.mutate('<h2 id="sec">', '<h2 id="top">')
        self.assertFails('duplicate id "top"')

    def test_no_repo_declaration(self):
        self.mutate('id="repo" ', "")
        self.assertFails('id="repo"')

    def test_repo_must_be_https(self):
        self.mutate('"https://example.test/org/repo"', '"http://example.test/org/repo"')
        self.assertFails("must be https")


class Images(Base):
    def test_missing_alt(self):
        self.mutate(' alt="An image"', "")
        self.assertFails("no alt")

    def test_image_without_width(self):
        self.mutate(' width="10"', "")
        self.assertFails("needs a numeric width")

    def test_image_without_height(self):
        self.mutate(' height="10"', "")
        self.assertFails("needs a numeric height")

    def test_image_non_numeric_height(self):
        self.mutate('height="10"', 'height="auto"')
        self.assertFails("needs a numeric height")

    def test_input_image_without_alt(self):
        self.mutate("</body>", '<input type="image" src="assets/a.svg"></body>')
        self.assertFails("input type=image")


class Metadata(Base):
    def test_missing_lang(self):
        self.mutate('<html lang="en">', "<html>")
        self.assertFails("no lang")

    def test_missing_title(self):
        self.mutate("<title>Test site</title>", "")
        self.assertFails("missing <title>")

    def test_missing_description(self):
        self.mutate('<meta name="description" content="A test.">', "")
        self.assertFails("missing meta description")

    def test_missing_viewport(self):
        self.mutate('<meta name="viewport" content="width=device-width, initial-scale=1">', "")
        self.assertFails("missing meta viewport")

    def test_missing_canonical(self):
        self.mutate('<link rel="canonical" href="__SITE_URL__/">', "")
        self.assertFails("missing <link rel=\"canonical\">")

    def test_og_url_outside_site_address(self):
        self.mutate('<meta property="og:url" content="__SITE_URL__/">', '<meta property="og:url" content="https://evil.test/">')
        self.assertFails("og:url is not under the site address")

    def test_og_image_missing_file(self):
        self.mutate("__SITE_URL__/assets/a.svg", "__SITE_URL__/assets/nope.png", )
        self.assertFails("points to a file that is not in the site")

    def test_twitter_image_relative(self):
        self.mutate('<meta name="twitter:image" content="__SITE_URL__/assets/a.svg">', '<meta name="twitter:image" content="assets/a.svg">')
        self.assertFails("twitter:image is not under the site address")

    def test_canonical_must_be_https(self):
        self.mutate('href="__SITE_URL__/"', 'href="http://example.test/"')
        self.assertFails("canonical must be https")

    def test_base_not_committed(self):
        self.mutate("</head>", '<base href="https://example.test/"></head>')
        self.assertFails("<base> is only added at deploy time")

    def test_placeholder_left_in_built_output(self):
        self.assertFails("placeholder", built=True)


class Referrer(Base):
    def test_referrer_policy_required_and_exact(self):
        self.mutate('<meta name="referrer" content="no-referrer">', "")
        self.assertFails('index.html: missing <meta name="referrer"')
        self.mutate("<title>Test site", '<meta name="referrer" content="origin"><title>Test site')
        self.assertFails('index.html: missing <meta name="referrer"')


class Csp(Base):
    def assertCspFails(self, new_csp, needle):
        self.write("index.html", GOOD.format(csp=new_csp))
        self.assertFails(needle)

    def test_missing_csp(self):
        self.mutate(CSP_META, "")
        self.assertFails("missing <meta http-equiv")

    def test_unsafe_inline(self):
        self.assertCspFails(CSP.replace("style-src 'self'", "style-src 'self' 'unsafe-inline'"), "'unsafe-inline'")

    def test_unsafe_eval(self):
        self.assertCspFails(CSP.replace("script-src 'self'", "script-src 'self' 'unsafe-eval'"), "'unsafe-eval'")

    def test_data_and_blob_in_script_src(self):
        self.assertCspFails(CSP.replace("script-src 'self'", "script-src 'self' data:"), "script-src allows data:")
        self.assertCspFails(CSP.replace("script-src 'self'", "script-src 'self' blob:"), "script-src allows blob:")

    def test_wildcard_host(self):
        self.assertCspFails(CSP.replace("img-src 'self'", "img-src 'self' *.example.test"), "*.example.test")

    def test_bare_host_in_img_src(self):
        self.assertCspFails(CSP.replace("img-src 'self'", "img-src 'self' images.example.test"), "images.example.test")

    def test_scheme_sources(self):
        self.assertCspFails(CSP.replace("img-src 'self'", "img-src 'self' https:"), "https:")
        self.assertCspFails(CSP.replace("img-src 'self'", "img-src 'self' http:"), "http:")

    def test_full_url_source(self):
        self.assertCspFails(CSP.replace("img-src 'self'", "img-src 'self' https://x.example.test"), "https://x.example.test")

    def test_missing_default_src(self):
        self.assertCspFails("script-src 'self'; style-src 'self'", "no default-src")

    def test_csp_after_script(self):
        self.mutate(CSP_META, "")
        self.mutate("</head>", CSP_META + "</head>")  # after the <link> and <script> that load resources
        self.assertFails("comes after an element that loads a resource")


class InlineCode(Base):
    def test_inline_handler(self):
        self.mutate("</body>", '<button onclick="x()">b</button></body>')
        self.assertFails("inline event handler")

    def test_inline_style_attribute(self):
        self.mutate("</body>", '<p style="color:red">x</p></body>')
        self.assertFails("inline style attribute")

    def test_inline_script_and_style(self):
        self.mutate("</body>", "<script>var a=1</script><style>a{}</style></body>")
        self.assertFails("inline <script>")
        self.assertFails("inline <style>")


class Assets(Base):
    def test_css_external_url(self):
        self.write("assets/site.css", "body{background:url(https://x.example.test/a.png)}")
        self.assertFails("external or data URL in CSS")

    def test_css_import_external(self):
        self.write("assets/site.css", '@import "https://x.example.test/a.css";')
        self.assertFails("external or data URL in CSS")

    def test_css_broken_local_url(self):
        self.write("assets/site.css", "body{background:url(gone.png)}")
        self.assertFails("broken url() in CSS")

    def test_css_image_set(self):
        self.write("assets/site.css", 'body{background:image-set("https://x.example.test/a.png" 1x, url(a.svg) 2x)}')
        self.assertFails("external or data URL in CSS")

    def test_css_image_set_broken_local_string(self):
        self.write("assets/site.css", 'body{background:image-set("gone.png" 1x)}')
        self.assertFails("broken url() in CSS")

    def test_css_escaped_url(self):
        self.write("assets/site.css", r"body{background:\75rl(https://x.example.test/a.png)}")
        self.assertFails("external or data URL in CSS")
        self.write("assets/site.css", r"body{background:url(\68ttps://x.example.test/a.png)}")
        self.assertFails("external or data URL in CSS")

    def test_css_comment_url_is_ignored(self):
        self.write("assets/site.css", "/* url(https://x.example.test/a.png) */ body{color:red}")
        self.assertClean()

    def test_js_external_url_and_fetch(self):
        self.write("assets/site.js", 'fetch("https://x.example.test/a")')
        self.assertFails("URL literal in JavaScript")
        self.assertFails("network or code-evaluation API")

    def test_js_protocol_relative_literal(self):
        self.write("assets/site.js", 'var u = "//cdn.example.test/x.js";')
        self.assertFails("URL literal in JavaScript")

    def test_js_eval(self):
        self.write("assets/site.js", "eval('1')")
        self.assertFails("network or code-evaluation API")

    def test_js_urls_in_comments_are_fine(self):
        self.write("assets/site.js", "// see https://example.test/doc\n/* and https://example.test/b */\nvar ok = 'op://dev/token';\n")
        self.assertClean()

    def test_js_url_in_string_after_comment_marker_in_string(self):
        self.write("assets/site.js", "var s = '// not a comment'; var u = 'https://x.example.test/';")
        self.assertFails("URL literal in JavaScript")

    def test_svg_external_ref(self):
        self.write("assets/a.svg", '<svg xmlns="http://www.w3.org/2000/svg"><image href="https://x.example.test/a.png"/></svg>')
        self.assertFails("external reference in SVG")

    def test_svg_use_xlink_external(self):
        self.write("assets/a.svg", '<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href="//x.example.test/s.svg#a"/></svg>')
        self.assertFails("external reference in SVG")

    def test_svg_script_and_handler(self):
        self.write("assets/a.svg", '<svg xmlns="http://www.w3.org/2000/svg" onload="x()"><script>1</script></svg>')
        self.assertFails("<script> in SVG")
        self.assertFails("inline event handler")

    def test_svg_style_external(self):
        self.write("assets/a.svg", '<svg xmlns="http://www.w3.org/2000/svg"><style>a{fill:url(https://x.example.test/p)}</style></svg>')
        self.assertFails("external or data URL in CSS")

    def test_page_too_heavy(self):
        self.write("assets/site.js", "var x = '" + "a" * 1_100_000 + "';")
        self.assertFails("exceeds")


DOC_PAGE = """<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="{csp}">
<meta name="referrer" content="no-referrer">
<title>{name}</title><meta name="description" content="A docs page.">
<link rel="canonical" href="__SITE_URL__/docs/{name}.html">
<link rel="stylesheet" href="../assets/site.css"><script src="../assets/site.js"></script></head>
<body><a class="skip" href="#main">Skip</a>
<div class="dsearch" id="dsearch" data-root="" hidden><input id="docs-q" aria-controls="docs-list"><ul id="docs-list"></ul></div>
<nav id="docnav"><ul><li><a href="a.html">A</a></li><li><a href="b.html">B</a></li></ul></nav>
<main id="main"><h1>{name}</h1><h2 id="sec">Sec</h2><ul><li>x<ul><li>y</li></ul></li></ul></main></body></html>"""
SEARCH_INDEX_OK = 'window.CCSHELF_DOCS_INDEX = {"v":1,"pages":[{"u":"a.html","t":"A","g":"G","s":[["sec","Sec","text about fetch and eval"]]},{"u":"b.html","t":"B","g":"G","s":[]}]};\n'


class Docs(Base):
    """Rules for the generated documentation pages under docs/."""

    def setUp(self):
        super().setUp()
        os.makedirs(os.path.join(self.d, "docs"))
        for n in ("a", "b"):
            self.write("docs/%s.html" % n, DOC_PAGE.format(csp=CSP, name=n))
        self.write("docs/search-index.js", SEARCH_INDEX_OK)
        self.mutate("</body>", '<a href="docs/a.html">docs</a></body>')

    def test_good_docs_pass_and_the_index_may_mention_forbidden_words(self):
        self.assertClean()

    def test_missing_skip_link(self):
        self.mutate('<a class="skip" href="#main">Skip</a>', "", "docs/a.html")
        self.assertFails("docs/a.html: docs page has no skip link")

    def test_skip_link_must_hit_an_id(self):
        self.mutate('href="#main"', 'href="#content"', "docs/a.html")
        self.assertFails("skip link #content does not point at an id")

    def test_two_h1(self):
        self.mutate("<h2 id=\"sec\">", "<h1>Again</h1><h2 id=\"sec\">", "docs/a.html")
        self.assertFails("exactly one <h1>, found 2")

    def test_no_h1(self):
        self.mutate("<h1>a</h1>", "", "docs/a.html")
        self.assertFails("exactly one <h1>, found 0")

    def test_main_landmark_required(self):
        self.mutate('<main id="main">', "<main>", "docs/a.html")
        self.assertFails('needs exactly one <main id="main">')

    def test_nav_must_list_every_page(self):
        self.mutate('<li><a href="b.html">B</a></li>', "", "docs/a.html")
        self.assertFails("the docs navigation does not link docs/b.html")

    def test_nav_missing_entirely(self):
        self.mutate('<nav id="docnav">', "<nav>", "docs/a.html")
        self.assertFails('no navigation with id="docnav"')

    def test_search_box_required_and_its_root_checked(self):
        self.mutate('data-root=""', 'data-root="../"', "docs/a.html")
        self.assertFails("search data-root")
        self.mutate('<div class="dsearch" id="dsearch" data-root="../" hidden><input id="docs-q" aria-controls="docs-list"><ul id="docs-list"></ul></div>', "", "docs/a.html")
        self.assertFails('no search box (id="dsearch")')

    def test_search_index_missing(self):
        os.remove(os.path.join(self.d, "docs", "search-index.js"))
        self.assertFails("docs/search-index.js: missing")

    def test_search_index_must_be_pure_data(self):
        self.write("docs/search-index.js", "alert(1);\n")
        self.assertFails("must be exactly")
        self.write("docs/search-index.js", 'window.CCSHELF_DOCS_INDEX = {"pages":[]};alert(1);\n')
        self.assertFails("not valid JSON")

    def test_search_index_must_list_exactly_the_pages(self):
        self.write("docs/search-index.js", SEARCH_INDEX_OK.replace(',{"u":"b.html","t":"B","g":"G","s":[]}', ""))
        self.assertFails("does not list the page b.html")
        self.write("docs/search-index.js", SEARCH_INDEX_OK.replace('"u":"b.html"', '"u":"c.html"'))
        self.assertFails("lists a page that does not exist: 'c.html'")

    def test_search_index_anchor_must_exist(self):
        self.write("docs/search-index.js", SEARCH_INDEX_OK.replace('"sec"', '"nope"', 1))
        self.assertFails("a.html#nope is not an id on that page")

    def test_search_index_size_limit(self):
        big = "x" * (cs.MAX_SEARCH_INDEX_BYTES + 10)
        self.write("docs/search-index.js", SEARCH_INDEX_OK.replace("text about fetch and eval", big))
        self.assertFails("search index is over")

    def test_orphan_page(self):
        self.mutate('<a href="docs/a.html">docs</a>', "", "index.html")
        self.assertFails("orphan page")

    def test_404_is_not_an_orphan(self):
        self.write("404.html", OTHER.format(csp=CSP).replace("<title>Other page", "<title>Not found").replace('<h1 id="o">', '<h1 id="x">'))
        self.assertFalse(any("404.html" in e and "orphan" in e for e in self.errs()))

    def test_a_docs_page_still_gets_the_generic_rules(self):
        self.mutate("<title>a</title>", "<title></title>", "docs/a.html")
        self.assertFails("docs/a.html: missing <title>")
        self.mutate("</body>", '<a href="https://evil.test/">x</a></body>', "docs/b.html")
        self.assertFails("external URL not allowed")
        self.mutate("</body>", '<a href="#zzz">x</a></body>', "docs/b.html")
        self.assertFails("missing fragment id #zzz")

    def test_heading_levels_may_not_be_skipped(self):
        self.mutate('<h2 id="sec">Sec</h2>', '<h2 id="sec">Sec</h2><h4>Deep</h4>', "docs/a.html")
        self.assertFails("heading level skips from h2 to h4")

    def test_first_heading_after_the_title_must_be_h2(self):
        self.mutate('<h2 id="sec">Sec</h2>', '<h3 id="sec">Sec</h3>', "docs/a.html")
        self.assertFails("heading level skips from h1 to h3")

    def test_going_back_up_a_level_is_fine(self):
        self.mutate('<h2 id="sec">Sec</h2>', '<h2 id="sec">Sec</h2><h3>S</h3><h2>T</h2>', "docs/a.html")
        self.assertClean()

    def test_list_directly_inside_list_is_invalid(self):
        self.mutate("<ul><li>x<ul>", "<ul><li>x</li><ul>", "docs/a.html")
        self.assertFails("invalid list nesting: <ul> directly inside <ul>")

    def test_unclosed_and_stray_list_items(self):
        self.mutate("<li>x<ul>", "<li>x<li>z<ul>", "docs/a.html")
        self.assertFails("opened while the previous <li> is still open")

    def test_stray_list_item(self):
        self.mutate("<main id=\"main\">", '<main id="main"><li>stray</li>', "docs/b.html")
        self.assertFails("<li> not inside a <ul> or <ol>")

    def test_stray_list_close(self):
        self.mutate("</main>", "</ul></main>", "docs/b.html")
        self.assertFails("</ul> does not match the open list element")

    def test_search_input_must_control_a_list_on_the_page(self):
        self.mutate('aria-controls="docs-list"', 'aria-controls="nowhere"', "docs/a.html")
        self.assertFails("search input's aria-controls")
        self.mutate('aria-controls="nowhere"', "", "docs/a.html")
        self.assertFails("search input's aria-controls")

    def test_only_the_exact_search_index_is_exempt_from_the_js_word_checks(self):
        for rel in ("assets/search-index.js", "docs/my-index.js", "assets/index.js"):
            self.write(rel, "fetch('x');")
            self.assertFails("%s: network or code-evaluation API" % rel)
            os.remove(os.path.join(self.d, rel))

    def test_search_index_must_end_exactly_with_a_semicolon_and_newline(self):
        self.write("docs/search-index.js", SEARCH_INDEX_OK[:-2] + "ab")
        self.assertFails("must be exactly")
        self.write("docs/search-index.js", SEARCH_INDEX_OK[:-1])
        self.assertFails("must be exactly")

    def test_search_index_may_not_list_a_page_twice(self):
        page = '{"u":"a.html","t":"A","g":"G","s":[["sec","Sec","text about fetch and eval"]]}'
        self.write("docs/search-index.js", SEARCH_INDEX_OK.replace(page, page + "," + page))
        self.assertFails("lists a.html twice")

    def test_search_data_root_must_lead_to_the_one_index(self):
        self.write("assets/search-index.js", "var x = 1;")
        self.mutate('data-root=""', 'data-root="../assets/"', "docs/a.html")
        self.assertFails('search data-root "../assets/" does not lead to docs/search-index.js')

    def test_nav_links_count_inside_nested_elements_and_not_after_the_nav(self):
        self.mutate('<nav id="docnav"><ul><li><a href="a.html">A</a></li><li><a href="b.html">B</a></li></ul></nav>',
                    '<div id="docnav"><div><a href="a.html">A</a></div><div><a href="b.html">B</a></div></div>', "docs/a.html")
        self.assertClean()
        self.mutate('<div id="docnav"><div><a href="a.html">A</a></div><div><a href="b.html">B</a></div></div>',
                    '<div id="docnav"><div><a href="a.html">A</a></div></div><a href="b.html">B</a>', "docs/a.html")
        self.assertFails("the docs navigation does not link docs/b.html")

    def test_exactly_one_main_landmark(self):
        self.mutate("</main>", '</main><main id="main"></main>', "docs/a.html")
        self.assertFails('needs exactly one <main id="main">')
        self.mutate('</main><main id="main"></main>', '</main><main></main>', "docs/a.html")
        self.assertFails('needs exactly one <main id="main">')
        self.mutate('</main><main></main>', '</main><div id="main2"></div>', "docs/a.html")
        self.assertClean()

    def test_js_words_are_still_refused_outside_the_index(self):
        self.write("assets/site.js", "fetch('x');")
        self.assertFails("network or code-evaluation API")


class Build(unittest.TestCase):
    """build-site.sh fills in the placeholders; its output passes --built, and a wrong URL is refused."""

    def setUp(self):
        if shutil.which("bash") is None:
            self.skipTest("no bash")
        self.out = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, self.out, True)

    def build(self, *args):
        return subprocess.run(["bash", os.path.join(HERE, "build-site.sh"), os.path.join(self.out, "o"), *args],
                              capture_output=True, text=True)

    def test_build_fills_placeholders(self):
        r = self.build("https://example.github.io/ccshelf")
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(os.path.join(self.out, "o", "404.html"), encoding="utf-8") as f:
            self.assertIn('<base href="https://example.github.io/ccshelf/">', f.read())
        with open(os.path.join(self.out, "o", "index.html"), encoding="utf-8") as f:
            self.assertIn('og:image" content="https://example.github.io/ccshelf/assets/og.png"', f.read())
        self.assertEqual(cs.check_site(os.path.join(self.out, "o"), built=True), [])

    def test_build_swaps_repo_url(self):
        r = self.build("https://example.github.io/ccshelf", "--repo-url", "https://github.com/example/public")
        self.assertEqual(r.returncode, 0, r.stderr)
        with open(os.path.join(self.out, "o", "index.html"), encoding="utf-8") as f:
            text = f.read()
        self.assertIn('id="repo" href="https://github.com/example/public"', text)
        # the author credit links to the profile (github.com/yorch); only the repository address is swapped
        self.assertNotIn("github.com/yorch/ccshelf", text)
        self.assertIn("git clone https://github.com/example/public\n", text)
        # every page of the build, docs included, carries the swapped address and none the old one
        for d, _, fs in os.walk(os.path.join(self.out, "o")):
            for f in fs:
                if f.endswith((".html", ".js")):
                    with open(os.path.join(d, f), encoding="utf-8") as fh:
                        body = fh.read()
                    # the module path in prose ("github.com/yorch/ccshelf") is documentation text,
                    # not a link: only the full address must be swapped
                    self.assertNotIn("https://github.com/yorch/ccshelf", body, f)
                    self.assertNotIn("__SITE_URL__", body, f)
        with open(os.path.join(self.out, "o", "docs", "index.html"), encoding="utf-8") as f:
            self.assertIn('href="https://github.com/example/public/blob/main/', f.read())
        with open(os.path.join(self.out, "o", "docs", "search-index.js"), encoding="utf-8") as f:
            self.assertNotIn("https://github.com/yorch/ccshelf", f.read())

    def test_build_refuses_bad_url(self):
        self.assertNotEqual(self.build("http://example.test").returncode, 0)
        self.assertNotEqual(self.build("https://example.test/a|b").returncode, 0)
        self.assertNotEqual(self.build("https://example.test/?x=1").returncode, 0)


class RealSite(unittest.TestCase):
    def test_the_real_site_with_its_generated_docs_passes(self):
        root = os.path.join(HERE, "..", "site")
        if not os.path.isdir(root):
            self.skipTest("no site/ directory")
        out = tempfile.mkdtemp()
        self.addCleanup(shutil.rmtree, out, True)
        built = os.path.join(out, "o")
        shutil.copytree(root, built)
        r = subprocess.run(["python3", "-I", os.path.join(HERE, "build_docs.py"), "--out", built], capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(cs.check_site(built), [])

    def test_the_committed_site_alone_only_misses_the_generated_pages(self):
        root = os.path.join(HERE, "..", "site")
        errs = cs.check_site(root)
        self.assertTrue(errs and all("broken relative link" in e and "docs/" in e for e in errs), errs)


if __name__ == "__main__":
    unittest.main()

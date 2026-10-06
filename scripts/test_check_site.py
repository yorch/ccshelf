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
        self.assertNotIn("github.com/yorch", text)

    def test_build_refuses_bad_url(self):
        self.assertNotEqual(self.build("http://example.test").returncode, 0)
        self.assertNotEqual(self.build("https://example.test/a|b").returncode, 0)
        self.assertNotEqual(self.build("https://example.test/?x=1").returncode, 0)


class RealSite(unittest.TestCase):
    def test_the_real_site_passes(self):
        root = os.path.join(HERE, "..", "site")
        if not os.path.isdir(root):
            self.skipTest("no site/ directory")
        self.assertEqual(cs.check_site(root), [])


if __name__ == "__main__":
    unittest.main()

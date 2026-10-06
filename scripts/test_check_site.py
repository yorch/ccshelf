#!/usr/bin/env python3
"""Unit tests for check_site.py. Run: python3 -I scripts/test_check_site.py"""
import importlib.util
import os
import shutil
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("check_site", os.path.join(HERE, "check_site.py"))
cs = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cs)

CSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'"
GOOD = """<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="{csp}">
<title>Test site</title><meta name="description" content="A test.">
<link rel="stylesheet" href="assets/site.css"><script src="assets/site.js"></script></head>
<body><h1 id="top">Hi</h1><a href="#sec">Section</a><h2 id="sec">Sec</h2>
<a href="other.html#o">Other</a>
<img src="assets/a.svg" alt="An image" width="10" height="10">
<a id="repo" href="https://example.test/org/repo">repo</a>
<a href="https://example.test/org/repo/blob/main/x.md">doc</a>
<a href="https://code.claude.com/docs">docs</a>
</body></html>"""
OTHER = GOOD.replace('id="top"', 'id="o"').replace("<title>Test site", "<title>Other page")


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

    def errs(self):
        return cs.check_site(self.d)

    def assertFails(self, needle):
        e = self.errs()
        self.assertTrue(any(needle in x for x in e), f"expected {needle!r} in {e}")


class Tests(Base):
    def test_good_site_passes(self):
        self.assertEqual(self.errs(), [])

    def test_external_src(self):
        self.mutate('<script src="assets/site.js">', '<script src="https://cdn.example.test/x.js">')
        self.assertFails("external URL not allowed")

    def test_protocol_relative(self):
        self.mutate('href="assets/site.css"', 'href="//cdn.example.test/x.css"')
        self.assertFails("external URL not allowed")

    def test_other_external_anchor(self):
        self.mutate("</body>", '<a href="https://evil.test/">x</a></body>')
        self.assertFails("external URL not allowed")

    def test_outbound_host_only_for_anchors(self):
        self.mutate("</body>", '<img src="https://code.claude.com/x.png" alt="x" width="1" height="1"></body>')
        self.assertFails("external URL not allowed")

    def test_repo_prefix_boundary(self):
        self.mutate("</body>", '<a href="https://example.test/org/repo-evil/x">x</a></body>')
        self.assertFails("external URL not allowed")

    def test_broken_link(self):
        self.mutate("</body>", '<a href="missing.html">x</a></body>')
        self.assertFails("broken relative link")

    def test_missing_fragment(self):
        self.mutate('href="#sec"', 'href="#nope"')
        self.assertFails("missing fragment id #nope")

    def test_missing_fragment_other_page(self):
        self.mutate('other.html#o', 'other.html#zzz')
        self.assertFails("missing fragment id #zzz")

    def test_missing_alt(self):
        self.mutate(' alt="An image"', "")
        self.assertFails("no alt")

    def test_image_without_dimensions(self):
        self.mutate(' width="10" height="10"', "")
        self.assertFails("lacks width and height")

    def test_duplicate_id(self):
        self.mutate('<h2 id="sec">', '<h2 id="top">')
        self.assertFails('duplicate id "top"')

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

    def test_missing_csp(self):
        self.mutate('<meta http-equiv="Content-Security-Policy" content="%s">' % CSP, "")
        self.assertFails("missing <meta http-equiv")

    def test_permissive_csp(self):
        self.mutate("style-src 'self'", "style-src 'self' 'unsafe-inline'")
        self.assertFails("'unsafe-inline'")

    def test_csp_external_host(self):
        self.mutate("img-src 'self'", "img-src 'self' https://x.example.test")
        self.assertFails("external host")

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

    def test_css_external_url(self):
        self.write("assets/site.css", "body{background:url(https://x.example.test/a.png)}")
        self.assertFails("external or data URL in CSS")

    def test_css_import_external(self):
        self.write("assets/site.css", '@import "https://x.example.test/a.css";')
        self.assertFails("external or data URL in CSS")

    def test_css_broken_local_url(self):
        self.write("assets/site.css", "body{background:url(gone.png)}")
        self.assertFails("broken url() in CSS")

    def test_js_external_url_and_fetch(self):
        self.write("assets/site.js", 'fetch("https://x.example.test/a")')
        self.assertFails("URL literal in JavaScript")
        self.assertFails("network API in JavaScript")

    def test_svg_external_ref(self):
        self.write("assets/a.svg", '<svg xmlns="http://www.w3.org/2000/svg"><image href="https://x.example.test/a.png"/></svg>')
        self.assertFails("external reference in SVG")

    def test_page_too_heavy(self):
        self.write("assets/site.js", "var x = '" + "a" * 1_100_000 + "';")
        self.assertFails("exceeds")

    def test_no_repo_declaration(self):
        self.mutate('id="repo" ', "")
        self.assertFails('id="repo"')

    def test_repo_must_be_https(self):
        self.mutate("https://example.test/org/repo\"", "http://example.test/org/repo\"")
        self.assertFails("must be https")

    def test_path_escape_is_broken(self):
        self.mutate("</body>", '<a href="../secret.html">x</a></body>')
        self.assertFails("broken relative link")


class RealSite(unittest.TestCase):
    def test_the_real_site_passes(self):
        root = os.path.join(HERE, "..", "site")
        if not os.path.isdir(root):
            self.skipTest("no site/ directory")
        self.assertEqual(cs.check_site(root), [])


if __name__ == "__main__":
    unittest.main()

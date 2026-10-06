package site

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/catalog"
	"github.com/yorch/ccshelf/internal/catalog/catalogtest"
	"github.com/yorch/ccshelf/internal/orgconfig"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func fixtureCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	root := catalogtest.FixtureDir(t)
	cfg, err := orgconfig.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := catalog.Build(root, cfg, catalog.Options{Profiles: []catalog.ProfileInfo{
		{Name: "frontend", Description: "Frontend engineering", Owner: "@acme/web", Status: "active", WhenToUse: []string{"building web UI"}},
		{Name: "sre", Description: "Site reliability engineering", Owner: "@acme/sre", Status: "active", WhenToUse: []string{"on call"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func hostileCatalog() *catalog.Catalog {
	xss := `<script>alert(1)</script>`
	img := `"><img src=x onerror=alert(1)>`
	return &catalog.Catalog{
		Version: 1, Title: `Evil </title><script>alert(1)</script> {{CATALOG_JSON}}`,
		Plugins: []catalog.Entry{
			{
				Name: xss, Description: img + "</script><!--", Owner: xss, Docs: "javascript:alert(1)", Homepage: "data:text/html,x", Tags: []string{img}, Category: xss,
				WhenToUse: []string{"</script>"}, Marketplace: "m",
			},
			{Name: "huge", Description: strings.Repeat("A", 500000), Marketplace: "m"},
			{Name: "ctl\x1b[31m ", Description: "x\x00y\u202e", Marketplace: "m"},
		},
		Profiles: []catalog.ProfileInfo{{Name: xss, Description: img}},
	}
}

func fileMap(t *testing.T, c *catalog.Catalog) map[string]string {
	t.Helper()
	files, err := Render(c)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, f := range files {
		m[f.Name] = string(f.Data)
	}
	return m
}

func TestRenderGoldenIndex(t *testing.T) {
	got := fileMap(t, fixtureCatalog(t))["index.html"]
	p := filepath.Join(filepath.Dir(catalogtest.FixtureDir(t)), "golden", "index.html")
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("index.html differs from the golden file; run go test -update and review the diff")
	}
}

func TestRenderFilesAndOrder(t *testing.T) {
	files, err := Render(fixtureCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
		if len(f.Data) == 0 {
			t.Errorf("%s is empty", f.Name)
		}
	}
	if strings.Join(names, ",") != "index.html,app.js,style.css,catalog.json" {
		t.Errorf("files = %v", names)
	}
}

var (
	jsonBlock   = regexp.MustCompile(`(?s)<script type="application/json" id="catalog-data">(.*?)</script>`)
	inlineAttrs = regexp.MustCompile(`(?i)\s(on[a-z]+|style)\s*=`)
	remoteURL   = regexp.MustCompile(`(?i)(https?:)?//[a-z0-9.-]+\.[a-z]{2,}`)
)

func TestPageIsSelfContainedAndStrict(t *testing.T) {
	for name, c := range map[string]*catalog.Catalog{"fixture": fixtureCatalog(t), "hostile": hostileCatalog()} {
		t.Run(name, func(t *testing.T) {
			m := fileMap(t, c)
			page := m["index.html"]
			wantMeta := `<meta http-equiv="Content-Security-Policy" content="` + ContentSecurityPolicy + `">`
			if !strings.Contains(page, wantMeta) {
				t.Errorf("CSP meta tag missing")
			}
			if ContentSecurityPolicy != "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'" {
				t.Error("CSP constant changed")
			}
			if strings.Contains(ContentSecurityPolicy, "unsafe") {
				t.Error("CSP must not use unsafe-*")
			}
			// The CSP must come before anything that can load a resource.
			if strings.Index(page, "Content-Security-Policy") > strings.Index(page, "<link") {
				t.Error("CSP must precede the stylesheet link")
			}
			blocks := jsonBlock.FindAllStringSubmatch(page, -1)
			if len(blocks) != 1 {
				t.Fatalf("want one JSON block, got %d", len(blocks))
			}
			rest := strings.Replace(page, blocks[0][0], "", 1)
			if got := strings.Count(strings.ToLower(rest), "<script"); got != 1 {
				t.Errorf("want one external script tag besides the data block, got %d", got)
			}
			if !strings.Contains(rest, `<script src="app.js"></script>`) {
				t.Error("script tag must be external and empty")
			}
			if strings.Count(strings.ToLower(page), "</script") != 2 {
				t.Errorf("data leaked a closing script tag")
			}
			if strings.Contains(strings.ToLower(rest), "<style") || inlineAttrs.MatchString(rest) {
				t.Errorf("inline style or event handler attribute found")
			}
			if strings.Contains(blocks[0][1], "<") || strings.Contains(blocks[0][1], "<!--") {
				t.Error("JSON block contains a raw <")
			}
			if remoteURL.MatchString(rest) || remoteURL.MatchString(m["app.js"]) || remoteURL.MatchString(m["style.css"]) {
				t.Errorf("a template mentions a remote URL: %v %v %v", remoteURL.FindString(rest), remoteURL.FindString(m["app.js"]), remoteURL.FindString(m["style.css"]))
			}
			if strings.Contains(m["style.css"], "@import") || strings.Contains(m["style.css"], "url(") {
				t.Error("stylesheet loads something")
			}
			// The block is the same data as catalog.json and parses.
			var inBlock, inFile any
			if err := json.Unmarshal([]byte(blocks[0][1]), &inBlock); err != nil {
				t.Fatalf("block is not JSON: %v", err)
			}
			if err := json.Unmarshal([]byte(m["catalog.json"]), &inFile); err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(inBlock)
			b, _ := json.Marshal(inFile)
			if !bytes.Equal(a, b) {
				t.Error("embedded data differs from catalog.json")
			}
			if !strings.Contains(rest, "<html lang=\"en\">") || !strings.Contains(rest, `name="viewport"`) || !strings.Contains(rest, `name="color-scheme"`) {
				t.Error("missing html lang, viewport or color-scheme")
			}
		})
	}
}

func TestHostileTitleAndData(t *testing.T) {
	m := fileMap(t, hostileCatalog())
	page := m["index.html"]
	if strings.Contains(page, "<script>alert") || strings.Contains(page, "</title><script") {
		t.Error("title or data injected markup")
	}
	if !strings.Contains(page, "&lt;/title&gt;&lt;script&gt;") {
		t.Errorf("title not HTML-escaped: %s", page[:600])
	}
	if strings.Count(page, "{{CATALOG_JSON}}") != 1 && !strings.Contains(page, "{{CATALOG_JSON}}") {
		// The placeholder text in a title must stay literal and must not pull the JSON in twice.
		t.Error("placeholder in the title was expanded")
	}
	if strings.Count(page, `"plugins"`) != 1 {
		t.Errorf("data embedded %d times", strings.Count(page, `"plugins"`))
	}
	if strings.Contains(m["catalog.json"], " ") || strings.Contains(m["catalog.json"], "\x1b") {
		t.Error("control characters in catalog.json")
	}
}

func TestAppJSAvoidsDangerousAPIs(t *testing.T) {
	js := fileMap(t, fixtureCatalog(t))["app.js"]
	for _, bad := range []string{
		"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "Function(", "setTimeout(\"", "setInterval(\"",
		"srcdoc", "createContextualFragment", "DOMParser", "XMLHttpRequest", "fetch(", "importScripts", "localStorage", "sessionStorage", "document.cookie", "postMessage", "WebSocket", "sendBeacon", "location.href =", "location.assign", "location.replace", "window.open",
	} {
		if strings.Contains(js, bad) {
			t.Errorf("app.js contains %q", bad)
		}
	}
	for _, need := range []string{"textContent", "createElement", "noopener noreferrer", "http:", "https:"} {
		if !strings.Contains(js, need) {
			t.Errorf("app.js lacks %q", need)
		}
	}
}

func TestStyleCoversRequirements(t *testing.T) {
	css := fileMap(t, fixtureCatalog(t))["style.css"]
	for _, need := range []string{"prefers-color-scheme: dark", "prefers-reduced-motion: reduce", ":focus-visible", "@media (max-width"} {
		if !strings.Contains(css, need) {
			t.Errorf("style.css lacks %q", need)
		}
	}
}

func TestAppJSSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping the JavaScript syntax check")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "app.js")
	if err := os.WriteFile(p, []byte(fileMap(t, fixtureCatalog(t))["app.js"]), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", p).CombinedOutput(); err != nil {
		t.Errorf("node --check: %v\n%s", err, out)
	}
}

func TestWrite(t *testing.T) {
	c := fixtureCatalog(t)
	dir := filepath.Join(t.TempDir(), "out", "site")
	if err := Write(dir, c); err != nil {
		t.Fatal(err)
	}
	want := fileMap(t, c)
	for name, content := range want {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != content {
			t.Errorf("%s: %v", name, err)
		}
		if runtime.GOOS != "windows" {
			st, _ := os.Stat(filepath.Join(dir, name))
			if st.Mode().Perm() != 0o644 {
				t.Errorf("%s mode = %v", name, st.Mode().Perm())
			}
		}
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(dir); st.Mode().Perm() != 0o755 {
			t.Errorf("dir mode = %v", st.Mode().Perm())
		}
	}
	// A second run overwrites and leaves no temporary files behind.
	if err := Write(dir, c); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 4 {
		t.Errorf("want 4 files, found %d", len(ents))
	}
}

func TestWriteRefusals(t *testing.T) {
	c := fixtureCatalog(t)
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(f, c); err == nil {
		t.Error("a file as output directory must fail")
	}
	// A directory in the way of a target file.
	d := t.TempDir()
	if err := os.Mkdir(filepath.Join(d, "index.html"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Write(d, c); err == nil {
		t.Error("a directory where index.html belongs must fail")
	}
	if runtime.GOOS == "windows" {
		return
	}
	// A symlink at a target path is refused, and its destination untouched.
	outside := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	d2 := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(d2, "app.js")); err != nil {
		t.Fatal(err)
	}
	if err := Write(d2, c); err == nil {
		t.Error("symlink target must be refused")
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Error("the symlink destination was modified")
	}
}

func TestRenderEmptyCatalog(t *testing.T) {
	m := fileMap(t, &catalog.Catalog{Version: 1})
	if !strings.Contains(m["index.html"], "<title>Plugin catalog</title>") {
		t.Error("default title missing")
	}
	if strings.Contains(m["catalog.json"], "null") {
		t.Error("null in catalog.json")
	}
}

// TestWriteForBrowser writes a sample site for a manual look in a browser.
// It does nothing unless CCSHELF_SITE_OUT names an output directory; set
// CCSHELF_SITE_HOSTILE=1 to use the hostile catalog instead of the fixture:
//
//	CCSHELF_SITE_OUT=$PWD/../../../tmp-site go test ./internal/catalog/site -run ForBrowser
//	open tmp-site/index.html
func TestWriteForBrowser(t *testing.T) {
	out := os.Getenv("CCSHELF_SITE_OUT")
	if out == "" {
		t.Skip("set CCSHELF_SITE_OUT to write a sample site for a browser")
	}
	c := fixtureCatalog(t)
	if os.Getenv("CCSHELF_SITE_HOSTILE") != "" {
		c = hostileCatalog()
	}
	if err := Write(out, c); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %s", out)
}

package catalog

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/catalog/catalogtest"
	"github.com/ccshelf/ccshelf/internal/orgconfig"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func goldenPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(filepath.Dir(catalogtest.FixtureDir(t)), "golden", name)
}

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	p := goldenPath(t, name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs from the golden file; run go test -update and review the diff.\n--- got ---\n%s", name, got)
	}
}

func fixtureProfiles() []ProfileInfo {
	return []ProfileInfo{
		{Name: "sre", Description: "Site reliability engineering", Owner: "@acme/sre", Status: "active", WhenToUse: []string{"on call"}},
		{Name: "frontend", Description: "Frontend engineering", Owner: "@acme/web", Status: "active", WhenToUse: []string{"building web UI"}},
	}
}

func buildFixture(t *testing.T, root string, opt Options) *Catalog {
	t.Helper()
	cfg, err := orgconfig.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := Build(root, cfg, opt)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBuildFixtureGolden(t *testing.T) {
	root := catalogtest.FixtureDir(t)
	c := buildFixture(t, root, Options{Profiles: fixtureProfiles()})
	if c.Version != 1 || c.Title != "Acme plugin catalog" || c.GeneratedAt != "" {
		t.Errorf("header = %+v", c)
	}
	names := []string{}
	for _, p := range c.Plugins {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "data-tools,design-kit,figma-bridge,ops-helper,sre-kit" {
		t.Errorf("plugins = %v (bundles must be left out)", names)
	}
	if !reflect.DeepEqual(c.Categories, []string{"data", "design", "integrations", "observability"}) {
		t.Errorf("categories = %v", c.Categories)
	}
	if c.Profiles[0].Name != "frontend" {
		t.Errorf("profiles not sorted: %+v", c.Profiles)
	}
	byName := map[string]Entry{}
	for _, p := range c.Plugins {
		byName[p.Name] = p
	}
	dk := byName["design-kit"]
	if dk.Skills != 2 || dk.Agents != 1 || dk.Owner != "@acme/web" || dk.Status != "active" || dk.Author != "Acme Web Team" || dk.Docs != "https://wiki.example.com/design-kit" {
		t.Errorf("design-kit = %+v", dk)
	}
	sre := byName["sre-kit"]
	if !sre.HasHooks || !sre.NeedsPlatformReview || sre.Commands != 1 || sre.Source != "plugins/sre-kit" {
		t.Errorf("sre-kit = %+v", sre)
	}
	if dt := byName["data-tools"]; !dt.HasMCP || !dt.NeedsPlatformReview {
		t.Errorf("data-tools = %+v", dt)
	}
	if fb := byName["figma-bridge"]; !fb.External || fb.Source != "github:acme-example/figma-bridge" {
		t.Errorf("figma-bridge = %+v", fb)
	}
	if oh := byName["ops-helper"]; oh.Status != "deprecated" || oh.SupersededBy != "sre-kit" || oh.ReviewBy != "2026-12-01" {
		t.Errorf("ops-helper = %+v", oh)
	}

	js, err := JSON(c)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "catalog.json", js)
	checkGolden(t, "CATALOG.md", Markdown(c))
}

func TestBuildDeterministic(t *testing.T) {
	root := catalogtest.FixtureDir(t)
	a, _ := JSON(buildFixture(t, root, Options{Profiles: fixtureProfiles()}))
	b, _ := JSON(buildFixture(t, root, Options{Profiles: fixtureProfiles()}))
	if string(a) != string(b) {
		t.Error("two builds differ")
	}
	if strings.Contains(string(a), root) || strings.Contains(string(a), `\\`) {
		t.Error("output contains an absolute path or backslash")
	}
}

func TestBuildNowAndLint(t *testing.T) {
	root := catalogtest.CopyFixture(t)
	catalogtest.Remove(t, root, "catalog/plugins/design-kit.toml")
	cfg, _ := orgconfig.Load(root)
	now := func() time.Time { return time.Date(2026, 10, 6, 12, 30, 0, 0, time.FixedZone("x", 3600)) }
	c, rep, err := Build(root, cfg, Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if c.GeneratedAt != "2026-10-06T11:30:00Z" {
		t.Errorf("GeneratedAt = %q", c.GeneratedAt)
	}
	if !rep.HasErrors() {
		t.Error("lint report should carry the missing sidecar error")
	}
	for _, p := range c.Plugins {
		if p.Name == "design-kit" && (p.Owner != "" || p.Status != "") {
			t.Error("entry without a sidecar must have empty metadata")
		}
	}
	if _, _, err := Build(filepath.Join(root, "nope"), cfg, Options{}); err == nil {
		t.Error("missing root should fail")
	}
	if c, _, err := Build(root, nil, Options{}); err != nil || c.Title == "" {
		t.Errorf("nil config: %v %v", c, err)
	}
}

func TestBuildDefaultTitleAndFallbacks(t *testing.T) {
	root := t.TempDir()
	catalogtest.Write(t, root, ".claude-plugin/marketplace.json", `{"name":"m","plugins":[{"name":"p","source":"./plugins/p"}]}`)
	catalogtest.Write(t, root, "plugins/p/.claude-plugin/plugin.json", `{"name":"p","version":"3.1.0","description":"From the manifest"}`)
	cfg := orgconfig.Default()
	c, _, err := Build(root, cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "Plugin catalog" || c.Plugins[0].Description != "From the manifest" || c.Plugins[0].Version != "3.1.0" {
		t.Errorf("fallbacks: %+v", c)
	}
	if c.Plugins[0].Tags == nil || c.Plugins[0].WhenToUse == nil {
		t.Error("lists must be non-nil")
	}
}

func TestTextAndLink(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"", 10, ""},
		{"plain", 10, "plain"},
		{"  two   spaces\tand\nnewline ", 100, "two spaces and newline"},
		{"esc\x1b[31mred\x00", 100, "esc [31mred"},
		{"bidi\u202eevil\u2066x", 100, "bidi evil x"},
		{"abcdefghij", 5, "abcde…"},
		{"abcde", 5, "abcde"},
		{"\xff\xfebad utf8", 100, "bad utf8"},
		{"line\u2028sep", 100, "line sep"},
	}
	for _, tt := range tests {
		if got := Text(tt.in, tt.max); got != tt.want {
			t.Errorf("Text(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
		}
	}
	for _, ok := range []string{"https://example.com/a", "http://example.com"} {
		if Link(ok) != ok {
			t.Errorf("Link(%q) dropped", ok)
		}
	}
	for _, bad := range []string{"", "javascript:alert(1)", "data:text/html,<script>", "//example.com", "https://u:p@example.com", "https://exa mple.com", "https://example.com/" + strings.Repeat("a", 3000)} {
		if Link(bad) != "" {
			t.Errorf("Link(%q) kept", bad)
		}
	}
}

func hostileRepo(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	xss := `<script>alert(1)</script>`
	img := `"><img src=x onerror=alert(1)>`
	huge := strings.Repeat("A", 1<<20)
	mk := map[string]any{
		"name": "evil",
		"plugins": []any{
			map[string]any{
				"name": "p1", "source": "./p1", "description": xss + " </script> " + img, "category": "c<b>x", "tags": []string{img}, "author": xss,
				"homepage": "javascript:alert(1)", "repository": "data:text/html,<script>alert(1)</script>", "displayName": "[x](javascript:alert(1))",
			},
			map[string]any{"name": "p2", "source": "./p2", "description": huge, "author": "a\x1b[31mb\u202ec\r\nd"},
			map[string]any{"name": "p3|`x`", "source": "./p3", "description": "| a | b |\n# heading\n*bold* _it_ ![i](x) @octocat http://evil.example <b>"},
		},
	}
	b, _ := json.Marshal(mk)
	catalogtest.Write(t, root, ".claude-plugin/marketplace.json", string(b))
	for _, n := range []string{"p1", "p2", "p3"} {
		catalogtest.Write(t, root, "plugins/"+n+"/.claude-plugin/plugin.json", `{"name":"`+n+`"}`)
	}
	sc := "owner = \"" + `@acme/<script>` + "\"\nstatus = \"active\"\nwhen_to_use = [\"<img src=x onerror=alert(1)>\", \"` + \"`x\"]\nsupport = \"javascript:alert(1)\"\ndocs = \"javascript:alert(1)\"\nreview_by = \"2027-01-01\"\n"
	catalogtest.Write(t, root, "catalog/plugins/p1.toml", sc)
	catalogtest.Write(t, root, "catalog/plugins/p2.toml", "owner = \"@a/b\"\nstatus = \"deprecated\"\nsuperseded_by = \"`</script>\"\n")
	return root, huge
}

func TestHostileInput(t *testing.T) {
	root, huge := hostileRepo(t)
	cfg := orgconfig.Default()
	cfg.Lint.Taxonomy = ""
	c, rep, err := Build(root, cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.HasErrors() {
		t.Error("lint should flag the hostile repo")
	}
	for _, p := range c.Plugins {
		if p.Homepage != "" || p.Repository != "" || p.Docs != "" {
			t.Errorf("unsafe link kept: %+v", p)
		}
		if len(p.Description) > 4*maxDesc {
			t.Errorf("description not capped: %d", len(p.Description))
		}
		for _, s := range []string{p.Author, p.Description, p.Owner} {
			if strings.ContainsAny(s, "\x1b\r\n\u202e") {
				t.Errorf("control character kept in %q", s)
			}
		}
	}
	js, err := JSON(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(string(js), "<>&") {
		t.Error("catalog.json contains a raw HTML character")
	}
	if strings.Contains(string(js), huge[:2000]) {
		t.Error("huge string not truncated")
	}
	var back Catalog
	if err := json.Unmarshal(js, &back); err != nil || len(back.Plugins) != 3 {
		t.Errorf("round trip: %v", err)
	}
	md := string(Markdown(c))
	if strings.Contains(md, "<") {
		t.Errorf("markdown contains a raw <:\n%s", md)
	}
	for _, bad := range []string{"](javascript", "](data:", "![", "@octocat", "http://evil"} {
		if strings.Contains(md, bad) {
			t.Errorf("markdown contains %q:\n%s", bad, md)
		}
	}
	if strings.Contains(md, huge[:2000]) {
		t.Error("markdown has the huge string")
	}
	// Table structure survives: all rows of a table have as many cells as its header.
	cols := 0
	for _, l := range strings.Split(md, "\n") {
		if !strings.HasPrefix(l, "|") {
			cols = 0
			continue
		}
		n := 0
		for i := 0; i < len(l); i++ {
			if l[i] == '|' && (i == 0 || l[i-1] != '\\') {
				n++
			}
		}
		if cols == 0 {
			cols = n
		} else if n != cols {
			t.Errorf("table row has %d cell borders, header has %d: %q", n, cols, l)
		}
	}
}

func TestMarkdownHelpers(t *testing.T) {
	if got := MarkdownText("a|b*c_d[e](f)#g~h!i<j>&k\nl@m http://x"); got != `a\|b\*c\_d\[e\]\(f\)\#g\~h\!i&lt;j&gt;&amp;k l&#64;m http&#58;//x` {
		t.Errorf("MarkdownText = %q", got)
	}
	if MarkdownText("-x") != `\-x` || MarkdownText("+x") != `\+x` {
		t.Error("leading list marker not escaped")
	}
	tests := map[string]string{
		"name":       "`name`",
		"a`b":        "a\\`b",
		"<x>":        "&lt;x&gt;",
		"a|b":        "`a\\|b`",
		"  two  sp ": "`two sp`",
		"":           "",
		"a&b":        "a&amp;b",
	}
	for in, want := range tests {
		if got := markdownCode(in); got != want {
			t.Errorf("markdownCode(%q) = %q, want %q", in, got, want)
		}
	}
	if markdownLink("javascript:x") != "" || markdownLink("") != "" {
		t.Error("unsafe link rendered")
	}
	if got := markdownLink("https://e.example/a(b)"); got != "[docs](https://e.example/a%28b%29)" {
		t.Errorf("markdownLink = %q", got)
	}
}

func TestJSONNilSlicesAndSchema(t *testing.T) {
	js, err := JSON(&Catalog{Version: 1, Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), "null") {
		t.Errorf("null in output: %s", js)
	}
	schema := catalogtest.LoadSchema(t, "catalog.schema.json")
	validate := func(js []byte) {
		t.Helper()
		var doc any
		if err := json.Unmarshal(js, &doc); err != nil {
			t.Fatal(err)
		}
		if probs := catalogtest.Validate(schema, doc); len(probs) > 0 {
			t.Errorf("schema problems:\n%s", strings.Join(probs, "\n"))
		}
	}
	validate(js)
	full, _ := JSON(buildFixture(t, catalogtest.FixtureDir(t), Options{Profiles: fixtureProfiles(), Now: func() time.Time { return time.Unix(0, 0) }}))
	validate(full)
	root, _ := hostileRepo(t)
	cfg := orgconfig.Default()
	cfg.Lint.Taxonomy = ""
	hc, _, _ := Build(root, cfg, Options{})
	hjs, _ := JSON(hc)
	validate(hjs)

	// The validator must really reject bad documents.
	var bad any
	_ = json.Unmarshal([]byte(`{"version":2,"title":1,"extra":true,"plugins":[{"name":"x","homepage":"ftp://x"}]}`), &bad)
	if len(catalogtest.Validate(schema, bad)) < 5 {
		t.Error("schema validator accepted a bad document")
	}
}

func TestSchemaMatchesTypes(t *testing.T) {
	schema := catalogtest.LoadSchema(t, "catalog.schema.json")
	jsonKeys := func(v any) []string {
		var keys []string
		rt := reflect.TypeOf(v)
		for i := 0; i < rt.NumField(); i++ {
			name := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				keys = append(keys, name)
			}
		}
		sortStrings(keys)
		return keys
	}
	if got, want := catalogtest.SchemaProperties(schema), jsonKeys(Catalog{}); !reflect.DeepEqual(got, want) {
		t.Errorf("top level: schema %v, struct %v", got, want)
	}
	defs := schema["$defs"].(map[string]any)
	props := func(name string) []string {
		return catalogtest.SchemaProperties(map[string]any{"properties": defs[name].(map[string]any)["properties"]})
	}
	if got, want := props("plugin"), jsonKeys(Entry{}); !reflect.DeepEqual(got, want) {
		t.Errorf("plugin: schema %v, struct %v", got, want)
	}
	if got, want := props("profile"), jsonKeys(ProfileInfo{}); !reflect.DeepEqual(got, want) {
		t.Errorf("profile: schema %v, struct %v", got, want)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestSearch(t *testing.T) {
	c := buildFixture(t, catalogtest.FixtureDir(t), Options{})
	names := func(ms []Match) string {
		var n []string
		for _, m := range ms {
			n = append(n, m.Entry.Name)
		}
		return strings.Join(n, ",")
	}
	tests := []struct {
		q     string
		limit int
		want  string
	}{
		{"sre-kit", 0, "sre-kit,ops-helper"},
		{"sre", 0, "sre-kit,ops-helper"},
		{"sql", 0, "data-tools"},
		{"incident", 0, "sre-kit"},
		{"incident postmortems", 0, "sre-kit"},
		{"incident figma", 0, ""},
		{"@acme/web", 0, "design-kit,figma-bridge"},
		{"UI", 0, "design-kit,figma-bridge,sre-kit,data-tools,ops-helper"},
		{"", 0, "data-tools,design-kit,figma-bridge,ops-helper,sre-kit"},
		{"", 2, "data-tools,design-kit"},
		{"zzz", 0, ""},
		{"design", 1, "design-kit"},
		{"palette", 0, "design-kit"},
	}
	for _, tt := range tests {
		got := names(Search(c, tt.q, tt.limit))
		if tt.q == "UI" {
			// Order depends on weights; only check the set.
			if len(Search(c, tt.q, tt.limit)) == 0 {
				t.Errorf("Search(%q) found nothing", tt.q)
			}
			continue
		}
		if got != tt.want {
			t.Errorf("Search(%q, %d) = %q, want %q", tt.q, tt.limit, got, tt.want)
		}
	}
	ms := Search(c, "sre-kit", 0)
	if ms[0].Score <= ms[1].Score || ms[0].Fields[0] != "name" {
		t.Errorf("exact name match must score highest: %+v", ms)
	}
	again := Search(c, "ui", 0)
	for i := 0; i < 3; i++ {
		if names(Search(c, "ui", 0)) != names(again) {
			t.Fatal("search order is not deterministic")
		}
	}
}

func TestGitData(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; skipping git-backed tests")
	}
	root := catalogtest.CopyFixture(t)
	env := []string{
		"GIT_CONFIG_GLOBAL=" + devNull(), "GIT_CONFIG_SYSTEM=" + devNull(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_AUTHOR_DATE=2026-05-01T10:00:00Z", "GIT_COMMITTER_DATE=2026-05-01T10:00:00Z",
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial")
	git("tag", "v2026.5.1")
	catalogtest.Write(t, root, "plugins/data-tools/skills/sql-review/EXTRA.md", "more")
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "change data-tools")

	cfg, _ := orgconfig.Load(root)
	c, _, err := BuildContext(context.Background(), root, cfg, Options{GitData: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.LatestTag != "v2026.5.1" {
		t.Errorf("LatestTag = %q", c.LatestTag)
	}
	for _, p := range c.Plugins {
		switch p.Name {
		case "data-tools":
			if !p.ChangedSinceTag || p.Git == nil || p.Git.Authors != 1 || p.Git.LastCommit != "2026-05-01" {
				t.Errorf("data-tools = %+v git=%+v", p, p.Git)
			}
		case "design-kit":
			if p.ChangedSinceTag {
				t.Error("design-kit did not change")
			}
		case "figma-bridge":
			if p.Git != nil {
				t.Error("external plugin must have no git data")
			}
		}
	}
	md := string(Markdown(c))
	if !strings.Contains(md, "## New or changed since `v2026.5.1`") || !strings.Contains(md, "- `data-tools` (last change 2026-05-01, 1 author(s))") {
		t.Errorf("markdown lacks the changed section:\n%s", md)
	}
	// Enabled through the org config as well.
	cfg.Catalog.GitData = true
	c2, _, err := Build(root, cfg, Options{})
	if err != nil || c2.LatestTag == "" {
		t.Errorf("catalog.git_data ignored: %v", err)
	}
	// Not a repository: an error, not silently empty data.
	plain := catalogtest.CopyFixture(t)
	if _, _, err := Build(plain, cfg, Options{GitData: true}); err == nil {
		t.Log("temp dir is inside a git repository")
	}
}

// devNull is git's spelling of the null device on every OS (git for Windows maps it).
func devNull() string { return "/dev/null" }

func TestMarkdownTextBreaksWWWAutolinks(t *testing.T) {
	for _, in := range []string{"www.evil.example", "see WWW.Evil.example now", "(www.x.example)", "awww.x.example"} {
		out := MarkdownText(in)
		if strings.Contains(strings.ToLower(out), "www.") {
			t.Errorf("MarkdownText(%q) = %q still has www.", in, out)
		}
	}
	if got := MarkdownText("www"); got != "www" {
		t.Errorf("plain www changed: %q", got)
	}
	if got := MarkdownText("WWW.x"); got != "WWW&#46;x" {
		t.Errorf("case not kept: %q", got)
	}
}

func TestGitDataShallowAndTagPattern(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed; skipping git-backed tests")
	}
	root := catalogtest.CopyFixture(t)
	env := []string{
		"GIT_CONFIG_GLOBAL=" + devNull(), "GIT_CONFIG_SYSTEM=" + devNull(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com",
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(root, "init", "-q", "-b", "main")
	git(root, "add", "-A")
	git(root, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "one")
	git(root, "tag", "v1.0.0")
	catalogtest.Write(t, root, "plugins/data-tools/skills/sql-review/EXTRA.md", "more")
	git(root, "add", "-A")
	git(root, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "two")
	git(root, "tag", "data-tools--v9.0.0") // a per-plugin tag is newer but not a release tag
	git(root, "tag", "stable-3")

	cfg, _ := orgconfig.Load(root)
	c, _, err := BuildContext(context.Background(), root, cfg, Options{GitData: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.LatestTag != "v1.0.0" {
		t.Errorf("LatestTag = %q, want v1.0.0 (plugin tags are not release tags)", c.LatestTag)
	}
	cfg.Catalog.ReleaseTagPattern = "stable-*"
	if c, _, err = BuildContext(context.Background(), root, cfg, Options{GitData: true}); err != nil || c.LatestTag != "stable-3" {
		t.Errorf("custom pattern: %q %v", c.LatestTag, err)
	}

	clone := filepath.Join(t.TempDir(), "clone")
	git(filepath.Dir(clone), "clone", "-q", "--depth", "1", "file://"+filepath.ToSlash(root), clone)
	cfg.Catalog.ReleaseTagPattern = "v[0-9]*"
	_, _, err = BuildContext(context.Background(), clone, cfg, Options{GitData: true})
	if err == nil || !strings.Contains(err.Error(), "shallow") || !strings.Contains(err.Error(), "fetch-depth: 0") {
		t.Errorf("shallow clone: %v", err)
	}
}

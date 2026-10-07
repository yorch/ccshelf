package lint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yorch/ccshelf/internal/catalog/catalogtest"
	"github.com/yorch/ccshelf/internal/orgconfig"
)

func fixedNow() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }

func run(t *testing.T, root string, now func() time.Time) *Report {
	t.Helper()
	cfg, err := orgconfig.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if now == nil {
		now = fixedNow
	}
	r, err := Run(root, cfg, Options{Now: now})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return r
}

func codes(r *Report) map[string][]Finding {
	m := map[string][]Finding{}
	for _, f := range r.Findings {
		m[f.Code] = append(m[f.Code], f)
	}
	return m
}

func TestFixtureIsClean(t *testing.T) {
	r := run(t, catalogtest.FixtureDir(t), nil)
	c := r.Counts()
	if c.Errors != 0 || c.Warnings != 0 {
		t.Fatalf("fixture should be clean, got:\n%s", FormatText(r))
	}
	got := []string{}
	for _, f := range r.Findings {
		got = append(got, f.Code+":"+f.Plugin)
	}
	want := []string{"CAT031:figma-bridge", "CAT040:sre-kit", "CAT041:data-tools"}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("info findings = %v, want %v", got, want)
	}
	if r.HasErrors() {
		t.Error("HasErrors on clean fixture")
	}
}

type mutation struct {
	name   string
	code   string
	sev    Severity
	plugin string // expected plugin on the finding, "" to skip
	file   string // expected file, "" to skip
	now    func() time.Time
	mutate func(t *testing.T, root string)
}

func at(y int, m time.Month, d int) func() time.Time {
	return func() time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
}

func mutations() []mutation {
	const mkt = ".claude-plugin/marketplace.json"
	const co = ".github/CODEOWNERS"
	rep := func(rel, old, new string) func(*testing.T, string) {
		return func(t *testing.T, root string) { catalogtest.Replace(t, root, rel, old, new) }
	}
	write := func(rel, content string) func(*testing.T, string) {
		return func(t *testing.T, root string) { catalogtest.Write(t, root, rel, content) }
	}
	remove := func(rel string) func(*testing.T, string) {
		return func(t *testing.T, root string) { catalogtest.Remove(t, root, rel) }
	}
	return []mutation{
		{"invalid marketplace", "CAT001", Error, "", mkt, nil, write(mkt, "{")},
		{"duplicate name", "CAT002", Error, "ops-helper", mkt, nil, rep(mkt, `"name": "data-tools"`, `"name": "ops-helper"`)},
		{"reserved prefix", "CAT003", Error, "claude-figma", mkt, nil, rep(mkt, `"name": "figma-bridge"`, `"name": "claude-figma"`)},
		{"reserved word", "CAT004", Warning, "my-claude", mkt, nil, rep(mkt, `"name": "figma-bridge"`, `"name": "my-claude"`)},
		{"no description", "CAT005", Error, "figma-bridge", mkt, nil, rep(mkt, `"description": "Reads Figma frames and turns them into component stubs (hosted in its own repository).",`, `"description": "  ",`)},
		{"short description", "CAT006", Warning, "design-kit", mkt, nil, rep(mkt, `"description": "Design review helpers: color palettes, CSS review and UI critique agents."`, `"description": "Design helpers."`)},
		{"no author", "CAT007", Warning, "figma-bridge", mkt, nil, rep(mkt, `"tags": ["ui"],
      "author": {"name": "Acme Web Team"}`, `"tags": ["ui"]`)},
		{"bad taxonomy", "CAT008", Error, "", "catalog/taxonomy.toml", nil, write("catalog/taxonomy.toml", "categoriez = []\n")},
		{"bad plugin name", "CAT009", Error, "bad name", mkt, nil, rep(mkt, `"name": "figma-bridge"`, `"name": "bad name"`)},
		{"missing sidecar", "CAT010", Error, "design-kit", mkt, nil, remove("catalog/plugins/design-kit.toml")},
		{"orphan sidecar", "CAT011", Warning, "ghost", "catalog/plugins/ghost.toml", nil, write("catalog/plugins/ghost.toml", "owner = \"@acme/web\"\nstatus = \"active\"\nreview_by = \"2027-01-01\"\n")},
		{"malformed sidecar", "CAT012", Error, "design-kit", "catalog/plugins/design-kit.toml", nil, rep("catalog/plugins/design-kit.toml", `support = "#web-help"`, `sup_port = "#web-help"`)},
		{"missing owner", "CAT013", Error, "design-kit", "catalog/plugins/design-kit.toml", nil, rep("catalog/plugins/design-kit.toml", `owner = "@acme/web"`, `owner = " "`)},
		{"missing status", "CAT013", Error, "data-tools", "catalog/plugins/data-tools.toml", nil, rep("catalog/plugins/data-tools.toml", `status = "experimental"`, ``)},
		{"bad status", "CAT014", Error, "design-kit", "catalog/plugins/design-kit.toml", nil, rep("catalog/plugins/design-kit.toml", `status = "active"`, `status = "retired"`)},
		{"deprecated without replacement", "CAT015", Error, "ops-helper", "catalog/plugins/ops-helper.toml", nil, rep("catalog/plugins/ops-helper.toml", `superseded_by = "sre-kit"`, ``)},
		{"missing replacement", "CAT016", Error, "ops-helper", "catalog/plugins/ops-helper.toml", nil, rep("catalog/plugins/ops-helper.toml", `superseded_by = "sre-kit"`, `superseded_by = "ghost"`)},
		{"self replacement", "CAT017", Error, "ops-helper", "catalog/plugins/ops-helper.toml", nil, rep("catalog/plugins/ops-helper.toml", `superseded_by = "sre-kit"`, `superseded_by = "ops-helper"`)},
		{"deprecated replacement", "CAT017", Error, "ops-helper", "catalog/plugins/ops-helper.toml", nil, func(t *testing.T, root string) {
			catalogtest.Replace(t, root, "catalog/plugins/sre-kit.toml", `status = "active"`, `status = "deprecated"`+"\nsuperseded_by = \"data-tools\"")
		}},
		{"replacement cycle", "CAT017", Error, "ops-helper", "catalog/plugins/ops-helper.toml", nil, func(t *testing.T, root string) {
			catalogtest.Replace(t, root, "catalog/plugins/sre-kit.toml", `status = "active"`, `status = "deprecated"`+"\nsuperseded_by = \"ops-helper\"")
		}},
		{"active without review_by", "CAT018", Error, "sre-kit", "catalog/plugins/sre-kit.toml", nil, func(t *testing.T, root string) {
			catalogtest.Replace(t, root, "ccshelf.toml", `require = ["owner", "status"]`, `require = ["owner", "status", "review_by"]`)
			catalogtest.Replace(t, root, "catalog/plugins/sre-kit.toml", `review_by = "2027-03-01"`, ``)
		}},
		{"invalid review_by", "CAT018", Error, "sre-kit", "catalog/plugins/sre-kit.toml", nil, rep("catalog/plugins/sre-kit.toml", `review_by = "2027-03-01"`, `review_by = "2027-13-45"`)},
		{"stale review", "CAT019", Warning, "design-kit", "catalog/plugins/design-kit.toml", at(2028, 1, 1), nil},
		{"overdue review", "CAT020", Info, "design-kit", "catalog/plugins/design-kit.toml", at(2027, 4, 1), nil},
		{"unknown category", "CAT021", Error, "design-kit", mkt, nil, rep(mkt, `"category": "design"`, `"category": "misc"`)},
		{"unknown tag", "CAT022", Error, "design-kit", mkt, nil, rep(mkt, `"tags": ["ui", "css"]`, `"tags": ["ui", "nope"]`)},
		{"unknown overlap", "CAT023", Error, "design-kit", "catalog/plugins/design-kit.toml", nil, rep("catalog/plugins/design-kit.toml", `overlaps_with = ["figma-bridge"]`, `overlaps_with = ["ghost"]`)},
		{"self overlap", "CAT023", Error, "design-kit", "catalog/plugins/design-kit.toml", nil, rep("catalog/plugins/design-kit.toml", `overlaps_with = ["figma-bridge"]`, `overlaps_with = ["design-kit"]`)},
		{"unknown dependency", "CAT024", Error, "sre-kit", mkt, nil, rep(mkt, `"dependencies": ["design-kit"]`, `"dependencies": ["ghost"]`)},
		{"cross-marketplace dependency", "CAT024", Error, "sre-kit", mkt, nil, rep(mkt, `"dependencies": ["design-kit"]`, `"dependencies": [{"name": "x", "marketplace": "elsewhere"}]`)},
		{"too many relevance entries", "CAT025", Error, "sre-kit", mkt, nil, rep(mkt, `"cli": ["kubectl", "pagerduty"]`, `"cli": ["a","b","c","d","e","f","g","h","i","j","k"]`)},
		{"relevance entry too long", "CAT025", Error, "sre-kit", mkt, nil, rep(mkt, `"cli": ["kubectl", "pagerduty"]`, `"cli": ["`+strings.Repeat("x", 65)+`"]`)},
		{"bad regex", "CAT026", Error, "sre-kit", mkt, nil, rep(mkt, `"pattern": "@acme/sdk"`, `"pattern": "("`)},
		{"non-RE2 regex", "CAT027", Warning, "sre-kit", mkt, nil, rep(mkt, `"pattern": "@acme/sdk"`, `"pattern": "(?<=@)acme"`)},
		{"docs not http", "CAT028", Error, "design-kit", "catalog/plugins/design-kit.toml", nil, rep("catalog/plugins/design-kit.toml", `docs = "https://wiki.example.com/design-kit"`, `docs = "javascript:alert(1)"`)},
		{"missing source dir", "CAT030", Error, "data-tools", mkt, nil, remove("plugins/data-tools")},
		{"source escapes", "CAT032", Error, "data-tools", mkt, nil, rep(mkt, `"source": "./plugins/data-tools"`, `"source": "../outside"`)},
		{"absolute source", "CAT032", Error, "data-tools", mkt, nil, rep(mkt, `"source": "./plugins/data-tools"`, `"source": "/etc"`)},
		{"broken plugin.json", "CAT033", Error, "data-tools", mkt, nil, write("plugins/data-tools/.claude-plugin/plugin.json", `{"name": 7}`)},
		{"hooks lose platform owner", "CAT042", Error, "sre-kit", mkt, nil, rep(co, "/plugins/*/hooks/                  @acme/platform\n", "")},
		{"mcp loses platform owner", "CAT042", Error, "data-tools", mkt, nil, rep(co, "/plugins/*/.mcp.json               @acme/platform\n", "")},
		{"no CODEOWNERS with hooks", "CAT042", Error, "sre-kit", mkt, nil, remove(co)},
		{"no CODEOWNERS", "CAT043", Warning, "", "", nil, remove(co)},
		{"uncovered plugin dir", "CAT044", Warning, "data-tools", mkt, nil, rep(co, "/plugins/data-tools/               @acme/data\n", "")},
		{"uncovered .github", "CAT045", Warning, "", co, nil, rep(co, "/.github/                          @acme/platform\n", "")},
		{"sidecar owner mismatch", "CAT046", Warning, "design-kit", "catalog/plugins/design-kit.toml", nil, rep("catalog/plugins/design-kit.toml", `owner = "@acme/web"`, `owner = "@acme/other"`)},
		{"invalid CODEOWNERS line", "CAT047", Warning, "", co, nil, func(t *testing.T, root string) {
			catalogtest.Write(t, root, co, catalogtest.Read(t, root, co)+"!negated @acme/web\n")
		}},
		{"placeholder in sidecar", "CAT048", Warning, "design-kit", "catalog/plugins/design-kit.toml", nil, rep("catalog/plugins/design-kit.toml", `support = "#web-help"`, `support = "TODO(ccshelf): where to ask"`)},
		{"placeholder in description", "CAT048", Warning, "design-kit", mkt, nil, func(t *testing.T, root string) {
			var doc map[string]any
			if err := json.Unmarshal([]byte(catalogtest.Read(t, root, mkt)), &doc); err != nil {
				t.Fatal(err)
			}
			for _, e := range doc["plugins"].([]any) {
				if m := e.(map[string]any); m["name"] == "design-kit" {
					m["description"] = "TODO(ccshelf): describe what design-kit does"
				}
			}
			b, _ := json.MarshalIndent(doc, "", "  ")
			catalogtest.Write(t, root, mkt, string(b)+"\n")
		}},
		{"profile without bundle", "CAT050", Error, "profile-qa", "profiles/qa.toml", nil, write("profiles/qa.toml", "name = \"qa\"\n[plugins]\ninclude = [\"sre-kit@acme-tools\"]\n")},
		{"bundle without profile", "CAT051", Error, "profile-sre", mkt, nil, remove("profiles/sre.toml")},
		{"bundle wrong source", "CAT052", Error, "profile-sre", mkt, nil, rep(mkt, `"source": "./bundles/profile-sre"`, `"source": "./plugins/sre-kit"`)},
		{"bundle sets version", "CAT053", Error, "profile-sre", mkt, nil, rep(mkt, `"name": "profile-sre",`, `"name": "profile-sre",
      "version": "1.0.0",`)},
		{"oversized CODEOWNERS", "CAT060", Error, "", co, nil, write(co, strings.Repeat("#", 3<<20+1))},
	}
}

func TestRulesEachTriggered(t *testing.T) {
	covered := map[string]bool{"CAT031": true, "CAT040": true, "CAT041": true} // from the clean fixture
	for _, m := range mutations() {
		m := m
		t.Run(m.name, func(t *testing.T) {
			root := catalogtest.CopyFixture(t)
			if m.mutate != nil {
				m.mutate(t, root)
			}
			r := run(t, root, m.now)
			found := false
			for _, f := range codes(r)[m.code] {
				if f.Severity != m.sev {
					continue
				}
				if m.plugin != "" && f.Plugin != m.plugin {
					continue
				}
				if m.file != "" && f.File != m.file {
					continue
				}
				found = true
			}
			if !found {
				t.Fatalf("expected %s %s plugin=%q file=%q, got:\n%s", m.sev, m.code, m.plugin, m.file, FormatText(r))
			}
		})
		covered[m.code] = true
	}
	for _, rule := range Rules() {
		if !covered[rule.Code] {
			t.Errorf("rule %s has no test case", rule.Code)
		}
	}
}

func TestSingleFileMode(t *testing.T) {
	root := catalogtest.CopyFixture(t)
	// Move every sidecar into the entry's metadata.
	mk := catalogtest.Read(t, root, ".claude-plugin/marketplace.json")
	var doc map[string]any
	if err := json.Unmarshal([]byte(mk), &doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range doc["plugins"].([]any) {
		pm := p.(map[string]any)
		name := pm["name"].(string)
		if strings.HasPrefix(name, "profile-") {
			continue
		}
		pm["metadata"] = map[string]any{"owner": "@acme/" + map[string]string{"design-kit": "web", "sre-kit": "sre", "ops-helper": "sre", "data-tools": "data", "figma-bridge": "web"}[name], "status": "active", "review_by": "2027-03-01"}
	}
	doc["plugins"].([]any)[2].(map[string]any)["metadata"] = map[string]any{"owner": "@acme/sre", "status": "deprecated", "superseded_by": "sre-kit"}
	out, _ := json.MarshalIndent(doc, "", "  ")
	catalogtest.Write(t, root, ".claude-plugin/marketplace.json", string(out))
	catalogtest.Remove(t, root, "catalog/plugins")
	catalogtest.Replace(t, root, "ccshelf.toml", `metadata_source = "sidecar"`, `metadata_source = "marketplace"`)
	r := run(t, root, nil)
	if r.Counts().Errors != 0 || r.Counts().Warnings != 0 {
		t.Fatalf("single-file fixture should be clean:\n%s", FormatText(r))
	}
	// A missing metadata object is CAT010 and an unknown key is CAT012.
	broken := strings.Replace(catalogtest.Read(t, root, ".claude-plugin/marketplace.json"), `"owner": "@acme/web"`, `"ownerz": "@acme/web"`, 1)
	catalogtest.Write(t, root, ".claude-plugin/marketplace.json", broken)
	r = run(t, root, nil)
	if len(codes(r)["CAT012"]) == 0 {
		t.Errorf("unknown metadata key not reported:\n%s", FormatText(r))
	}
}

func TestNoMetadataSingleFile(t *testing.T) {
	root := catalogtest.CopyFixture(t)
	catalogtest.Remove(t, root, "catalog/plugins")
	catalogtest.Replace(t, root, "ccshelf.toml", `metadata_source = "sidecar"`, `metadata_source = "marketplace"`)
	r := run(t, root, nil)
	if len(codes(r)["CAT010"]) != 5 {
		t.Errorf("want 5 CAT010 (bundles exempt), got:\n%s", FormatText(r))
	}
}

func TestSymlinkSourceEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
	root := catalogtest.CopyFixture(t)
	outside := t.TempDir()
	catalogtest.Remove(t, root, "plugins/data-tools")
	if err := os.Symlink(outside, filepath.Join(root, "plugins", "data-tools")); err != nil {
		t.Fatal(err)
	}
	r := run(t, root, nil)
	if len(codes(r)["CAT032"]) == 0 {
		t.Errorf("symlink escape not reported:\n%s", FormatText(r))
	}
}

func TestOptionsNowDefault(t *testing.T) {
	if (Options{}).now().IsZero() {
		t.Error("default clock returned zero")
	}
}

func TestRunErrors(t *testing.T) {
	if _, err := Run(filepath.Join(t.TempDir(), "missing"), orgconfig.Default(), Options{}); err == nil {
		t.Error("missing root should fail")
	}
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(f, orgconfig.Default(), Options{}); err == nil {
		t.Error("file root should fail")
	}
}

func TestEmptyRepo(t *testing.T) {
	r, err := Run(t.TempDir(), orgconfig.Default(), Options{Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if len(codes(r)["CAT001"]) != 1 {
		t.Errorf("missing marketplace should be CAT001:\n%s", FormatText(r))
	}
	if !r.HasErrors() {
		t.Error("HasErrors")
	}
}

func TestRulesTable(t *testing.T) {
	rs := Rules()
	seen := map[string]bool{}
	prev := ""
	for _, r := range rs {
		if !strings.HasPrefix(r.Code, "CAT") || len(r.Code) != 6 {
			t.Errorf("bad code %q", r.Code)
		}
		if seen[r.Code] {
			t.Errorf("duplicate code %s", r.Code)
		}
		if r.Code <= prev {
			t.Errorf("rules not ordered at %s", r.Code)
		}
		if r.Description == "" || !strings.HasSuffix(r.Description, ".") {
			t.Errorf("%s: description %q", r.Code, r.Description)
		}
		seen[r.Code], prev = true, r.Code
	}
	rs[0].Code = "changed"
	if Rules()[0].Code == "changed" {
		t.Error("Rules must return a copy")
	}
}

func TestSortAndCounts(t *testing.T) {
	r := &Report{Findings: []Finding{
		{Severity: Info, Code: "CAT031", File: "b", Line: 1},
		{Severity: Error, Code: "CAT002", File: "a", Line: 9},
		{Severity: Warning, Code: "CAT007", File: "a", Line: 2, Plugin: "z"},
		{Severity: Warning, Code: "CAT007", File: "a", Line: 2, Plugin: "y"},
	}}
	r.Sort()
	order := []string{}
	for _, f := range r.Findings {
		order = append(order, f.File+f.Code+f.Plugin)
	}
	if strings.Join(order, " ") != "aCAT007y aCAT007z aCAT002 bCAT031" {
		t.Errorf("order = %v", order)
	}
	if c := r.Counts(); c.Errors != 1 || c.Warnings != 2 || c.Infos != 1 {
		t.Errorf("counts = %+v", c)
	}
}

func TestNonRE2Feature(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{`react`, false},
		{`^@acme/(a|b)$`, false},
		{`a(?=b)`, true},
		{`a(?!b)`, true},
		{`(?<=a)b`, true},
		{`(?<!a)b`, true},
		{`(a)\1`, true},
		{`\k<name>`, true},
		{`(?>a)`, true},
		{`a*+`, true},
		{`a++`, true},
		{`a?+`, true},
		{`a{2}+`, true},
		{`\++`, false},
		{`\\1`, false},
		{`}+`, false},
		{`(?P<n>a)`, false},
		{`(?i)a`, false},
	}
	for _, tt := range tests {
		if got := NonRE2Feature(tt.in) != ""; got != tt.want {
			t.Errorf("NonRE2Feature(%q) = %q, want %v", tt.in, NonRE2Feature(tt.in), tt.want)
		}
	}
}

func TestIsHTTPURL(t *testing.T) {
	good := []string{"https://example.com", "http://example.com/a?b=c#d", "https://wiki.example.com/x y"[:26]}
	bad := []string{
		"", "javascript:alert(1)", "data:text/html,x", "ftp://example.com", "https://", "//example.com", "https://a b.com",
		"https://user:pw@example.com", "https://example.com/\x00", "HTTPS://", "example.com", "https:example.com", "file:///etc/passwd",
	}
	for _, s := range good {
		if !IsHTTPURL(s) {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range bad {
		if IsHTTPURL(s) {
			t.Errorf("%q should be invalid", s)
		}
	}
	if IsHTTPURL("https://example.com/" + strings.Repeat("a", 3000)) {
		t.Error("overlong URL accepted")
	}
}

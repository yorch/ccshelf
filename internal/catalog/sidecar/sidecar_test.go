package sidecar

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/catalog/catalogtest"
	toml "github.com/pelletier/go-toml/v2"

	"github.com/ccshelf/ccshelf/internal/marketplace"
	"github.com/ccshelf/ccshelf/internal/orgconfig"
)

const good = `# comment
owner = "@acme/sre"
status = "active"
when_to_use = ["incident response", "postmortems"]
avoid_when = ["routine deploy checks"]
overlaps_with = ["ops-helper"]
superseded_by = ""
review_by = 2027-03-01
support = "#sre-help"
docs = "https://wiki.example/sre-kit"
`

func TestParseGood(t *testing.T) {
	s, err := Parse("catalog/plugins/sre-kit.toml", []byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if s.Owner != "@acme/sre" || s.Status != "active" || s.ReviewBy != "2027-03-01" || s.Docs != "https://wiki.example/sre-kit" ||
		!reflect.DeepEqual(s.WhenToUse, []string{"incident response", "postmortems"}) {
		t.Errorf("parsed = %+v", s)
	}
	if s.Line != 2 || s.FieldLines["status"] != 3 || s.LineOf("docs") != 10 || s.LineOf("nope") != 2 {
		t.Errorf("lines = %d %v", s.Line, s.FieldLines)
	}
	if !s.Has("superseded_by") || s.Has("nothere") || (*Sidecar)(nil).Has("x") {
		t.Errorf("Present = %v", s.Present)
	}
	d, ok, err := s.ReviewDate()
	if err != nil || !ok || !d.Equal(time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("ReviewDate = %v %v %v", d, ok, err)
	}
	s2, err := Parse("f", []byte(`review_by = "2027-03-01"`))
	if err != nil || s2.ReviewBy != "2027-03-01" {
		t.Errorf("string date: %+v %v", s2, err)
	}
}

func TestReviewDate(t *testing.T) {
	if _, ok, err := (&Sidecar{}).ReviewDate(); ok || err != nil {
		t.Error("empty date")
	}
	for _, bad := range []string{"2027-13-01", "tomorrow", "2027-3-1", "2027-02-30"} {
		if _, ok, err := (&Sidecar{ReviewBy: bad}).ReviewDate(); !ok || err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, in, want string
		line           int
	}{
		{"unknown", "owner = \"a\"\nbogus = 1\n", "unknown key", 2},
		{"type", "owner = 3\n", "owner", 1},
		{"list type", "when_to_use = \"x\"\n", "when_to_use", 1},
		{"syntax", "owner = \n", "", 1},
		{"date type", "owner = \"a\"\nreview_by = 5\n", "review_by", 2},
		{"datetime", "review_by = 2027-03-01T10:00:00Z\n", "review_by", 1},
		{"table", "[x]\na = 1\n", "unknown", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("f", []byte(tt.in))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want containing %q", err, tt.want)
			}
			var le *lineError
			if errors.As(err, &le) && tt.line > 0 && le.line != tt.line {
				t.Errorf("line = %d, want %d", le.line, tt.line)
			}
		})
	}
}

func TestFromMetadata(t *testing.T) {
	s, err := FromMetadata("m.json", map[string]any{
		"owner": "@a/b", "status": "deprecated", "when_to_use": []any{"x", "y"},
		"superseded_by": "n", "review_by": "2027-01-01", "support": "s", "docs": "https://x.example",
		"avoid_when": []any{}, "overlaps_with": []any{"z"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Owner != "@a/b" || s.Status != "deprecated" || len(s.WhenToUse) != 2 || s.SupersededBy != "n" || s.ReviewBy != "2027-01-01" ||
		!s.Has("avoid_when") || s.File != "m.json" || s.OverlapsWith[0] != "z" {
		t.Errorf("= %+v", s)
	}
	bad := []map[string]any{
		{"owner": 1},
		{"when_to_use": "x"},
		{"when_to_use": []any{1}},
		{"nope": "x"},
		{"status": []any{}},
	}
	for _, md := range bad {
		if _, err := FromMetadata("m", md); err == nil {
			t.Errorf("%v should fail", md)
		}
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSidecarsDir(t *testing.T) {
	root := t.TempDir()
	cfg := orgconfig.Default()
	m, probs, err := LoadSidecars(root, cfg)
	if err != nil || len(m) != 0 || len(probs) != 0 {
		t.Fatalf("empty = %v %v %v", m, probs, err)
	}
	write(t, root, "catalog/plugins/sre-kit.toml", good)
	write(t, root, "catalog/plugins/broken.toml", "bogus = 1\n")
	write(t, root, "catalog/plugins/bad name.toml", "owner = \"a\"\n")
	write(t, root, "catalog/plugins/README.md", "ignored")
	write(t, root, "catalog/plugins/huge.toml", strings.Repeat("#", MaxFileSize+1))
	m, probs, err = LoadSidecars(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["sre-kit"] == nil || m["sre-kit"].File != "catalog/plugins/sre-kit.toml" {
		t.Errorf("map = %v", m)
	}
	if len(probs) != 3 {
		t.Fatalf("problems = %+v", probs)
	}
	for _, p := range probs {
		if p.File == "" || p.Message == "" || p.Error() == "" {
			t.Errorf("incomplete problem %+v", p)
		}
	}
	if probs[0].Plugin != "bad name" && probs[0].Plugin != "" {
		t.Logf("order: %+v", probs)
	}
}

func TestLoadSidecarsSingleFile(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude-plugin/marketplace.json", `{"name":"m","plugins":[
	 {"name":"a","source":"./a","metadata":{"owner":"@x/y","status":"active"}},
	 {"name":"b","source":"./b"},
	 {"name":"c","source":"./c","metadata":{"owner":5}}]}`)
	cfg := orgconfig.Default()
	cfg.Catalog.MetadataSource = orgconfig.SourceMarketplace
	m, probs, err := LoadSidecars(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["a"].Owner != "@x/y" || len(probs) != 1 || probs[0].Plugin != "c" {
		t.Errorf("m=%v probs=%+v", m, probs)
	}
	cfg.Catalog.Marketplaces = []string{"missing.json"}
	if _, _, err := LoadSidecars(root, cfg); err == nil {
		t.Error("missing marketplace should fail")
	}
}

func TestFromMarketplace(t *testing.T) {
	m := &marketplace.Marketplace{Plugins: []marketplace.Plugin{{Name: "a", Metadata: map[string]any{"owner": "@o/p"}}, {Name: "b"}}}
	got, probs := FromMarketplace("f", m)
	if len(got) != 1 || len(probs) != 0 {
		t.Errorf("%v %v", got, probs)
	}
}

func TestTaxonomy(t *testing.T) {
	root := t.TempDir()
	cfg := orgconfig.Default()
	if tx, err := LoadTaxonomy(root, cfg); tx != nil || err != nil {
		t.Errorf("missing taxonomy: %v %v", tx, err)
	}
	write(t, root, "catalog/taxonomy.toml", "categories = [\"web\", \"data\"]\ntags = [\"ui\", \"sql\"]\n")
	tx, err := LoadTaxonomy(root, cfg)
	if err != nil || !tx.HasCategory("web") || tx.HasCategory("x") || !tx.HasTag("sql") || tx.HasTag("web") || tx.Categories[0] != "data" || tx.File != "catalog/taxonomy.toml" {
		t.Errorf("taxonomy = %+v %v", tx, err)
	}
	write(t, root, "catalog/taxonomy.toml", "categoriez = [\"web\"]\n")
	if _, err := LoadTaxonomy(root, cfg); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Errorf("unknown key: %v", err)
	}
	write(t, root, "catalog/taxonomy.toml", "categories = 3\n")
	if _, err := LoadTaxonomy(root, cfg); err == nil {
		t.Error("type error expected")
	}
	cfg.Lint.Taxonomy = ""
	if tx, err := LoadTaxonomy(root, cfg); tx != nil || err != nil {
		t.Error("empty path disables taxonomy")
	}
}

func TestValidStatus(t *testing.T) {
	if !ValidStatus("active") || !ValidStatus("experimental") || !ValidStatus("deprecated") || ValidStatus("Active") || ValidStatus("") {
		t.Error("ValidStatus")
	}
}

func TestSchemaMatchesFields(t *testing.T) {
	s := catalogtest.LoadSchema(t, "sidecar.schema.json")
	got := catalogtest.SchemaProperties(s)
	want := append([]string(nil), orgconfig.SidecarFields()...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("schema %v, fields %v", got, want)
	}
	var doc map[string]any
	if err := toml.Unmarshal([]byte(strings.Replace(good, "review_by = 2027-03-01", `review_by = "2027-03-01"`, 1)), &doc); err != nil {
		t.Fatal(err)
	}
	if probs := catalogtest.Validate(s, doc); len(probs) > 0 {
		t.Errorf("schema problems: %v", probs)
	}
}

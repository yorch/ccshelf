package analytics

import (
	"os"
	"strings"
	"testing"
)

func TestParseOTelJSONLFixture(t *testing.T) {
	f, err := os.Open("testdata/otel.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	u, err := ParseOTelJSONL(f)
	if err != nil {
		t.Fatal(err)
	}
	if u.Source != "otel" || u.Skipped != 1 || u.Redacted != 1 {
		t.Errorf("source=%q skipped=%d redacted=%d", u.Source, u.Skipped, u.Redacted)
	}
	want := map[string]Counts{
		"sre-kit@acme":   {Invocations: 2, Loads: 1},
		"design-kit":     {Invocations: 1},
		"seo-tools@acme": {Installs: 1},
	}
	if len(u.PerPlugin) != len(want) {
		t.Fatalf("PerPlugin = %+v", u.PerPlugin)
	}
	for k, w := range want {
		if u.PerPlugin[k] != w {
			t.Errorf("%s = %+v, want %+v", k, u.PerPlugin[k], w)
		}
	}
	if u.From != "2026-08-29" || u.To == "" {
		t.Errorf("window = %q..%q", u.From, u.To)
	}
	if c, ok := u.Lookup("design-kit@acme"); !ok || c.Invocations != 1 {
		t.Errorf("Lookup by id of a name-keyed plugin = %+v %v", c, ok)
	}
}

func TestParseOTelJSONLErrors(t *testing.T) {
	if _, err := ParseOTelJSONL(strings.NewReader("hello\nworld\n")); err == nil {
		t.Error("no JSON objects: want error")
	}
	if u, err := ParseOTelJSONL(strings.NewReader("")); err != nil || len(u.PerPlugin) != 0 {
		t.Errorf("empty input: %v %v", u, err)
	}
	long := `{"x":"` + strings.Repeat("a", maxLineSize) + `"}`
	if _, err := ParseOTelJSONL(strings.NewReader(long)); err == nil {
		t.Error("long line: want error")
	}
}

func TestEventTime(t *testing.T) {
	tests := []struct {
		in   string
		year int
		ok   bool
	}{
		{"2026-09-01T10:00:00Z", 2026, true},
		{"1788000000000000000", 2026, true},
		{"1788000000000000", 2026, true},
		{"1788000000000", 2026, true},
		{"1788000000", 2026, true},
		{"yesterday", 0, false},
		{"", 0, false},
	}
	for _, tc := range tests {
		got, ok := eventTime(map[string]string{"time": tc.in})
		if ok != tc.ok || (ok && got.UTC().Year() != tc.year) {
			t.Errorf("eventTime(%q) = %v %v", tc.in, got, ok)
		}
	}
}

func TestPluginKeyMarketplaceAndRedaction(t *testing.T) {
	tests := []struct {
		name  string
		ev    map[string]string
		skill bool
		want  string
	}{
		{"id given", map[string]string{"plugin.name": "a@m"}, false, "a@m"},
		{"joined", map[string]string{"plugin_name": "a", "marketplace": "m"}, false, "a@m"},
		{"redacted marketplace", map[string]string{"plugin_name": "a", "marketplace": "<redacted>"}, false, "a"},
		{"redacted plugin", map[string]string{"plugin_name": "redacted"}, false, ""},
		{"skill prefix", map[string]string{"skill_name": "p:s"}, true, "p"},
		{"skill without prefix", map[string]string{"skill_name": "s"}, true, ""},
		{"load needs plugin", map[string]string{"skill_name": "p:s"}, false, ""},
	}
	for _, tc := range tests {
		if got := pluginKey(tc.ev, tc.skill); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLookup(t *testing.T) {
	u := newUsage("api")
	u.add("a@m1", Counts{Invocations: 1})
	u.add("a@m2", Counts{Invocations: 2})
	u.add("b", Counts{Installs: 3})
	tests := []struct {
		id    string
		want  int64
		found bool
	}{
		{"a@m1", 1, true},
		{"a", 3, true},
		{"a@other", 0, false},
		{"b@m1", 0, true},
		{"zzz", 0, false},
	}
	for _, tc := range tests {
		c, ok := u.Lookup(tc.id)
		if ok != tc.found || c.Invocations != tc.want {
			t.Errorf("Lookup(%q) = %+v %v", tc.id, c, ok)
		}
	}
	var nilU *Usage
	if _, ok := nilU.Lookup("a"); ok {
		t.Error("nil usage found something")
	}
	if !(Counts{Invocations: 1}).Used() || (Counts{Installs: 5}).Used() {
		t.Error("Used")
	}
}

package analytics

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
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
	_, err := ParseOTelJSONL(strings.NewReader(long))
	if err == nil || !errors.Is(err, bufio.ErrTooLong) || !strings.Contains(err.Error(), "a line is longer than") {
		t.Errorf("long line: want the line-length error, got %v", err)
	}
}

func TestParseOTelInputCapIsAnErrorNotATruncation(t *testing.T) {
	ev := `{"event.name":"claude_code.skill_activated","plugin.name":"p@m"}` + "\n"
	// Exactly at the cap is fine.
	if u, err := parseOTel(strings.NewReader(ev), OTelOptions{}, int64(len(ev))); err != nil || u.PerPlugin["p@m"].Invocations != 1 {
		t.Fatalf("at the cap: %v %v", u, err)
	}
	// One byte over is an error, and no partial usage is returned.
	u, err := parseOTel(strings.NewReader(ev+ev), OTelOptions{}, int64(len(ev))+1)
	if !errors.Is(err, ErrInputTooLarge) || u != nil {
		t.Errorf("over the cap: %v %v", u, err)
	}
}

func TestCapReaderReportsTheOverflow(t *testing.T) {
	c := &capReader{r: strings.NewReader(strings.Repeat("x", 100)), max: 10}
	buf := make([]byte, 64)
	var total int
	var err error
	for err == nil {
		var n int
		n, err = c.Read(buf)
		total += n
	}
	if !errors.Is(err, ErrInputTooLarge) || total != 11 {
		t.Errorf("read %d bytes, err %v", total, err)
	}
	if n, err := c.Read(nil); n != 0 || err != nil {
		t.Errorf("empty read: %d %v", n, err)
	}
}

func TestParseOTelWindow(t *testing.T) {
	in := strings.Join([]string{
		`{"event.name":"claude_code.skill_activated","plugin.name":"old@m","timestamp":"2026-06-01T10:00:00Z"}`,
		`{"event.name":"claude_code.skill_activated","plugin.name":"in@m","timestamp":"2026-09-10T10:00:00Z"}`,
		`{"event.name":"claude_code.skill_activated","plugin.name":"edge@m","timestamp":"2026-10-06T00:00:00Z"}`,
		`{"event.name":"claude_code.skill_activated","plugin.name":"undated@m"}`,
		`{"event.name":"claude_code.skill_activated","plugin.name":"<redacted>","timestamp":"2026-05-01T10:00:00Z"}`,
		`{"event.name":"claude_code.skill_activated","plugin.name":"<redacted>","timestamp":"2026-09-11T10:00:00Z"}`,
	}, "\n")
	from := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	u, err := ParseOTelJSONLWindow(strings.NewReader(in), OTelOptions{From: from, To: to})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := u.PerPlugin["old@m"]; ok {
		t.Error("an event before the window was counted")
	}
	if _, ok := u.PerPlugin["edge@m"]; ok {
		t.Error("To is exclusive")
	}
	if u.PerPlugin["in@m"].Invocations != 1 || u.PerPlugin["undated@m"].Invocations != 1 {
		t.Errorf("per plugin = %+v", u.PerPlugin)
	}
	if u.Redacted != 1 {
		t.Errorf("only the in-window redacted event counts, got %d", u.Redacted)
	}
	if u.From != "2026-09-06" || u.To != "2026-10-05" {
		t.Errorf("window echo = %s..%s", u.From, u.To)
	}
}

func TestEventTimeRejectsImplausible(t *testing.T) {
	for _, in := range []string{
		"1e30", "1e300", "9999999999999999999999", "1788000000000000000000", "0.5", "-5", "NaN", "Inf", "+Inf",
		"0001-01-01T00:00:00Z", "9999-12-31T23:59:59Z", "1999-12-31T23:59:59Z", "2101-01-01T00:00:00Z",
		"4102444801", "946684799",
	} {
		if got, ok := eventTime(map[string]string{"time": in}); ok {
			t.Errorf("eventTime(%q) = %v, want it rejected", in, got)
		}
	}
	for _, in := range []string{"946684800", "4102444800", "2000-01-01T00:00:00Z"} {
		if _, ok := eventTime(map[string]string{"time": in}); !ok {
			t.Errorf("eventTime(%q) must be accepted", in)
		}
	}
}

func TestImplausibleTimestampDoesNotStretchTheWindow(t *testing.T) {
	in := `{"event.name":"claude_code.skill_activated","plugin.name":"a@m","timestamp":"1e30"}` + "\n" +
		`{"event.name":"claude_code.skill_activated","plugin.name":"a@m","timestamp":"2026-09-01T10:00:00Z"}` + "\n"
	u, err := ParseOTelJSONL(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if u.From != "2026-09-01" || u.To != "2026-09-01" || u.PerPlugin["a@m"].Invocations != 2 {
		t.Errorf("%+v", u)
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

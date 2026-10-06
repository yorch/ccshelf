package doctor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/analytics"
	"github.com/ccshelf/ccshelf/internal/catalog"
	"github.com/ccshelf/ccshelf/internal/catalog/lint"
	"github.com/ccshelf/ccshelf/internal/claude"
	"github.com/ccshelf/ccshelf/internal/orgconfig"
)

var now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func entry(name string, f func(*catalog.Entry)) catalog.Entry {
	e := catalog.Entry{Name: name, Marketplace: "acme", Status: "active", Owner: "team", ReviewBy: "2026-12-01"}
	if f != nil {
		f(&e)
	}
	return e
}

func cat(es ...catalog.Entry) *catalog.Catalog { return &catalog.Catalog{Plugins: es} }

func run1(t *testing.T, code string, in Input) ([]Finding, string) {
	t.Helper()
	in.Now = now
	for _, c := range checks {
		if c.code == code {
			return c.run(&in)
		}
	}
	t.Fatalf("no check %s", code)
	return nil, ""
}

func plugins(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Plugin)
	}
	return out
}

func TestOverlap(t *testing.T) {
	tags := func(ts ...string) func(*catalog.Entry) {
		return func(e *catalog.Entry) { e.Tags = ts }
	}
	tests := []struct {
		name    string
		plugins []catalog.Entry
		want    int
		contain string
	}{
		{"nine of twelve", []catalog.Entry{
			entry("sre-kit", tags("a", "b", "c", "d", "e", "f", "g", "h", "i", "j")),
			entry("ops-helper", tags("a", "b", "c", "d", "e", "f", "g", "h", "i", "k", "l")),
		}, 1, "share 9 of 12 tags (a, b, c, d, e, f, g, h, i)"},
		{"exactly 60 percent", []catalog.Entry{
			entry("x", tags("a", "b", "c")), entry("y", tags("a", "b", "c", "d", "e")),
		}, 1, "share 3 of 5 tags"},
		{"below 60 percent", []catalog.Entry{
			entry("x", tags("a", "b")), entry("y", tags("a", "b", "c", "d")),
		}, 0, ""},
		{"single shared tag is ignored", []catalog.Entry{
			entry("x", tags("a")), entry("y", tags("a")),
		}, 0, ""},
		{"declared one way", []catalog.Entry{
			entry("x", func(e *catalog.Entry) { e.OverlapsWith = []string{"y"} }), entry("y", nil),
		}, 1, "list each other in overlaps_with"},
		{"declared by id", []catalog.Entry{
			entry("x", func(e *catalog.Entry) { e.OverlapsWith = []string{"y@acme"} }), entry("y", nil),
		}, 1, "overlaps_with"},
		{"deprecated excluded", []catalog.Entry{
			entry("x", tags("a", "b")), entry("y", func(e *catalog.Entry) { e.Tags = []string{"a", "b"}; e.Status = "deprecated" }),
		}, 0, ""},
		{"both reasons", []catalog.Entry{
			entry("x", func(e *catalog.Entry) { e.Tags = []string{"a", "b"}; e.OverlapsWith = []string{"y"} }), entry("y", tags("a", "b")),
		}, 1, "share 2 of 2 tags (a, b) and list each other"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs, skip := run1(t, "DOC001", Input{Catalog: cat(tc.plugins...)})
			if skip != "" || len(fs) != tc.want {
				t.Fatalf("findings = %+v skip=%q", fs, skip)
			}
			if tc.want == 1 && !strings.Contains(fs[0].Message, tc.contain) {
				t.Errorf("message = %q, want %q", fs[0].Message, tc.contain)
			}
		})
	}
}

func TestUnused(t *testing.T) {
	c := cat(entry("used", nil), entry("idle", nil), entry("busy", nil), entry("old", func(e *catalog.Entry) { e.Status = "deprecated" }))
	profiles := []ProfileView{{Name: "sre", Plugins: []string{"used@acme"}}}
	usage := &analytics.Usage{PerPlugin: map[string]analytics.Counts{"busy@acme": {Invocations: 4}, "idle@acme": {Installs: 9}}, From: "2026-09-06", To: "2026-10-06"}

	fs, skip := run1(t, "DOC002", Input{Catalog: c, Profiles: profiles})
	if skip != "" || !reflect.DeepEqual(plugins(fs), []string{"idle@acme", "busy@acme"}) {
		t.Errorf("without usage: %v %q", plugins(fs), skip)
	}
	if !strings.Contains(fs[0].Message, "no usage data") {
		t.Errorf("message = %q", fs[0].Message)
	}

	fs, _ = run1(t, "DOC002", Input{Catalog: c, Profiles: profiles, Usage: usage})
	if !reflect.DeepEqual(plugins(fs), []string{"idle@acme"}) {
		t.Errorf("with usage: %v", plugins(fs))
	}
	if !strings.Contains(fs[0].Message, "0 skill_activated events between 2026-09-06 and 2026-10-06") {
		t.Errorf("message = %q", fs[0].Message)
	}

	// A plain name in a profile counts as used.
	fs, _ = run1(t, "DOC002", Input{Catalog: cat(entry("solo", nil)), Profiles: []ProfileView{{Name: "p", Plugins: []string{"solo"}}}})
	if len(fs) != 0 {
		t.Errorf("plain name: %v", fs)
	}
	if _, skip := run1(t, "DOC002", Input{Catalog: c}); skip == "" {
		t.Error("no profiles: want skip")
	}
}

func TestDeprecatedInUse(t *testing.T) {
	c := cat(
		entry("old", func(e *catalog.Entry) { e.Status = "deprecated"; e.SupersededBy = "new" }),
		entry("gone", func(e *catalog.Entry) { e.Status = "deprecated" }),
		entry("new", nil),
	)
	fs, skip := run1(t, "DOC003", Input{Catalog: c, Profiles: []ProfileView{
		{Name: "sre", Plugins: []string{"old@acme", "new@acme", "unknown@acme"}},
		{Name: "dev", Plugins: []string{"gone@acme"}},
	}})
	if skip != "" || len(fs) != 2 {
		t.Fatalf("findings = %+v", fs)
	}
	if fs[0].Profile != "sre" || !strings.Contains(fs[0].Message, "use new instead") {
		t.Errorf("first = %+v", fs[0])
	}
	if !strings.Contains(fs[1].Hint, "no replacement") {
		t.Errorf("second = %+v", fs[1])
	}
	if _, skip := run1(t, "DOC003", Input{Catalog: c}); skip == "" {
		t.Error("no profiles: want skip")
	}
}

func TestReview(t *testing.T) {
	rv := func(d string) func(*catalog.Entry) { return func(e *catalog.Entry) { e.ReviewBy = d } }
	c := cat(
		entry("fresh", rv("2026-10-06")),
		entry("missing", rv("")),
		entry("recent-past", rv("2026-09-01")),
		entry("stale", rv("2026-01-01")),
		entry("garbled", rv("soon")),
		entry("old", func(e *catalog.Entry) { e.Status = "deprecated"; e.ReviewBy = "" }),
	)
	fs, _ := run1(t, "DOC004", Input{Catalog: c})
	got := map[string]Severity{}
	for _, f := range fs {
		got[f.Plugin] = f.Severity
	}
	want := map[string]Severity{"missing@acme": "", "recent-past@acme": Info, "stale@acme": "", "garbled@acme": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// ReviewMaxAgeDays moves the line between info and warning.
	fs, _ = run1(t, "DOC004", Input{Catalog: cat(entry("p", rv("2026-09-01"))), ReviewMaxAgeDays: 10})
	if len(fs) != 1 || fs[0].Severity != "" {
		t.Errorf("custom max age: %+v", fs)
	}
}

func TestOwner(t *testing.T) {
	c := cat(entry("a", nil), entry("b", func(e *catalog.Entry) { e.Owner = "" }), entry("c", func(e *catalog.Entry) { e.Owner = "  " }))
	fs, _ := run1(t, "DOC005", Input{Catalog: c})
	if !reflect.DeepEqual(plugins(fs), []string{"b@acme", "c@acme"}) {
		t.Errorf("got %v", plugins(fs))
	}
}

func TestStandaloneSkills(t *testing.T) {
	in := Input{
		Catalog:          cat(),
		StandaloneSkills: []string{"pdf", "notes", "listed-off", "listed-name", "pdf", "bad\"name", "evil\nname"},
		Profiles: []ProfileView{
			{Name: "a", StandaloneOff: []string{"listed-off"}},
			{Name: "b", NameOnly: []string{"listed-name"}},
		},
	}
	fs, skip := run1(t, "DOC006", in)
	if skip != "" || len(fs) != 2 {
		t.Fatalf("findings = %+v", fs)
	}
	if !strings.Contains(fs[0].Message, "notes, pdf") || !strings.Contains(fs[0].Hint, "[skills]\noff = [\"notes\", \"pdf\"]") {
		t.Errorf("first = %+v", fs[0])
	}
	if strings.Contains(fs[0].Hint, "bad") || !strings.Contains(fs[1].Message, "unusual names") || strings.Contains(fs[1].Message, "\n") {
		t.Errorf("second = %+v", fs[1])
	}
	in.StandaloneSkills = []string{"listed-off"}
	if fs, _ := run1(t, "DOC006", in); len(fs) != 0 {
		t.Errorf("all mentioned: %v", fs)
	}
	in.StandaloneSkills = nil
	if _, skip := run1(t, "DOC006", in); skip == "" {
		t.Error("nil skills: want skip")
	}
	in.StandaloneSkills = []string{}
	in.Profiles = nil
	if _, skip := run1(t, "DOC006", in); skip == "" {
		t.Error("no profiles: want skip")
	}
}

func TestMissingUpstreamAndNotInstalled(t *testing.T) {
	c := cat(entry("here", nil))
	installed := []claude.Plugin{
		{ID: "here@acme", Name: "here", Marketplace: "acme"},
		{ID: "renamed@acme", Name: "renamed", Marketplace: "acme"},
		{ID: "x@official", Name: "x", Marketplace: "official"},
		{ID: "bare", Name: "bare"},
		{ID: "we ird@acme", Name: "we ird", Marketplace: "acme"},
	}
	fs, skip := run1(t, "DOC007", Input{Catalog: c, Installed: installed})
	if skip != "" || !reflect.DeepEqual(plugins(fs), []string{"renamed@acme", "we ird@acme"}) {
		t.Fatalf("DOC007 = %+v", fs)
	}
	if fs[0].Hint != "claude plugin uninstall renamed@acme" || fs[1].Hint != "" {
		t.Errorf("hints = %q %q", fs[0].Hint, fs[1].Hint)
	}
	if _, skip := run1(t, "DOC007", Input{Catalog: c}); skip == "" {
		t.Error("nil installed: want skip")
	}

	profiles := []ProfileView{{Name: "sre", Plugins: []string{"here@acme", "missing@acme", "$(rm -rf)@acme"}}}
	fs, skip = run1(t, "DOC008", Input{Catalog: c, Installed: installed, Profiles: profiles})
	if skip != "" || len(fs) != 2 {
		t.Fatalf("DOC008 = %+v", fs)
	}
	if fs[0].Hint != "claude plugin install missing@acme" || fs[0].Profile != "sre" {
		t.Errorf("first = %+v", fs[0])
	}
	if fs[1].Hint != "" {
		t.Errorf("unsafe id got a command: %q", fs[1].Hint)
	}
	if fs, _ := run1(t, "DOC008", Input{Catalog: c, Installed: []claude.Plugin{}, Profiles: profiles[:0]}); len(fs) != 0 {
		t.Errorf("no profiles: %v", fs)
	}
	if _, skip := run1(t, "DOC008", Input{Catalog: c}); skip == "" {
		t.Error("nil installed: want skip")
	}
}

func TestForcedByPolicy(t *testing.T) {
	installed := []claude.Plugin{
		{ID: "audit@acme", RequiredByOrg: true},
		{ID: "free@acme"},
		{ID: "kept@acme", RequiredByOrg: true},
	}
	profiles := []ProfileView{
		{Name: "a", Plugins: []string{"kept@acme"}},
		{Name: "b", Mode: "additive"},
		{Name: "c", Exclude: []string{"kept@acme"}, Mode: "additive"},
	}
	fs, skip := run1(t, "DOC009", Input{Catalog: cat(), Installed: installed, Profiles: profiles})
	if skip != "" || len(fs) != 2 {
		t.Fatalf("findings = %+v", fs)
	}
	if !strings.Contains(fs[0].Message, "cannot be masked") || !strings.Contains(fs[0].Message, "profiles that leave it out still run with it: a") {
		t.Errorf("audit = %q", fs[0].Message)
	}
	if !strings.Contains(fs[1].Message, ": c") {
		t.Errorf("kept = %q", fs[1].Message)
	}
	if _, skip := run1(t, "DOC009", Input{Catalog: cat()}); skip == "" {
		t.Error("nil installed: want skip")
	}
}

func TestPlatformReview(t *testing.T) {
	review := func(hooks, mcp bool) func(*catalog.Entry) {
		return func(e *catalog.Entry) { e.HasHooks, e.HasMCP, e.NeedsPlatformReview = hooks, mcp, hooks || mcp }
	}
	c := cat(entry("hooky", review(true, false)), entry("both", review(true, true)), entry("mcp", review(false, true)), entry("plain", nil))
	rep := &lint.Report{Findings: []lint.Finding{
		{Code: "CAT042", Plugin: "hooky"}, {Code: "CAT044", Plugin: "both"}, {Code: "CAT019", Plugin: "mcp"}, {Code: "CAT042"},
	}}
	org := orgconfig.Default()
	org.Lint.PlatformOwners = []string{"@acme/platform"}
	fs, skip := run1(t, "DOC010", Input{Catalog: c, Lint: rep, Org: org})
	if skip != "" || !reflect.DeepEqual(plugins(fs), []string{"hooky@acme", "both@acme"}) {
		t.Fatalf("findings = %+v", fs)
	}
	if !strings.Contains(fs[0].Message, "ships hooks but") || !strings.Contains(fs[1].Message, "hooks and MCP servers") {
		t.Errorf("messages = %q / %q", fs[0].Message, fs[1].Message)
	}

	rep.Findings = []lint.Finding{{Code: "CAT043"}}
	fs, _ = run1(t, "DOC010", Input{Catalog: c, Lint: rep, Org: org})
	if !reflect.DeepEqual(plugins(fs), []string{"hooky@acme", "both@acme", "mcp@acme"}) || !strings.Contains(fs[0].Message, "no CODEOWNERS") ||
		!strings.Contains(fs[2].Message, "ships MCP servers") {
		t.Errorf("no CODEOWNERS: %+v", fs)
	}
	if fs, _ := run1(t, "DOC010", Input{Catalog: c, Lint: &lint.Report{}, Org: org}); len(fs) != 0 {
		t.Errorf("clean lint: %v", fs)
	}
	if _, skip := run1(t, "DOC010", Input{Catalog: c, Org: org}); skip == "" {
		t.Error("no lint: want skip")
	}
}

func TestPlatformReviewSkipsWithoutPlatformOwners(t *testing.T) {
	c := cat(entry("hooky", func(e *catalog.Entry) { e.HasHooks, e.NeedsPlatformReview = true, true }))
	rep := &lint.Report{Findings: []lint.Finding{{Code: "CAT042", Plugin: "hooky"}}}
	empty := orgconfig.Default()
	empty.Lint.PlatformOwners = nil
	fs, skip := run1(t, "DOC010", Input{Catalog: c, Lint: rep, Org: empty})
	if len(fs) != 0 || !strings.Contains(skip, "lint.platform_owners is empty") {
		t.Errorf("empty platform_owners: %v %q", fs, skip)
	}
	if _, skip := run1(t, "DOC010", Input{Catalog: c, Lint: rep}); !strings.Contains(skip, "org config") {
		t.Errorf("no org config: skip = %q", skip)
	}
	// The skip reaches the report, so it is never silent.
	r := Run(Input{Now: now, Catalog: c, Lint: rep, Org: empty})
	found := false
	for _, s := range r.Skipped {
		if s.Code == "DOC010" && strings.Contains(s.Reason, "platform_owners") {
			found = true
		}
	}
	if !found {
		t.Errorf("skipped = %+v", r.Skipped)
	}
}

func TestPlatformReviewIncludesLSPPlugins(t *testing.T) {
	// The catalog entry has neither HasHooks nor HasMCP (LSP is not tracked
	// there), but lint reports it by CAT042.
	c := cat(entry("lsp-only", nil), entry("plain", nil))
	org := orgconfig.Default()
	org.Lint.PlatformOwners = []string{"@acme/platform"}
	rep := &lint.Report{Findings: []lint.Finding{{Code: "CAT042", Plugin: "lsp-only"}}}
	fs, skip := run1(t, "DOC010", Input{Catalog: c, Lint: rep, Org: org})
	if skip != "" || !reflect.DeepEqual(plugins(fs), []string{"lsp-only@acme"}) {
		t.Fatalf("findings = %+v", fs)
	}
	if !strings.Contains(fs[0].Message, "hooks, MCP or LSP servers") {
		t.Errorf("message = %q", fs[0].Message)
	}
}

func TestProtectedMasked(t *testing.T) {
	org := orgconfig.Default()
	org.Protect.Plugins = []string{"audit@acme"}
	profiles := []ProfileView{
		{Name: "strict"}, // omission in allow-only is normal: not reported
		{Name: "good", Plugins: []string{"audit@acme"}},
		{Name: "add", Mode: "additive"},
		{Name: "excl", Mode: "additive", Exclude: []string{"audit@acme"}},
		{Name: "bad name!", Exclude: []string{"audit@acme"}},
		{Name: "base", Abstract: true, Exclude: []string{"audit@acme"}},
	}
	fs, skip := run1(t, "DOC011", Input{Catalog: cat(), Org: org, Profiles: profiles})
	var names []string
	for _, f := range fs {
		names = append(names, f.Profile)
	}
	if skip != "" || !reflect.DeepEqual(names, []string{"excl", "bad name!"}) {
		t.Fatalf("profiles = %v", names)
	}
	if !strings.Contains(fs[0].Message, "the launcher keeps it enabled") || strings.Contains(fs[0].Message, "would mask") {
		t.Errorf("message = %q", fs[0].Message)
	}
	if !strings.Contains(fs[0].Hint, `remove "audit@acme" from plugins.exclude of profile excl`) || fs[1].Hint != "" {
		t.Errorf("hints = %q / %q", fs[0].Hint, fs[1].Hint)
	}
	if fs[0].Severity != "" && fs[0].Severity != Info {
		t.Errorf("severity = %q", fs[0].Severity)
	}
	if _, skip := run1(t, "DOC011", Input{Catalog: cat()}); skip == "" {
		t.Error("no org: want skip")
	}
}

func TestProtectedMCP(t *testing.T) {
	org := orgconfig.Default()
	org.Protect.MCP = []string{"claude.ai Shopify", "plugin:audit:audit"}
	profiles := []ProfileView{
		{Name: "hides", HideConnectors: true, Plugins: []string{"a@acme"}},
		{Name: "strict", StrictMCP: true, Plugins: []string{"a@acme"}},
		{Name: "fine", Plugins: []string{"a@acme"}},
		{Name: "base", Abstract: true, HideConnectors: true, StrictMCP: true},
	}
	fs, skip := run1(t, "DOC012", Input{Catalog: cat(), Org: org, Profiles: profiles})
	if skip != "" || len(fs) != 2 {
		t.Fatalf("findings = %+v (skip %q)", fs, skip)
	}
	if fs[0].Profile != "hides" || !strings.Contains(fs[0].Message, "claude.ai Shopify") || !strings.Contains(fs[0].Message, "refuses to start") {
		t.Errorf("hides = %+v", fs[0])
	}
	if fs[1].Profile != "strict" || !strings.Contains(fs[1].Message, "mcp.strict") {
		t.Errorf("strict = %+v", fs[1])
	}
	r := Run(Input{Now: now, Catalog: cat(), Org: org, Profiles: profiles})
	if !r.HasErrors() {
		t.Error("a profile the launcher refuses is an error")
	}

	// Only a plugin-provided protected server: hiding connectors is fine.
	org.Protect.MCP = []string{"plugin:audit:audit"}
	fs, _ = run1(t, "DOC012", Input{Catalog: cat(), Org: org, Profiles: profiles[:1]})
	if len(fs) != 0 {
		t.Errorf("no protected connector: %+v", fs)
	}
	org.Protect.MCP = nil
	fs, _ = run1(t, "DOC012", Input{Catalog: cat(), Org: org, Profiles: profiles})
	if len(fs) != 0 {
		t.Errorf("no protected MCP: %+v", fs)
	}
	if _, skip := run1(t, "DOC012", Input{Catalog: cat()}); skip == "" {
		t.Error("no org: want skip")
	}
}

func TestUnusedHonorsProtectedAndRedaction(t *testing.T) {
	org := orgconfig.Default()
	org.Protect.Plugins = []string{"audit@acme"}
	c := cat(entry("audit", nil), entry("idle", nil))
	profiles := []ProfileView{{Name: "p", Plugins: []string{"other@acme"}}}

	fs, _ := run1(t, "DOC002", Input{Catalog: c, Profiles: profiles, Org: org})
	if !reflect.DeepEqual(plugins(fs), []string{"idle@acme"}) {
		t.Errorf("a protected plugin is not unused: %v", plugins(fs))
	}

	usage := &analytics.Usage{PerPlugin: map[string]analytics.Counts{}, From: "2026-09-06", To: "2026-10-06", Redacted: 7}
	fs, _ = run1(t, "DOC002", Input{Catalog: c, Profiles: profiles, Org: org, Usage: usage})
	if len(fs) != 1 {
		t.Fatalf("findings = %+v", fs)
	}
	if strings.Contains(fs[0].Message, "has 0 skill_activated events") || !strings.Contains(fs[0].Message, "7 usage event(s)") ||
		!strings.Contains(fs[0].Message, "cannot be confirmed unused") {
		t.Errorf("redacted usage must not claim zero events: %q", fs[0].Message)
	}

	// An id with recorded invocations is never reported, redacted or not.
	usage.PerPlugin["idle@acme"] = analytics.Counts{Invocations: 1}
	if fs, _ = run1(t, "DOC002", Input{Catalog: c, Profiles: profiles, Org: org, Usage: usage}); len(fs) != 0 {
		t.Errorf("used plugin reported: %+v", fs)
	}
}

func TestMasksTable(t *testing.T) {
	tests := []struct {
		name string
		p    ProfileView
		want bool
	}{
		{"default mode, not included", ProfileView{}, true},
		{"explicit allow-only, not included", ProfileView{Mode: "allow-only"}, true},
		{"explicit allow-only, included", ProfileView{Mode: "allow-only", Plugins: []string{"x@m"}}, false},
		{"default mode, included", ProfileView{Plugins: []string{"x@m"}}, false},
		{"additive, not included", ProfileView{Mode: "additive"}, false},
		{"additive, excluded", ProfileView{Mode: "additive", Exclude: []string{"x@m"}}, true},
		{"allow-only, included and excluded", ProfileView{Mode: "allow-only", Plugins: []string{"x@m"}, Exclude: []string{"x@m"}}, true},
		{"abstract masks nothing", ProfileView{Abstract: true}, false},
		{"abstract with exclude masks nothing", ProfileView{Abstract: true, Exclude: []string{"x@m"}}, false},
	}
	for _, tc := range tests {
		if got := masks(tc.p, "x@m"); got != tc.want {
			t.Errorf("%s: masks = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestForcedByPolicyIgnoresAbstractProfiles(t *testing.T) {
	installed := []claude.Plugin{{ID: "audit@acme", RequiredByOrg: true}}
	profiles := []ProfileView{{Name: "base", Abstract: true}, {Name: "real"}}
	fs, _ := run1(t, "DOC009", Input{Catalog: cat(), Installed: installed, Profiles: profiles})
	if len(fs) != 1 || !strings.HasSuffix(fs[0].Message, ": real") {
		t.Errorf("findings = %+v", fs)
	}
}

func TestRunAssemblesReport(t *testing.T) {
	r := Run(Input{
		Catalog: cat(entry("a", func(e *catalog.Entry) { e.Owner = "" })),
		Now:     now,
		Policy:  []Finding{{Code: "POL001", Message: "managed settings found\x1b[31m", Hint: "x"}, {Code: "POL002", Severity: Error, Message: "bad"}},
	})
	if r.Counts().Warnings != 1 || r.Counts().Errors != 1 || r.Counts().Infos != 1 || !r.HasErrors() {
		t.Errorf("counts = %+v findings=%+v", r.Counts(), r.Findings)
	}
	var codes []string
	for _, f := range r.Findings {
		codes = append(codes, f.Code)
		if strings.Contains(f.Message, "\x1b") {
			t.Errorf("control character in policy message: %q", f.Message)
		}
	}
	if !reflect.DeepEqual(codes, []string{"DOC005", "POL001", "POL002"}) {
		t.Errorf("codes = %v", codes)
	}
	if len(r.Skipped) == 0 {
		t.Error("checks without input must be listed as skipped")
	}
	// A nil catalog is reported, not a panic.
	r = Run(Input{})
	if r.Skipped[0].Code != "DOC000" {
		t.Errorf("skipped = %+v", r.Skipped)
	}
}

func TestRules(t *testing.T) {
	rs := Rules()
	if len(rs) != len(checks) {
		t.Fatalf("%d rules", len(rs))
	}
	for i, r := range rs {
		if !strings.HasPrefix(r.Code, "DOC") || r.Check == "" || r.Description == "" || r.Severity == "" {
			t.Errorf("bad rule %+v", r)
		}
		if i > 0 && rs[i-1].Code >= r.Code {
			t.Errorf("not ordered: %s after %s", r.Code, rs[i-1].Code)
		}
	}
}

func goldenOrg() *orgconfig.Config {
	org := orgconfig.Default()
	org.Lint.PlatformOwners = []string{"@acme/platform"}
	org.Protect.Plugins = []string{"audit-log@acme"}
	return org
}

func goldenInput() Input {
	tags := func(ts ...string) func(*catalog.Entry) { return func(e *catalog.Entry) { e.Tags = ts } }
	return Input{
		Now: now,
		Catalog: cat(
			entry("sre-kit", tags("ops", "alerts", "oncall", "runbook", "slo")),
			entry("ops-helper", tags("ops", "alerts", "oncall", "runbook")),
			entry("seo-tools", func(e *catalog.Entry) { e.ReviewBy = "" }),
			entry("postmortem-lite", func(e *catalog.Entry) { e.Status = "deprecated"; e.SupersededBy = "sre-kit" }),
			entry("hooky", func(e *catalog.Entry) { e.Owner = ""; e.HasHooks = true; e.NeedsPlatformReview = true }),
		),
		Lint:     &lint.Report{Findings: []lint.Finding{{Code: "CAT042", Plugin: "hooky"}}},
		Profiles: []ProfileView{{Name: "sre", Plugins: []string{"sre-kit@acme", "postmortem-lite@acme"}}},
		Installed: []claude.Plugin{
			{ID: "sre-kit@acme", Marketplace: "acme"},
			{ID: "audit-log@acme", Marketplace: "acme", RequiredByOrg: true},
		},
		StandaloneSkills: []string{"pdf"},
		Org:              goldenOrg(),
		Usage:            &analytics.Usage{PerPlugin: map[string]analytics.Counts{}, From: "2026-09-06", To: "2026-10-06"},
		Policy:           []Finding{{Code: "POL001", Severity: Info, Message: "no managed policy detected on this machine"}},
	}
}

func TestGolden(t *testing.T) {
	r := Run(goldenInput())
	js, err := r.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{"report.txt": r.Text(), "report.json": string(js)} {
		path := filepath.Join("testdata", "golden", name)
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
			t.Errorf("%s differs from the golden file (run with UPDATE_GOLDEN=1 to update):\n%s", name, got)
		}
	}
}

package scaffold

import (
	"encoding/json"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
)

var hostile = []string{
	"plain", `quote " and \ backslash`, "line\nbreak", "cr\rlf", "tab\there", "nul\x00byte", "esc\x1b[31m",
	"bidi\u202eRLO", "zwsp\u200b", "ls\u2028ps\u2029", "bom\ufeff", "emoji 🎉", "]\n[protect]\nplugins = [\"x\"]",
	`" ]` + "\n" + `injected = "yes`, "${{ secrets.TOKEN }}", "`backtick`", "<script>alert(1)</script>", "[x](javascript:alert(1))",
	"\u0085next-line", "\u007fdel", "'single'", "'''triple'''", `"""triple"""`,
}

func TestTomlStringRoundTrips(t *testing.T) {
	for _, in := range hostile {
		doc := "k = " + tomlString(in) + "\nlist = " + tomlArray([]string{in, "second"}) + "\n"
		var out struct {
			K    string
			List []string
		}
		if err := toml.Unmarshal([]byte(doc), &out); err != nil {
			t.Errorf("%q: not valid TOML: %v\n%s", in, err, doc)
			continue
		}
		if out.K != in || len(out.List) != 2 || out.List[0] != in {
			t.Errorf("%q: round trip gave %q %q", in, out.K, out.List)
		}
		var generic map[string]any
		if err := toml.Unmarshal([]byte(doc), &generic); err != nil || len(generic) != 2 {
			t.Errorf("%q: injected keys: %v %v", in, generic, err)
		}
	}
}

func TestMarkdownEscape(t *testing.T) {
	for _, in := range hostile {
		got := mdEscape(in)
		// Every ASCII punctuation character is escaped, so no markup survives.
		for i := 0; i < len(got); i++ {
			c := got[i]
			if strings.IndexByte("!\"#$%&'()*+,-./:;<=>?@[]^_`{|}~", c) >= 0 && (i == 0 || got[i-1] != '\\') {
				t.Errorf("%q: unescaped %q in %q", in, c, got)
			}
			if c == '\\' && i+1 < len(got) {
				i++ // the escaped character
			}
		}
	}
	if got := mdEscape("a_b*c"); got != `a\_b\*c` {
		t.Error(got)
	}
	if got := mdCode("a`b"); got != "``a`b``" {
		t.Error(got)
	}
	if got := mdCode("`a"); got != "`` `a ``" {
		t.Error(got)
	}
}

func TestJSONIndent(t *testing.T) {
	type v struct {
		S string `json:"s"`
	}
	for _, in := range hostile {
		b, err := jsonIndent(v{in})
		if err != nil {
			t.Fatal(err)
		}
		var out v
		if err := json.Unmarshal(b, &out); err != nil || out.S != in && !strings.ContainsRune(in, 0xfffd) {
			t.Errorf("%q: %v %q", in, err, out.S)
		}
		if !strings.HasSuffix(string(b), "\n") || strings.Contains(string(b), "\\u003c") {
			t.Errorf("%q: format %q", in, b)
		}
	}
}

func TestHostileOrgInEveryGeneratedFile(t *testing.T) {
	for _, org := range hostile {
		if !ValidOrg(org) {
			continue
		}
		p := baseParams()
		p.Org = org
		plan, err := Build(Empty(), p)
		if err != nil {
			t.Fatalf("%q: %v", org, err)
		}
		for _, e := range plan.Entries {
			switch e.Path {
			case "ccshelf.toml":
				var cfg struct{ Catalog struct{ Title string } }
				if err := toml.Unmarshal(e.Content, &cfg); err != nil || cfg.Catalog.Title != org+" plugin catalog" {
					t.Errorf("%q: ccshelf.toml: %v %q", org, err, cfg.Catalog.Title)
				}
			case ".claude-plugin/marketplace.json":
				var m struct{ Owner struct{ Name string } }
				if err := json.Unmarshal(e.Content, &m); err != nil || m.Owner.Name != org {
					t.Errorf("%q: marketplace.json: %v %q", org, err, m.Owner.Name)
				}
			default:
				if e.Group == GroupWorkflows && strings.Contains(string(e.Content), org) && len(org) > 3 {
					t.Errorf("%q leaked into %s", org, e.Path)
				}
			}
		}
	}
}

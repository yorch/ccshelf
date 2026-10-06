package settings

import (
	"encoding/json"
	"flag"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/claude"
)

var update = flag.Bool("update", false, "rewrite golden files")

func plug(id string, mods ...func(*claude.Plugin)) claude.Plugin {
	n, m := claude.SplitID(id)
	p := claude.Plugin{ID: id, Name: n, Marketplace: m, Scope: "user", Enabled: true}
	for _, f := range mods {
		f(&p)
	}
	return p
}

func required(p *claude.Plugin) { p.RequiredByOrg = true }
func disabled(p *claude.Plugin) { p.Enabled = false }

var installed = []claude.Plugin{
	plug("design-kit@acme"),
	plug("sre-kit@acme"),
	plug("seo-tools@acme", disabled),
	plug("audit@acme", required),
	plug("frontend-design@claude-plugins-official"),
	plug("secret-scan@acme"),
}

func TestBuildGolden(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
	}{
		{"allow_only", Spec{Installed: installed, Include: []string{"sre-kit@acme", "seo-tools@acme"}, Profile: "sre"}},
		{"additive", Spec{Installed: installed, Mode: ModeAdditive, Include: []string{"design-kit@acme"}, Exclude: []string{"frontend-design@claude-plugins-official"}}},
		{"protected", Spec{Installed: installed, Protected: []string{"secret-scan@acme"}, Include: []string{"design-kit@acme"}}},
		{"skills_mcp", Spec{
			Installed: installed, Mode: ModeAdditive,
			OffSkills: []string{"legacy-helper", "pdf"}, NameOnlySkills: []string{"big-skill"},
			HideConnectors: true, DenyMCP: []string{"plugin:context7:context7", "claude.ai Shopify", "claude.ai Shopify"},
			Model: "opus", Env: map[string]string{"FIGMA_TOKEN_REF": "env:FIGMA", "CCSHELF_VAR_TEAM": "sre"}, Profile: "sre",
		}},
		{"empty", Spec{Mode: ModeAdditive}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := Build(c.spec)
			if err != nil {
				t.Fatal(err)
			}
			got, err := res.JSON()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", c.name+".golden.json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(want) != string(got) {
				t.Fatalf("golden mismatch for %s:\n%s", c.name, got)
			}
		})
	}
}

func TestBuildSemantics(t *testing.T) {
	t.Run("allow-only lists", func(t *testing.T) {
		res, err := Build(Spec{Installed: installed, Include: []string{"sre-kit@acme", "seo-tools@acme", "ghost@acme"}, Protected: []string{"secret-scan@acme"}})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Enabled, []string{"seo-tools@acme", "sre-kit@acme"})
		eq(t, res.Masked, []string{"design-kit@acme", "frontend-design@claude-plugins-official"})
		eq(t, res.Locked, []string{"audit@acme"})
		eq(t, res.Missing, []string{"ghost@acme"})
		eq(t, res.Protected, []string{"secret-scan@acme"})
		if _, ok := res.Doc.EnabledPlugins["ghost@acme"]; ok {
			t.Error("missing plugin written")
		}
		if _, ok := res.Doc.EnabledPlugins["audit@acme"]; ok {
			t.Error("locked plugin written")
		}
		if _, ok := res.Doc.EnabledPlugins["secret-scan@acme"]; ok {
			t.Error("protected plugin written")
		}
		if len(res.Warnings) < 2 {
			t.Errorf("warnings: %v", res.Warnings)
		}
	})
	t.Run("additive masks only exclude", func(t *testing.T) {
		res, err := Build(Spec{Installed: installed, Mode: ModeAdditive, Exclude: []string{"design-kit@acme", "audit@acme"}})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Masked, []string{"design-kit@acme"})
		eq(t, res.Locked, []string{"audit@acme"})
	})
	t.Run("protected beats exclude", func(t *testing.T) {
		res, err := Build(Spec{Installed: installed, Mode: ModeAdditive, Exclude: []string{"secret-scan@acme"}, Protected: []string{"secret-scan@acme"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Masked) != 0 || len(res.Warnings) != 1 {
			t.Errorf("masked=%v warnings=%v", res.Masked, res.Warnings)
		}
	})
	t.Run("protected and included is true", func(t *testing.T) {
		res, _ := Build(Spec{Installed: installed, Protected: []string{"secret-scan@acme"}, Include: []string{"secret-scan@acme"}})
		if !res.Doc.EnabledPlugins["secret-scan@acme"] {
			t.Error("expected true")
		}
	})
	t.Run("unusual installed id ignored", func(t *testing.T) {
		res, err := Build(Spec{Installed: []claude.Plugin{{ID: "weird id"}, plug("a@b")}})
		if err != nil || len(res.Warnings) != 1 || len(res.Masked) != 1 {
			t.Fatalf("%v %+v", err, res)
		}
	})
	t.Run("duplicate installed ids", func(t *testing.T) {
		res, _ := Build(Spec{Installed: []claude.Plugin{plug("a@b"), plug("a@b", func(p *claude.Plugin) { p.Scope = "project" })}})
		eq(t, res.Masked, []string{"a@b"})
	})
	t.Run("plugin skill warning", func(t *testing.T) {
		res, err := Build(Spec{Installed: installed, Mode: ModeAdditive, OffSkills: []string{"design-kit:thing", "other:thing"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "design-kit") {
			t.Errorf("warnings: %v", res.Warnings)
		}
	})
	t.Run("connectors never false", func(t *testing.T) {
		res, _ := Build(Spec{Mode: ModeAdditive})
		b, _ := res.JSON()
		if strings.Contains(string(b), "disableClaudeAiConnectors") {
			t.Errorf("wrote key: %s", b)
		}
	})
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildErrors(t *testing.T) {
	cases := []struct {
		name string
		spec Spec
		want string
	}{
		{"mode", Spec{Mode: "weird"}, "unknown mode"},
		{"bad include", Spec{Include: []string{"nomarketplace"}}, "include"},
		{"bad exclude", Spec{Exclude: []string{"a b@c"}}, "exclude"},
		{"bad protected", Spec{Protected: []string{"@"}}, "protected"},
		{"include and exclude", Spec{Include: []string{"a@b"}, Exclude: []string{"a@b"}}, "both include and exclude"},
		{"skill both", Spec{OffSkills: []string{"x"}, NameOnlySkills: []string{"x"}}, "both"},
		{"skill name", Spec{OffSkills: []string{"bad name"}}, "skill name"},
		{"name-only skill name", Spec{NameOnlySkills: []string{"a/b"}}, "skill name"},
		{"deny empty", Spec{DenyMCP: []string{""}}, "deny MCP"},
		{"deny whitespace", Spec{DenyMCP: []string{" x"}}, "deny MCP"},
		{"deny control", Spec{DenyMCP: []string{"a\nb"}}, "deny MCP"},
		{"model space", Spec{Model: "a b"}, "model"},
		{"env denied", Spec{Env: map[string]string{"ANTHROPIC_BASE_URL": "x"}}, "ANTHROPIC_BASE_URL"},
		{"env not allowlisted", Spec{Env: map[string]string{"FOO": "x"}}, "FOO"},
		{"env path", Spec{Env: map[string]string{"PATH": "x"}}, "PATH"},
		{"env nul", Spec{Env: map[string]string{"FIGMA_TOKEN_REF": "a\x00b"}}, "NUL"},
		{"env long", Spec{Env: map[string]string{"FIGMA_TOKEN_REF": strings.Repeat("a", 5000)}}, "too long"},
		{"profile conflict", Spec{Profile: "a", Env: map[string]string{"CCSHELF_PROFILE": "b"}}, "conflicts"},
		{"profile control", Spec{Profile: "a\nb"}, "profile"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Build(c.spec)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	if _, err := Build(Spec{Profile: "p", Env: map[string]string{"CCSHELF_PROFILE": "p"}}); err != nil {
		t.Errorf("same profile value should be fine: %v", err)
	}
}

func TestValidate(t *testing.T) {
	good := []string{
		`{}`,
		`{"enabledPlugins":{"a@b":true,"c-d@e.f":false}}`,
		`{"skillOverrides":{"x":"off","a:b":"user-invocable-only","y":"on","z":"name-only"}}`,
		`{"disableClaudeAiConnectors":true,"model":"opus"}`,
		`{"deniedMcpServers":[{"serverName":"claude.ai Slack"},{"serverUrl":"https://*.example.com/*"},{"serverCommand":["npx","x"]}]}`,
		`{"env":{"CCSHELF_PROFILE":"sre","FIGMA_TOKEN_REF":"env:X","CCSHELF_VAR_A":"1"}}`,
		"\xef\xbb\xbf{}",
		" {\"model\": \"x\"}\n",
	}
	for _, g := range good {
		if err := Validate([]byte(g)); err != nil {
			t.Errorf("Validate(%q) = %v", g, err)
		}
	}
	bad := []struct{ in, want string }{
		{``, "invalid JSON"},
		{`[]`, "not a JSON object"},
		{`null`, "not a JSON object"},
		{`"x"`, "not a JSON object"},
		{`{`, "invalid JSON"},
		{`{} {}`, "trailing"},
		{`{"model":"a","model":"b"}`, "duplicate key"},
		{`{"enabledPlugins":{"a@b":true,"a@b":false}}`, "duplicate key"},
		{`{"permissions":{"defaultMode":"bypassPermissions"}}`, `"permissions"`},
		{`{"hooks":{}}`, `"hooks"`},
		{`{"apiKeyHelper":"x"}`, `"apiKeyHelper"`},
		{`{"allowedMcpServers":[]}`, `"allowedMcpServers"`},
		{`{"disableAllHooks":true}`, `"disableAllHooks"`},
		{`{"enableAllProjectMcpServers":true}`, `"enableAllProjectMcpServers"`},
		{`{"statusLine":{}}`, `"statusLine"`},
		{`{"effort":"high"}`, `"effort"`},
		{`{"enabledPlugins":[]}`, "enabledPlugins"},
		{`{"enabledPlugins":null}`, "null"},
		{`{"enabledPlugins":{"nomarket":true}}`, "name@marketplace"},
		{`{"enabledPlugins":{"a@b":"yes"}}`, "true or false"},
		{`{"skillOverrides":{"x":"maybe"}}`, "one of"},
		{`{"skillOverrides":{"x":1}}`, "one of"},
		{`{"skillOverrides":{"bad name":"off"}}`, "skill name"},
		{`{"skillOverrides":5}`, "object"},
		{`{"disableClaudeAiConnectors":"true"}`, "boolean"},
		{`{"model":5}`, "string"},
		{`{"model":""}`, "non-empty"},
		{`{"deniedMcpServers":{}}`, "array"},
		{`{"deniedMcpServers":["x"]}`, "exactly one key"},
		{`{"deniedMcpServers":[{}]}`, "exactly one key"},
		{`{"deniedMcpServers":[{"serverName":"a","serverUrl":"b"}]}`, "exactly one key"},
		{`{"deniedMcpServers":[{"name":"a"}]}`, "serverName, serverCommand or serverUrl"},
		{`{"deniedMcpServers":[{"serverName":" a"}]}`, "whitespace"},
		{`{"deniedMcpServers":[{"serverName":5}]}`, "serverName"},
		{`{"deniedMcpServers":[{"serverCommand":"npx"}]}`, "serverCommand"},
		{`{"deniedMcpServers":[{"serverCommand":[]}]}`, "serverCommand"},
		{`{"env":{"ANTHROPIC_BASE_URL":"http://evil"}}`, "ANTHROPIC_BASE_URL"},
		{`{"env":{"FOO":"x"}}`, `"FOO"`},
		{`{"env":{"FIGMA_TOKEN_REF":1}}`, "must be a string"},
		{`{"env":[]}`, "object"},
		{`{"env":{"FIGMA_TOKEN_REF":"` + strings.Repeat("a", 5000) + `"}}`, "too long"},
	}
	for _, b := range bad {
		err := Validate([]byte(b.in))
		if err == nil || !strings.Contains(err.Error(), b.want) {
			t.Errorf("Validate(%q) = %v, want %q", b.in, err, b.want)
		}
	}
	if err := Validate(make([]byte, MaxSize+1)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("size: %v", err)
	}
	// Several problems are reported together.
	err := Validate([]byte(`{"hooks":1,"permissions":2}`))
	if err == nil || !strings.Contains(err.Error(), "hooks") || !strings.Contains(err.Error(), "permissions") {
		t.Errorf("joined: %v", err)
	}
}

func randStr(r *rand.Rand, alphabet string, n int) string {
	b := make([]byte, 1+r.Intn(n))
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return string(b)
}

func TestPropertyBuildAlwaysValidates(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	pick := func(pool []string) []string {
		var out []string
		for _, p := range pool {
			if r.Intn(7) == 0 {
				out = append(out, p)
			}
		}
		return out
	}
	ids := []string{"a@m", "b@m", "c@n", "d-e@n", "audit@acme", "ghost@x"}
	skills := []string{"s1", "s2", "pdf", "a:b", "x.y", "bad name", "w/x"}
	denies := []string{"plugin:a:b", "claude.ai X", "", " pad ", "ok"}
	envs := []string{"FIGMA_TOKEN_REF", "CCSHELF_VAR_A", "PATH", "FOO", "ANTHROPIC_API_KEY", "CCSHELF_PROFILE", "lower"}
	allowed := set(AllowedKeys)
	okCount := 0
	for i := 0; i < 2000; i++ {
		spec := Spec{
			Include: pick(ids), Exclude: pick(ids), Protected: pick(ids),
			OffSkills: pick(skills), NameOnlySkills: pick(skills), DenyMCP: pick(denies),
			HideConnectors: r.Intn(2) == 0, Profile: []string{"", "p", "", "p2", "a b", ""}[r.Intn(6)],
		}
		if r.Intn(2) == 0 {
			spec.Mode = ModeAdditive
		}
		if r.Intn(3) == 0 {
			spec.Model = randStr(r, "abc -", 8)
		}
		if r.Intn(2) == 0 {
			spec.Env = map[string]string{}
			for _, e := range pick(envs) {
				spec.Env[e] = randStr(r, "abc=", 10)
			}
		}
		for _, id := range ids[:5] {
			if r.Intn(2) == 0 {
				spec.Installed = append(spec.Installed, plug(id, func(p *claude.Plugin) { p.RequiredByOrg = id == "audit@acme" }))
			}
		}
		res, err := Build(spec)
		if err != nil {
			continue
		}
		okCount++
		raw, err := res.JSON()
		if err != nil {
			t.Fatalf("spec %+v: %v", spec, err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		for k := range top {
			if !allowed[k] {
				t.Fatalf("key %q outside the allowlist", k)
			}
		}
	}
	if okCount < 100 {
		t.Fatalf("only %d successful builds; the generator is too hostile", okCount)
	}
}

func FuzzValidate(f *testing.F) {
	for _, s := range []string{
		`{}`, `{"enabledPlugins":{"a@b":true}}`, `{"permissions":{}}`, `{"model":"x","model":"y"}`,
		`{"env":{"FIGMA_TOKEN_REF":"x"}}`, `{"deniedMcpServers":[{"serverName":"x"}]}`,
		`{"skillOverrides":{"a":"off"}}`, `[`, `{"a":`, "\xef\xbb\xbf{}",
	} {
		f.Add([]byte(s))
	}
	allowed := set(AllowedKeys)
	f.Fuzz(func(t *testing.T, data []byte) {
		if err := Validate(data); err != nil {
			return
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(data[len(data)-len(trimBOM(data)):], &top); err != nil {
			t.Fatalf("accepted but unparsable: %q", data)
		}
		for k := range top {
			if !allowed[k] {
				t.Fatalf("accepted key %q in %q", k, data)
			}
		}
	})
}

func trimBOM(b []byte) []byte {
	return []byte(strings.TrimPrefix(string(b), "\xef\xbb\xbf"))
}

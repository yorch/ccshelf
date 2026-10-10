package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/claude"
	"github.com/yorch/ccshelf/internal/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.Cleanup()
	os.Exit(code)
}

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
		{"style control", Spec{OutputStyle: "a\nb"}, "outputStyle"},
		{"style long", Spec{OutputStyle: strings.Repeat("s", 65)}, "outputStyle"},
		{"style odd chars", Spec{OutputStyle: "x;y"}, "outputStyle"},
		{"style leading space", Spec{OutputStyle: " x"}, "outputStyle"},
		{"style trailing space", Spec{OutputStyle: "x "}, "outputStyle"},
		{"env denied", Spec{Env: map[string]string{"ANTHROPIC_BASE_URL": "x"}}, "ANTHROPIC_BASE_URL"},
		{"env not allowlisted", Spec{Env: map[string]string{"FOO": "x"}}, "FOO"},
		{"env path", Spec{Env: map[string]string{"PATH": "x"}}, "PATH"},
		{"env nul", Spec{Env: map[string]string{"FIGMA_TOKEN_REF": "a\x00b"}}, "control characters"},
		{"env newline", Spec{Env: map[string]string{"FIGMA_TOKEN_REF": "a\nb"}}, "control characters"},
		{"env invalid utf8", Spec{Env: map[string]string{"FIGMA_TOKEN_REF": "a\xffb"}}, "UTF-8"},
		{"model control", Spec{Model: "a\x00b"}, "model"},
		{"model long", Spec{Model: strings.Repeat("m", 129)}, "model"},
		{"model odd chars", Spec{Model: "opus;rm"}, "model"},
		{"env long", Spec{Env: map[string]string{"FIGMA_TOKEN_REF": strings.Repeat("a", 5000)}}, "too long"},
		{"profile in env", Spec{Profile: "a", Env: map[string]string{"CCSHELF_PROFILE": "b"}}, "launcher sets env"},
		{"profile in env alone", Spec{Env: map[string]string{"CCSHELF_PROFILE": "b"}}, "launcher sets env"},
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
	if _, err := Build(Spec{Profile: "p", Env: map[string]string{"CCSHELF_PROFILE": "p"}}); err == nil {
		t.Error("CCSHELF_PROFILE in Env must be rejected even when it equals the profile")
	}
	if _, err := Build(Spec{Model: strings.Repeat("m", 128)}); err != nil {
		t.Errorf("128 characters is fine: %v", err)
	}
	if _, err := Build(Spec{Model: "claude-opus-4.5:thinking[1m]"}); err != nil {
		t.Errorf("usual model names are fine: %v", err)
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
		{`{"outputStyle":5}`, "string"},
		{`{"outputStyle":""}`, "non-empty"},
		{`{"outputStyle":"a\nb"}`, "outputStyle"},
		{`{"outputStyle":"a;b"}`, "outputStyle"},
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
		{"\xef\xbb\xbf{}", "byte order mark"},
		{"{\"model\":\"a\xffb\"}", "UTF-8"},
		{"\xff{}", "UTF-8"},
		{`{"env":{"FIGMA_TOKEN_REF":"a\u0000b"}}`, "control characters"},
		{`{"env":{"FIGMA_TOKEN_REF":"a\nb"}}`, "control characters"},
		{`{"env":{"FIGMA_TOKEN_REF":"a\u007fb"}}`, "control characters"},
		{`{"env":{"CCSHELF_PROFILE":"a\nb"}}`, "control characters"},
		{`{"env":{"CCSHELF_PROFILE":"` + strings.Repeat("p", 300) + `"}}`, "longer than"},
		{`{"model":"a\u0000b"}`, "model"},
		{`{"model":"a\nb"}`, "model"},
		{`{"model":"a b"}`, "model"},
		{`{"model":"` + strings.Repeat("m", 129) + `"}`, "model"},
		{`{"model":"op;us"}`, "model"},
		{`{"deniedMcpServers":[{"serverName":"` + strings.Repeat("s", 257) + `"}]}`, "serverName"},
		{`{"deniedMcpServers":[{"serverName":"a\u0000b"}]}`, "serverName"},
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

func TestEmptyInstalledWarns(t *testing.T) {
	const want = "no installed plugins were found. Nothing will be masked"
	res, err := Build(Spec{})
	if err != nil || !slices.Contains(res.Warnings, want) {
		t.Fatalf("allow-only: %v %v", err, res)
	}
	res, err = Build(Spec{Mode: ModeAllowOnly, Include: []string{"a@b"}})
	if err != nil || !slices.Contains(res.Warnings, want) {
		t.Fatalf("allow-only with include: %v %v", err, res.Warnings)
	}
	for _, spec := range []Spec{{Mode: ModeAdditive}, {Installed: installed}} {
		res, err := Build(spec)
		if err != nil || slices.Contains(res.Warnings, want) {
			t.Fatalf("unexpected warning for %+v: %v %v", spec.Mode, err, res.Warnings)
		}
	}
}

func TestPolicyLocked(t *testing.T) {
	inst := []claude.Plugin{plug("a@m"), plug("b@m"), plug("c@m")}
	res, err := Build(Spec{Installed: inst, PolicyLocked: []string{"b@m", "ghost@m"}, Include: []string{"a@m"}})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, res.Locked, []string{"b@m"})
	eq(t, res.Masked, []string{"c@m"})
	eq(t, res.Enabled, []string{"a@m"})
	if v, ok := res.Doc.EnabledPlugins["b@m"]; ok {
		t.Fatalf("a policy-locked plugin was written (%v)", v)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "b@m is required by org policy") {
		t.Errorf("warnings: %v", res.Warnings)
	}
	// Merged with the plugins that report RequiredByOrg themselves.
	inst = append(inst, plug("d@m", required))
	res, err = Build(Spec{Installed: inst, PolicyLocked: []string{"b@m"}, Mode: ModeAdditive, Exclude: []string{"b@m", "d@m", "c@m"}})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, res.Locked, []string{"b@m", "d@m"})
	eq(t, res.Masked, []string{"c@m"})
	if _, err := Build(Spec{PolicyLocked: []string{"nomarket"}}); err == nil || !strings.Contains(err.Error(), "policy-locked") {
		t.Errorf("bad id: %v", err)
	}
}

func TestProtectedMCP(t *testing.T) {
	_, err := Build(Spec{DenyMCP: []string{"plugin:sec:scan", "claude.ai Slack"}, ProtectedMCP: []string{"claude.ai Slack"}})
	if !errors.Is(err, ErrProtectedMCP) || !strings.Contains(err.Error(), "claude.ai Slack") {
		t.Fatalf("deny of a protected server: %v", err)
	}
	// A deny that names something else is fine.
	res, err := Build(Spec{DenyMCP: []string{"plugin:other:x"}, ProtectedMCP: []string{"claude.ai Slack"}})
	if err != nil || len(res.Doc.DeniedMcpServers) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	// Hiding all connectors removes a protected connector.
	_, err = Build(Spec{HideConnectors: true, ProtectedMCP: []string{"plugin:sec:scan", "claude.ai Slack"}})
	if !errors.Is(err, ErrProtectedConnector) || !strings.Contains(err.Error(), "claude.ai Slack") {
		t.Fatalf("hide with a protected connector: %v", err)
	}
	// A protected non-connector server does not conflict with hiding.
	if res, err := Build(Spec{HideConnectors: true, ProtectedMCP: []string{"plugin:sec:scan"}}); err != nil || !res.Doc.DisableClaudeAiConnectors {
		t.Fatalf("%v %+v", err, res)
	}
	// Protected connectors do not conflict when connectors are not hidden.
	if _, err := Build(Spec{ProtectedMCP: []string{"claude.ai Slack"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Build(Spec{ProtectedMCP: []string{" bad"}}); err == nil {
		t.Error("invalid protected label accepted")
	}
}

// TestEndToEndThroughFake drives claude.ListInstalled against the fake and
// feeds the result to Build.
func TestEndToEndThroughFake(t *testing.T) {
	testutil.IsolatedEnv(t)
	bin := testutil.BuildFakeClaude(t)
	list := func(env map[string]string) ([]claude.Plugin, error) {
		return claude.ListInstalled(context.Background(), bin, t.TempDir(), testutil.Environ(env))
	}
	t.Run("empty list warns", func(t *testing.T) {
		f := testutil.PluginsFile(t)
		plugins, err := list(map[string]string{"FAKE_CLAUDE_PLUGINS": f})
		if err != nil || len(plugins) != 0 {
			t.Fatalf("%v %v", plugins, err)
		}
		res, err := Build(Spec{Installed: plugins, Include: []string{"a@b"}})
		if err != nil || !slices.Contains(res.Warnings, "no installed plugins were found. Nothing will be masked") {
			t.Fatalf("%v %v", err, res.Warnings)
		}
	})
	t.Run("bad shapes never reach Build", func(t *testing.T) {
		for _, shape := range []string{`null`, `{}`, `{"installed":null}`, `{"plugins":[]}`, `"x"`} {
			f := filepath.Join(t.TempDir(), "p.json")
			testutil.WriteFile(t, f, shape)
			if plugins, err := list(map[string]string{"FAKE_CLAUDE_PLUGINS": f}); err == nil {
				t.Errorf("%s accepted: %v", shape, plugins)
			}
		}
	})
	t.Run("forced by policy is never masked", func(t *testing.T) {
		plugins, err := list(map[string]string{"FAKE_CLAUDE_MANAGED": `{"enabledPlugins":{"sre-kit@acme":true}}`})
		if err != nil {
			t.Fatal(err)
		}
		res, err := Build(Spec{Installed: plugins, Include: []string{"design-kit@acme"}})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Locked, []string{"sre-kit@acme"})
		if _, ok := res.Doc.EnabledPlugins["sre-kit@acme"]; ok {
			t.Fatalf("sre-kit was written: %v", res.Doc.EnabledPlugins)
		}
		if res.Doc.EnabledPlugins["seo-tools@acme"] != false || len(res.Masked) != 3 {
			t.Fatalf("%+v", res)
		}
	})
	t.Run("policy-locked ids work when the listing hides the marker", func(t *testing.T) {
		plugins, err := list(nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Build(Spec{Installed: plugins, PolicyLocked: []string{"sre-kit@acme"}})
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Locked, []string{"sre-kit@acme"})
		if _, ok := res.Doc.EnabledPlugins["sre-kit@acme"]; ok {
			t.Fatal("sre-kit was written")
		}
	})
	t.Run("generated settings take effect in the fake", func(t *testing.T) {
		res, err := Build(Spec{Installed: mustList(t, list), Include: []string{"design-kit@acme"}, Profile: "p"})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := res.JSON()
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(bin, "-p", "x", "--output-format", "stream-json", "--verbose", "--settings", string(raw)).Output() //nolint:gosec // the fake claude built for this test
		if err != nil {
			t.Fatal(err)
		}
		info, err := claude.ParseInit(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(info.Plugins, "design-kit") || slices.Contains(info.Plugins, "sre-kit") {
			t.Fatalf("%v", info.Plugins)
		}
	})
}

func mustList(t *testing.T, list func(map[string]string) ([]claude.Plugin, error)) []claude.Plugin {
	t.Helper()
	p, err := list(nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUserLayerDropped(t *testing.T) {
	inst := []claude.Plugin{plug("a@m"), plug("audit@m", required), plug("guard@m"), plug("c@m"), plug("forced@m")}
	for _, mode := range []string{ModeAllowOnly, ModeAdditive} {
		t.Run(mode, func(t *testing.T) {
			spec := Spec{
				Installed: inst, Mode: mode, Include: []string{"a@m"},
				Protected:    []string{"guard@m", "uninstalled@m"},
				PolicyLocked: []string{"forced@m"}, UserLayerDropped: true,
			}
			res, err := Build(spec)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"a@m", "audit@m", "guard@m", "forced@m"} {
				if v, ok := res.Doc.EnabledPlugins[id]; !ok || !v {
					t.Errorf("%s = %v, %v; want true", id, v, ok)
				}
			}
			if _, ok := res.Doc.EnabledPlugins["uninstalled@m"]; ok {
				t.Error("an uninstalled protected plugin was written")
			}
			for id, v := range res.Doc.EnabledPlugins {
				if !v && (id == "guard@m" || id == "audit@m" || id == "forced@m") {
					t.Errorf("%s written false", id)
				}
			}
			eq(t, res.Locked, []string{"audit@m", "forced@m"})
			if mode == ModeAllowOnly {
				eq(t, res.Masked, []string{"c@m"})
				eq(t, res.Protected, []string{"guard@m"})
			} else if len(res.Masked) != 0 {
				t.Errorf("additive masked %v", res.Masked)
			}
		})
	}
	// Without the flag nothing changes: protected is omitted.
	res, err := Build(Spec{Installed: inst, Include: []string{"a@m"}, Protected: []string{"guard@m"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Doc.EnabledPlugins["guard@m"]; ok {
		t.Error("protected plugin written without UserLayerDropped")
	}
}

func TestProtectedMCPProtectsOwningPlugin(t *testing.T) {
	for _, dropped := range []bool{false, true} {
		res, err := Build(Spec{
			Installed:        []claude.Plugin{plug("context7@official"), plug("other@official")},
			ProtectedMCP:     []string{"plugin:context7:context7"},
			UserLayerDropped: dropped,
		})
		if err != nil {
			t.Fatal(err)
		}
		v, ok := res.Doc.EnabledPlugins["context7@official"]
		if dropped && (!ok || !v) {
			t.Errorf("dropped: owning plugin not enabled: %v", res.Doc.EnabledPlugins)
		}
		if !dropped && ok {
			t.Errorf("owning plugin written: %v", res.Doc.EnabledPlugins)
		}
		if v, ok := res.Doc.EnabledPlugins["other@official"]; dropped == ok && !dropped && (!ok || v) {
			t.Errorf("other plugin not masked: %v", res.Doc.EnabledPlugins)
		}
		eq(t, res.Protected, []string{"context7@official"})
		found := false
		for _, w := range res.Warnings {
			found = found || strings.Contains(w, "protected MCP server plugin:context7:context7")
		}
		if !found {
			t.Errorf("no warning: %v", res.Warnings)
		}
	}
}

func TestProtectedMCPOwnerAmbiguityAndExclude(t *testing.T) {
	res, err := Build(Spec{
		Installed:    []claude.Plugin{plug("audit@acme"), plug("audit@community"), plug("solo@acme"), plug("zzz@acme")},
		ProtectedMCP: []string{"plugin:audit:audit", "plugin:solo:s"},
		Exclude:      []string{"solo@acme"},
	})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, res.Protected, []string{"audit@acme", "audit@community", "solo@acme"})
	if v, ok := res.Doc.EnabledPlugins["zzz@acme"]; !ok || v {
		t.Errorf("unrelated plugin not masked: %v", res.Doc.EnabledPlugins)
	}
	all := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"is ambiguous", "plugin audit@community is protected as a possible owner", "name@marketplace", "solo@acme is excluded but provides the protected MCP server plugin:solo:s. Protection wins"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing warning %q in:\n%s", want, all)
		}
	}
}

func TestOutputStyle(t *testing.T) {
	for _, name := range []string{"Explanatory", "Diagrams first", "my_style-2", "plugin:style", strings.Repeat("s", 64)} {
		res, err := Build(Spec{OutputStyle: name})
		if err != nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		b, err := json.Marshal(res.Doc)
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(b); err != nil {
			t.Errorf("%q: Build output fails Validate: %v", name, err)
		}
		if !strings.Contains(string(b), `"outputStyle":`) {
			t.Errorf("%q: no outputStyle in %s", name, b)
		}
	}
	// Without a style, the document has no outputStyle key.
	res, err := Build(Spec{Model: "opus"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.Doc)
	if strings.Contains(string(b), "outputStyle") {
		t.Errorf("unexpected key: %s", b)
	}
	if !slices.Contains(AllowedKeys, "outputStyle") || !slices.IsSorted(AllowedKeys) {
		t.Errorf("AllowedKeys: %v", AllowedKeys)
	}
}

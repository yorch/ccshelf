package schema

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/profile"
)

// tomlName returns the TOML key of a struct field, or "" to skip it.
func tomlName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("toml")
	if !ok {
		return strings.ToLower(f.Name)
	}
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return ""
	}
	return name
}

func elem(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// compare walks a struct type against its schema node, reporting drift.
func compare(t *testing.T, typ reflect.Type, s node, root node, path string) {
	t.Helper()
	typ = elem(typ)
	s = deref(s, root)
	switch typ.Kind() {
	case reflect.Struct:
		props, _ := s["properties"].(node)
		if s["additionalProperties"] != false {
			t.Errorf("%s: additionalProperties must be false", path)
		}
		want := map[string]reflect.StructField{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if n := tomlName(f); n != "" {
				want[n] = f
			}
		}
		for n, f := range want {
			sub, ok := props[n]
			if !ok {
				t.Errorf("%s.%s: field %s is missing from the schema", path, n, f.Name)
				continue
			}
			compare(t, f.Type, sub.(node), root, path+"."+n)
		}
		for n := range props {
			if _, ok := want[n]; !ok {
				t.Errorf("%s.%s: schema property has no struct field", path, n)
			}
		}
	case reflect.Slice:
		if elem(typ.Elem()).Kind() == reflect.Struct {
			it, _ := s["items"].(node)
			if it == nil {
				t.Errorf("%s: array needs items", path)
				return
			}
			compare(t, typ.Elem(), it, root, path+"[]")
		}
	case reflect.Map:
		if elem(typ.Elem()).Kind() == reflect.Struct {
			ap, _ := s["additionalProperties"].(node)
			if ap == nil {
				t.Errorf("%s: map of tables needs additionalProperties schema", path)
				return
			}
			compare(t, typ.Elem(), ap, root, path+"{}")
		} else if s["type"] != "object" {
			t.Errorf("%s: map must be an object", path)
		}
	}
}

func TestProfileSchemaMatchesStruct(t *testing.T) {
	compare(t, reflect.TypeOf(profile.Manifest{}), parseSchema(Profile()), parseSchema(Profile()), "profile")
}

func TestConfigSchemaMatchesStruct(t *testing.T) {
	compare(t, reflect.TypeOf(config.Config{}), parseSchema(Config()), parseSchema(Config()), "config")
}

func TestRegistrySchemaMatchesStruct(t *testing.T) {
	type file struct {
		Servers map[string]profile.MCPServer `toml:"servers"`
	}
	compare(t, reflect.TypeOf(file{}), parseSchema(MCPRegistry()), parseSchema(MCPRegistry()), "registry")
}

// at follows a dotted path of property names (descending into array items and
// map values) and returns the node there.
func at(root node, path string) node {
	cur := root
	for _, p := range strings.Split(path, ".") {
		cur = deref(cur, root)
		if cur["type"] == "array" {
			cur = deref(cur["items"].(node), root)
		}
		if props, ok := cur["properties"].(node); ok {
			if sub, ok := props[p].(node); ok {
				cur = sub
				continue
			}
		}
		if ap, ok := cur["additionalProperties"].(node); ok {
			cur = deref(ap, root)
			if props, ok := cur["properties"].(node); ok {
				cur = props[p].(node)
				continue
			}
		}
		panic("no schema path " + path)
	}
	return deref(cur, root)
}

func enumOf(n node) []string {
	var out []string
	for _, e := range n["enum"].([]any) {
		out = append(out, e.(string))
	}
	sort.Strings(out)
	return out
}

func sorted(s []string) []string {
	s = append([]string(nil), s...)
	sort.Strings(s)
	return s
}

func TestEnumerationsMatchGoConstants(t *testing.T) {
	p, c, r := parseSchema(Profile()), parseSchema(Config()), parseSchema(MCPRegistry())
	cases := []struct {
		name string
		root node
		path string
		want []string
	}{
		{"status", p, "status", profile.Statuses()},
		{"plugins.mode", p, "plugins.mode", profile.PluginModes()},
		{"session.effort", p, "session.effort", profile.Efforts()},
		{"mcp.claudeai_connectors", p, "mcp.claudeai_connectors", profile.ConnectorModes()},
		{"policy.on_blocked", p, "policy.on_blocked", profile.OnBlockedModes()},
		{"mcp server type", r, "servers.type", profile.MCPTypes()},
		{"sources.type", c, "sources.type", config.SourceTypes()},
		{"trust.on_change", c, "trust.on_change", config.OnChangeModes()},
		{"ui.color", c, "ui.color", config.ColorModes()},
		{"ui.interactive", c, "ui.interactive", config.InteractiveModes()},
		{"update.mode", c, "update.mode", config.UpdateModes()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := enumOf(at(tc.root, tc.path)); !reflect.DeepEqual(got, sorted(tc.want)) {
				t.Errorf("schema enum %v != Go %v", got, sorted(tc.want))
			}
		})
	}
	for _, bad := range []string{"allow"} {
		for _, e := range enumOf(at(c, "trust.on_change")) {
			if e == bad {
				t.Errorf("config schema must not accept %q", bad)
			}
		}
	}
}

func decode(t *testing.T, src string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := toml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatalf("bad test TOML: %v\n%s", err, src)
	}
	return v
}

func TestProfileExamples(t *testing.T) {
	s := parseSchema(Profile())
	files, _ := filepath.Glob("../internal/profile/testdata/valid/*.toml")
	files2, _ := filepath.Glob("../internal/profile/testdata/tree/*/profiles/*.toml")
	files = append(files, files2...)
	if len(files) < 5 {
		t.Fatalf("expected fixtures, found %v", files)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if errs := check(s, s, decode(t, string(b)), "$"); len(errs) > 0 {
			t.Errorf("%s should be valid: %v", f, errs)
		}
	}
	// invalid fixtures that the schema is expected to reject
	for _, n := range []string{
		"top-permissions", "top-hooks", "top-apikeyhelper", "top-allowedmcp", "top-deniedmcp", "top-disablehooks",
		"top-statusline", "top-env", "mcp-command", "mcp-definition", "unknown-key", "unknown-nested", "no-name", "bad-name", "bad-status",
		"bad-plugin-id", "dup-plugin", "bad-mode", "bad-skill", "bad-effort", "bad-model", "env-denied", "env-anthropic",
		"bad-connectors", "bad-server-name", "bad-onblocked", "empty-hint", "extends-bad", "wrong-type", "account-path", "account-tilde",
		"prompt-abs", "prompt-backslash", "prompt-drive", "prompt-dotdot",
	} {
		b, err := os.ReadFile("../internal/profile/testdata/invalid/" + n + ".toml")
		if err != nil {
			t.Fatal(err)
		}
		if errs := check(s, s, decode(t, string(b)), "$"); len(errs) == 0 {
			t.Errorf("%s should be rejected by the schema", n)
		}
	}
}

func TestProfilePromptPathPattern(t *testing.T) {
	s := parseSchema(Profile())
	doc := func(p string) map[string]any {
		return map[string]any{"name": "a", "session": map[string]any{"append_system_prompt_file": p}}
	}
	for _, ok := range []string{"prompts/a.md", "prompts/sub/a.md", "prompts/a.b.md"} {
		if errs := check(s, s, doc(ok), "$"); len(errs) > 0 {
			t.Errorf("%q: %v", ok, errs)
		}
	}
	for _, bad := range []string{".ssh/id_ed25519", "../x", "prompts/../x", "prompts/.hidden", "prompts/.git/x", "other/a.md", "a.md", "/etc/passwd", "prompts", "prompts/", `prompts\a.md`} {
		if errs := check(s, s, doc(bad), "$"); len(errs) == 0 {
			t.Errorf("%q accepted by the schema", bad)
		}
	}
}

func TestSchemaAndGoAgreeOnPromptPaths(t *testing.T) {
	s := parseSchema(Profile())
	for _, p := range []string{"prompts/a.md", "prompts/sub/a.md", ".ssh/id_ed25519", "../x", "prompts/.hidden", "other/a.md", "a.md", "/etc/passwd", "prompts", `prompts\a.md`, "prompts//a.md"} {
		schemaOK := len(check(s, s, map[string]any{"name": "a", "session": map[string]any{"append_system_prompt_file": p}}, "$")) == 0
		_, err := profile.Parse([]byte("name = \"a\"\n[session]\nappend_system_prompt_file = "+strconv.Quote(p)+"\n"), "")
		if schemaOK != (err == nil) {
			t.Errorf("%q: schema accepts = %v, Go accepts = %v (%v)", p, schemaOK, err == nil, err)
		}
	}
}

func TestConfigExamples(t *testing.T) {
	s := parseSchema(Config())
	valid := []string{
		"",
		"default_account = \"work\"\n[accounts.work]\nconfig_dir = \"~/.claude-work\"\n[trust]\nrequire_pin = true\non_change = \"fail\"\n[ui]\ncolor = \"never\"\n",
		"[update]\nmode = \"notify\"\ninterval = \"36h\"\nbase_url = \"https://ghe.example.com\"\n",
		"[update]\nmode = \"install\"\ninterval = \"1h30m\"\n",
		"[update]\ncosign_identity_repo = \"acme/ccshelf-fork\"\nasset_hosts = [\"assets.ghe.example.com\"]\n",
		"[[sources]]\ntype = \"git\"\nurl = \"u\"\nref = \"v1\"\npath = \"profiles\"\n[[sources]]\ntype = \"plugin\"\nplugin = \"a@b\"\nmarketplace = \"https://h.example/o/r.git\"\n",
	}
	invalid := map[string]string{
		"unknown":       "bogus = 1\n",
		"allow":         "[trust]\non_change = \"allow\"\n",
		"bad color":     "[ui]\ncolor = \"red\"\n",
		"source type":   "[[sources]]\ntype = \"ftp\"\n",
		"no type":       "[[sources]]\npath = \"x\"\n",
		"plugin id":     "[[sources]]\ntype = \"plugin\"\nplugin = \"x\"\n",
		"account name":  "[accounts.Work]\nconfig_dir = \"/x\"\n",
		"account field": "[accounts.work]\nhome = \"/x\"\n",
		"no config_dir": "[accounts.work]\n",
		"nested":        "[claude]\nargs = \"x\"\n",
		"wrong type":    "[trust]\nrequire_pin = \"yes\"\n",
		"update mode":   "[update]\nmode = \"auto\"\n",
		"update chan":   "[update]\nchannel = \"beta\"\n",
		"update intvl":  "[update]\ninterval = \"daily\"\n",
		"update type":   "[update]\ninterval = 24\n",
		"intvl sign":    "[update]\ninterval = \"+2h\"\n",
		"signer url":    "[update]\ncosign_identity_repo = \"https://github.com/a/b\"\n",
		"signer ref":    "[update]\ncosign_identity_repo = \"a/b@main\"\n",
		"asset url":     "[update]\nasset_hosts = [\"https://a.example.com\"]\n",
		"asset caps":    "[update]\nasset_hosts = [\"A.example.com\"]\n",
		"asset dup":     "[update]\nasset_hosts = [\"a.example.com\", \"a.example.com\"]\n",
		"asset string":  "[update]\nasset_hosts = \"a.example.com\"\n",
	}
	for i, v := range valid {
		if errs := check(s, s, decode(t, v), "$"); len(errs) > 0 {
			t.Errorf("valid[%d]: %v", i, errs)
		}
	}
	for n, v := range invalid {
		if errs := check(s, s, decode(t, v), "$"); len(errs) == 0 {
			t.Errorf("%s should be rejected", n)
		}
	}
}

func TestRegistryExamples(t *testing.T) {
	s := parseSchema(MCPRegistry())
	validDocs := []string{
		"[servers.a]\ncommand = \"x\"\nenv_refs = [\"CCSHELF_PROFILE\", \"FIGMA_TOKEN_REF\", \"CCSHELF_VAR_X\"]\n",
		"[servers.a]\ntype = \"sse\"\nurl = \"https://x.example:8443/a/b\"\n",
	}
	for _, v := range validDocs {
		if errs := check(s, s, decode(t, v), "$"); len(errs) > 0 {
			t.Errorf("%q: %v", v, errs)
		}
	}
	b, err := os.ReadFile("../internal/profile/testdata/tree/org/mcp/registry.toml")
	if err != nil {
		t.Fatal(err)
	}
	if errs := check(s, s, decode(t, string(b)), "$"); len(errs) > 0 {
		t.Errorf("fixture: %v", errs)
	}
	invalid := map[string]string{
		"unknown field":   "[servers.a]\ncommand = \"x\"\nenv = { A = \"1\" }\n",
		"http url":        "[servers.a]\ntype = \"http\"\nurl = \"http://x\"\n",
		"bad type":        "[servers.a]\ntype = \"ws\"\n",
		"bad name":        "[servers.\"a b\"]\ncommand = \"x\"\n",
		"override":        "[servers.a]\ncommand = \"x\"\n[servers.a.windows]\nargs = [\"x\"]\n",
		"top":             "[other]\n",
		"no servers":      "",
		"env ref":         "[servers.a]\ncommand = \"x\"\nenv_refs = [\"lower\"]\n",
		"env ref no _REF": "[servers.a]\ncommand = \"x\"\nenv_refs = [\"FIGMA_TOKEN\"]\n",
		"env ref denied":  "[servers.a]\ncommand = \"x\"\nenv_refs = [\"ANTHROPIC_X_REF\"]\n",
		"https uppercase": "[servers.a]\ntype = \"http\"\nurl = \"HTTPS://x.example\"\n",
		"url query":       "[servers.a]\ntype = \"http\"\nurl = \"https://x.example/a?token=1\"\n",
		"url fragment":    "[servers.a]\ntype = \"http\"\nurl = \"https://x.example/a#f\"\n",
		"url whitespace":  "[servers.a]\ntype = \"http\"\nurl = \"https://x.example/a b\"\n",
	}
	for n, v := range invalid {
		if errs := check(s, s, decode(t, v), "$"); len(errs) == 0 {
			t.Errorf("%s should be rejected", n)
		}
	}
}

func TestCheckerCombinators(t *testing.T) {
	s := parseSchema([]byte(`{"type":"object","propertyNames":{"allOf":[{"pattern":"^[A-Z]+$"},{"not":{"enum":["BAD"]}}]},"additionalProperties":{"anyOf":[{"const":"x"},{"type":"boolean"}]}}`))
	if errs := check(s, s, map[string]any{"OK": "x", "ALSO": true}, "$"); len(errs) != 0 {
		t.Errorf("unexpected: %v", errs)
	}
	for name, v := range map[string]map[string]any{
		"pattern": {"low": true},
		"not":     {"BAD": true},
		"anyOf":   {"OK": "y"},
	} {
		if errs := check(s, s, v, "$"); len(errs) == 0 {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCheckerItself(t *testing.T) {
	s := parseSchema([]byte(`{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"string","minLength":2,"maxLength":3},"n":{"type":"integer"},"l":{"type":"array","uniqueItems":true,"items":{"type":"boolean"}}}}`))
	ok := map[string]any{"a": "xy", "n": int64(1), "l": []any{true, false}}
	if errs := check(s, s, ok, "$"); len(errs) != 0 {
		t.Errorf("unexpected: %v", errs)
	}
	for name, v := range map[string]map[string]any{
		"short":   {"a": "x"},
		"long":    {"a": "xxxx"},
		"missing": {},
		"extra":   {"a": "xy", "z": 1},
		"dup":     {"a": "xy", "l": []any{true, true}},
		"type":    {"a": "xy", "n": "1"},
		"item":    {"a": "xy", "l": []any{"s"}},
	} {
		if errs := check(s, s, v, "$"); len(errs) == 0 {
			t.Errorf("%s accepted", name)
		}
	}
	if typeOK("weird", 1) {
		t.Error("unknown type accepted")
	}
}

func TestSchemasAreValidJSONWithMeta(t *testing.T) {
	for name, b := range map[string][]byte{"profile": Profile(), "config": Config(), "registry": MCPRegistry()} {
		n := parseSchema(b)
		if n["$schema"] != "https://json-schema.org/draft/2020-12/schema" || n["additionalProperties"] != false {
			t.Errorf("%s: missing $schema or closed top level", name)
		}
	}
	b := Profile()
	b[0] = 'X'
	if Profile()[0] == 'X' {
		t.Error("accessor must return a copy")
	}
}

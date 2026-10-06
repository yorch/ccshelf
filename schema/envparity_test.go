package schema

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/envpolicy"
)

// envpolicyData holds the constants of internal/envpolicy that define which
// variable names a profile may use. They are read from the package source
// because they are unexported: the schema must follow them, and this test
// fails when it does not.
type envpolicyData struct {
	namePattern  string
	allowPattern string
	prefixes     []string
	suffixes     []string
	exact        []string
}

func stringsOf(t *testing.T, e ast.Expr) []string {
	t.Helper()
	cl, ok := e.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("expected a composite literal, got %T", e)
	}
	var out []string
	for _, el := range cl.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			el = kv.Key
		}
		lit, ok := el.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Fatalf("expected string literals, got %T", el)
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func readEnvpolicy(t *testing.T) envpolicyData {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../internal/envpolicy/envpolicy.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var d envpolicyData
	seen := map[string]bool{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, sp := range gd.Specs {
			vs := sp.(*ast.ValueSpec)
			for i, n := range vs.Names {
				switch n.Name {
				case "namePattern", "allowPattern":
					call := vs.Values[i].(*ast.CallExpr)
					lit := call.Args[0].(*ast.BasicLit)
					s, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					if n.Name == "namePattern" {
						d.namePattern = s
					} else {
						d.allowPattern = s
					}
				case "deniedPrefixes":
					d.prefixes = stringsOf(t, vs.Values[i])
				case "deniedSuffixes":
					d.suffixes = stringsOf(t, vs.Values[i])
				case "deniedExact":
					d.exact = stringsOf(t, vs.Values[i])
				default:
					continue
				}
				seen[n.Name] = true
			}
		}
	}
	for _, n := range []string{"namePattern", "allowPattern", "deniedPrefixes", "deniedSuffixes", "deniedExact"} {
		if !seen[n] {
			t.Fatalf("envpolicy.%s not found: update this test together with the package", n)
		}
	}
	sort.Strings(d.exact)
	return d
}

// expectedEnvRule builds the schema fragment for an allowed variable name from
// the envpolicy constants.
func expectedEnvRule(t *testing.T) node {
	d := readEnvpolicy(t)
	quoted := make([]string, len(d.prefixes))
	for i, p := range d.prefixes {
		quoted[i] = regexp.QuoteMeta(p)
	}
	sufs := make([]string, len(d.suffixes))
	for i, p := range d.suffixes {
		sufs[i] = regexp.QuoteMeta(p)
	}
	exact := make([]any, len(d.exact))
	for i, e := range d.exact {
		exact[i] = e
	}
	rule := node{"allOf": []any{
		node{"pattern": d.namePattern},
		node{"pattern": d.allowPattern},
		node{"not": node{"pattern": "^(" + strings.Join(quoted, "|") + ")"}},
		node{"not": node{"pattern": "(" + strings.Join(sufs, "|") + ")$"}},
		node{"not": node{"pattern": "_PROXY"}},
		node{"not": node{"enum": exact}},
	}}
	// round-trip through JSON so numbers and slices have the decoded types
	b, err := json.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	return parseSchema(b)
}

func TestEnvNameRuleMatchesEnvpolicy(t *testing.T) {
	want := expectedEnvRule(t)
	for name, b := range map[string][]byte{"profile": Profile(), "registry": MCPRegistry()} {
		root := parseSchema(b)
		got, _ := root["$defs"].(node)["envName"].(node)
		if !reflect.DeepEqual(got, want) {
			pretty, _ := json.MarshalIndent(want, "    ", "  ")
			t.Errorf("%s schema $defs.envName is out of date with internal/envpolicy; it must be:\n    %s", name, pretty)
		}
	}
}

// corpus builds at least 200 names from the envpolicy constants and from
// shapes that have gone wrong before.
func corpus(t *testing.T) []string {
	d := readEnvpolicy(t)
	set := map[string]bool{}
	add := func(n string) { set[n] = true }
	for _, p := range d.prefixes {
		add(p + "X")
		add(p + "X_REF")
		add(p + "CCSHELF_VAR_X")
		add(strings.TrimSuffix(p, "_") + "_THING_REF")
	}
	for _, s := range d.suffixes {
		add("MY" + s)
		add("MY" + s + "_REF")
		add("CCSHELF_VAR_A" + s)
	}
	for _, e := range d.exact {
		add(e)
		add(e + "_REF")
		add("CCSHELF_VAR_" + e)
	}
	for _, n := range []string{
		"FIGMA_TOKEN_REF", "PAGERDUTY_TOKEN_REF", "CCSHELF_VAR_TEAM", "CCSHELF_VAR_A_B_2", "CCSHELF_PROFILE", "CCSHELF_VAR_", "CCSHELF_VARTEAM",
		"REF", "_REF", "A_REF", "lowercase_REF", "Mixed_REF", "1LEAD_REF", "HAS-DASH_REF", "", "MY_PROXY_URL_REF", "HTTP_PROXY_REF", "NO_PROXY", "SOME_PROXY",
		"MY_OPTS", "SOME_OPTIONS_REF", "LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "NODE_OPTIONS", "ANTHROPIC_API_KEY", "GITHUB_REF", "CI", "CI_REF",
		strings.Repeat("A", 56) + "_REF", strings.Repeat("A", 55) + "_REF", strings.Repeat("A", 57) + "_REF", "CCSHELF_VAR_" + strings.Repeat("A", 40), "CCSHELF_VAR_" + strings.Repeat("A", 41),
		"CCSHELF_PROFILE_REF", "CCSHELF_VAR_REF", "É_REF", "A\n_REF",
	} {
		add(n)
	}
	// deterministic pseudo-random names from tokens
	toks := []string{"MY", "TOOL", "TEAM", "TOKEN", "REF", "VAR", "CCSHELF", "KEY", "PATH", "HOME", "_", "X", "2", "OPTS", "PROXY", "NODE", "GIT", "API", "URL"}
	r := rand.New(rand.NewSource(42))
	for len(set) < 400 {
		var b strings.Builder
		for i, n := 0, 1+r.Intn(4); i < n; i++ {
			b.WriteString(toks[r.Intn(len(toks))])
			if r.Intn(2) == 0 {
				b.WriteByte('_')
			}
		}
		add(b.String())
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestEnvNameSchemaAgreesWithGo(t *testing.T) {
	names := corpus(t)
	if len(names) < 200 {
		t.Fatalf("corpus has only %d names", len(names))
	}
	profile, registry := parseSchema(Profile()), parseSchema(MCPRegistry())
	rule := profile["$defs"].(node)["envName"].(node)
	allowed, denied := 0, 0
	for _, n := range names {
		// CCSHELF_PROFILE is set by the launcher: a profile may not set it,
		// so the profile rule must reject it even though envpolicy allows it.
		goAllowed := envpolicy.Allowed(n) && n != envpolicy.Profile
		schemaAllowed := len(check(rule, profile, n, "$")) == 0
		if goAllowed != schemaAllowed {
			t.Errorf("%q: Go allowed = %v, schema allowed = %v", n, goAllowed, schemaAllowed)
		}
		if goAllowed {
			allowed++
		} else {
			denied++
		}
		// env_refs: Go (envpolicy.Check) allows exactly what envpolicy allows.
		item := at(registry, "servers.env_refs")["items"].(node)
		if got, want := len(check(item, registry, n, "$")) == 0, envpolicy.Allowed(n); got != want {
			t.Errorf("env_refs %q: Go allowed = %v, schema allowed = %v", n, want, got)
		}
	}
	if allowed < 20 || denied < 100 {
		t.Errorf("corpus is lopsided: %d allowed, %d denied", allowed, denied)
	}
}

func TestEnvRuleWiredIntoSchemas(t *testing.T) {
	p := parseSchema(Profile())
	env := at(p, "session.env")
	pn, _ := env["propertyNames"].(node)
	if pn["$ref"] != "#/$defs/envName" {
		t.Errorf("session.env propertyNames = %v", pn)
	}
	r := parseSchema(MCPRegistry())
	items := at(r, "servers.env_refs")["items"].(node)
	b, _ := json.Marshal(items)
	if !strings.Contains(string(b), "#/$defs/envName") || !strings.Contains(string(b), envpolicy.Profile) {
		t.Errorf("env_refs items = %s", b)
	}
	// the profile rule rejects what a profile must never set, and says so
	if errs := check(env, p, map[string]any{"CCSHELF_PROFILE": "x"}, "$"); len(errs) == 0 {
		t.Error("schema must reject CCSHELF_PROFILE in session.env")
	}
	for _, bad := range []string{"ANTHROPIC_BASE_URL", "HTTPS_PROXY", "NODE_OPTIONS", "FOO"} {
		if errs := check(env, p, map[string]any{bad: "x"}, "$"); len(errs) == 0 {
			t.Errorf("schema accepted %s", bad)
		}
	}
}

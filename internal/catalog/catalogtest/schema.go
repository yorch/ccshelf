package catalogtest

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// SchemaPath returns the path of a schema file in the repo's schema/ directory.
func SchemaPath(t testing.TB, name string) string {
	t.Helper()
	return filepath.Join(filepath.Dir(FixtureDir(t)), "..", "..", "..", "schema", name)
}

// LoadSchema reads and parses a schema file of the repo's schema/ directory.
func LoadSchema(t testing.TB, name string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(SchemaPath(t, name))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

// Validate checks a decoded JSON document against a schema and returns the
// problems found. It supports the subset of JSON Schema the ccshelf schemas
// use: type, enum, const, properties, required, additionalProperties (false),
// items, minItems, minimum, maxLength, pattern and local $ref into $defs.
func Validate(root map[string]any, doc any) []string {
	var out []string
	validate(root, root, doc, "$", &out)
	sort.Strings(out)
	return out
}

func validate(root, s map[string]any, v any, at string, out *[]string) {
	fail := func(format string, a ...any) { *out = append(*out, at+": "+fmt.Sprintf(format, a...)) }
	if ref, ok := s["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/$defs/")
		defs, _ := root["$defs"].(map[string]any)
		target, _ := defs[name].(map[string]any)
		if target == nil {
			fail("unresolved $ref %s", ref)
			return
		}
		validate(root, target, v, at, out)
		return
	}
	if c, ok := s["const"]; ok && fmt.Sprint(c) != fmt.Sprint(v) {
		fail("want const %v, got %v", c, v)
	}
	if e, ok := s["enum"].([]any); ok {
		found := false
		for _, x := range e {
			if fmt.Sprint(x) == fmt.Sprint(v) {
				found = true
			}
		}
		if !found {
			fail("%v is not one of %v", v, e)
		}
	}
	if t, ok := s["type"].(string); ok {
		okType := false
		switch t {
		case "object":
			_, okType = v.(map[string]any)
		case "array":
			_, okType = v.([]any)
		case "string":
			_, okType = v.(string)
		case "boolean":
			_, okType = v.(bool)
		case "integer":
			f, isNum := v.(float64)
			okType = isNum && f == math.Trunc(f)
		case "number":
			_, okType = v.(float64)
		}
		if !okType {
			fail("want type %s, got %T", t, v)
			return
		}
	}
	switch x := v.(type) {
	case string:
		if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(x) {
			fail("%q does not match %s", x, p)
		}
		if m, ok := s["maxLength"].(float64); ok && float64(utf8.RuneCountInString(x)) > m {
			fail("longer than %v", m)
		}
	case float64:
		if m, ok := s["minimum"].(float64); ok && x < m {
			fail("%v is below %v", x, m)
		}
	case []any:
		if m, ok := s["minItems"].(float64); ok && float64(len(x)) < m {
			fail("fewer than %v items", m)
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, it := range x {
				validate(root, items, it, fmt.Sprintf("%s[%d]", at, i), out)
			}
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, present := x[r.(string)]; !present {
					fail("missing required property %q", r)
				}
			}
		}
		for k, val := range x {
			ps, known := props[k].(map[string]any)
			if !known {
				if ap, ok := s["additionalProperties"].(bool); ok && !ap {
					*out = append(*out, fmt.Sprintf("%s: unexpected property %q", at, k))
				}
				continue
			}
			validate(root, ps, val, at+"."+k, out)
		}
	}
}

// SchemaProperties returns the property names the schema declares at the top
// level, or inside the named property when path is given.
func SchemaProperties(s map[string]any, path ...string) []string {
	cur := s
	for _, p := range path {
		props, _ := cur["properties"].(map[string]any)
		next, _ := props[p].(map[string]any)
		cur = next
	}
	props, _ := cur["properties"].(map[string]any)
	out := make([]string, 0, len(props))
	for k := range props {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

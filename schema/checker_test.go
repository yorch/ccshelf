package schema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// A deliberately small JSON Schema checker: just the keywords the ccshelf
// schemas use.

type node = map[string]any

func parseSchema(b []byte) node {
	var n node
	if err := json.Unmarshal(b, &n); err != nil {
		panic(err)
	}
	return n
}

func deref(n node, root node) node {
	ref, ok := n["$ref"].(string)
	if !ok {
		return n
	}
	cur := any(root)
	for _, p := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cur = cur.(node)[p]
	}
	return cur.(node)
}

func check(s node, root node, v any, path string) []string {
	s = deref(s, root)
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, path+": "+fmt.Sprintf(f, a...)) }
	if en, ok := s["enum"].([]any); ok {
		found := false
		for _, e := range en {
			if e == v {
				found = true
			}
		}
		if !found {
			add("%v is not in enum %v", v, en)
		}
	}
	if c, ok := s["const"]; ok && c != v {
		add("%v is not the constant %v", v, c)
	}
	if all, ok := s["allOf"].([]any); ok {
		for _, sub := range all {
			errs = append(errs, check(sub.(node), root, v, path)...)
		}
	}
	if anyOf, ok := s["anyOf"].([]any); ok {
		matched := false
		for _, sub := range anyOf {
			if len(check(sub.(node), root, v, path)) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			add("%v matches none of the anyOf alternatives", v)
		}
	}
	if n, ok := s["not"].(node); ok && len(check(n, root, v, path)) == 0 {
		add("%v matches a schema it must not match", v)
	}
	if t, ok := s["type"].(string); ok && !typeOK(t, v) {
		add("want %s, got %T", t, v)
		return errs
	}
	switch x := v.(type) {
	case string:
		if p, ok := s["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(x) {
			add("%q does not match %s", x, p)
		}
		if m, ok := s["minLength"].(float64); ok && float64(utf8.RuneCountInString(x)) < m {
			add("shorter than %v", m)
		}
		if m, ok := s["maxLength"].(float64); ok && float64(utf8.RuneCountInString(x)) > m {
			add("longer than %v", m)
		}
	case []any:
		if it, ok := s["items"].(node); ok {
			for i, e := range x {
				errs = append(errs, check(it, root, e, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
		if m, ok := s["maxItems"].(float64); ok && float64(len(x)) > m {
			add("%d items, more than %v", len(x), m)
		}
		if u, _ := s["uniqueItems"].(bool); u {
			seen := map[string]bool{}
			for _, e := range x {
				k := fmt.Sprint(e)
				if seen[k] {
					add("duplicate item %s", k)
				}
				seen[k] = true
			}
		}
	case map[string]any:
		props, _ := s["properties"].(node)
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, ok := x[r.(string)]; !ok {
					add("missing required %q", r)
				}
			}
		}
		for _, k := range keys {
			if pn, ok := s["propertyNames"].(node); ok {
				errs = append(errs, check(pn, root, k, path+"{"+k+"}")...)
			}
			sub, known := props[k]
			switch {
			case known:
				errs = append(errs, check(sub.(node), root, x[k], path+"."+k)...)
			case s["additionalProperties"] == false:
				add("unknown property %q", k)
			default:
				if ap, ok := s["additionalProperties"].(node); ok {
					errs = append(errs, check(ap, root, x[k], path+"."+k)...)
				}
			}
		}
	}
	return errs
}

func typeOK(t string, v any) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "integer":
		_, ok := v.(int64)
		return ok
	}
	return false
}

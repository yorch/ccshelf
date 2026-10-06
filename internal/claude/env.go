package claude

import (
	"runtime"
	"sort"
	"strings"
)

// Env merges extra into base and returns a new environment slice. The result
// keeps base order (first appearance of each name), the last value for a
// duplicated name wins, extras override base values, and names that are new
// are appended sorted. Entries without "=" are dropped. On Windows names are
// compared case-insensitively.
func Env(base []string, extra map[string]string) []string {
	fold := func(k string) string {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(k)
		}
		return k
	}
	var order []string
	vals := map[string]string{}
	names := map[string]string{}
	set := func(k, v string) {
		f := fold(k)
		if _, ok := vals[f]; !ok {
			order = append(order, f)
			names[f] = k
		}
		vals[f] = v
	}
	for _, e := range base {
		k, v, ok := strings.Cut(e, "=")
		if !ok || k == "" {
			continue
		}
		set(k, v)
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		set(k, extra[k])
	}
	out := make([]string, 0, len(order))
	for _, f := range order {
		out = append(out, names[f]+"="+vals[f])
	}
	return out
}

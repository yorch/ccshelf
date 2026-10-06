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
// compared case-insensitively and entries whose name starts with "=" (the
// per-drive working directories such as =C:=C:\work, which child processes
// rely on) are kept.
func Env(base []string, extra map[string]string) []string {
	return mergeEnv(runtime.GOOS, base, extra)
}

// splitEnv splits one environment entry into name and value. On Windows a
// leading "=" belongs to the name.
func splitEnv(goos, e string) (name, value string, ok bool) {
	if goos == "windows" && strings.HasPrefix(e, "=") {
		k, v, ok := strings.Cut(e[1:], "=")
		return "=" + k, v, ok && k != ""
	}
	k, v, ok := strings.Cut(e, "=")
	return k, v, ok && k != ""
}

func mergeEnv(goos string, base []string, extra map[string]string) []string {
	fold := func(k string) string {
		if goos == "windows" {
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
		k, v, ok := splitEnv(goos, e)
		if !ok {
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

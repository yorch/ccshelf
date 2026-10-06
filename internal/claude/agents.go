package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var winAbs = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func looksAbsolute(s string) bool {
	if s == "" || len(s) > 4096 || strings.ContainsAny(s, "\n\r\x00") {
		return false
	}
	return filepath.IsAbs(s) || strings.HasPrefix(s, "/") || winAbs.MatchString(s)
}

func collectPaths(v any, set map[string]bool) {
	switch x := v.(type) {
	case string:
		if looksAbsolute(x) {
			set[x] = true
		}
	case []any:
		for _, e := range x {
			collectPaths(e, set)
		}
	case map[string]any:
		for _, e := range x {
			collectPaths(e, set)
		}
	}
}

// ReferencedPaths asks `claude agents --json --all` (5 second timeout) for
// live background sessions and returns every string value in the answer that
// looks like an absolute file path, sorted. The cache uses it to avoid
// deleting generated files a running session still points at. It is best
// effort by design: the command may not exist in older Claude Code versions
// and the cache must keep working without it, so any failure (missing
// command, non-zero exit, bad JSON, timeout) yields nil, nil.
func ReferencedPaths(ctx context.Context, bin string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code, err := Spawn(ctx, bin, []string{"agents", "--json", "--all"}, nil, nil, &stdout, &stderr)
	if err != nil || code != 0 {
		return nil, nil
	}
	var v any
	if json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &v) != nil {
		return nil, nil
	}
	set := map[string]bool{}
	collectPaths(v, set)
	if len(set) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

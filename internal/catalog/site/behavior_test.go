package site

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type harnessOut struct {
	Selects  map[string][]string `json:"selects"`
	Count    string              `json:"count"`
	Meta     string              `json:"meta"`
	Clusters []string            `json:"clusters"`
	Plugins  struct {
		Headings []string `json:"headings"`
		Anchors  []string `json:"anchors"`
		Count    string   `json:"count"`
	} `json:"plugins"`
}

// runApp executes templates/app.js in node against a fake DOM and returns
// what it rendered. The test is skipped when node is not installed.
func runApp(t *testing.T, catalog map[string]any, hash string, click ...string) harnessOut {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping the JavaScript behavior test")
	}
	in, err := json.Marshal(map[string]any{"catalog": catalog, "hash": hash, "click": click})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join("testdata", "harness.js"), filepath.Join("templates", "app.js"))
	cmd.Stdin = bytes.NewReader(in)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var out harnessOut
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("harness output: %v\n%s", err, stdout.String())
	}
	return out
}

func plugin(name string, extra map[string]any) map[string]any {
	p := map[string]any{"name": name, "description": "d " + name, "status": "active", "owner": "@a/b"}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func TestAppHandlesPrototypeNames(t *testing.T) {
	names := []string{"constructor", "toString", "__proto__", "hasOwnProperty", "valueOf", "plain", "alone"}
	var ps []any
	for _, n := range names {
		ps = append(ps, plugin(n, map[string]any{"tags": []string{n}, "category": n, "owner": "@own/" + n}))
	}
	ps[0].(map[string]any)["overlaps_with"] = []string{"toString", "ghost", "isPrototypeOf"}
	ps[2].(map[string]any)["overlaps_with"] = []string{"hasOwnProperty"}
	ps[5].(map[string]any)["overlaps_with"] = []string{"valueOf", "constructor"}
	cat := map[string]any{"version": 1, "title": "t", "plugins": ps, "profiles": []any{}}

	out := runApp(t, cat, "", "Overlaps")
	want := []string{"__proto__ / hasOwnProperty", "constructor / plain / toString / valueOf"}
	if !reflect.DeepEqual(out.Clusters, want) {
		t.Errorf("clusters = %q, want %q", out.Clusters, want)
	}
	if !reflect.DeepEqual(out.Plugins.Headings, sortedByStatusName(names)) {
		t.Errorf("plugin cards = %q", out.Plugins.Headings)
	}
	for _, k := range []string{"category", "tag"} {
		got := out.Selects[k]
		if len(got) != len(names) {
			t.Errorf("%s options = %q, want all %d names (including __proto__)", k, got, len(names))
		}
	}
	if len(out.Selects["owner"]) != len(names) {
		t.Errorf("owner options = %q", out.Selects["owner"])
	}

	// Filtering by a name that is also an Object.prototype member.
	for _, n := range []string{"__proto__", "constructor", "toString"} {
		o := runApp(t, cat, "tag="+n)
		if o.Plugins.Count != "1 of 7 plugins" || len(o.Plugins.Headings) != 1 || o.Plugins.Headings[0] != n {
			t.Errorf("tag filter %q: %q %q", n, o.Plugins.Count, o.Plugins.Headings)
		}
	}
}

func sortedByStatusName(names []string) []string {
	out := append([]string(nil), names...)
	// All are active: the app sorts by name with plain code unit order.
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestAppSafeHref(t *testing.T) {
	long := "https://a.example/" + strings.Repeat("x", 2040)
	tests := []struct {
		raw  string
		want string // "" means no link
	}{
		{"https://docs.example.com/a?b=c#d", "https://docs.example.com/a?b=c#d"},
		{"http://docs.example.com", "http://docs.example.com/"},
		{"HTTPS://Docs.Example.com/x", "https://docs.example.com/x"},
		{"javascript:alert(1)", ""},
		{"JaVaScRiPt:alert(1)", ""},
		{"data:text/html,x", ""},
		{"ftp://a.example/", ""},
		{"file:///etc/passwd", ""},
		{"//a.example/x", ""},
		{"a.example/x", ""},
		{"https://user@a.example/", ""},
		{"https://user:pw@a.example/", ""},
		{"https://:pw@a.example/", ""},
		{"https://a.example/\x01", ""},
		{"https://a.example/\x00", ""},
		{"https://a.example/ x", ""},
		{" https://a.example/", ""},
		{"https://a.example/\t", ""},
		{"https://a.example/\n", ""},
		{"https://a.example/\u007f", ""},
		{"https://a.example/é", "https://a.example/%C3%A9"},
		{long[:2048], long[:2048]},
		{long[:2049], ""},
		{"", ""},
	}
	var ps []any
	for i, tt := range tests {
		ps = append(ps, plugin("p"+string(rune('a'+i)), map[string]any{"docs": tt.raw}))
	}
	// Every link of every card, in card order (cards are sorted by name).
	out := runApp(t, map[string]any{"version": 1, "plugins": ps}, "")
	var want []string
	for _, tt := range tests {
		if tt.want != "" {
			want = append(want, tt.want)
		}
	}
	if !reflect.DeepEqual(out.Plugins.Anchors, want) {
		t.Errorf("anchors:\n got %q\nwant %q", out.Plugins.Anchors, want)
	}
	// Non-string values are never links.
	out = runApp(t, map[string]any{"version": 1, "plugins": []any{plugin("x", map[string]any{"docs": 5, "homepage": []string{"https://a.example"}, "repository": nil})}}, "")
	if len(out.Plugins.Anchors) != 0 {
		t.Errorf("non-string links rendered: %q", out.Plugins.Anchors)
	}
}

func TestAppRendersWithoutPlugins(t *testing.T) {
	out := runApp(t, map[string]any{"version": 1, "plugins": []any{}}, "")
	if out.Plugins.Count != "0 of 0 plugins" {
		t.Errorf("count = %q", out.Plugins.Count)
	}
}

func TestHarnessPresent(t *testing.T) {
	if _, err := os.Stat(filepath.Join("testdata", "harness.js")); err != nil {
		t.Fatal(err)
	}
}

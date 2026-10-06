package orgcmd

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/ui"
)

var update = flag.Bool("update", false, "rewrite golden files")

// golden compares got with testdata/<name>, rewriting it with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", p, err)
	}
	if strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Errorf("%s differs from golden:\n--- got ---\n%s--- want ---\n%s", name, got, want)
	}
}

func decode(t *testing.T, s string) (kind string, data map[string]any) {
	t.Helper()
	var env struct {
		Version int            `json:"version"`
		Kind    string         `json:"kind"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(s), &env); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	if env.Version != ui.JSONVersion {
		t.Errorf("version = %d", env.Version)
	}
	return env.Kind, env.Data
}

func TestCommandNames(t *testing.T) {
	h := newHarness(t, copyExample(t))
	_ = h
	cmds := Commands(nil)
	var names []string
	for _, c := range cmds {
		names = append(names, c.Name())
	}
	want := "lint compile catalog search recommend doctor"
	if strings.Join(names, " ") != want {
		t.Errorf("commands = %v, want %s", names, want)
	}
}

func TestLintExampleClean(t *testing.T) {
	h := newHarness(t, copyExample(t))
	r := h.run("lint")
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if strings.Contains(r.out, " error ") {
		t.Errorf("error findings in the starter:\n%s", r.out)
	}
	golden(t, "lint-example.golden.txt", r.out)
}

func TestLintFormats(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, root)

	j := h.run("lint", "--format", "json")
	var doc struct {
		Version int    `json:"version"`
		Kind    string `json:"kind"`
		Data    struct {
			Summary  struct{ Errors, Warnings, Infos int }
			Findings []struct{ Severity, Code string }
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(j.out), &doc); err != nil || doc.Version != 1 || doc.Kind != "lint" || len(doc.Data.Findings) == 0 {
		t.Fatalf("json: %v %+v\n%s", err, doc, j.out)
	}
	if doc.Data.Summary.Errors+doc.Data.Summary.Warnings+doc.Data.Summary.Infos != len(doc.Data.Findings) {
		t.Errorf("summary %+v does not match %d findings", doc.Data.Summary, len(doc.Data.Findings))
	}
	if g := h.run("--json", "lint"); g.out != j.out {
		t.Errorf("--json and --format json differ")
	}

	gh := h.run("lint", "--format", "github")
	if gh.code != 0 || !strings.Contains(gh.out, "::warning ") || !strings.Contains(gh.out, "title=PRF002") {
		t.Errorf("github format:\n%s", gh.out)
	}

	bad := h.run("lint", "--format", "xml")
	if bad.code != ui.ExitUsage {
		t.Errorf("unknown format code = %d", bad.code)
	}
	if h.run("lint", "extra").code != ui.ExitUsage {
		t.Error("positional argument should be a usage error")
	}
}

func TestLintStrict(t *testing.T) {
	h := newHarness(t, copyExample(t))
	if r := h.run("lint", "--strict"); r.code != ui.ExitFailure || !strings.Contains(r.err, "--strict") {
		t.Errorf("strict: code %d %s", r.code, r.err)
	}
}

func TestLintFailures(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
		want   string // substring of the output
	}{
		{"missing sidecar", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "catalog", "plugins", "docs-writer.toml")); err != nil {
				t.Fatal(err)
			}
		}, "CAT010"},
		{"bad status", func(t *testing.T, root string) {
			p := filepath.Join("catalog", "plugins", "docs-writer.toml")
			write(t, root, filepath.ToSlash(p), strings.Replace(read(t, root, filepath.ToSlash(p)), `status = "active"`, `status = "bogus"`, 1))
		}, "CAT014"},
		{"invalid profile", func(t *testing.T, root string) {
			write(t, root, "profiles/frontend.toml", read(t, root, "profiles/frontend.toml")+"\n[hooks]\nx = 1\n")
		}, "PRF001"},
		{"profile without bundle entry", func(t *testing.T, root string) {
			write(t, root, "profiles/extra.toml", "name = \"extra\"\ndescription = \"A fictional extra profile\"\n[plugins]\ninclude = [\"design-kit@acme\"]\n")
		}, "CAT050"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := copyExample(t)
			tt.mutate(t, root)
			h := newHarness(t, root)
			r := h.run("lint")
			if r.code != ui.ExitFailure {
				t.Fatalf("code %d, want 1\n%s\n%s", r.code, r.out, r.err)
			}
			if !strings.Contains(r.out, tt.want) {
				t.Errorf("output lacks %s:\n%s", tt.want, r.out)
			}
			if gh := h.run("lint", "--format", "github"); gh.code != 1 || !strings.Contains(gh.out, "::error ") {
				t.Errorf("github annotations:\n%s", gh.out)
			}
		})
	}
}

func TestLintRootErrors(t *testing.T) {
	h := newHarness(t, filepath.Join(t.TempDir(), "nope"))
	if r := h.run("lint"); r.code != 1 || !strings.Contains(r.err, "org data repo root") {
		t.Errorf("missing root: %d %s", r.code, r.err)
	}
	f := filepath.Join(t.TempDir(), "file")
	write(t, filepath.Dir(f), "file", "x")
	h.g.Root = f
	if r := h.run("lint"); r.code != 1 || !strings.Contains(r.err, "not a directory") {
		t.Errorf("file root: %d %s", r.code, r.err)
	}
	root := copyExample(t)
	write(t, root, "ccshelf.toml", "bogus = 1\n")
	h.g.Root = root
	if r := h.run("lint"); r.code != 1 || !strings.Contains(r.err, "org config") {
		t.Errorf("bad config: %d %s", r.code, r.err)
	}
}

func TestRootDefaultsToWorkingDirectory(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, "")
	h.cwd = root
	if r := h.run("lint"); r.code != 0 {
		t.Fatalf("lint from cwd: %d\n%s", r.code, r.err)
	}
	// A relative --root is resolved against the working directory.
	h.cwd = filepath.Dir(root)
	if r := h.run("--root", filepath.Base(root), "lint"); r.code != 0 {
		t.Fatalf("relative root: %d\n%s", r.code, r.err)
	}
}

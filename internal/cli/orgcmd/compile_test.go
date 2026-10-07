package orgcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/ui"
)

func TestCompileCheckClean(t *testing.T) {
	h := newHarness(t, copyExample(t))
	r := h.run("compile", "--check")
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	golden(t, "compile-check-clean.golden.txt", r.out)

	j := h.run("--json", "compile", "--check")
	kind, data := decode(t, j.out)
	if kind != "compile" || data["changed"] != false || data["check"] != true {
		t.Errorf("json: %v %v", kind, data)
	}
	if bs, _ := data["bundles"].([]any); len(bs) != 3 {
		t.Errorf("bundles = %v", data["bundles"])
	}
	if sk, _ := data["skipped"].([]any); len(sk) != 1 || sk[0] != "base" {
		t.Errorf("skipped = %v", data["skipped"])
	}
}

func TestCompileCheckDetectsStale(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
		want   string
	}{
		{"modified manifest", func(t *testing.T, root string) {
			p := "bundles/profile-sre/.claude-plugin/plugin.json"
			write(t, root, p, strings.Replace(read(t, root, p), "sre-kit", "sre-kit-x", 1))
		}, "modified"},
		{"missing bundle", func(t *testing.T, root string) {
			if err := os.RemoveAll(filepath.Join(root, "bundles", "profile-seo")); err != nil {
				t.Fatal(err)
			}
		}, "missing"},
		{"stale bundle", func(t *testing.T, root string) {
			write(t, root, "bundles/profile-old/.claude-plugin/plugin.json", "{}\n")
		}, "stale"},
		{"extra file", func(t *testing.T, root string) {
			write(t, root, "bundles/profile-sre/hooks.json", "{}\n")
		}, "extra"},
		{"profile changed", func(t *testing.T, root string) {
			p := "profiles/seo.toml"
			write(t, root, p, strings.Replace(read(t, root, p), "include = [", "include = [\"audit-logger@acme\", ", 1))
		}, "modified"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := copyExample(t)
			tt.mutate(t, root)
			before := snapshot(t, root)
			h := newHarness(t, root)
			r := h.run("compile", "--check")
			if r.code != ui.ExitFailure {
				t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
			}
			if !strings.Contains(r.out, tt.want) || !strings.Contains(r.err, "stale") {
				t.Errorf("out:\n%s\nerr:\n%s", r.out, r.err)
			}
			if after := snapshot(t, root); after != before {
				t.Error("--check wrote to the repo")
			}
			j := h.run("--json", "compile", "--check")
			if j.code != 1 {
				t.Errorf("json code %d", j.code)
			}
			if _, data := decode(t, j.out); data["changed"] != true {
				t.Errorf("changed = %v", data["changed"])
			}
		})
	}
}

func TestCompileWritesAndIsIdempotent(t *testing.T) {
	root := copyExample(t)
	write(t, root, "bundles/profile-old/.claude-plugin/plugin.json", "{}\n")
	if err := os.Remove(filepath.Join(root, "bundles", "profile-sre", ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, root)
	r := h.run("compile")
	if r.code != 0 {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if !strings.Contains(r.out, "wrote 3 bundles") {
		t.Errorf("out: %s", r.out)
	}
	if _, err := os.Stat(filepath.Join(root, "bundles", "profile-old")); !os.IsNotExist(err) {
		t.Error("stale bundle was not pruned")
	}
	if c := h.run("compile", "--check"); c.code != 0 {
		t.Errorf("check after compile: %s\n%s", c.out, c.err)
	}
	again := h.run("compile")
	if again.code != 0 || !strings.Contains(again.out, "nothing written") {
		t.Errorf("second run: %s", again.out)
	}
	j := h.run("--json", "compile")
	_, data := decode(t, j.out)
	if w, _ := data["written"].([]any); len(w) != 0 {
		t.Errorf("idempotent run reported written files: %v", w)
	}
}

func TestCompileErrors(t *testing.T) {
	t.Run("invalid profile", func(t *testing.T) {
		root := copyExample(t)
		write(t, root, "profiles/sre.toml", "name = \"sre\"\n[hooks]\nx = 1\n")
		h := newHarness(t, root)
		r := h.run("compile")
		if r.code != 1 || !strings.Contains(r.err, "profile sre") || !strings.Contains(r.err, "cannot compile") {
			t.Errorf("%d %s", r.code, r.err)
		}
	})
	t.Run("no marketplace file", func(t *testing.T) {
		root := copyExample(t)
		if err := os.Remove(filepath.Join(root, ".claude-plugin", "marketplace.json")); err != nil {
			t.Fatal(err)
		}
		h := newHarness(t, root)
		r := h.run("compile", "--check")
		if r.code != 1 || !strings.Contains(r.err, "marketplace") {
			t.Errorf("%d %s", r.code, r.err)
		}
	})
}

func TestCompileCrossMarketplaceNote(t *testing.T) {
	root := copyExample(t)
	write(t, root, "profiles/seo.toml", strings.Replace(read(t, root, "profiles/seo.toml"), "include = [", "include = [\"linter@other\", ", 1))
	h := newHarness(t, root)
	r := h.run("compile")
	if r.code != 0 || !strings.Contains(r.out, "allowCrossMarketplaceDependenciesOn") {
		t.Errorf("%d\n%s\n%s", r.code, r.out, r.err)
	}
}

// snapshot is a stable text of every file below root.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b.WriteString(filepath.ToSlash(rel))
		if !d.IsDir() {
			c, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			b.WriteString("\x00" + string(c))
		}
		b.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

package orgcmd

import (
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/ui"
)

// project makes a fictional infrastructure project directory.
func project(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	write(t, d, "main.tf", "terraform {}\n")
	write(t, d, "kustomization.yaml", "resources: []\n")
	return d
}

func TestRecommend(t *testing.T) {
	h := newHarness(t, copyExample(t))
	dir := project(t)
	r := h.run("recommend", "--dir", dir)
	if r.code != 0 {
		t.Fatalf("%d %s", r.code, r.err)
	}
	if !strings.Contains(r.out, "sre-kit@acme") || !strings.Contains(r.out, "KIND") {
		t.Errorf("out:\n%s", r.out)
	}

	j := h.run("--json", "recommend", "--dir", dir)
	kind, data := decode(t, j.out)
	recs, _ := data["recommendations"].([]any)
	if kind != "recommend" || len(recs) == 0 {
		t.Fatalf("json: %v %v", kind, data)
	}
	first := recs[0].(map[string]any)
	if first["kind"] == "" || first["name"] == "" || first["score"].(float64) <= 0 {
		t.Errorf("first = %v", first)
	}
	again := h.run("--json", "recommend", "--dir", dir)
	if again.out != j.out {
		t.Error("recommend is not deterministic")
	}
	one := h.run("--json", "recommend", "--dir", dir, "--limit", "1")
	if _, d := decode(t, one.out); len(d["recommendations"].([]any)) != 1 {
		t.Errorf("limit: %v", d)
	}
}

func TestRecommendEmptyAndErrors(t *testing.T) {
	h := newHarness(t, copyExample(t))
	empty := t.TempDir()
	if r := h.run("recommend", "--dir", empty); r.code != 0 || !strings.Contains(r.out, "no suggestions") {
		t.Errorf("empty dir: %d %s", r.code, r.out)
	}
	if _, d := decode(t, h.run("--json", "recommend", "--dir", empty).out); d["recommendations"] == nil {
		t.Error("recommendations must be [] not null")
	}
	if r := h.run("recommend", "--dir", empty+"-missing"); r.code != 1 || !strings.Contains(r.err, "project directory") {
		t.Errorf("missing dir: %d %s", r.code, r.err)
	}
	if r := h.run("recommend", "--limit", "-2"); r.code != ui.ExitUsage {
		t.Errorf("negative limit: %d", r.code)
	}
	// Default directory is the working directory.
	h.cwd = project(t)
	if r := h.run("recommend"); r.code != 0 || !strings.Contains(r.out, "sre-kit@acme") {
		t.Errorf("cwd default: %d %s", r.code, r.out)
	}
}

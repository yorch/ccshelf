package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func orgRun(s *sandbox, org string, args ...string) result {
	s.t.Helper()
	return s.runIn(org, "", args...)
}

func TestOrgRepoHappyPath(t *testing.T) {
	s := newSandbox(t)
	org := exampleOrg(t)

	r := orgRun(s, org, "lint")
	if r.Code != 0 {
		t.Fatalf("lint: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	contains(t, "lint", r.Stdout, "0 error(s)")
	r = orgRun(s, org, "lint", "--format", "json")
	if !json.Valid([]byte(r.Stdout)) {
		t.Errorf("lint --format json is not JSON:\n%s", r.Stdout)
	}
	r = orgRun(s, org, "lint", "--format", "github")
	if r.Code != 0 || !strings.Contains(r.Stdout, "::") {
		t.Errorf("lint --format github: exit %d\n%s", r.Code, r.Stdout)
	}
	// The same through --root from an unrelated directory.
	if r := s.run("--root", org, "lint"); r.Code != 0 {
		t.Errorf("lint --root: exit %d\n%s", r.Code, r.Stderr)
	}

	if r = orgRun(s, org, "compile", "--check"); r.Code != 0 {
		t.Errorf("compile --check on the starter: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}

	out := filepath.Join(t.TempDir(), "site")
	if r = orgRun(s, org, "catalog", "build", "--out", out); r.Code != 0 {
		t.Fatalf("catalog build: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	for _, f := range []string{"catalog.json", "CATALOG.md", "index.html"} {
		if fi, err := os.Stat(filepath.Join(out, f)); err != nil || fi.Size() == 0 {
			t.Errorf("catalog build wrote no %s: %v", f, err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(out, "catalog.json"))
	if !json.Valid(b) || !strings.Contains(string(b), "sre-kit") {
		t.Errorf("catalog.json is not valid or lacks sre-kit")
	}
	// The generated page is self-contained: no external requests.
	html, _ := os.ReadFile(filepath.Join(out, "index.html"))
	for _, bad := range []string{"http://", "https://cdn", "//cdn."} {
		if strings.Contains(string(html), bad) {
			t.Errorf("index.html references %q", bad)
		}
	}
	// A second build is byte-identical (deterministic output).
	out2 := filepath.Join(t.TempDir(), "site")
	orgRun(s, org, "catalog", "build", "--out", out2)
	b2, _ := os.ReadFile(filepath.Join(out2, "catalog.json"))
	if string(b) != string(b2) {
		t.Error("catalog.json differs between two builds of the same input")
	}

	r = orgRun(s, org, "search", "sre")
	contains(t, "search", r.Stdout, "sre-kit@acme")
	r = orgRun(s, org, "search", "--json", "sre")
	if !json.Valid([]byte(r.Stdout)) {
		t.Errorf("search --json: %s", r.Stdout)
	}
	if r = orgRun(s, org, "search", "zzz-no-such-thing"); r.Code != 0 {
		t.Errorf("an empty search is not an error: exit %d", r.Code)
	}

	r = orgRun(s, org, "doctor")
	if r.Code != 0 {
		t.Errorf("doctor: exit %d\n%s", r.Code, r.Stdout)
	}
	contains(t, "doctor", r.Stdout, "doctor:", "skipped")
	if r = orgRun(s, org, "doctor", "--json"); !json.Valid([]byte(r.Stdout)) {
		t.Errorf("doctor --json: %s", r.Stdout)
	}
	if r = orgRun(s, org, "doctor", "--installed"); r.Code != 0 {
		t.Errorf("doctor --installed: exit %d\n%s", r.Code, r.Stdout)
	}

	proj := t.TempDir()
	write(t, filepath.Join(proj, "package.json"), `{"name":"web","dependencies":{"react":"18"}}`)
	r = orgRun(s, org, "recommend", "--dir", proj)
	if r.Code != 0 {
		t.Errorf("recommend: exit %d\n%s", r.Code, r.Stderr)
	}
	if r = orgRun(s, org, "recommend", "--dir", proj, "--json"); !json.Valid([]byte(r.Stdout)) {
		t.Errorf("recommend --json: %s", r.Stdout)
	}
	if n := len(s.anyStart()); n != 0 {
		t.Errorf("org commands started claude %d times", n)
	}
}

func TestOrgRepoMutations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, org string)
		cmd    []string
		want   string
	}{
		{
			"stale bundle",
			func(t *testing.T, org string) {
				write(t, filepath.Join(org, "bundles", "profile-sre", ".claude-plugin", "plugin.json"), "{}\n")
			},
			[]string{"compile", "--check"},
			"stale",
		},
		{
			"unknown sidecar key",
			func(t *testing.T, org string) {
				p := filepath.Join(org, "catalog", "plugins", "sre-kit.toml")
				b, _ := os.ReadFile(p)
				write(t, p, string(b)+"bogus = 1\n")
			},
			[]string{"lint"},
			"sre-kit",
		},
		{
			"sidecar is not toml",
			func(t *testing.T, org string) {
				write(t, filepath.Join(org, "catalog", "plugins", "sre-kit.toml"), "owner = = \n")
			},
			[]string{"lint"},
			"sre-kit",
		},
		{
			"missing owner",
			func(t *testing.T, org string) {
				p := filepath.Join(org, "catalog", "plugins", "design-kit.toml")
				b, _ := os.ReadFile(p)
				var keep []string
				for _, l := range strings.Split(string(b), "\n") {
					if !strings.HasPrefix(l, "owner") {
						keep = append(keep, l)
					}
				}
				write(t, p, strings.Join(keep, "\n"))
			},
			[]string{"lint"},
			"owner",
		},
		{
			"profile extends a missing parent",
			func(t *testing.T, org string) {
				p := filepath.Join(org, "profiles", "seo.toml")
				b, _ := os.ReadFile(p)
				write(t, p, strings.Replace(string(b), `extends = ["base"]`, `extends = ["ghost"]`, 1))
			},
			[]string{"lint"},
			"ghost",
		},
		{
			"broken org config",
			func(t *testing.T, org string) {
				write(t, filepath.Join(org, "ccshelf.toml"), "[lint]\nbogus = true\n")
			},
			[]string{"lint"},
			"ccshelf.toml",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSandbox(t)
			org := exampleOrg(t)
			tc.mutate(t, org)
			r := orgRun(s, org, tc.cmd...)
			if r.Code != 1 {
				t.Fatalf("exit %d, want 1\n%s%s", r.Code, r.Stdout, r.Stderr)
			}
			contains(t, "output", r.Stdout+r.Stderr, tc.want)
		})
	}
}

func TestOrgRepoStrictAndCompileRepairs(t *testing.T) {
	s := newSandbox(t)
	org := exampleOrg(t)
	// The starter launches figma on Windows through the documented cmd /c npx
	// pattern with a pinned package: info only, so --strict passes.
	r := orgRun(s, org, "lint", "--strict")
	if r.Code != 0 {
		t.Errorf("lint --strict: exit %d, want 0 (the documented launcher is info)\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	contains(t, "lint output", r.Stdout, "PRF002")
	// An unpinned package is still a warning, and --strict fails on it.
	reg := filepath.Join(org, "mcp", "registry.toml")
	b, err := os.ReadFile(reg)
	if err != nil {
		t.Fatal(err)
	}
	write(t, reg, strings.ReplaceAll(string(b), "figma-mcp@1.0.0\"]\n\n# Read-only", "figma-mcp\"]\n\n# Read-only"))
	r = orgRun(s, org, "lint", "--strict")
	if r.Code != 1 {
		t.Errorf("lint --strict with an unpinned cmd /c launcher: exit %d, want 1\n%s", r.Code, r.Stdout)
	}
	write(t, reg, string(b))
	// Staleness is repaired by compile, after which --check passes.
	write(t, filepath.Join(org, "bundles", "profile-sre", ".claude-plugin", "plugin.json"), "{}\n")
	if r = orgRun(s, org, "compile", "--check"); r.Code != 1 {
		t.Fatalf("compile --check: exit %d, want 1", r.Code)
	}
	// --check wrote nothing.
	b, _ = os.ReadFile(filepath.Join(org, "bundles", "profile-sre", ".claude-plugin", "plugin.json"))
	if string(b) != "{}\n" {
		t.Error("compile --check modified a file")
	}
	if r = orgRun(s, org, "compile"); r.Code != 0 {
		t.Fatalf("compile: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if r = orgRun(s, org, "compile", "--check"); r.Code != 0 {
		t.Errorf("compile --check after compile: exit %d\n%s", r.Code, r.Stdout)
	}
}

func TestOrgCommandsOutsideARepo(t *testing.T) {
	s := newSandbox(t)
	for _, cmd := range [][]string{{"lint"}, {"compile", "--check"}, {"catalog", "build", "--out", filepath.Join(t.TempDir(), "x")}} {
		r := s.run(cmd...)
		if r.Code == 0 {
			t.Errorf("ccshelf %v in an empty directory: exit 0, want a failure\n%s", cmd, r.Stdout)
		}
		if r.Code != 1 && r.Code != 2 {
			t.Errorf("ccshelf %v: exit %d, want 1 or 2", cmd, r.Code)
		}
	}
}

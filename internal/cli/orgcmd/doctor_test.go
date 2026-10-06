package orgcmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/policy"
	"github.com/ccshelf/ccshelf/internal/testutil"
	"github.com/ccshelf/ccshelf/internal/ui"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.Cleanup()
	os.Exit(code)
}

// fakePolicy replaces policy detection for one test.
func fakePolicy(t *testing.T, p *policy.Policy, err error) {
	t.Helper()
	old := detectPolicy
	detectPolicy = func(context.Context, policy.Options) (*policy.Policy, error) { return p, err }
	t.Cleanup(func() { detectPolicy = old })
}

func TestDoctorExample(t *testing.T) {
	h := newHarness(t, copyExample(t))
	r := h.run("doctor")
	if r.code != 0 {
		t.Fatalf("%d\n%s\n%s", r.code, r.out, r.err)
	}
	golden(t, "doctor-example.golden.txt", r.out)

	kind, data := decode(t, h.run("--json", "doctor").out)
	if kind != "doctor" {
		t.Fatalf("kind %q", kind)
	}
	for _, k := range []string{"summary", "findings", "skipped"} {
		if data[k] == nil {
			t.Errorf("json lacks %s", k)
		}
	}
	if _, ok := data["capabilities"]; ok {
		t.Error("capabilities only with --policy")
	}
	if h.run("doctor", "x").code != ui.ExitUsage {
		t.Error("positional argument should be a usage error")
	}
}

func TestDoctorFindsProblems(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
		want   string
		code   int
	}{
		{"deprecated in use", func(t *testing.T, root string) {
			write(t, root, "profiles/seo.toml", strings.Replace(read(t, root, "profiles/seo.toml"), "include = [", "include = [\"release-notes@acme\", ", 1))
		}, "DOC003", 0},
		{"invalid profile", func(t *testing.T, root string) {
			write(t, root, "profiles/sre.toml", "name = \"sre\"\n[hooks]\nx = 1\n")
		}, "PRF001", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := copyExample(t)
			tt.mutate(t, root)
			h := newHarness(t, root)
			r := h.run("doctor")
			if r.code != tt.code || !strings.Contains(r.out, tt.want) {
				t.Errorf("code %d (want %d)\n%s\n%s", r.code, tt.code, r.out, r.err)
			}
		})
	}
}

func TestDoctorPolicy(t *testing.T) {
	t.Run("no policy", func(t *testing.T) {
		fakePolicy(t, &policy.Policy{ManagedSourcesBehavior: "first-wins"}, nil)
		h := newHarness(t, copyExample(t))
		r := h.run("doctor", "--policy")
		if r.code != 0 || !strings.Contains(r.out, "capability matrix") || !strings.Contains(r.out, "forced-plugins") {
			t.Errorf("%d\n%s\n%s", r.code, r.out, r.err)
		}
		_, data := decode(t, h.run("--json", "doctor", "--policy").out)
		caps, _ := data["capabilities"].([]any)
		if len(caps) < 5 {
			t.Errorf("capabilities = %v", data["capabilities"])
		}
	})
	t.Run("blocked plugin in a profile exits 3", func(t *testing.T) {
		fakePolicy(t, &policy.Policy{EnabledPlugins: map[string]bool{"design-kit@acme": false}}, nil)
		h := newHarness(t, copyExample(t))
		r := h.run("doctor", "--policy")
		if r.code != ui.ExitPolicy || !strings.Contains(r.out, "POL002") || !strings.Contains(r.out, "design-kit@acme") {
			t.Errorf("%d\n%s\n%s", r.code, r.out, r.err)
		}
		if j := h.run("--json", "doctor", "--policy"); j.code != 3 {
			t.Errorf("json code %d", j.code)
		}
	})
	t.Run("unreadable policy exits 3 only with --strict", func(t *testing.T) {
		fakePolicy(t, &policy.Policy{Unreadable: true, Unknown: []string{"managed-settings.json: permission denied"}}, nil)
		h := newHarness(t, copyExample(t))
		if r := h.run("doctor", "--policy"); r.code != 0 || !strings.Contains(r.out, "POL001") {
			t.Errorf("%d\n%s", r.code, r.out)
		}
		if r := h.run("doctor", "--policy", "--strict"); r.code != 3 || !strings.Contains(r.out, "POL001") {
			t.Errorf("%d\n%s", r.code, r.out)
		}
	})
	t.Run("forced plugin", func(t *testing.T) {
		fakePolicy(t, &policy.Policy{EnabledPlugins: map[string]bool{"audit-logger@acme": true}}, nil)
		root := copyExample(t)
		write(t, root, "profiles/seo.toml", strings.Replace(read(t, root, "profiles/seo.toml"), "exclude = []", `exclude = ["audit-logger@acme"]`, 1))
		h := newHarness(t, root)
		r := h.run("doctor", "--policy")
		if r.code != 0 || !strings.Contains(r.out, "POL003") || !strings.Contains(r.out, "POL004") {
			t.Errorf("%d\n%s", r.code, r.out)
		}
		if !strings.Contains(r.out, "blocked") {
			t.Errorf("forced-plugins feature should be blocked in the matrix:\n%s", r.out)
		}
	})
	t.Run("detection fails", func(t *testing.T) {
		fakePolicy(t, nil, errors.New("boom"))
		h := newHarness(t, copyExample(t))
		if r := h.run("doctor", "--policy"); r.code != 1 || !strings.Contains(r.err, "boom") {
			t.Errorf("%d %s", r.code, r.err)
		}
	})
	t.Run("hostile policy text", func(t *testing.T) {
		fakePolicy(t, &policy.Policy{Unreadable: true, Unknown: []string{"x\x1b[31mred"}, Warnings: []string{"w\x1b[2J"}}, nil)
		h := newHarness(t, copyExample(t))
		if r := h.run("doctor", "--policy"); strings.Contains(r.out, "\x1b") {
			t.Errorf("escape in output: %q", r.out)
		}
	})
}

func TestDoctorInstalledWithFakeClaude(t *testing.T) {
	bin := testutil.BuildFakeClaude(t)
	h := newHarness(t, copyExample(t))
	h.g.ClaudePath = bin
	plugins := testutil.PluginsFile(t, "sre-kit@acme", "stale-thing@acme")
	h.env.Environ = func() []string { return []string{"FAKE_CLAUDE_PLUGINS=" + plugins, "PATH=" + os.Getenv("PATH")} }
	r := h.run("doctor", "--installed")
	if r.code != 0 {
		t.Fatalf("%d\n%s\n%s", r.code, r.out, r.err)
	}
	if !strings.Contains(r.out, "DOC008") || strings.Contains(r.out, "skipped DOC008") {
		t.Errorf("not-installed check should run:\n%s", r.out)
	}

	h.g.ClaudePath = filepath.Join(t.TempDir(), "missing-claude")
	if r := h.run("doctor", "--installed"); r.code != 1 || !strings.Contains(r.err, "--installed") {
		t.Errorf("missing claude: %d %s", r.code, r.err)
	}
}

func TestDoctorUsage(t *testing.T) {
	root := copyExample(t)
	h := newHarness(t, root)
	usage := filepath.Join(t.TempDir(), "usage.jsonl")
	write(t, filepath.Dir(usage), "usage.jsonl", `{"event.name":"claude_code.skill_activated","plugin.name":"docs-writer","plugin.marketplace":"acme","timestamp":"2026-09-01T10:00:00Z"}`+"\n")
	r := h.run("doctor", "--usage-file", usage)
	if r.code != 0 || strings.Contains(r.out, "no usage data") {
		t.Errorf("%d\n%s\n%s", r.code, r.out, r.err)
	}
	if r := h.run("doctor", "--usage-file", usage+"-missing"); r.code != 1 || !strings.Contains(r.err, "--usage-file") {
		t.Errorf("missing file: %d %s", r.code, r.err)
	}
	if r := h.run("doctor", "--usage-file", usage, "--usage-api"); r.code != ui.ExitUsage {
		t.Errorf("both sources: %d", r.code)
	}
	if r := h.run("doctor", "--usage-days", "0"); r.code != ui.ExitUsage {
		t.Errorf("days: %d", r.code)
	}
	// No admin key: a usage error naming the way out, and no network call.
	if r := h.run("doctor", "--usage-api"); r.code != ui.ExitUsage || !strings.Contains(r.err, "CCSHELF_ANALYTICS_KEY") {
		t.Errorf("no key: %d %s", r.code, r.err)
	}
}

package orgcmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/policy"
)

// provider returns a CatalogProvider that serves dir, counting its calls.
func provider(dir string, err error, calls *int) clicore.CatalogProvider {
	return func(context.Context, *clicore.Context) (*clicore.CatalogData, error) {
		*calls++
		if err != nil {
			return nil, err
		}
		return &clicore.CatalogData{Root: dir, Source: "git https://example.com/acme/data.git at 1a2b3c4d5e6f"}, nil
	}
}

func TestSearchAndRecommendUseTheConfiguredSourceOutsideAnOrgRepo(t *testing.T) {
	org := copyExample(t)
	h := newHarness(t, "") // no --root; the working directory is an empty directory
	calls := 0
	h.catalog = provider(org, nil, &calls)

	r := h.run("search", "seo")
	if r.code != 0 || !strings.Contains(r.out, "seo-tools") {
		t.Fatalf("search: %d\n%s\n%s", r.code, r.out, r.err)
	}
	if !strings.Contains(r.err, "reading the cached catalog of git https://example.com/acme/data.git at 1a2b3c4d5e6f") {
		t.Errorf("no note about the source:\n%s", r.err)
	}
	_, data := decode(t, h.run("--json", "search", "seo").out)
	if data["source"] == "" || data["source"] == nil {
		t.Errorf("JSON carries no source: %v", data)
	}
	if j := h.run("--json", "search", "seo"); strings.Contains(j.err, "note:") {
		t.Errorf("a note in JSON mode: %s", j.err)
	}

	dir := project(t)
	rr := h.run("recommend", "--dir", dir)
	if rr.code != 0 || !strings.Contains(rr.out, "sre-kit@acme") {
		t.Fatalf("recommend: %d\n%s\n%s", rr.code, rr.out, rr.err)
	}
	if calls == 0 {
		t.Error("the provider was never asked")
	}
}

func TestCatalogCommandsPreferTheWorkingDirectoryAndExplicitRoot(t *testing.T) {
	org := copyExample(t)
	calls := 0
	// The working directory is an org repo: the provider is not consulted.
	h := newHarness(t, "")
	h.cwd = org
	h.catalog = provider(t.TempDir(), nil, &calls)
	if r := h.run("search", "seo"); r.code != 0 || calls != 0 || strings.Contains(r.err, "note:") {
		t.Errorf("cwd repo: code %d calls %d\n%s", r.code, calls, r.err)
	}
	// An explicit --root that is not an org repo fails; no fallback.
	h = newHarness(t, t.TempDir())
	h.catalog = provider(org, nil, &calls)
	if r := h.run("search", "seo"); r.code != 1 || calls != 0 {
		t.Errorf("explicit root: code %d calls %d\n%s", r.code, calls, r.err)
	}
}

func TestCatalogCommandsWithNothingAvailableSayWhatToDo(t *testing.T) {
	for name, prov := range map[string]clicore.CatalogProvider{
		"no provider": nil,
		"no catalog":  provider("", clicore.ErrNoCatalog, new(int)),
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, "")
			h.catalog = prov
			for _, args := range [][]string{{"search", "seo"}, {"recommend", "--dir", t.TempDir()}} {
				r := h.run(args...)
				if r.code != 1 || !strings.Contains(r.err, "not an org data repo") || !strings.Contains(r.err, "--root") {
					t.Errorf("%v: code %d\n%s", args, r.code, r.err)
				}
				if prov != nil && !strings.Contains(r.err, "git source") {
					t.Errorf("%v: no hint about the git source:\n%s", args, r.err)
				}
			}
		})
	}
	// A provider that fails for another reason is reported, not hidden.
	h := newHarness(t, "")
	h.catalog = provider("", errors.New("configuration is broken"), new(int))
	if r := h.run("search", "seo"); r.code != 1 || !strings.Contains(r.err, "configuration is broken") {
		t.Errorf("provider error: %d\n%s", r.code, r.err)
	}
}

func TestDoctorPolicyRunsOutsideAnOrgRepo(t *testing.T) {
	fakePolicy(t, &policy.Policy{ManagedSourcesBehavior: "first-wins"}, nil)
	h := newHarness(t, "")
	r := h.run("doctor", "--policy")
	if r.code != 0 || !strings.Contains(r.out, "capability matrix") {
		t.Fatalf("code %d\n%s\n%s", r.code, r.out, r.err)
	}
	if !strings.Contains(r.err, "managed policy only") {
		t.Errorf("no note:\n%s", r.err)
	}
	j := h.run("--json", "doctor", "--policy")
	_, data := decode(t, j.out)
	if j.code != 0 || data["capabilities"] == nil {
		t.Fatalf("json: %d %v\n%s", j.code, data, j.err)
	}
	// An unreadable policy is still reported, and --strict still exits 3.
	fakePolicy(t, &policy.Policy{Unreadable: true, Unknown: []string{"managed-settings.json: permission denied"}}, nil)
	if r := h.run("doctor", "--policy", "--strict"); r.code != 3 || !strings.Contains(r.out, "POL001") {
		t.Errorf("strict: %d\n%s", r.code, r.out)
	}
	// Without --policy, and with an explicit --root, it is still an error.
	if r := h.run("doctor"); r.code != 1 || !strings.Contains(r.err, "--policy works anywhere") {
		t.Errorf("no --policy: %d\n%s", r.code, r.err)
	}
	h2 := newHarness(t, t.TempDir())
	if r := h2.run("doctor", "--policy"); r.code != 1 {
		t.Errorf("explicit --root: %d\n%s", r.code, r.err)
	}
}

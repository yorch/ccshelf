package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/testutil"
)

const deprecatedSidecar = "owner = \"@acme/seo\"\nstatus = \"deprecated\"\nsuperseded_by = \"docs-writer\"\n"

// deprecationHarness is the example org as a trusted dir source, whose seo
// profile includes seo-tools@acme and docs-writer@acme.
func deprecationHarness(t *testing.T, sidecar string) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	org := h.exampleOrg()
	switch sidecar {
	case "":
		if err := os.RemoveAll(filepath.Join(org, "catalog")); err != nil {
			t.Fatal(err)
		}
	default:
		testutil.WriteFile(t, filepath.Join(org, "catalog", "plugins", "seo-tools.toml"), sidecar)
	}
	h.useOrg(org)
	trustSeo(t, h)
	h.out.Reset()
	h.errb.Reset()
	return h, org
}

func TestRunWarnsOnceAboutDeprecatedPlugins(t *testing.T) {
	h, _ := deprecationHarness(t, deprecatedSidecar)
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("a deprecated plugin must not block: code %d\n%s", code, h.errb)
	}
	want := "plugin seo-tools@acme is deprecated; use docs-writer (profile seo includes it)"
	if got := strings.Count(h.errb.String(), want); got != 1 {
		t.Errorf("warning shown %d times, want once:\n%s", got, h.errb)
	}
	if strings.Contains(h.errb.String(), "docs-writer@acme is deprecated") {
		t.Errorf("an active plugin was reported:\n%s", h.errb)
	}
}

func TestDryRunAndShowJSONCarryTheDeprecationWarning(t *testing.T) {
	h, _ := deprecationHarness(t, deprecatedSidecar)
	for _, cmd := range []string{"dry-run", "show"} {
		h.out.Reset()
		if code := h.run("--json", cmd, "seo"); code != 0 {
			t.Fatalf("%s: code %d\n%s", cmd, code, h.errb)
		}
		var env struct {
			Data struct {
				Warnings []string `json:"warnings"`
			} `json:"data"`
		}
		if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
			t.Fatalf("%s: %v\n%s", cmd, err, h.out)
		}
		found := false
		for _, w := range env.Data.Warnings {
			found = found || strings.Contains(w, "seo-tools@acme is deprecated")
		}
		if !found {
			t.Errorf("%s: no deprecation in the JSON warnings: %v", cmd, env.Data.Warnings)
		}
	}
}

func TestDeprecationWarningDoesNotChangeTheClosureHash(t *testing.T) {
	h, org := deprecationHarness(t, deprecatedSidecar)
	before := orgHash(t, h, "seo")
	testutil.WriteFile(t, filepath.Join(org, "catalog", "plugins", "seo-tools.toml"), "owner = \"@acme/seo\"\nstatus = \"active\"\n")
	if after := orgHash(t, h, "seo"); after != before {
		t.Errorf("the closure hash depends on the catalog: %s != %s", before, after)
	}
}

func TestNoDeprecationWarningWithoutDeprecatedSidecars(t *testing.T) {
	for name, sc := range map[string]string{
		"no sidecars":    "",
		"active sidecar": "owner = \"@acme/seo\"\nstatus = \"active\"\n",
		"broken sidecar": "this is not toml = = =\n",
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := deprecationHarness(t, sc)
			if code := h.run("run", "seo"); code != 0 || h.started != 1 {
				t.Fatalf("code %d\n%s", code, h.errb)
			}
			if strings.Contains(h.errb.String(), "deprecated") {
				t.Errorf("unexpected warning:\n%s", h.errb)
			}
		})
	}
}

// A git source carries the sidecars too: the fake remote serves the example
// org, whose root holds catalog/plugins.
func TestDeprecationWarningFromAGitSource(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	testutil.WriteFile(t, filepath.Join(rem.org, "catalog", "plugins", "seo-tools.toml"), deprecatedSidecar)
	h.useFakeGit(rem)
	trustSeo(t, h)
	h.errb.Reset()
	if code := h.run("run", "seo"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if !strings.Contains(h.errb.String(), "seo-tools@acme is deprecated; use docs-writer") {
		t.Errorf("no warning:\n%s", h.errb)
	}
}

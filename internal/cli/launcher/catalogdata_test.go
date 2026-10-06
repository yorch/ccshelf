package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/profile/gitsource"
	"github.com/ccshelf/ccshelf/internal/testutil"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// catalogOf runs the launcher's catalog provider under the harness's config.
func (h *harness) catalogOf() (*clicore.CatalogData, error) {
	h.t.Helper()
	env := &clicore.Env{
		Streams: ui.Streams{Out: h.out, Err: h.errb},
		Getenv:  os.Getenv, Environ: os.Environ,
		Getwd: func() (string, error) { return h.cwd, nil },
		GOOS:  h.goos,
	}
	cc := env.Context(&h.g, "", "")
	return CatalogProvider(Options{NewGit: h.newGit})(context.Background(), cc)
}

func TestCatalogProviderUsesACachedGitSourceWithoutTheNetwork(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	rem.prepareErr = errors.New("the network must not be used")
	rem.cachedList = []string{fakeSHA}
	h.useFakeGit(rem)
	cd, err := h.catalogOf()
	if err != nil {
		t.Fatal(err)
	}
	if cd.Root != rem.org || !strings.Contains(cd.Source, "git "+fakeURL+" at "+fakeSHA[:12]) {
		t.Errorf("got %+v", cd)
	}
	for _, c := range rem.calls {
		if c == "prepare" {
			t.Errorf("Prepare (network) was called: %v", rem.calls)
		}
	}
}

func TestCatalogProviderSkipsSourcesWithNothingUsable(t *testing.T) {
	// Not cached: nothing available.
	h := newHarness(t)
	rem := newRemote(h)
	rem.cachedErr = gitsource.ErrNotCached
	rem.cachedList = []string{fakeSHA}
	h.useFakeGit(rem)
	if _, err := h.catalogOf(); !errors.Is(err, clicore.ErrNoCatalog) {
		t.Errorf("uncached: err = %v", err)
	}
	// A cached checkout that is not an org data repo (no marketplace file).
	h = newHarness(t)
	rem = newRemote(h)
	rem.cachedList = []string{fakeSHA}
	if err := os.RemoveAll(filepath.Join(rem.org, ".claude-plugin")); err != nil {
		t.Fatal(err)
	}
	h.useFakeGit(rem)
	if _, err := h.catalogOf(); !errors.Is(err, clicore.ErrNoCatalog) {
		t.Errorf("no marketplace: err = %v", err)
	}
	// No sources at all.
	h = newHarness(t)
	h.writeConfig("")
	if _, err := h.catalogOf(); !errors.Is(err, clicore.ErrNoCatalog) {
		t.Errorf("no sources: err = %v", err)
	}
	// A broken configuration is an error, not "nothing available".
	h.writeConfig("bogus = 1\n")
	if _, err := h.catalogOf(); err == nil || errors.Is(err, clicore.ErrNoCatalog) {
		t.Errorf("broken config: err = %v", err)
	}
}

func TestCatalogProviderUsesADirSource(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	h.useOrg(org)
	cd, err := h.catalogOf()
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := filepath.EvalSymlinks(cd.Root); got != mustEval(t, org) || !strings.HasPrefix(cd.Source, "dir ") {
		t.Errorf("got %+v, want root %s", cd, org)
	}
	// A dir source that is not an org data repo is skipped.
	h = newHarness(t)
	empty := t.TempDir()
	testutil.WriteFile(t, filepath.Join(empty, "profiles", "x.toml"), "name = \"x\"\ndescription = \"d\"\n")
	h.useOrg(empty)
	if _, err := h.catalogOf(); !errors.Is(err, clicore.ErrNoCatalog) {
		t.Errorf("err = %v", err)
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

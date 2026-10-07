package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/profile/gitsource"
	"github.com/yorch/ccshelf/internal/testutil"
	"github.com/yorch/ccshelf/internal/ui"
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

// trustedRemote returns a fake remote whose profile seo was accepted, so the
// trust lockfile records fakeSHA for the configured tag.
func trustedRemote(t *testing.T, h *harness) *fakeRemote {
	t.Helper()
	rem := newRemote(h)
	h.useFakeGit(rem)
	trustSeo(t, h)
	rem.calls = nil
	return rem
}

func TestCatalogProviderUsesACachedGitSourceWithoutTheNetwork(t *testing.T) {
	h := newHarness(t)
	rem := trustedRemote(t, h)
	rem.prepareErr = errors.New("the network must not be used")
	other := strings.Repeat("b", 40)
	rem.cachedList = []string{other, fakeSHA} // a newer, never accepted checkout is cached too
	cd, err := h.catalogOf()
	if err != nil {
		t.Fatal(err)
	}
	if cd.Root != rem.org || !strings.Contains(cd.Source, "git "+fakeURL+" at "+fakeSHA[:12]) {
		t.Errorf("got %+v", cd)
	}
	if got := strings.Join(rem.calls, ","); got != "cached:"+fakeSHA {
		t.Errorf("calls = %s, want only the accepted commit from the cache (no network, no other commit)", got)
	}
}

// A cached checkout nobody accepted is never read for the catalog, whatever
// else is in the cache.
func TestCatalogProviderNeverReadsACommitThatWasNotAccepted(t *testing.T) {
	check := func(t *testing.T, h *harness, rem *fakeRemote) {
		t.Helper()
		rem.calls = nil
		_, err := h.catalogOf()
		if !errors.Is(err, clicore.ErrNoCatalog) || !strings.Contains(err.Error(), "ccshelf trust") {
			t.Errorf("err = %v, want ErrNoCatalog with the trust hint", err)
		}
		if len(rem.calls) > 0 {
			t.Errorf("the source was touched: %v", rem.calls)
		}
	}
	t.Run("nothing accepted", func(t *testing.T) {
		h := newHarness(t)
		rem := newRemote(h)
		rem.cachedList = []string{fakeSHA}
		h.useFakeGit(rem)
		check(t, h, rem)
	})
	t.Run("accepted for another tag", func(t *testing.T) {
		h := newHarness(t)
		rem := trustedRemote(t, h)
		rem.cachedList = []string{fakeSHA}
		h.writeConfig(strings.Replace(gitConf, "v1.0.0", "v2.0.0", 1))
		check(t, h, rem)
	})
	t.Run("require_pin off does not widen it", func(t *testing.T) {
		h := newHarness(t)
		rem := newRemote(h)
		rem.cachedList = []string{fakeSHA}
		h.useFakeGit(rem)
		h.writeConfig("[trust]\nrequire_pin = false\n" + gitConf)
		check(t, h, rem)
	})
}

func TestCatalogProviderUsesExactlyAFullSHARef(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	pinned := strings.Repeat("c", 40)
	rem.cachedList = []string{fakeSHA}
	h.useFakeGit(rem)
	h.writeConfig(strings.Replace(gitConf, "v1.0.0", strings.ToUpper(pinned), 1))
	cd, err := h.catalogOf()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cd.Source, pinned[:12]) {
		t.Errorf("source = %q", cd.Source)
	}
	if got := strings.Join(rem.calls, ","); got != "cached:"+pinned {
		t.Errorf("calls = %s, want only the pinned commit", got)
	}
}

func TestCatalogProviderSkipsSourcesWithNothingUsable(t *testing.T) {
	// Accepted but not cached: nothing available.
	h := newHarness(t)
	rem := trustedRemote(t, h)
	rem.cachedErr = gitsource.ErrNotCached
	if _, err := h.catalogOf(); !errors.Is(err, clicore.ErrNoCatalog) {
		t.Errorf("uncached: err = %v", err)
	}
	// A cached checkout that is not an org data repo (no marketplace file).
	h = newHarness(t)
	rem = trustedRemote(t, h)
	if err := os.RemoveAll(filepath.Join(rem.org, ".claude-plugin")); err != nil {
		t.Fatal(err)
	}
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

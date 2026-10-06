package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yorch/ccshelf/internal/orgconfig"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/profile/gitsource"
	"github.com/yorch/ccshelf/internal/ui"
)

const (
	fakeURL = "https://example.com/acme/data.git"
	fakeSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	gitConf = "[[sources]]\ntype = \"git\"\nurl = \"" + fakeURL + "\"\nref = \"v1.0.0\"\n"
)

// fakeRemote is the shared behavior of the fake git sources of one test.
type fakeRemote struct {
	org        string
	prepareErr error    // returned by Prepare; nil means reachable
	cachedErr  error    // returned by PrepareCached; nil means cached
	cachedList []string // returned by CachedCommits (newest first)
	calls      []string
	useCfg     bool // answer OrgConfig from the source instead of the disk
}

// fakeGit is a git source backed by a directory.
type fakeGit struct {
	rem   *fakeRemote
	inner profile.Source
}

func (g *fakeGit) ID() string               { return "git:" + fakeURL + "@" + fakeSHA }
func (g *fakeGit) Kind() profile.Kind       { return profile.KindOrg }
func (g *fakeGit) Root() string             { return g.inner.Root() }
func (g *fakeGit) Commit() string           { return fakeSHA }
func (g *fakeGit) Locator() string          { return "git:" + fakeURL }
func (g *fakeGit) Ref() string              { return "v1.0.0" }
func (g *fakeGit) Names() ([]string, error) { return g.inner.Names() }
func (g *fakeGit) Open(name string) (*profile.File, error) {
	f, err := g.inner.Open(name)
	if err != nil {
		return nil, err
	}
	f.Source = g
	return f, nil
}

func (g *fakeGit) Prepare(context.Context) error {
	g.rem.calls = append(g.rem.calls, "prepare")
	return g.rem.prepareErr
}

func (g *fakeGit) PrepareCached(_ context.Context, commit string) error {
	g.rem.calls = append(g.rem.calls, "cached:"+commit)
	return g.rem.cachedErr
}

func (g *fakeGit) CachedCommits() ([]string, error) { return g.rem.cachedList, nil }

func (g *fakeGit) OrgConfig() (*orgconfig.Config, bool) {
	if !g.rem.useCfg {
		return nil, false
	}
	cfg, found, _ := orgconfig.Find(g.inner.Root())
	return cfg, found
}

func (h *harness) useFakeGit(rem *fakeRemote) {
	h.t.Helper()
	h.newGit = func(gitOpts) (PreparedSource, error) {
		return &fakeGit{rem: rem, inner: dirSourceFor(rem.org)}, nil
	}
	h.writeConfig(gitConf)
}

func newRemote(h *harness) *fakeRemote { return &fakeRemote{org: h.exampleOrg(), useCfg: true} }

// protectedKey reports whether the settings of the last start mention the
// plugin at all (a masked plugin is written as false, a protected one is not
// written).
func (h *harness) pluginWritten(id string) bool {
	h.t.Helper()
	ep, _ := h.settingsOf(h.startArgs)["enabledPlugins"].(map[string]any)
	_, ok := ep[id]
	return ok
}

// trustSeo accepts trust for the profile seo of the fake remote (its closure
// pins the protect list of the example org: audit-logger@acme).
func trustSeo(t *testing.T, h *harness) {
	t.Helper()
	h.mustRun("trust", "seo", "--accept", orgHash(t, h, "seo"))
}

func TestUnreachableUncachedNeverTrustedRunsOthersWithWarning(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	rem.prepareErr = errors.New("git ls-remote failed: could not resolve host")
	rem.cachedErr = gitsource.ErrNotCached
	h.useFakeGit(rem)
	h.writeProfile("mine", personalMine)

	if code := h.run("run", "mine"); code != 0 || h.started != 1 {
		t.Fatalf("personal profile: code %d started %d\n%s", code, h.started, h.errb)
	}
	for _, want := range []string{"unavailable", "could not resolve host", "nothing is known"} {
		if !strings.Contains(h.errb.String(), want) {
			t.Errorf("warning lacks %q:\n%s", want, h.errb)
		}
	}
	// The org profile fails with a clear message, exit 1, and nothing starts.
	h.started = 0
	if code := h.run("run", "seo"); code != ui.ExitFailure || h.started != 0 {
		t.Fatalf("org profile: code %d started %d", code, h.started)
	}
	if !strings.Contains(h.errb.String(), "unavailable") || !strings.Contains(h.errb.String(), "seo") {
		t.Errorf("message: %s", h.errb)
	}
	if code := h.run("ls"); code != 0 || !strings.Contains(h.out.String(), "mine") {
		t.Errorf("ls: code %d\n%s", code, h.out)
	}
}

func TestUnreachableButCachedTrustedRunsFromCacheAndKeepsProtection(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	h.useFakeGit(rem)
	trustSeo(t, h)

	rem.prepareErr = errors.New("network down")
	rem.calls = nil
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if len(rem.calls) != 1 || rem.calls[0] != "cached:"+fakeSHA {
		t.Errorf("calls = %v, want only the cached preparation", rem.calls)
	}
	if strings.Contains(h.errb.String(), "unavailable") {
		t.Errorf("a cached source is not unavailable:\n%s", h.errb)
	}
	// The cached ccshelf.toml still protects: the example org protects audit-logger@acme.
	if h.pluginWritten("audit-logger@acme") {
		t.Error("the protected plugin was written")
	}
}

func TestUnreachableUncachedKeepsLastKnownProtectionForEveryProfile(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	// A plugin that only the lockfile knows is protected.
	if err := os.WriteFile(filepath.Join(rem.org, "ccshelf.toml"), []byte("[protect]\nplugins = [\"seo-tools@acme\"]\nmcp = [\"audit\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.useFakeGit(rem)
	h.writeProfile("mine", personalMine)
	trustSeo(t, h)

	// Without the source, a personal profile masks everything it does not include.
	rem.prepareErr = errors.New("repository not found")
	rem.cachedErr = gitsource.ErrNotCached
	if code := h.run("run", "mine"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if h.pluginWritten("seo-tools@acme") {
		t.Errorf("the last known protected plugin was masked: %v", h.settingsOf(h.startArgs)["enabledPlugins"])
	}
	if !strings.Contains(h.errb.String(), "still enforced") {
		t.Errorf("no warning that the last known lists are enforced:\n%s", h.errb)
	}
	// The closure of other profiles does not change because of the outage.
	h.started = 0
	if code := h.run("run", "seo"); code != ui.ExitFailure || h.started != 0 {
		t.Errorf("org profile: code %d", code)
	}
}

func TestNeverLoadedSourceProtectsNothingButWarns(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	if err := os.WriteFile(filepath.Join(rem.org, "ccshelf.toml"), []byte("[protect]\nplugins = [\"seo-tools@acme\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rem.prepareErr = errors.New("offline")
	rem.cachedErr = gitsource.ErrNotCached
	h.useFakeGit(rem)
	h.writeProfile("mine", personalMine)
	if code := h.run("run", "mine"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	// Nothing is known about the source, so nothing is enforced: the profile
	// masks the installed plugin the org would have protected.
	if !h.pluginWritten("seo-tools@acme") {
		t.Errorf("masking was expected without any known protect list: %v", h.settingsOf(h.startArgs)["enabledPlugins"])
	}
	for _, want := range []string{"LOUD WARNING", "nothing is known", "offline"} {
		if !strings.Contains(h.errb.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, h.errb)
		}
	}
	// The same warning is in the structured warnings of dry-run --json.
	h.mustRun("--json", "dry-run", "mine")
	var env struct {
		Data struct {
			Warnings []string `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range env.Data.Warnings {
		found = found || (strings.Contains(w, "unavailable") && strings.Contains(w, "nothing is known"))
	}
	if !found {
		t.Errorf("no source warning in the JSON warnings: %v", env.Data.Warnings)
	}
}

func TestBrokenOrgConfigOfGitSourceIsFatal(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	rem.prepareErr = fmt.Errorf("%w: ccshelf.toml: unknown key", orgconfig.ErrInvalid)
	h.useFakeGit(rem)
	h.writeProfile("mine", personalMine)
	if code := h.run("run", "mine"); code != ui.ExitFailure || h.started != 0 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if !strings.Contains(h.errb.String(), "org config") {
		t.Errorf("message: %s", h.errb)
	}
}

func TestBrokenOrgConfigOfDirSourceIsFatalForPersonalToo(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	if err := os.WriteFile(filepath.Join(org, "ccshelf.toml"), []byte("[protect]\nbogus = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.useOrg(org)
	h.writeProfile("mine", personalMine)
	if code := h.run("run", "mine"); code != ui.ExitFailure || h.started != 0 {
		t.Fatalf("code %d started %d", code, h.started)
	}
}

func TestCachedCommitIsUsedWithoutNetworkUntilRefresh(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	h.useFakeGit(rem)

	// Nothing pinned yet: the tag is resolved.
	trustSeo(t, h)
	if len(rem.calls) == 0 || rem.calls[0] != "prepare" {
		t.Fatalf("first calls = %v", rem.calls)
	}

	rem.calls = nil
	h.mustRun("run", "seo")
	if len(rem.calls) != 1 || rem.calls[0] != "cached:"+fakeSHA {
		t.Errorf("run: calls = %v", rem.calls)
	}

	rem.calls = nil
	h.mustRun("ls")
	if len(rem.calls) != 1 || !strings.HasPrefix(rem.calls[0], "cached:") {
		t.Errorf("ls: calls = %v", rem.calls)
	}
	rem.calls = nil
	h.mustRun("ls", "--refresh")
	if len(rem.calls) != 1 || rem.calls[0] != "prepare" {
		t.Errorf("ls --refresh: calls = %v", rem.calls)
	}
	rem.calls = nil
	h.mustRun("trust", "seo")
	if len(rem.calls) != 1 || rem.calls[0] != "prepare" {
		t.Errorf("trust: calls = %v", rem.calls)
	}

	// Nothing cached any more: fall back to resolving.
	rem.calls = nil
	rem.cachedErr = gitsource.ErrNotCached
	h.mustRun("run", "seo")
	if len(rem.calls) != 2 || rem.calls[1] != "prepare" {
		t.Errorf("not cached: calls = %v", rem.calls)
	}

	// Refs that may move are always resolved.
	rem.cachedErr = nil
	rem.calls = nil
	h.writeConfig("[trust]\nrequire_pin = false\n" + gitConf)
	h.mustRun("ls")
	if len(rem.calls) != 1 || rem.calls[0] != "prepare" {
		t.Errorf("require_pin = false: calls = %v", rem.calls)
	}
}

func TestGitSourceWithoutOrgConfigWarns(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	rem.useCfg = false
	if err := os.Remove(filepath.Join(rem.org, "ccshelf.toml")); err != nil {
		t.Fatal(err)
	}
	h.useFakeGit(rem)
	h.mustRun("ls")
	if !strings.Contains(h.errb.String(), "has no ccshelf.toml") {
		t.Errorf("no warning:\n%s", h.errb)
	}
	// An empty file says "nothing to protect": no warning.
	if err := os.WriteFile(filepath.Join(rem.org, "ccshelf.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.mustRun("ls")
	if strings.Contains(h.errb.String(), "has no ccshelf.toml") {
		t.Errorf("warned about an empty file:\n%s", h.errb)
	}
	// A directory source without the file is not warned about.
	h2 := newHarness(t)
	org := h2.exampleOrg()
	_ = os.Remove(filepath.Join(org, "ccshelf.toml"))
	h2.useOrg(org)
	h2.mustRun("ls")
	if strings.Contains(h2.errb.String(), "ccshelf.toml") {
		t.Errorf("dir source warned:\n%s", h2.errb)
	}
}

func TestOrgConfigFromTheSourceIsUsedNotTheDisk(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	h.useFakeGit(rem)
	trustSeo(t, h)
	h.mustRun("run", "seo")
	if h.pluginWritten("audit-logger@acme") {
		t.Error("the protect list of the source was ignored")
	}
}

func TestRegistryPathFromOrgConfigForDirSource(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	if err := os.MkdirAll(filepath.Join(org, "cfg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(org, "mcp", "registry.toml"), filepath.Join(org, "cfg", "mcp.toml")); err != nil {
		t.Fatal(err)
	}
	h.useOrg(org)
	// The default location is empty now: the profile cannot resolve.
	if code := h.run("show", "frontend"); code == 0 {
		t.Fatal("frontend resolved without a registry")
	}
	if err := os.WriteFile(filepath.Join(org, "ccshelf.toml"), []byte("[profiles]\nmcp_registry = \"cfg/mcp.toml\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.mustRun("show", "frontend")
	// A registry path that leaves the repository makes the org config invalid.
	if err := os.WriteFile(filepath.Join(org, "ccshelf.toml"), []byte("[profiles]\nmcp_registry = \"../mcp.toml\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := h.run("show", "frontend"); code != ui.ExitFailure {
		t.Errorf("escaping registry path: code %d", code)
	}
}

func TestProfilesDirMismatchIsReported(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	if err := os.WriteFile(filepath.Join(org, "ccshelf.toml"), []byte("[profiles]\ndir = \"teams\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.useOrg(org)
	h.mustRun("ls")
	if !strings.Contains(h.errb.String(), "profiles.dir") {
		t.Errorf("no mismatch warning:\n%s", h.errb)
	}
}

func TestCachePruneIsDailyAndKeepsPinnedCheckouts(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	h.useFakeGit(rem)
	trustSeo(t, h) // pins fakeSHA of fakeURL in the lockfile

	cacheDir := filepath.Join(h.dirs["XDG_CACHE_HOME"], "ccshelf")
	base := filepath.Join(cacheDir, "git")
	old := time.Now().Add(-90 * 24 * time.Hour)
	mk := func(url, sha string) string {
		p := gitsource.CheckoutDir(base, url, sha)
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pinned := mk(fakeURL, fakeSHA)
	stale := mk(fakeURL, strings.Repeat("b", 40))
	// The setup commands above already pruned today; force the next pruning.
	stamp := filepath.Join(cacheDir, pruneStamp)
	if err := os.Remove(stamp); err != nil {
		t.Fatalf("no stamp was written: %v", err)
	}
	h.mustRun("ls")
	if _, err := os.Stat(stale); err == nil {
		t.Error("the stale checkout was not pruned")
	}
	if _, err := os.Stat(pinned); err != nil {
		t.Errorf("the pinned checkout was pruned: %v", err)
	}
	// Within a day nothing is pruned again.
	stale2 := mk(fakeURL, strings.Repeat("c", 40))
	h.mustRun("ls")
	if _, err := os.Stat(stale2); err != nil {
		t.Error("pruned twice in one day")
	}
}

package launcher

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/config"
	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/profile/gitsource"
	"github.com/ccshelf/ccshelf/internal/trust"
)

// A git source that only declares [protect] (no profile of its own, so it is
// in no lockfile entry's sources) is unreachable: the protect lists pinned by
// the other entries are still enforced.
func TestProtectOnlySourceOfflineStaysProtected(t *testing.T) {
	h := newHarness(t)
	a := h.exampleOrg()
	if err := os.WriteFile(filepath.Join(a, "ccshelf.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	rem := newRemote(h)
	if err := os.WriteFile(filepath.Join(rem.org, "ccshelf.toml"), []byte("[protect]\nplugins = [\"seo-tools@acme\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(rem.org, "profiles")); err != nil {
		t.Fatal(err)
	}
	h.useFakeGit(rem)
	h.writeConfig("[[sources]]\ntype = \"dir\"\npath = " + tomlString(filepath.Join(a, "profiles")) + "\n" + gitConf)
	h.writeProfile("mine", personalMine)
	h.mustRun("trust", "frontend", "--accept", orgHash(t, h, "frontend"))
	h.mustRun("run", "mine")
	if h.pluginWritten("seo-tools@acme") {
		t.Fatalf("setup: protected plugin written while online: %v", h.settingsOf(h.startArgs)["enabledPlugins"])
	}
	rem.prepareErr = errors.New("could not resolve host")
	rem.cachedErr = gitsource.ErrNotCached
	h.started = 0
	if code := h.run("run", "mine"); code != 0 || h.started != 1 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if h.pluginWritten("seo-tools@acme") {
		t.Errorf("protected plugin masked while the protect-only source is offline: %v\nstderr: %s",
			h.settingsOf(h.startArgs)["enabledPlugins"], strings.TrimSpace(h.errb.String()))
	}
}

// profiles.dir = "./profiles" is refused by the org config validation (a fatal
// error), so the valid protect list is never silently dropped.
func TestDotSlashProfilesDirIsRejectedByTheOrgConfig(t *testing.T) {
	h := newHarness(t)
	org := h.exampleOrg()
	if err := os.WriteFile(filepath.Join(org, "ccshelf.toml"), []byte("[profiles]\ndir = \"./profiles\"\n[protect]\nplugins = [\"seo-tools@acme\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.useOrg(org)
	h.writeProfile("mine", personalMine)
	if code := h.run("run", "mine"); code == 0 || h.started != 0 {
		t.Fatalf("code %d started %d: a layout the source refuses must be fatal\n%s", code, h.started, h.errb)
	}
	if !strings.Contains(h.errb.String(), "profiles.dir") {
		t.Errorf("message: %s", h.errb)
	}
}

// With nothing pinned for the tag (a personal-only closure has no lockfile
// entry), an unreachable remote falls back to the newest verified cached
// checkout, loudly, and the org protection stays on.
func TestUnreachableFallsBackToNewestVerifiedCachedCheckout(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	if err := os.WriteFile(filepath.Join(rem.org, "ccshelf.toml"), []byte("[protect]\nplugins = [\"seo-tools@acme\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rem.prepareErr = errors.New("could not resolve host")
	rem.cachedList = []string{fakeSHA}
	h.useFakeGit(rem)
	h.writeProfile("mine", personalMine)

	if code := h.run("run", "mine"); code != 0 || h.started != 1 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if h.pluginWritten("seo-tools@acme") {
		t.Errorf("protected plugin masked despite the cached checkout: %v", h.settingsOf(h.startArgs)["enabledPlugins"])
	}
	if !strings.Contains(h.errb.String(), "newest verified cached checkout") {
		t.Errorf("no warning about the cached checkout:\n%s", h.errb)
	}
	if strings.Contains(h.errb.String(), "unavailable") {
		t.Errorf("the source is usable from the cache:\n%s", h.errb)
	}
	h.mustRun("--json", "dry-run", "mine")
	var env struct {
		Data struct {
			Warnings []string `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data.Warnings) == 0 || !strings.Contains(strings.Join(env.Data.Warnings, "\n"), "cached checkout") {
		t.Errorf("JSON warnings: %v", env.Data.Warnings)
	}

	// A review (--refresh) never takes an older checkout silently.
	if code := h.run("ls", "--refresh"); code != 0 {
		t.Logf("ls --refresh: code %d", code)
	}
	if strings.Contains(h.errb.String(), "newest verified cached checkout") {
		t.Errorf("--refresh used the offline fallback:\n%s", h.errb)
	}

	// A cached checkout that fails verification is not used.
	rem.cachedErr = gitsource.ErrNotCached
	h.started = 0
	if code := h.run("run", "mine"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if !strings.Contains(h.errb.String(), "unavailable") || !h.pluginWritten("seo-tools@acme") {
		t.Errorf("an unverifiable cache must leave the source unavailable:\n%s", h.errb)
	}
}

func lockSession(entries ...trust.Entry) *session {
	return &session{cfg: config.Default(), cc: &clicore.Context{Env: &clicore.Env{Now: time.Now}}, lock: entries, lockLoaded: true, gitIDs: map[string]bool{}}
}

func TestLockedCommitPicksTheMostRecentAcceptance(t *testing.T) {
	const url = "https://example.com/acme/data.git"
	older, newer := strings.Repeat("1", 40), strings.Repeat("2", 40)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func(commit string, when time.Time) trust.Entry {
		return trust.Entry{
			Profile: "p" + commit[:1], Source: "git:" + url, Ref: "v1", Commit: commit, AcceptedAt: when,
			Sources: []trust.SourceRecord{{Source: "git:" + url, Ref: "v1", Commit: commit}},
		}
	}
	sc := config.SourceConfig{Type: config.SourceGit, URL: url, Ref: "v1"}
	// The newer acceptance is listed first and last: order must not matter.
	for _, entries := range [][]trust.Entry{
		{mk(newer, at.Add(time.Hour)), mk(older, at)},
		{mk(older, at), mk(newer, at.Add(time.Hour))},
	} {
		if got := lockSession(entries...).lockedCommit(sc); got != newer {
			t.Errorf("lockedCommit = %q, want the most recent %q", got, newer)
		}
	}
	// A record for another ref or URL is never used.
	if got := lockSession(mk(newer, at)).lockedCommit(config.SourceConfig{Type: config.SourceGit, URL: url, Ref: "v2"}); got != "" {
		t.Errorf("other ref: %q", got)
	}
}

// The protect lists of every entry are enforced when a source fails,
// whichever source the entries name.
func TestLastKnownProtectedIsTheUnionOfAllEntries(t *testing.T) {
	items := func(plugin, mcp string) []profile.ClosureItem {
		return []profile.ClosureItem{{Kind: itemProtectPlugin, Name: plugin}, {Kind: itemProtectMCP, Name: mcp}}
	}
	s := lockSession(
		trust.Entry{Profile: "a", Source: "dir:org", Items: items("one@m", "m1")},
		trust.Entry{Profile: "b", Source: "git:https://example.com/x.git", Items: items("two@m", "m2")},
	)
	plugins, mcp := s.lastKnownProtected()
	if strings.Join(uniqSorted(plugins), ",") != "one@m,two@m" || strings.Join(uniqSorted(mcp), ",") != "m1,m2" {
		t.Errorf("plugins %v mcp %v", plugins, mcp)
	}
}

// The plugin that carries the profiles of a plugin source stays protected even
// when the source cannot be prepared.
func TestPluginSourceCarrierStaysProtected(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("[[sources]]\ntype = \"plugin\"\nplugin = \"seo-tools@acme\"\n")
	h.writeProfile("mine", personalMine)
	if code := h.run("run", "mine"); code != 0 || h.started != 1 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if h.pluginWritten("seo-tools@acme") {
		t.Errorf("the carrier plugin was masked: %v", h.settingsOf(h.startArgs)["enabledPlugins"])
	}
}

// What the pruning keeps comes from the sources of an entry as well as from
// its top-level source.
func TestPruneKeepsCheckoutsPinnedOnlyByEntrySources(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
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
	s := lockSession(trust.Entry{Profile: "x", Sources: []trust.SourceRecord{{Source: "git:" + fakeURL, Ref: "v1", Commit: fakeSHA}}})
	s.pruneCache()
	if _, err := os.Stat(pinned); err != nil {
		t.Errorf("a checkout pinned by an entry's sources was pruned: %v", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("the stale checkout was not pruned")
	}
}

// When the lockfile cannot be read nothing is known about what it pins, so the
// git checkouts are not pruned.
func TestPruneSkipsCheckoutsWhenTheLockfileIsUnreadable(t *testing.T) {
	h := newHarness(t)
	cacheDir := filepath.Join(h.dirs["XDG_CACHE_HOME"], "ccshelf")
	base := filepath.Join(cacheDir, "git")
	p := gitsource.CheckoutDir(base, fakeURL, fakeSHA)
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	lock, err := config.LockfilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &session{cfg: config.Default(), cc: &clicore.Context{Env: &clicore.Env{Now: time.Now}}, gitIDs: map[string]bool{}}
	s.pruneCache()
	if s.lockErr == nil {
		t.Fatal("the lockfile was expected to be unreadable")
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("a trusted checkout was pruned although the lockfile is unreadable: %v", err)
	}
}

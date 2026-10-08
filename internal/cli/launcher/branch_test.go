package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/orgconfig"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/profile/gitsource"
	"github.com/yorch/ccshelf/internal/ui"
)

const (
	branchSHA1 = "1111111111111111111111111111111111111111"
	branchSHA2 = "2222222222222222222222222222222222222222"
	branchSHA3 = "3333333333333333333333333333333333333333"
	branchConf = "[[sources]]\ntype = \"git\"\nurl = \"" + fakeURL + "\"\nbranch = \"main\"\n"
)

// branchRemote is a fake remote whose branch head can move.
type branchRemote struct {
	orgs    map[string]string // commit -> org tree
	head    string
	cached  map[string]bool
	headErr error
	// afterResolve runs after ResolveHead has answered (the head can move
	// between the check and the fetch).
	afterResolve func()
	used         []string // commits the sources were prepared at, in order
	atFetched    []string // commits fetched by id (PrepareAt)
	// counters
	resolves int
	prepares []string // commits prepared from the network
	cachedOK []string // commits prepared from the cache
}

type fakeBranchGit struct {
	rem    *branchRemote
	commit string
	inner  profile.Source
}

func (g *fakeBranchGit) ID() string {
	if g.commit == "" {
		return "git:" + fakeURL + "@branch:main"
	}
	return "git:" + fakeURL + "@" + g.commit
}
func (g *fakeBranchGit) Kind() profile.Kind { return profile.KindOrg }
func (g *fakeBranchGit) Root() string {
	if g.inner == nil {
		return ""
	}
	return g.inner.Root()
}
func (g *fakeBranchGit) Commit() string  { return g.commit }
func (g *fakeBranchGit) Locator() string { return "git:" + fakeURL }
func (g *fakeBranchGit) Ref() string     { return "branch:main" }
func (g *fakeBranchGit) Branch() string  { return "main" }
func (g *fakeBranchGit) Names() ([]string, error) {
	if g.inner == nil {
		return nil, gitsource.ErrNotPrepared
	}
	return g.inner.Names()
}
func (g *fakeBranchGit) Open(name string) (*profile.File, error) {
	f, err := g.inner.Open(name)
	if err != nil {
		return nil, err
	}
	f.Source = g
	return f, nil
}
func (g *fakeBranchGit) use(commit string) {
	g.rem.used = append(g.rem.used, commit)
	g.commit = commit
	g.inner = dirSourceFor(g.rem.orgs[commit])
}
func (g *fakeBranchGit) Prepare(context.Context) error {
	if g.rem.headErr != nil {
		return g.rem.headErr
	}
	g.rem.prepares = append(g.rem.prepares, g.rem.head)
	g.rem.cached[g.rem.head] = true
	g.use(g.rem.head)
	return nil
}
func (g *fakeBranchGit) PrepareCached(_ context.Context, commit string) error {
	if !g.rem.cached[commit] {
		return gitsource.ErrNotCached
	}
	g.rem.cachedOK = append(g.rem.cachedOK, commit)
	g.use(commit)
	return nil
}
func (g *fakeBranchGit) PrepareAt(_ context.Context, commit string) error {
	g.rem.atFetched = append(g.rem.atFetched, commit)
	g.rem.cached[commit] = true
	g.use(commit)
	return nil
}
func (g *fakeBranchGit) ResolveHead(context.Context) (string, error) {
	g.rem.resolves++
	head, err := g.rem.head, g.rem.headErr
	if g.rem.afterResolve != nil {
		g.rem.afterResolve()
	}
	return head, err
}
func (g *fakeBranchGit) CachedCommits() ([]string, error) { return nil, nil }
func (g *fakeBranchGit) OrgConfig() (*orgconfig.Config, bool) {
	cfg, found, _ := orgconfig.Find(g.inner.Root())
	return cfg, found
}

// newBranchHarness sets up a branch source whose head is branchSHA1, with a
// second tree (the seo description differs) behind branchSHA2.
func newBranchHarness(t *testing.T, extraConfig string) (*harness, *branchRemote) {
	t.Helper()
	h := newHarness(t)
	rem := &branchRemote{orgs: map[string]string{}, head: branchSHA1, cached: map[string]bool{}}
	rem.orgs[branchSHA1] = h.exampleOrg()
	// Later commits change seo, and the model shows in the generated settings.
	for sha, model := range map[string]string{branchSHA2: "model-two", branchSHA3: "model-three"} {
		rem.orgs[sha] = h.exampleOrg()
		seo := filepath.Join(rem.orgs[sha], "profiles", "seo.toml")
		b, err := os.ReadFile(seo)
		if err != nil {
			t.Fatal(err)
		}
		s := strings.Replace(string(b), "Search optimization of web content and templates", "Search optimization, "+model, 1)
		s = strings.Replace(s, `effort = "medium"`, "effort = \"medium\"\nmodel = \""+model+"\"", 1)
		if err := os.WriteFile(seo, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.newGit = func(o gitOpts) (PreparedSource, error) {
		if o.Branch != "main" || o.Ref != "" {
			t.Errorf("git options = %+v; want branch main and no ref", o)
		}
		return &fakeBranchGit{rem: rem}, nil
	}
	h.writeConfig(extraConfig + branchConf)
	h.now = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	return h, rem
}

func (h *harness) trustBranchSeo(t *testing.T) {
	t.Helper()
	h.mustRun("trust", "seo", "--accept", orgHash(t, h, "seo"))
}

func (r *branchRemote) reset() {
	r.resolves, r.prepares, r.cachedOK, r.used, r.atFetched = 0, nil, nil, nil, nil
}

// ranModel returns the model in the settings of the last start: "" for the
// first tree, "model-two" and "model-three" for the later commits. It shows
// which commit the invocation that just ran used.
func (h *harness) ranModel() string {
	h.t.Helper()
	if h.started == 0 {
		h.t.Fatal("nothing started")
	}
	m, _ := h.settingsOf(h.startArgs)["model"].(string)
	return m
}

func TestBranchRunStaysOnTrustedCommitWithoutNetwork(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.head = branchSHA2 // the branch moved
	rem.reset()
	// Inside the interval nothing contacts the remote and the trusted commit runs.
	h.now = h.now.Add(23 * time.Hour)
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if rem.resolves != 0 || len(rem.prepares) != 0 {
		t.Errorf("network use: resolves %d prepares %v", rem.resolves, rem.prepares)
	}
	if len(rem.cachedOK) != 1 || rem.cachedOK[0] != branchSHA1 {
		t.Errorf("cached commits = %v", rem.cachedOK)
	}
	if strings.Contains(h.errb.String(), "new commit") {
		t.Errorf("unexpected notice:\n%s", h.errb)
	}
}

func TestBranchPeriodicCheckNotifiesWithoutTerminal(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.head = branchSHA2
	rem.reset()
	h.now = h.now.Add(25 * time.Hour)
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if rem.resolves != 1 || len(rem.prepares) != 0 {
		t.Errorf("resolves %d prepares %v; want one ls-remote and no fetch", rem.resolves, rem.prepares)
	}
	for _, want := range []string{"branch main", "new commit (222222222222)", "keeps the trusted commit 111111111111", "ccshelf trust seo"} {
		if !strings.Contains(h.errb.String(), want) {
			t.Errorf("notice lacks %q:\n%s", want, h.errb)
		}
	}
	if got := strings.Count(h.errb.String(), "new commit"); got != 1 {
		t.Errorf("the notice appears %d times:\n%s", got, h.errb)
	}
	// The trusted commit ran.
	if rem.cachedOK[len(rem.cachedOK)-1] != branchSHA1 {
		t.Errorf("cached = %v", rem.cachedOK)
	}
	// The attempt is recorded: an hour later nothing is asked again.
	rem.reset()
	h.now = h.now.Add(time.Hour)
	h.mustRun("run", "seo")
	if rem.resolves != 0 {
		t.Errorf("checked again inside the interval: %d", rem.resolves)
	}
	// And after the interval it is asked again.
	h.now = h.now.Add(24 * time.Hour)
	h.mustRun("run", "seo")
	if rem.resolves != 1 {
		t.Errorf("not checked after the interval: %d", rem.resolves)
	}
}

func TestBranchCheckIntervalSetting(t *testing.T) {
	h, rem := newBranchHarness(t, "[trust]\nbranch_check_interval = \"2h\"\n")
	h.trustBranchSeo(t)
	rem.reset()
	h.now = h.now.Add(90 * time.Minute)
	h.mustRun("run", "seo")
	if rem.resolves != 0 {
		t.Errorf("checked inside a 2h interval: %d", rem.resolves)
	}
	h.now = h.now.Add(31 * time.Minute)
	h.mustRun("run", "seo")
	if rem.resolves != 1 {
		t.Errorf("not checked after a 2h interval: %d", rem.resolves)
	}
}

func TestBranchFailedCheckIsRecordedAndDoesNotBlock(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.headErr = errors.New("could not resolve host")
	rem.reset()
	h.now = h.now.Add(25 * time.Hour)
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if !strings.Contains(h.errb.String(), "cannot check branch main") || !strings.Contains(h.errb.String(), "could not resolve host") {
		t.Errorf("no warning:\n%s", h.errb)
	}
	if n := strings.Count(h.errb.String(), "cannot check branch"); n != 1 {
		t.Errorf("%d warnings", n)
	}
	// The failed attempt counts: the next run does not try again.
	rem.reset()
	h.now = h.now.Add(time.Hour)
	h.mustRun("run", "seo")
	if rem.resolves != 0 || strings.Contains(h.errb.String(), "cannot check") {
		t.Errorf("a failed check was repeated: %d\n%s", rem.resolves, h.errb)
	}
}

func TestBranchRefreshForcesTheCheckAndNeedsTrust(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.head = branchSHA2
	rem.reset()
	// No time has passed, but --refresh looks at the head now. The new commit
	// is an untrusted update: exit 4, and nothing starts.
	if code := h.run("run", "--refresh", "seo"); code != ui.ExitTrust || h.started != 0 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if len(rem.prepares) != 1 || rem.prepares[0] != branchSHA2 {
		t.Errorf("prepares = %v", rem.prepares)
	}
	if !strings.Contains(h.errb.String(), `branch "main" now points to a different commit`) {
		t.Errorf("message:\n%s", h.errb)
	}
	// dry-run takes the flag too.
	if code := h.run("dry-run", "--refresh", "seo"); code != ui.ExitTrust {
		t.Errorf("dry-run --refresh: %d", code)
	}
	// ccshelf trust re-resolves the branch as well, and shows the update.
	if code := h.run("trust", "seo", "--accept", "x"); code == 0 {
		t.Fatal("trust accepted a wrong hash")
	}
	if !strings.Contains(h.out.String(), "the branch \"main\"") {
		t.Errorf("trust output lacks the branch update:\n%s", h.out)
	}
	h.mustRun("trust", "seo", "--accept", closureHashFromOutput(t, h.out.String()))
	h.started = 0
	h.mustRun("run", "seo")
	if h.started != 1 {
		t.Fatal("did not start after trust")
	}
	if rem.cachedOK[len(rem.cachedOK)-1] != branchSHA2 {
		t.Errorf("ran %v; want the new commit", rem.cachedOK)
	}
}

func closureHashFromOutput(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "Closure: ")
	if i < 0 {
		t.Fatalf("no closure hash in:\n%s", out)
	}
	rest := out[i+len("Closure: "):]
	return strings.Fields(rest)[0]
}

func TestBranchRefreshOnChangeFailExitsFour(t *testing.T) {
	h, rem := newBranchHarness(t, "[trust]\non_change = \"fail\"\n")
	h.trustBranchSeo(t)
	rem.head = branchSHA2
	sc := ui.NewScripted(true) // a terminal; only the plugin question may be asked
	h.prompt = sc
	if code := h.run("run", "--refresh", "seo"); code != ui.ExitTrust || h.started != 0 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	// The daily check only notifies, even in a terminal.
	h.now = h.now.Add(25 * time.Hour)
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if !strings.Contains(h.errb.String(), "keeps the trusted commit") {
		t.Errorf("no notice:\n%s", h.errb)
	}
	for _, q := range sc.Asked {
		if strings.Contains(q, "Trust profile") {
			t.Errorf("the daily check asked about trust with on_change = fail: %q", q)
		}
	}
}

func TestBranchTerminalAcceptRunsNewCommit(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.head = branchSHA2
	sc := ui.NewScripted("yes", true)
	h.prompt = sc
	h.now = h.now.Add(25 * time.Hour)
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	for _, want := range []string{"Branch main", "2222222", "1111111", "needs trust", "untrusted update", "Equivalent: ccshelf trust seo --accept"} {
		if !strings.Contains(h.errb.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, h.errb)
		}
	}
	if got := h.ranModel(); got != "model-two" {
		t.Errorf("the invocation that accepted ran model %q; want the new commit's model-two", got)
	}
	// Trust was recorded for the new commit: a later run needs no question.
	h.prompt = nil
	h.started = 0
	h.mustRun("run", "seo")
	if h.started != 1 || rem.cachedOK[len(rem.cachedOK)-1] != branchSHA2 {
		t.Errorf("later run: started %d cached %v", h.started, rem.cachedOK)
	}
}

func TestBranchTerminalDeclineRunsTrustedCommit(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.head = branchSHA2
	sc := ui.NewScripted("no", true)
	h.prompt = sc
	h.now = h.now.Add(25 * time.Hour)
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	if !strings.Contains(h.errb.String(), "keeps the trusted commit 111111111111") {
		t.Errorf("no statement that the trusted commit runs:\n%s", h.errb)
	}
	// The settings of this invocation come from the trusted tree.
	if got := h.ranModel(); got != "" {
		t.Errorf("the invocation that declined ran model %q; want the trusted commit (no model)", got)
	}
}

func TestBranchYesNeverAcceptsTrust(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.head = branchSHA2
	sc := ui.NewScripted() // a terminal, but --yes means no question
	h.prompt = sc
	rem.reset()
	h.now = h.now.Add(25 * time.Hour)
	if code := h.run("run", "--yes", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	if !strings.Contains(h.errb.String(), "ccshelf trust seo") || len(rem.prepares) != 0 {
		t.Errorf("prepares %v\n%s", rem.prepares, h.errb)
	}
	if got := h.ranModel(); got != "" {
		t.Errorf("--yes ran model %q; want the trusted commit", got)
	}
}

func TestBranchDryRunJSONCarriesTheNotice(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.head = branchSHA2
	h.now = h.now.Add(25 * time.Hour)
	h.mustRun("--json", "dry-run", "seo")
	if !strings.Contains(h.out.String(), "keeps the trusted commit") {
		t.Errorf("warnings lack the notice:\n%s", h.out)
	}
}

func TestBranchLabelsInLsShowAndConfig(t *testing.T) {
	h, _ := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	h.mustRun("ls")
	if !strings.Contains(h.out.String(), "org (branch main @ 1111111)") {
		t.Errorf("ls:\n%s", h.out)
	}
	h.mustRun("--json", "ls")
	if !strings.Contains(h.out.String(), `"tracks":"branch main @ 1111111"`) && !strings.Contains(h.out.String(), `"tracks": "branch main @ 1111111"`) {
		t.Errorf("ls --json:\n%s", h.out)
	}
	h.mustRun("--json", "show", "seo")
	if !strings.Contains(h.out.String(), "branch main @ 1111111") {
		t.Errorf("show:\n%s", h.out)
	}
	h.mustRun("config", "show")
	if !strings.Contains(h.out.String(), "tracks: branch main @ 1111111") || !strings.Contains(h.out.String(), "branch_check_interval: 24h (default)") {
		t.Errorf("config show:\n%s", h.out)
	}
	h.mustRun("config", "source", "ls")
	if !strings.Contains(h.out.String(), "branch main @ 1111111") {
		t.Errorf("config source ls:\n%s", h.out)
	}
}

func TestTagSourceHasNoPeriodicCheck(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	h.useFakeGit(rem)
	trustSeo(t, h)
	rem.calls = nil
	h.now = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	h.mustRun("run", "seo")
	if len(rem.calls) != 1 || rem.calls[0] != "cached:"+fakeSHA {
		t.Errorf("a tag source did something besides the cached preparation: %v", rem.calls)
	}
}

func TestBranchSecondProfileKeepsItsOwnCommit(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	h.mustRun("trust", "sre", "--accept", orgHash(t, h, "sre"))
	rem.head = branchSHA2
	// A terminal accepts the update for seo only.
	h.prompt = ui.NewScripted("yes", true)
	h.now = h.now.Add(25 * time.Hour)
	h.mustRun("run", "seo")
	if got := h.ranModel(); got != "model-two" {
		t.Fatalf("seo ran model %q", got)
	}
	// sre was trusted at the first commit: with no terminal, inside the interval,
	// it still runs that commit from the cache and does not exit 4.
	h.prompt = nil
	h.started = 0
	rem.reset()
	h.now = h.now.Add(time.Hour)
	if code := h.run("run", "sre"); code != 0 || h.started != 1 {
		t.Fatalf("sre: code %d started %d\n%s", code, h.started, h.errb)
	}
	if rem.resolves != 0 || len(rem.prepares) != 0 {
		t.Errorf("network use: resolves %d prepares %v", rem.resolves, rem.prepares)
	}
	if rem.used[len(rem.used)-1] != branchSHA1 {
		t.Errorf("sre ran %v; want its trusted %s", rem.used, branchSHA1)
	}
	// After the interval, the check offers the new commit to sre as well.
	h.now = h.now.Add(25 * time.Hour)
	rem.reset()
	h.started = 0
	if code := h.run("run", "sre"); code != 0 || h.started != 1 {
		t.Fatalf("sre later: code %d\n%s", code, h.errb)
	}
	if !strings.Contains(h.errb.String(), "ccshelf trust sre") || rem.used[len(rem.used)-1] != branchSHA1 {
		t.Errorf("no notice for sre, or it ran %v:\n%s", rem.used, h.errb)
	}
	// seo keeps running the commit it accepted.
	h.started = 0
	h.mustRun("run", "seo")
	if got := h.ranModel(); got != "model-two" {
		t.Errorf("seo later ran model %q", got)
	}
}

func TestBranchTrustedCommitMissingFromCacheIsFetchedByID(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.head = branchSHA2
	rem.cached = map[string]bool{} // the cache was cleared
	rem.reset()
	h.now = h.now.Add(time.Hour) // the periodic check is not due
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if rem.resolves != 0 || len(rem.prepares) != 0 {
		t.Errorf("the head was resolved or fetched: resolves %d prepares %v", rem.resolves, rem.prepares)
	}
	if len(rem.atFetched) != 1 || rem.atFetched[0] != branchSHA1 {
		t.Errorf("fetched by id: %v; want the trusted commit", rem.atFetched)
	}
	if got := h.ranModel(); got != "" {
		t.Errorf("ran model %q; want the trusted commit", got)
	}
}

func TestBranchHeadMovesBetweenCheckAndFetch(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.head = branchSHA2
	// ls-remote says 2222..., and the head moves to 3333... before the fetch.
	rem.afterResolve = func() { rem.head = branchSHA3 }
	sc := ui.NewScripted("yes", true)
	h.prompt = sc
	h.now = h.now.Add(25 * time.Hour)
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	// The question names the commit that was diffed (3333...), not the first head.
	if !strings.Contains(h.errb.String(), "new commit: 333333333333") || strings.Contains(h.errb.String(), "new commit: 222222222222") {
		t.Errorf("header:\n%s", h.errb)
	}
	if got := h.ranModel(); got != "model-three" {
		t.Errorf("ran model %q; want what was diffed and accepted (model-three)", got)
	}
	// Without a terminal nothing is accepted, whatever the head does.
	h2, rem2 := newBranchHarness(t, "")
	h2.trustBranchSeo(t)
	rem2.head = branchSHA2
	rem2.afterResolve = func() { rem2.head = branchSHA3 }
	h2.now = h2.now.Add(25 * time.Hour)
	h2.mustRun("run", "seo")
	if got := h2.ranModel(); got != "" {
		t.Errorf("no terminal ran model %q", got)
	}
}

func TestBranchTagToBranchSwitchIsRekeyed(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	lp, err := config.LockfilePath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(lp)
	if err != nil {
		t.Fatal(err)
	}
	// Make the lockfile look as if seo was trusted when the source was a tag.
	old := strings.ReplaceAll(string(b), `"ref": "branch:main"`, `"ref": "v1"`)
	if old == string(b) {
		t.Fatalf("no branch ref in the lockfile:\n%s", b)
	}
	if err := os.WriteFile(lp, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	h.now = h.now.Add(time.Hour)
	rem.reset()
	h.mustRun("run", "seo") // Trusted by content at the same commit: rewrites the records
	b, _ = os.ReadFile(lp)
	if strings.Contains(string(b), `"v1"`) || !strings.Contains(string(b), `"branch:main"`) {
		t.Fatalf("lockfile not re-keyed:\n%s", b)
	}
	// Now the next runs use the cache, and a move is a moved branch.
	rem.reset()
	h.now = h.now.Add(time.Hour)
	h.mustRun("run", "seo")
	if len(rem.prepares) != 0 || rem.resolves != 0 {
		t.Errorf("network use after re-keying: %v %d", rem.prepares, rem.resolves)
	}
	rem.head = branchSHA2
	if code := h.run("run", "--refresh", "seo"); code != ui.ExitTrust || !strings.Contains(h.errb.String(), `branch "main" now points to a different commit`) {
		t.Errorf("after the move: code %d\n%s", code, h.errb)
	}
}

func TestBranchJSONKeepsTheOldSourceFormat(t *testing.T) {
	h, _ := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	h.mustRun("--json", "show", "seo")
	out := h.out.String()
	if !strings.Contains(out, "git:"+fakeURL+"@"+branchSHA1) || strings.Contains(out, "git:"+fakeURL+" branch") {
		t.Errorf("show sources changed format:\n%s", out)
	}
	if !strings.Contains(out, "branch main @ 1111111") {
		t.Errorf("show lacks tracks:\n%s", out)
	}
	h.mustRun("--json", "ls")
	out = h.out.String()
	if !strings.Contains(out, "git:"+fakeURL+"@"+branchSHA1) || strings.Contains(out, "git:"+fakeURL+" branch") {
		t.Errorf("ls source changed format:\n%s", out)
	}
	h.mustRun("show", "seo")
	if !strings.Contains(h.out.String(), "tracks branch main @ 1111111") {
		t.Errorf("show text:\n%s", h.out)
	}
}

func TestBranchPromptWordingForDryRun(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	rem.head = branchSHA2
	sc := ui.NewScripted("no", true)
	h.prompt = sc
	h.now = h.now.Add(25 * time.Hour)
	h.mustRun("dry-run", "seo")
	for _, q := range sc.Asked {
		if strings.Contains(q, "Trust profile") && strings.Contains(q, " run ") {
			t.Errorf("the dry-run question says run: %q", q)
		}
	}
}

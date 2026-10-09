package launcher

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yorch/ccshelf/internal/cache"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/orgconfig"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/profile/gitsource"
	"github.com/yorch/ccshelf/internal/trust"
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
	// noAncestry makes IsAncestor answer false (history that is not proven).
	noAncestry bool
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

// IsAncestor mimics a linear history in which a smaller id is older, proven
// only when the newer checkout is cached.
func (g *fakeBranchGit) IsAncestor(_ context.Context, older, newer string) bool {
	return !g.rem.noAncestry && g.rem.cached[newer] && older < newer
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

// acceptedElsewhereSetup trusts seo and sre at the first commit, then accepts
// the second commit for sre in a terminal. The profile seo sets the model that
// shows in the settings, so it is the profile that is still at the first
// commit. The clock ends one hour after that run, inside the check interval,
// with the remote head at the second commit.
func acceptedElsewhereSetup(t *testing.T, extraConfig string) (*harness, *branchRemote) {
	t.Helper()
	h, rem := newBranchHarness(t, "")
	h.trustBranchSeo(t)
	h.mustRun("trust", "sre", "--accept", orgHash(t, h, "sre"))
	rem.head = branchSHA2
	h.prompt = ui.NewScripted("yes", true)
	h.now = h.now.Add(25 * time.Hour)
	h.mustRun("run", "sre")
	if rem.used[len(rem.used)-1] != branchSHA2 {
		t.Fatalf("sre ran %v; want %s", rem.used, branchSHA2)
	}
	if extraConfig != "" {
		h.writeConfig(extraConfig + branchConf)
	}
	h.prompt = nil
	h.started = 0
	h.now = h.now.Add(time.Hour)
	rem.reset()
	return h, rem
}

func TestBranchAcceptedElsewhereOffersPromptInsideInterval(t *testing.T) {
	h, rem := acceptedElsewhereSetup(t, "")
	sc := ui.NewScripted("yes", true)
	h.prompt = sc
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	for _, want := range []string{"profile \"sre\" trusted commit 222222222222", "1111111", "needs trust"} {
		if !strings.Contains(h.errb.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, h.errb)
		}
	}
	if strings.Contains(h.errb.String(), "now points to") {
		t.Errorf("the text says that the branch moved, but no head was read:\n%s", h.errb)
	}
	if got := h.ranModel(); got != "model-two" {
		t.Errorf("the invocation that accepted ran model %q; want model-two", got)
	}
	// Inside the interval no check is due: no network call at all.
	if rem.resolves != 0 || len(rem.prepares) != 0 {
		t.Errorf("network use: resolves %d prepares %v", rem.resolves, rem.prepares)
	}
	// Trust was recorded for seo: a later run needs no question.
	h.prompt = nil
	h.started = 0
	h.mustRun("run", "seo")
	if got := h.ranModel(); got != "model-two" || h.started != 1 {
		t.Errorf("later run: model %q started %d", got, h.started)
	}
}

func TestBranchAcceptedElsewhereDeclineIsRemembered(t *testing.T) {
	h, rem := acceptedElsewhereSetup(t, "")
	sc := ui.NewScripted("no", true)
	h.prompt = sc
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Error(err)
	}
	if got := h.ranModel(); got != "" {
		t.Errorf("declining ran model %q; want the trusted commit", got)
	}
	if rem.resolves != 0 || len(rem.prepares) != 0 {
		t.Errorf("network use: resolves %d prepares %v", rem.resolves, rem.prepares)
	}
	// The next run, still inside the interval, only prints the notice.
	h.errb.Reset()
	h.started = 0
	h.now = h.now.Add(time.Hour)
	sc = ui.NewScripted(true) // only the plugin question
	h.prompt = sc
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("second run: code %d started %d\n%s", code, h.started, h.errb)
	}
	for _, q := range sc.Asked {
		if strings.Contains(q, "Trust profile") {
			t.Errorf("the declined commit was offered again: %q", q)
		}
	}
	if !strings.Contains(h.errb.String(), "profile sre trusted commit 222222222222") {
		t.Errorf("no notice for the declined commit:\n%s", h.errb)
	}
	if got := h.ranModel(); got != "" {
		t.Errorf("second run ran model %q", got)
	}
	// After one interval the question comes back (the head check is due too, and
	// the head is the same commit, so the ordinary question is asked).
	h.errb.Reset()
	h.started = 0
	h.now = h.now.Add(25 * time.Hour)
	sc = ui.NewScripted("no", true)
	h.prompt = sc
	if code := h.run("run", "seo"); code != 0 {
		t.Fatalf("third run: code %d\n%s", code, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Errorf("the question did not come back after the interval: %v", err)
	}
}

func TestBranchAcceptedElsewhereDeclineDoesNotHideAnotherCommit(t *testing.T) {
	h, rem := acceptedElsewhereSetup(t, "")
	h.prompt = ui.NewScripted("no", true)
	h.mustRun("run", "seo")
	// The branch moves and sre accepts the third commit: a different
	// candidate, so the decline of the second one does not apply.
	rem.head = branchSHA3
	h.now = h.now.Add(25 * time.Hour)
	h.prompt = ui.NewScripted("yes", true)
	h.mustRun("run", "sre")
	if rem.used[len(rem.used)-1] != branchSHA3 {
		t.Fatalf("sre ran %v; want %s", rem.used, branchSHA3)
	}
	h.started = 0
	h.now = h.now.Add(time.Hour)
	sc := ui.NewScripted("no", true)
	h.prompt = sc
	if code := h.run("run", "seo"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if err := sc.Done(); err != nil {
		t.Errorf("a new candidate was not offered: %v", err)
	}
	if !strings.Contains(h.errb.String(), "333333333333") {
		t.Errorf("output lacks the third commit:\n%s", h.errb)
	}
}

func TestBranchAcceptedElsewhereNoticeWithoutOffer(t *testing.T) {
	cases := []struct {
		name   string
		config string
		args   []string
		prompt bool
	}{
		{"no terminal", "", []string{"run", "seo"}, false},
		{"yes", "", []string{"run", "--yes", "seo"}, true},
		{"on_change fail", "[trust]\non_change = \"fail\"\n", []string{"run", "seo"}, true},
		{"dry-run", "", []string{"dry-run", "seo"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, rem := acceptedElsewhereSetup(t, c.config)
			if c.prompt {
				h.prompt = ui.NewScripted(true)
			}
			if code := h.run(c.args...); code != 0 {
				t.Fatalf("code %d\n%s", code, h.errb)
			}
			out := h.errb.String()
			if !strings.Contains(out, "profile sre trusted commit 222222222222") || !strings.Contains(out, "ccshelf run seo") || strings.Contains(out, "needs trust") {
				t.Errorf("wrong notice:\n%s", out)
			}
			if strings.Contains(out, "ccshelf trust seo") {
				t.Errorf("the hint names ccshelf trust, which reads the head:\n%s", out)
			}
			if rem.resolves != 0 || len(rem.prepares) != 0 || len(rem.atFetched) != 0 {
				t.Errorf("network use: resolves %d prepares %v fetched %v", rem.resolves, rem.prepares, rem.atFetched)
			}
			if c.args[0] == "run" && h.ranModel() != "" {
				t.Errorf("ran model %q; want the trusted commit", h.ranModel())
			}
		})
	}
}

func TestBranchAcceptedElsewhereJSONCarriesTheNotice(t *testing.T) {
	h, _ := acceptedElsewhereSetup(t, "")
	h.mustRun("--json", "dry-run", "seo")
	if !strings.Contains(h.out.String(), "profile sre trusted commit 222222222222") {
		t.Errorf("warnings lack the notice:\n%s", h.out)
	}
}

func TestBranchAcceptedElsewhereNeedsProofOfDescent(t *testing.T) {
	// A rollback or a force-push: the newest record is not a descendant.
	h, rem := acceptedElsewhereSetup(t, "")
	rem.noAncestry = true
	sc := ui.NewScripted(true) // only the plugin question may be asked
	h.prompt = sc
	if code := h.run("run", "seo"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	for _, q := range sc.Asked {
		if strings.Contains(q, "Trust profile") {
			t.Errorf("an unproven commit was offered: %q", q)
		}
	}
	if strings.Contains(h.errb.String(), "trusted commit 2222") {
		t.Errorf("an unproven commit was announced:\n%s", h.errb)
	}
}

func TestBranchAcceptedElsewhereOlderCommitIsNotOffered(t *testing.T) {
	// seo and sre are trusted at the first commit and sre accepts the second.
	// Then sre is accepted at the first commit again (its closure changed): the
	// newest record is the first commit, and seo, at the first commit, has
	// nothing to be offered.
	h, rem := acceptedElsewhereSetup(t, "")
	h.mustRun("trust", "seo", "--accept", orgHash(t, h, "seo")) // seo's own record is the newest, at the first commit
	h.prompt = ui.NewScripted(true)
	h.started = 0
	rem.reset()
	if code := h.run("run", "seo"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if strings.Contains(h.errb.String(), "trusted commit") {
		t.Errorf("unexpected offer:\n%s", h.errb)
	}
}

func TestBranchAcceptedElsewhereHeadCheckStillRuns(t *testing.T) {
	// 48 hours later the head moved to a third commit: the ordinary check is due,
	// it runs, and profile seo learns about the third commit.
	h, rem := acceptedElsewhereSetup(t, "")
	rem.head = branchSHA3
	rem.cached[branchSHA3] = true
	h.now = h.now.Add(48 * time.Hour)
	if code := h.run("run", "seo"); code != 0 || h.started != 1 {
		t.Fatalf("code %d started %d\n%s", code, h.started, h.errb)
	}
	if rem.resolves != 1 {
		t.Errorf("resolves = %d; the head check must run when it is due", rem.resolves)
	}
	out := h.errb.String()
	if !strings.Contains(out, "has a new commit (333333333333)") {
		t.Errorf("no notice for the head:\n%s", out)
	}
	if strings.Contains(out, "profile sre trusted commit") {
		t.Errorf("the elsewhere offer was not dropped for the head:\n%s", out)
	}
	// The check does not repeat inside the interval, and the elsewhere offer does
	// not return for a head it replaced... but it may, as a notice, for sre's commit.
	rem.reset()
	h.errb.Reset()
	h.now = h.now.Add(time.Hour)
	h.run("run", "seo")
	if rem.resolves != 0 {
		t.Errorf("the head was checked again inside the interval")
	}
}

func TestBranchAcceptedElsewhereOtherRecordsAreIgnored(t *testing.T) {
	h, rem := newBranchHarness(t, "")
	src := &fakeBranchGit{rem: rem, commit: branchSHA1}
	src.use(branchSHA1)
	r := &profile.Resolved{Name: "seo", Chain: []*profile.File{{Source: src}}}
	s := &session{lockLoaded: true}
	const url = "git:" + fakeURL
	at := h.now.Add(time.Hour)
	for name, rec := range map[string]trust.SourceRecord{
		"another branch": {Source: url, Ref: "branch:other", Commit: branchSHA2},
		"a tag":          {Source: url, Ref: "v2", Commit: branchSHA2},
		"another url":    {Source: "git:https://example.com/other.git", Ref: "branch:main", Commit: branchSHA2},
	} {
		s.lock = append(s.lock, trust.Entry{Profile: "sre", Source: rec.Source, Ref: rec.Ref, Commit: rec.Commit, Sources: []trust.SourceRecord{rec}, AcceptedAt: at})
		if got := s.acceptedElsewhere(context.Background(), r, nil); len(got) != 0 {
			t.Errorf("%s: offered %v", name, got[0].head)
		}
	}
	// The same source and branch is offered.
	rec := trust.SourceRecord{Source: url, Ref: "branch:main", Commit: branchSHA2}
	s.lock = append(s.lock, trust.Entry{Profile: "sre", Source: rec.Source, Ref: rec.Ref, Commit: rec.Commit, Sources: []trust.SourceRecord{rec}, AcceptedAt: at})
	rem.cached[branchSHA2] = true
	if got := s.acceptedElsewhere(context.Background(), r, nil); len(got) != 1 || got[0].head != branchSHA2 || got[0].by != "sre" {
		t.Errorf("same branch: %+v", got)
	}
}

func TestBranchStateDeclinesAreBoundedAndStrict(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir, err := cache.Dir()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	st := loadBranchState(dir, now)
	for i := 0; i < maxDeclinedEntries+20; i++ {
		st.Declined[declineKey("p", string(rune('a'+i%26))+strings.Repeat("x", i))] = declinedOffer{Commit: branchSHA2, At: now.Add(-time.Duration(i) * time.Minute)}
	}
	if err := saveBranchState(dir, st); err != nil {
		t.Fatal(err)
	}
	if got := loadBranchState(dir, now); len(got.Declined) != maxDeclinedEntries {
		t.Errorf("declines = %d; want %d", len(got.Declined), maxDeclinedEntries)
	}
	// A bad commit, a bad key or a future time is dropped.
	bad := `{"checks":{},"declined":{"` + declineKey("p", "k") + `":{"commit":"zz","at":"2026-10-06T12:00:00Z"}}}`
	if err := cache.WriteState(dir, branchStateName, []byte(bad)); err != nil {
		t.Fatal(err)
	}
	if got := loadBranchState(dir, now); len(got.Declined) != 0 {
		t.Errorf("kept a bad decline: %v", got.Declined)
	}
	// An unknown field makes the whole file count as absent.
	if err := cache.WriteState(dir, branchStateName, []byte(`{"checks":{},"other":1}`)); err != nil {
		t.Fatal(err)
	}
	if got := loadBranchState(dir, now); len(got.Checks) != 0 || len(got.Declined) != 0 {
		t.Errorf("read a file with an unknown field: %+v", got)
	}
	if !wasDeclined(branchState{Declined: map[string]declinedOffer{declineKey("p", "k"): {Commit: branchSHA2, At: now.Add(-time.Hour)}}}, "p", "k", branchSHA2, now, 24*time.Hour) {
		t.Error("a recent decline was not found")
	}
	if wasDeclined(branchState{Declined: map[string]declinedOffer{declineKey("p", "k"): {Commit: branchSHA2, At: now.Add(-25 * time.Hour)}}}, "p", "k", branchSHA2, now, 24*time.Hour) {
		t.Error("an old decline still counts")
	}
}

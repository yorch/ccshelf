package launcher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yorch/ccshelf/internal/cache"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/trust"
	"github.com/yorch/ccshelf/internal/ui"
)

// This file holds the update path of a git source that tracks a branch
// (D-55). A run stays on the commit the user trusted, with no network. At most
// once per trust.branch_check_interval it asks the remote where the branch
// points. A new head is a change of the closure, so it needs trust again: the
// check only tells the user, or asks in a terminal. It never runs a commit that
// the user did not accept.

// branchStateName is the cache file that remembers when each tracked branch
// was last checked. It holds times and hashes of source names, never a URL.
const branchStateName = "branch-check.json"

const (
	maxBranchStateBytes   = 16 << 10
	maxBranchStateEntries = 128
	// branchCheckTimeout bounds the one ls-remote of a periodic check, so a
	// host that is down does not hold a run for the length of a clone.
	branchCheckTimeout = 20 * time.Second
)

// branchState is the content of branchStateName.
type branchState struct {
	Checks map[string]time.Time `json:"checks"`
}

// branchSource is implemented by git sources that track a branch
// (gitsource.Source).
type branchSource interface {
	Locator() string
	Branch() string
	Commit() string
	ResolveHead(ctx context.Context) (string, error)
}

// branchKey identifies a tracked branch in the state file.
func branchKey(locator, branch string) string {
	sum := sha256.Sum256([]byte(locator + "\x00" + branch))
	return hex.EncodeToString(sum[:16])
}

func branchLocator(sc config.SourceConfig) string { return branchKey("git:"+sc.URL, sc.Branch) }

func (s *session) now() time.Time {
	if s.cc.Now != nil {
		return s.cc.Now()
	}
	return time.Now()
}

// loadBranchState reads the state file. A missing, unreadable or corrupt file,
// or a time far in the future, gives no record: the state is a cache and never
// a reason to fail.
func loadBranchState(dir string, now time.Time) branchState {
	st := branchState{Checks: map[string]time.Time{}}
	b, err := cache.ReadState(dir, branchStateName, maxBranchStateBytes)
	if err != nil {
		return st
	}
	var in branchState
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil || dec.More() {
		return st
	}
	for k, at := range in.Checks {
		if len(k) == 32 && !at.After(now.Add(24*time.Hour)) {
			st.Checks[k] = at
		}
	}
	return st
}

func saveBranchState(dir string, st branchState) error {
	if len(st.Checks) > maxBranchStateEntries {
		keys := make([]string, 0, len(st.Checks))
		for k := range st.Checks {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return st.Checks[keys[i]].After(st.Checks[keys[j]]) })
		for _, k := range keys[maxBranchStateEntries:] {
			delete(st.Checks, k)
		}
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return cache.WriteState(dir, branchStateName, append(b, '\n'))
}

// branchDue reports whether the branch with key was last checked at least
// interval ago (or never, or in the future).
func branchDue(st branchState, key string, now time.Time, interval time.Duration) bool {
	last, ok := st.Checks[key]
	return !ok || last.After(now) || now.Sub(last) >= interval
}

// recordBranchCheck stores the time of a check of the branch of sc. It is best
// effort: a cache that cannot be written only means the next run checks again.
func (s *session) recordBranchCheck(sc config.SourceConfig) {
	s.recordBranchKey(branchLocator(sc))
}

func (s *session) recordBranchKey(key string) {
	dir, err := cache.Dir()
	if err != nil {
		return
	}
	now := s.now()
	st := loadBranchState(dir, now)
	st.Checks[key] = now.UTC()
	_ = saveBranchState(dir, st)
}

// movedBranch is a tracked branch whose head is not the commit in use.
type movedBranch struct {
	src         branchSource
	head, using string
}

// profileCommit returns the commit that the trust lockfile recorded for the
// profile name at the source with locator and ref, or "".
func (s *session) profileCommit(name, locator, ref string) string {
	var best string
	var bestAt time.Time
	for _, e := range s.lockEntries() {
		if e.Profile != name {
			continue
		}
		recs := e.Sources
		if len(recs) == 0 {
			recs = []trust.SourceRecord{{Source: e.Source, Ref: e.Ref, Commit: e.Commit}}
		}
		for _, r := range recs {
			if r.Source == locator && r.Ref == ref && fullSHA.MatchString(r.Commit) && (best == "" || e.AcceptedAt.After(bestAt)) {
				best, bestAt = r.Commit, e.AcceptedAt
			}
		}
	}
	return best
}

// alignBranchCommits makes a run use, for each branch source of the profile,
// the commit that this profile trusted. The sources are first prepared at the
// newest commit that any profile accepted, which may be a commit that another
// profile of the same source accepted later. When the two differ, the sources
// are prepared again with the profile's own commit from the cache (or fetched
// by id). The periodic check then offers the newer commit to this profile too.
func (l *launcher) alignBranchCommits(ctx context.Context, cc *clicore.Context, s *session, name string) *session {
	r, err := s.resolve(name)
	if err != nil {
		return s // the run reports it
	}
	pins := map[string]string{}
	for _, f := range r.Chain {
		bs, ok := f.Source.(branchSource)
		if !ok || bs.Branch() == "" {
			continue
		}
		rs, ok := f.Source.(interface{ Ref() string })
		if !ok {
			continue
		}
		if want := s.profileCommit(name, bs.Locator(), rs.Ref()); want != "" && want != bs.Commit() {
			pins[branchKey(bs.Locator(), bs.Branch())] = want
		}
	}
	if len(pins) == 0 {
		return s
	}
	s2, err := l.openWith(ctx, cc, openOpts{prepare: true, needClaude: true, pinned: pins})
	if err != nil {
		s.warn("cannot use the commit that profile %s trusted: %s. ccshelf uses the newest trusted commit of the source", ui.SanitizeLine(name), ui.SanitizeLine(err.Error()))
		return s
	}
	return s2
}

// checkBranches is the periodic check of run and dry-run. It returns the
// session to run with: s, or (when the user accepts a new commit in a
// terminal) a session prepared with the new commit.
func (l *launcher) checkBranches(ctx context.Context, cc *clicore.Context, s *session, name string, yes bool) *session {
	r, err := s.resolve(name)
	if err != nil {
		return s // the run reports it
	}
	dir, err := cache.Dir()
	if err != nil {
		return s
	}
	now := s.now()
	interval := s.cfg.Trust.EffectiveBranchCheckInterval()
	st := loadBranchState(dir, now)
	var moved []movedBranch
	seen := map[string]bool{}
	for _, f := range r.Chain {
		bs, ok := f.Source.(branchSource)
		if !ok || bs.Branch() == "" {
			continue
		}
		key := branchKey(bs.Locator(), bs.Branch())
		if seen[key] || s.branchFresh[key] || !branchDue(st, key, now, interval) {
			continue
		}
		seen[key] = true
		// Record the attempt first: a host that is down is not asked again on
		// every run, only once per interval.
		s.recordBranchKey(key)
		cctx, cancel := context.WithTimeout(ctx, branchCheckTimeout)
		head, err := bs.ResolveHead(cctx)
		cancel()
		if err != nil {
			s.warn("cannot check branch %s of %s: %s. ccshelf keeps the trusted commit %s.",
				ui.SanitizeLine(bs.Branch()), ui.SanitizeLine(trimLocator(bs.Locator())), ui.SanitizeLine(err.Error()), shortSHA(bs.Commit()))
			continue
		}
		if head != bs.Commit() {
			moved = append(moved, movedBranch{src: bs, head: head, using: bs.Commit()})
		}
	}
	if len(moved) == 0 {
		return s
	}
	// on_change = "fail" means no question outside an explicit refresh.
	if !canPrompt(cc) || yes || s.cfg.Trust.OnChange != config.OnChangePrompt {
		for _, m := range moved {
			s.warn("branch %s of %s has a new commit (%s). ccshelf keeps the trusted commit %s. To review the update, run: ccshelf trust %s",
				ui.SanitizeLine(m.src.Branch()), ui.SanitizeLine(trimLocator(m.src.Locator())), shortSHA(m.head), shortSHA(m.using), ui.SanitizeLine(r.Name))
		}
		return s
	}
	return l.offerBranchUpdate(ctx, cc, s, r.Name, moved)
}

func trimLocator(loc string) string { return strings.TrimPrefix(loc, "git:") }

// offerBranchUpdate shows the trust diff of the new head and asks. On yes it
// records trust and returns a session that runs the new commit. On no, or on
// any problem, it returns s, which stays on the trusted commit.
func (l *launcher) offerBranchUpdate(ctx context.Context, cc *clicore.Context, s *session, name string, moved []movedBranch) *session {
	keep := func(reason string) *session {
		for _, m := range moved {
			s.warn("%s. ccshelf keeps the trusted commit %s of branch %s. To review the update, run: ccshelf trust %s",
				reason, shortSHA(m.using), ui.SanitizeLine(m.src.Branch()), ui.SanitizeLine(name))
		}
		return s
	}
	s2, err := l.openWith(ctx, cc, openOpts{prepare: true, needClaude: true, refreshBranches: true})
	if err != nil {
		return keep("cannot read the new commit: " + ui.SanitizeLine(err.Error()))
	}
	r2, err := s2.resolve(name)
	if err != nil {
		return keep("cannot resolve the profile at the new commit: " + ui.SanitizeLine(err.Error()))
	}
	lock, err := config.LockfilePath()
	if err != nil {
		return s
	}
	store, err := trust.Open(lock)
	if err != nil {
		return s // the trust check of the run reports it
	}
	v := store.CheckWithProject(r2, s2.proj.Allowed)
	switch {
	case v.State == trust.Trusted:
		return s2
	case v.Problem != "" || v.State == trust.ProjectUntrusted:
		return keep("ccshelf cannot review the new commit here")
	}
	for _, m := range moved {
		// Name the commit that was prepared and diffed. The head may have
		// moved again since the check.
		shown := m.head
		for _, f := range r2.Chain {
			if bs, ok := f.Source.(branchSource); ok && bs.Branch() == m.src.Branch() && bs.Locator() == m.src.Locator() && bs.Commit() != "" {
				shown = bs.Commit()
			}
		}
		fmt.Fprintf(cc.Streams.Err, "Branch %s of %s has a new commit: %s (you trusted %s).\n",
			ui.Sanitize(m.src.Branch()), ui.Sanitize(trimLocator(m.src.Locator())), shortSHA(shown), shortSHA(m.using))
	}
	fmt.Fprintf(cc.Streams.Err, "Profile %s needs trust (closure %s):\n", ui.Sanitize(r2.Name), v.Hash)
	v.Describe(cc.Streams.Err)
	ok, err := ui.ConfirmRisky(ctx, cc.Prompt, fmt.Sprintf("Trust profile %s as shown and use the new commit?", ui.Sanitize(r2.Name)))
	if err != nil || !ok {
		return keep("you did not trust the new commit")
	}
	// Accept what was shown (v.Hash), not whatever the closure is by now.
	if err := store.Accept(r2, v.Hash); err != nil {
		return keep("cannot record trust: " + ui.SanitizeLine(err.Error()))
	}
	rec := ui.NewRecorder("trust", r2.Name)
	rec.Flag("--accept", v.Hash)
	printEquivalent(cc, rec)
	return s2
}

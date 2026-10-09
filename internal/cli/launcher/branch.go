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
	"github.com/yorch/ccshelf/internal/profile"
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
	maxBranchStateBytes   = 64 << 10
	maxBranchStateEntries = 128
	// maxDeclinedEntries bounds the remembered declines.
	maxDeclinedEntries = 64
	// branchCheckTimeout bounds the one ls-remote of a periodic check, so a
	// host that is down does not hold a run for the length of a clone.
	branchCheckTimeout = 20 * time.Second
)

// branchState is the content of branchStateName.
type branchState struct {
	Checks map[string]time.Time `json:"checks"`
	// Declined remembers, per profile and tracked branch, the commit that the
	// user declined when it was offered because another profile trusted it.
	// The key is a hash of the profile name and the branch key.
	Declined map[string]declinedOffer `json:"declined,omitempty"`
}

// declinedOffer is one remembered decline.
type declinedOffer struct {
	Commit string    `json:"commit"`
	At     time.Time `json:"at"`
}

// declineKey is the key of a decline in the state file.
func declineKey(profile, branch string) string {
	sum := sha256.Sum256([]byte(profile + "\x00" + branch))
	return hex.EncodeToString(sum[:16])
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
	st := branchState{Checks: map[string]time.Time{}, Declined: map[string]declinedOffer{}}
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
	for k, d := range in.Declined {
		if len(k) == 32 && fullSHA.MatchString(d.Commit) && !d.At.After(now.Add(24*time.Hour)) {
			st.Declined[k] = d
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
	if len(st.Declined) > maxDeclinedEntries {
		keys := make([]string, 0, len(st.Declined))
		for k := range st.Declined {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return st.Declined[keys[i]].At.After(st.Declined[keys[j]].At) })
		for _, k := range keys[maxDeclinedEntries:] {
			delete(st.Declined, k)
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

// recordDecline remembers that the user declined commit for the profile name
// at the branch with key. Best effort, like recordBranchKey.
func (s *session) recordDecline(name, key, commit string) {
	dir, err := cache.Dir()
	if err != nil {
		return
	}
	now := s.now()
	st := loadBranchState(dir, now)
	st.Declined[declineKey(name, key)] = declinedOffer{Commit: commit, At: now.UTC()}
	_ = saveBranchState(dir, st)
}

// wasDeclined reports whether the user declined exactly this commit for the
// profile less than interval ago.
func wasDeclined(st branchState, name, key, commit string, now time.Time, interval time.Duration) bool {
	d, ok := st.Declined[declineKey(name, key)]
	return ok && d.Commit == commit && !d.At.After(now) && now.Sub(d.At) < interval
}

// movedBranch is a tracked branch whose head is not the commit in use.
type movedBranch struct {
	src         branchSource
	head, using string
	// elsewhere is true when head is not a remote head but a commit that the
	// trust lockfile holds for another profile (found with no network call).
	elsewhere bool
	// by is the profile that trusted head, for an elsewhere commit.
	by string
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

// checkBranches is the periodic check of run and dry-run, followed by the
// offer of a commit that another profile trusted. It returns the session to
// run with: s, or (when the user accepts a commit in a terminal) a session
// prepared with that commit.
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
	// covered holds the branches whose head is known in this run. For them the
	// remote answer decides, and no elsewhere offer is made.
	covered := map[string]bool{}
	for _, f := range r.Chain {
		bs, ok := f.Source.(branchSource)
		if !ok || bs.Branch() == "" {
			continue
		}
		key := branchKey(bs.Locator(), bs.Branch())
		if s.branchFresh[key] {
			covered[key] = true
			continue
		}
		if seen[key] || !branchDue(st, key, now, interval) {
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
		covered[key] = true
		if head != bs.Commit() {
			moved = append(moved, movedBranch{src: bs, head: head, using: bs.Commit()})
		}
	}
	if len(moved) > 0 {
		s = l.noticeOrOffer(ctx, cc, s, r.Name, moved, nil, yes)
		if r, err = s.resolve(name); err != nil {
			return s // the run reports it
		}
	}
	return l.checkAcceptedElsewhere(ctx, cc, s, r, st, covered, yes)
}

// checkAcceptedElsewhere handles a commit that another profile accepted. It
// needs no network call and no interval: the lockfile and the cached checkout
// are enough. A commit that the user declined, for this profile, less than one
// interval ago gets the notice only.
func (l *launcher) checkAcceptedElsewhere(ctx context.Context, cc *clicore.Context, s *session, r *profile.Resolved, st branchState, covered map[string]bool, yes bool) *session {
	cands := s.acceptedElsewhere(ctx, r, covered)
	if len(cands) == 0 {
		return s
	}
	now := s.now()
	interval := s.cfg.Trust.EffectiveBranchCheckInterval()
	var offer, notice []movedBranch
	for _, m := range cands {
		if wasDeclined(st, r.Name, branchKey(m.src.Locator(), m.src.Branch()), m.head, now, interval) {
			notice = append(notice, m)
		} else {
			offer = append(offer, m)
		}
	}
	s.noticeBranches(notice, r.Name)
	if len(offer) == 0 {
		return s
	}
	return l.noticeOrOffer(ctx, cc, s, r.Name, offer, s.pinsFor(r, offer), yes)
}

// noticeBranches prints the one-line notice for each moved branch.
func (s *session) noticeBranches(moved []movedBranch, name string) {
	for _, m := range moved {
		s.warn("%s", m.notice(name))
	}
}

// notice is the one-line notice: it says what ccshelf found, which commit
// keeps running, and how to review the commit.
func (m movedBranch) notice(name string) string {
	br, loc, pn := ui.SanitizeLine(m.src.Branch()), ui.SanitizeLine(trimLocator(m.src.Locator())), ui.SanitizeLine(name)
	if m.elsewhere {
		return fmt.Sprintf("branch %s of %s: profile %s trusted commit %s. Profile %s keeps the trusted commit %s. To review commit %s, run \"ccshelf run %s\" in a terminal, without --yes, with trust.on_change set to prompt",
			br, loc, ui.SanitizeLine(m.by), shortSHA(m.head), pn, shortSHA(m.using), shortSHA(m.head), pn)
	}
	return fmt.Sprintf("branch %s of %s has a new commit (%s). ccshelf keeps the trusted commit %s. To review the update, run: ccshelf trust %s",
		br, loc, shortSHA(m.head), shortSHA(m.using), pn)
}

// noticeOrOffer prints the one-line notice for each moved branch, or (in a
// terminal, without --yes and with on_change = "prompt") offers the update.
// on_change = "fail" means no question outside an explicit refresh.
func (l *launcher) noticeOrOffer(ctx context.Context, cc *clicore.Context, s *session, name string, moved []movedBranch, pins map[string]string, yes bool) *session {
	if !canPrompt(cc) || yes || s.cfg.Trust.OnChange != config.OnChangePrompt {
		s.noticeBranches(moved, name)
		return s
	}
	return l.offerBranchUpdate(ctx, cc, s, name, moved, pins)
}

// acceptedElsewhere finds the branch sources of the profile for which the
// trust lockfile holds a commit that another profile accepted more recently
// than the commit in use, and that is a strict descendant of it. It reads only
// the lockfile and the cached checkout: no network call, and a head is never
// resolved. Newer by time alone is not enough (a rollback or a force-push
// leaves an older or unrelated commit as the newest record), so a commit with
// no offline proof of descent is not offered. The profile does not trust the
// commit yet; the caller asks, or only says so. Branches in skip are left out.
func (s *session) acceptedElsewhere(ctx context.Context, r *profile.Resolved, skip map[string]bool) []movedBranch {
	var out []movedBranch
	for _, f := range r.Chain {
		bs, ok := f.Source.(branchSource)
		if !ok || bs.Branch() == "" || bs.Commit() == "" {
			continue
		}
		rs, ok := f.Source.(interface{ Ref() string })
		anc, ok2 := f.Source.(interface {
			IsAncestor(ctx context.Context, older, newer string) bool
		})
		key := branchKey(bs.Locator(), bs.Branch())
		if !ok || !ok2 || skip[key] {
			continue
		}
		if _, dup := seenKey(out, key); dup {
			continue
		}
		newest, by := s.newestAccepted(bs.Locator(), rs.Ref())
		if newest == "" || newest == bs.Commit() || !anc.IsAncestor(ctx, bs.Commit(), newest) {
			continue
		}
		out = append(out, movedBranch{src: bs, head: newest, using: bs.Commit(), elsewhere: true, by: by})
	}
	return out
}

// pinsFor gives the commit to prepare for every branch source of the chain:
// the offered commit, or the one in use.
func (s *session) pinsFor(r *profile.Resolved, offer []movedBranch) map[string]string {
	pins := map[string]string{}
	for _, f := range r.Chain {
		if bs, ok := f.Source.(branchSource); ok && bs.Branch() != "" {
			pins[branchKey(bs.Locator(), bs.Branch())] = bs.Commit()
		}
	}
	for _, m := range offer {
		pins[branchKey(m.src.Locator(), m.src.Branch())] = m.head
	}
	return pins
}

// newestAccepted returns the most recently accepted commit for the source and
// ref, and the profile it was accepted for.
func (s *session) newestAccepted(locator, ref string) (commit, by string) {
	var at time.Time
	for _, e := range s.lockEntries() {
		recs := e.Sources
		if len(recs) == 0 {
			recs = []trust.SourceRecord{{Source: e.Source, Ref: e.Ref, Commit: e.Commit}}
		}
		for _, r := range recs {
			if r.Source == locator && r.Ref == ref && fullSHA.MatchString(r.Commit) && (commit == "" || e.AcceptedAt.After(at)) {
				commit, by, at = r.Commit, e.Profile, e.AcceptedAt
			}
		}
	}
	return commit, by
}

func seenKey(ms []movedBranch, key string) (int, bool) {
	for i, m := range ms {
		if branchKey(m.src.Locator(), m.src.Branch()) == key {
			return i, true
		}
	}
	return 0, false
}

func trimLocator(loc string) string { return strings.TrimPrefix(loc, "git:") }

// offerBranchUpdate shows the trust diff of the new head and asks. On yes it
// records trust and returns a session that runs the new commit. On no, or on
// any problem, it returns s, which stays on the trusted commit.
func (l *launcher) offerBranchUpdate(ctx context.Context, cc *clicore.Context, s *session, name string, moved []movedBranch, pins map[string]string) *session {
	keep := func(reason string) *session {
		for _, m := range moved {
			hint := "To review the update, run: ccshelf trust " + ui.SanitizeLine(name)
			if m.elsewhere {
				hint = "To try again, run \"ccshelf run " + ui.SanitizeLine(name) + "\" in a terminal"
			}
			s.warn("%s. ccshelf keeps the trusted commit %s of branch %s. %s",
				reason, shortSHA(m.using), ui.SanitizeLine(m.src.Branch()), hint)
		}
		return s
	}
	// With pins, the commits are known (they come from the trust lockfile):
	// they are read from the cache, or fetched by id, and no head is resolved.
	opts := openOpts{prepare: true, needClaude: true, refreshBranches: pins == nil, pinned: pins}
	s2, err := l.openWith(ctx, cc, opts)
	if err != nil {
		return keep("cannot read the new commit: " + ui.SanitizeLine(err.Error()))
	}
	r2, err := s2.resolve(name)
	if err != nil {
		return keep("cannot resolve the profile at the new commit: " + ui.SanitizeLine(err.Error()))
	}
	if pins != nil {
		// The offline fallback may have used another cached checkout when the
		// commit could not be fetched. Offer only the commit that was found.
		for _, m := range moved {
			if !chainUses(r2, m.src, m.head) {
				return keep("cannot read the commit that another profile trusted")
			}
		}
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
	for i := range moved {
		if moved[i].elsewhere {
			v.AcceptedFor = moved[i].by
		}
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
		if m.elsewhere {
			fmt.Fprintf(cc.Streams.Err, "Profile %s trusted commit %s of branch %s of %s (you trusted %s).\n",
				ui.Sanitize(m.by), shortSHA(shown), ui.Sanitize(m.src.Branch()), ui.Sanitize(trimLocator(m.src.Locator())), shortSHA(m.using))
			continue
		}
		fmt.Fprintf(cc.Streams.Err, "Branch %s of %s has a new commit: %s (you trusted %s).\n",
			ui.Sanitize(m.src.Branch()), ui.Sanitize(trimLocator(m.src.Locator())), shortSHA(shown), shortSHA(m.using))
	}
	fmt.Fprintf(cc.Streams.Err, "Profile %s needs trust (closure %s):\n", ui.Sanitize(r2.Name), v.Hash)
	v.Describe(cc.Streams.Err)
	ok, err := ui.ConfirmRisky(ctx, cc.Prompt, fmt.Sprintf("Trust profile %s as shown and use the new commit?", ui.Sanitize(r2.Name)))
	if err != nil || !ok {
		if err == nil {
			// Remember a decline of a commit that another profile trusted, so
			// the question does not come back at every run.
			for _, m := range moved {
				if m.elsewhere {
					s.recordDecline(name, branchKey(m.src.Locator(), m.src.Branch()), m.head)
				}
			}
		}
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

// chainUses reports whether the resolved chain has the branch source of src
// prepared at exactly commit.
func chainUses(r *profile.Resolved, src branchSource, commit string) bool {
	for _, f := range r.Chain {
		if bs, ok := f.Source.(branchSource); ok && bs.Branch() == src.Branch() && bs.Locator() == src.Locator() {
			return bs.Commit() == commit
		}
	}
	return false
}

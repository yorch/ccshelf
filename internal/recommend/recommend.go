package recommend

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/yorch/ccshelf/internal/catalog"
	"github.com/yorch/ccshelf/internal/marketplace"
)

// Kinds of recommendation.
const (
	KindPlugin  = "plugin"
	KindProfile = "profile"
)

// Weights of each relevance signal kind in a plugin's score. A kind counts
// once, however many of its entries match.
const (
	weightCwd          = 2.0
	weightFilesRead    = 3.0
	weightManifestDeps = 3.0
	weightCLI          = 1.0
	weightHosts        = 1.0

	// keywordWeight is the score of one when_to_use keyword that matches the
	// directory; avoidWeight is subtracted per avoid_when keyword.
	keywordWeight = 1.0
	avoidWeight   = 1.5
	// minProfileScore is the least score a profile needs to be listed.
	minProfileScore = 1.0

	maxPatternLen  = 256
	maxMatchedList = 3
)

// ProfileInfo describes a profile to the recommender.
type ProfileInfo struct {
	Name      string
	Status    string // "", active, experimental or deprecated
	WhenToUse []string
	AvoidWhen []string
	// SupersededBy names the profile that replaces a deprecated one.
	SupersededBy string
}

// ProfilesFromCatalog converts the catalog's profile rows. The catalog does
// not carry avoid_when, so callers that have it should build ProfileInfo
// themselves.
func ProfilesFromCatalog(ps []catalog.ProfileInfo) []ProfileInfo {
	out := make([]ProfileInfo, 0, len(ps))
	for _, p := range ps {
		out = append(out, ProfileInfo{Name: p.Name, Status: p.Status, WhenToUse: p.WhenToUse})
	}
	return out
}

// Recommendation is one suggested plugin or profile.
type Recommendation struct {
	// Kind is KindPlugin or KindProfile.
	Kind string `json:"kind"`
	// Name is name@marketplace for plugins and the profile name for profiles.
	Name  string  `json:"name"`
	Score float64 `json:"score"`
	// Why explains every match, one line each.
	Why []string `json:"why"`
	// Status is the status of the recommended entry (active, experimental).
	Status string `json:"status,omitempty"`
	// Replaces names the deprecated entry this one was recommended instead of.
	Replaces string `json:"replaces,omitempty"`
}

// Option tunes ForCatalog.
type Option func(*config)

type config struct {
	relevance map[string]*marketplace.Relevance
}

// WithRelevance supplies the relevance blocks of the plugins, keyed by
// name@marketplace or by plain plugin name. The catalog does not carry them,
// so without this option no plugin can match. Use RelevanceOf to build the map.
func WithRelevance(m map[string]*marketplace.Relevance) Option {
	return func(c *config) { c.relevance = m }
}

// RelevanceOf collects the relevance blocks of every plugin of the given
// marketplaces, keyed by name@marketplace.
func RelevanceOf(ms ...*marketplace.Marketplace) map[string]*marketplace.Relevance {
	out := map[string]*marketplace.Relevance{}
	for _, m := range ms {
		if m == nil {
			continue
		}
		for _, p := range m.Plugins {
			if p.Relevance != nil {
				out[p.Name+"@"+m.Name] = p.Relevance
			}
		}
	}
	return out
}

// ForCatalog recommends plugins and profiles for the signals. It is
// deterministic, offline and rule based. Deprecated plugins and profiles are
// never recommended: the entry named by superseded_by is recommended instead
// (with Replaces set) when it exists and is not deprecated itself.
func ForCatalog(c *catalog.Catalog, sig *Signals, profiles []ProfileInfo, opts ...Option) []Recommendation {
	if sig == nil {
		return nil
	}
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	var out []Recommendation
	if c != nil {
		out = append(out, pluginRecs(c, sig, cfg)...)
	}
	out = append(out, profileRecs(sig, profiles)...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Score != b.Score:
			return a.Score > b.Score
		case a.Kind != b.Kind:
			return a.Kind == KindProfile
		default:
			return a.Name < b.Name
		}
	})
	return out
}

func pluginID(e catalog.Entry) string {
	if e.Marketplace == "" {
		return e.Name
	}
	return e.Name + "@" + e.Marketplace
}

func pluginRecs(c *catalog.Catalog, sig *Signals, cfg config) []Recommendation {
	byName := map[string][]catalog.Entry{}
	for _, e := range c.Plugins {
		byName[e.Name] = append(byName[e.Name], e)
	}
	byID := map[string]Recommendation{}
	put := func(r Recommendation) {
		if cur, ok := byID[r.Name]; ok {
			if cur.Score >= r.Score {
				cur.Why = appendUnique(cur.Why, r.Why...)
				byID[r.Name] = cur
				return
			}
			r.Why = appendUnique(r.Why, cur.Why...)
		}
		byID[r.Name] = r
	}
	for _, e := range c.Plugins {
		rel := cfg.relevance[pluginID(e)]
		if rel == nil {
			rel = cfg.relevance[e.Name]
		}
		if rel == nil {
			continue
		}
		score, why := scoreSignals(rel.Signals, sig)
		if score == 0 {
			continue
		}
		if e.Status != "deprecated" {
			put(Recommendation{Kind: KindPlugin, Name: pluginID(e), Score: score, Why: why, Status: e.Status})
			continue
		}
		// Deprecated: recommend the replacement, if there is a usable one.
		if repl, ok := replacement(byName, e); ok {
			r := Recommendation{Kind: KindPlugin, Name: pluginID(repl), Score: score, Status: repl.Status, Replaces: pluginID(e)}
			r.Why = append([]string{fmt.Sprintf("replaces deprecated %s, which matched this directory", pluginID(e))}, why...)
			put(r)
		}
	}
	out := make([]Recommendation, 0, len(byID))
	for _, r := range byID {
		out = append(out, r)
	}
	return out
}

func replacement(byName map[string][]catalog.Entry, e catalog.Entry) (catalog.Entry, bool) {
	if e.SupersededBy == "" {
		return catalog.Entry{}, false
	}
	cands := byName[e.SupersededBy]
	// Prefer the same marketplace.
	sort.SliceStable(cands, func(i, j int) bool {
		return (cands[i].Marketplace == e.Marketplace) && (cands[j].Marketplace != e.Marketplace)
	})
	for _, r := range cands {
		if r.Status != "deprecated" {
			return r, true
		}
	}
	return catalog.Entry{}, false
}

func appendUnique(dst []string, add ...string) []string {
	for _, a := range add {
		found := false
		for _, d := range dst {
			if d == a {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, a)
		}
	}
	return dst
}

// scoreSignals evaluates one plugin's relevance signals.
func scoreSignals(s marketplace.Signals, sig *Signals) (float64, []string) {
	var score float64
	var why []string
	if hits := matchCwd(s.Cwd, sig); len(hits) > 0 {
		score += weightCwd
		why = append(why, fmt.Sprintf("cwd glob %s matches the directory", quoteList(hits)))
	}
	if hits := matchCLI(s.CLI, sig); len(hits) > 0 {
		score += weightCLI
		why = append(why, fmt.Sprintf("the directory uses the %s command", quoteList(hits)))
	}
	if hits := matchHosts(s.Hosts, sig); len(hits) > 0 {
		score += weightHosts
		why = append(why, fmt.Sprintf("host %s is in use", quoteList(hits)))
	}
	if hits := matchFiles(s.FilesRead, sig); len(hits) > 0 {
		score += weightFilesRead
		why = append(why, "files found: "+quoteList(hits))
	}
	if hits := matchManifestDeps(s.ManifestDeps, sig); len(hits) > 0 {
		score += weightManifestDeps
		why = append(why, "manifest dependency: "+quoteList(hits))
	}
	return score, why
}

func quoteList(in []string) string {
	q := make([]string, 0, len(in))
	for _, s := range in {
		q = append(q, fmt.Sprintf("%q", s))
	}
	return strings.Join(q, ", ")
}

// cwdCandidates returns the slash-separated paths a cwd glob is tried on: the
// repo-relative path of the directory and of each directory above it inside
// the repository, and the absolute path.
func cwdCandidates(sig *Signals) (rel []string, abs string) {
	abs = filepath.ToSlash(sig.Cwd)
	if sig.RepoRoot == "" {
		return nil, abs
	}
	r, err := filepath.Rel(sig.RepoRoot, sig.Cwd)
	if err != nil || r == "." || strings.HasPrefix(r, "..") {
		return nil, abs
	}
	r = filepath.ToSlash(r)
	for p := r; p != "." && p != "/" && p != ""; p = path.Dir(p) {
		rel = append(rel, p)
	}
	return rel, abs
}

func matchCwd(patterns []string, sig *Signals) []string {
	if sig.Cwd == "" {
		return nil
	}
	rel, abs := cwdCandidates(sig)
	var hits []string
	for _, p := range limit(patterns) {
		matched := Match(p, abs)
		if !matched && !strings.HasPrefix(p, "/") {
			matched = Match("**/"+p, abs)
		}
		for _, r := range rel {
			if matched {
				break
			}
			matched = Match(p, r)
		}
		if matched {
			hits = append(hits, p)
		}
	}
	return hits
}

func matchFiles(patterns []string, sig *Signals) []string {
	var hits []string
	for _, p := range limit(patterns) {
		for _, f := range sig.Files {
			if matchAnyDepth(p, f) {
				hits = append(hits, p+" ("+f+")")
				break
			}
		}
	}
	return firstN(hits, maxMatchedList)
}

func matchCLI(signals []string, sig *Signals) []string {
	have := map[string]bool{}
	for _, c := range sig.CLIs {
		have[c] = true
	}
	var hits []string
	for _, s := range limit(signals) {
		fields := strings.Fields(s)
		if len(fields) > 0 && have[fields[0]] {
			hits = append(hits, fields[0])
		}
	}
	return unique(hits)
}

func matchHosts(signals []string, sig *Signals) []string {
	var hits []string
	for _, s := range limit(signals) {
		for _, h := range sig.Hosts {
			if strings.EqualFold(strings.TrimSpace(s), strings.TrimSpace(h)) {
				hits = append(hits, s)
				break
			}
		}
	}
	return unique(hits)
}

// matchManifestDeps tests each {file, pattern}: file is a regular expression
// matched against the whole relative path or the base name of a collected
// manifest, pattern is searched in its content. Both are RE2; invalid ones
// never match. Content is capped at MaxManifestSize, which bounds the work.
func matchManifestDeps(deps []marketplace.ManifestDep, sig *Signals) []string {
	if len(sig.ManifestFiles) == 0 {
		return nil
	}
	names := make([]string, 0, len(sig.ManifestFiles))
	for n := range sig.ManifestFiles {
		names = append(names, n)
	}
	sort.Strings(names)
	var hits []string
	for i, d := range deps {
		if i >= maxSignals || d.Pattern == "" || len(d.Pattern) > maxPatternLen || len(d.File) > maxPatternLen {
			continue
		}
		fileRe, err := regexp.Compile("^(?:" + d.File + ")$")
		if err != nil {
			continue
		}
		patRe, err := regexp.Compile(d.Pattern)
		if err != nil {
			continue
		}
		for _, n := range names {
			if !fileRe.MatchString(n) && !fileRe.MatchString(path.Base(n)) {
				continue
			}
			content := sig.ManifestFiles[n]
			if len(content) > MaxManifestSize {
				content = content[:MaxManifestSize]
			}
			if patRe.Match(content) {
				hits = append(hits, d.Pattern+" in "+n)
				break
			}
		}
	}
	return firstN(hits, maxMatchedList)
}

// maxSignals mirrors Claude Code's cap on entries per signal kind.
const maxSignals = 20

func limit(in []string) []string {
	var out []string
	for i, s := range in {
		if i >= maxSignals {
			break
		}
		if s == "" || len(s) > maxPatternLen {
			continue
		}
		out = append(out, s)
	}
	return out
}

func firstN(in []string, n int) []string {
	if len(in) > n {
		return in[:n]
	}
	return in
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

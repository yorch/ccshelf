package doctor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ccshelf/ccshelf/internal/catalog"
)

// check is one doctor check. run returns its findings, or a non-empty skip
// reason when its input is missing.
type check struct {
	code     string
	name     string
	severity Severity
	desc     string
	run      func(in *Input) (findings []Finding, skip string)
}

var checks = []check{
	{"DOC001", "overlap", Warning, "Plugins that share at least 60% of their tags, or list each other in overlaps_with.", checkOverlap},
	{"DOC002", "unused", Info, "Plugins in no profile and, with usage data, with no skill_activated events in the window.", checkUnused},
	{"DOC003", "deprecated-in-use", Warning, "A profile includes a deprecated plugin.", checkDeprecatedInUse},
	{"DOC004", "review", Warning, "A plugin's review_by is missing or in the past.", checkReview},
	{"DOC005", "owner", Warning, "A plugin has no owner.", checkOwner},
	{"DOC006", "standalone-skills", Warning, "Standalone skills that no profile lists in skills.off or skills.name_only.", checkStandaloneSkills},
	{"DOC007", "missing-upstream", Warning, "An installed plugin is no longer listed by its marketplace.", checkMissingUpstream},
	{"DOC008", "not-installed", Warning, "A profile includes a plugin that is not installed.", checkNotInstalled},
	{"DOC009", "forced-by-policy", Info, "A plugin is forced on by managed policy and no profile can mask it.", checkForced},
	{"DOC010", "platform-review", Warning, "A plugin with hooks or MCP servers is not covered by platform review.", checkPlatformReview},
	{"DOC011", "protected-masked", Warning, "A profile would mask a plugin the org config protects.", checkProtectedMasked},
}

// Safe-value patterns: only values that match are put into shell commands and
// TOML snippets.
var (
	pluginIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9._-]*$`)
	skillRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]*$`)
	profileRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

func txt(s string) string { return catalog.Text(s, 128) }

func entryID(e catalog.Entry) string {
	if e.Marketplace == "" {
		return e.Name
	}
	return e.Name + "@" + e.Marketplace
}

// entryIndex finds catalog entries by id or by plain name.
type entryIndex struct {
	byID   map[string]catalog.Entry
	byName map[string][]catalog.Entry
}

func newIndex(c *catalog.Catalog) *entryIndex {
	ix := &entryIndex{byID: map[string]catalog.Entry{}, byName: map[string][]catalog.Entry{}}
	for _, e := range c.Plugins {
		ix.byID[entryID(e)] = e
		ix.byName[e.Name] = append(ix.byName[e.Name], e)
	}
	return ix
}

// find returns the entry for a plugin id (name@marketplace) or plain name.
func (ix *entryIndex) find(id string) (catalog.Entry, bool) {
	if e, ok := ix.byID[id]; ok {
		return e, true
	}
	if !strings.Contains(id, "@") {
		if es := ix.byName[id]; len(es) == 1 {
			return es[0], true
		}
	}
	return catalog.Entry{}, false
}

// Run executes every check and returns the report.
func Run(in Input) *Report {
	r := &Report{Findings: []Finding{}, Skipped: []Skip{}}
	if in.Catalog == nil {
		in.Catalog = &catalog.Catalog{}
		r.Skipped = append(r.Skipped, Skip{Code: "DOC000", Check: "catalog", Reason: "no catalog was supplied"})
	}
	for _, c := range checks {
		fs, skip := c.run(&in)
		if skip != "" {
			r.Skipped = append(r.Skipped, Skip{Code: c.code, Check: c.name, Reason: skip})
			continue
		}
		for _, f := range fs {
			f.Code, f.Check = c.code, c.name
			if f.Severity == "" {
				f.Severity = c.severity
			}
			r.Findings = append(r.Findings, f)
		}
	}
	for _, f := range in.Policy {
		f.Message = catalog.Text(f.Message, 500)
		f.Hint = catalog.Text(f.Hint, 500)
		if f.Severity == "" {
			f.Severity = Info
		}
		r.Findings = append(r.Findings, f)
	}
	sortFindings(r.Findings)
	return r
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		switch {
		case a.Code != b.Code:
			return a.Code < b.Code
		case a.Plugin != b.Plugin:
			return a.Plugin < b.Plugin
		case a.Profile != b.Profile:
			return a.Profile < b.Profile
		default:
			return a.Message < b.Message
		}
	})
}

// --- DOC001 overlap ---

// minSharedTags is the least number of shared tags for a tag overlap, so two
// plugins that each carry the same single tag are not reported.
const minSharedTags = 2

// overlapRatio is the least share of tags (shared over all tags of the pair).
const overlapRatio = 0.6

func checkOverlap(in *Input) ([]Finding, string) {
	plugins := in.Catalog.Plugins
	var out []Finding
	for i := 0; i < len(plugins); i++ {
		for j := i + 1; j < len(plugins); j++ {
			a, b := plugins[i], plugins[j]
			if a.Status == "deprecated" || b.Status == "deprecated" {
				continue
			}
			declared := listed(a, b) || listed(b, a)
			shared, total := sharedTags(a.Tags, b.Tags)
			byTags := len(shared) >= minSharedTags && float64(len(shared)) >= overlapRatio*float64(total)
			if !declared && !byTags {
				continue
			}
			x, y := entryID(a), entryID(b)
			if x > y {
				x, y = y, x
			}
			var parts []string
			if byTags {
				parts = append(parts, fmt.Sprintf("share %d of %d tags (%s)", len(shared), total, strings.Join(shared, ", ")))
			}
			if declared {
				parts = append(parts, "list each other in overlaps_with")
			}
			f := Finding{Plugin: x, Message: fmt.Sprintf("%s and %s %s", txt(x), txt(y), strings.Join(parts, " and "))}
			if !declared {
				f.Hint = "if the overlap is intended, add each to the other's overlaps_with; otherwise consider merging or deprecating one"
			}
			out = append(out, f)
		}
	}
	return out, ""
}

func listed(a, b catalog.Entry) bool {
	for _, n := range a.OverlapsWith {
		if n == b.Name || n == entryID(b) {
			return true
		}
	}
	return false
}

// sharedTags returns the sorted shared tags and the size of the union.
func sharedTags(a, b []string) (shared []string, total int) {
	set := map[string]bool{}
	for _, t := range a {
		set[t] = true
	}
	union := map[string]bool{}
	for t := range set {
		union[t] = true
	}
	seen := map[string]bool{}
	for _, t := range b {
		union[t] = true
		if set[t] && !seen[t] {
			seen[t] = true
			shared = append(shared, t)
		}
	}
	sort.Strings(shared)
	return shared, len(union)
}

// --- DOC002 unused ---

func checkUnused(in *Input) ([]Finding, string) {
	if len(in.Profiles) == 0 {
		return nil, "no profiles were supplied"
	}
	used := map[string]bool{}
	ix := newIndex(in.Catalog)
	for _, p := range in.Profiles {
		for _, id := range p.Plugins {
			used[id] = true
			if e, ok := ix.find(id); ok {
				used[entryID(e)] = true
			}
		}
	}
	var out []Finding
	for _, e := range in.Catalog.Plugins {
		id := entryID(e)
		if used[id] || e.Status == "deprecated" {
			continue
		}
		msg := fmt.Sprintf("%s is in no profile", txt(id))
		if in.Usage != nil {
			c, _ := in.Usage.Lookup(id)
			if c.Used() {
				continue
			}
			msg += fmt.Sprintf(" and has 0 skill_activated events%s", window(in))
		} else {
			msg += " (no usage data to confirm it is unused)"
		}
		out = append(out, Finding{Plugin: id, Message: msg})
	}
	return out, ""
}

func window(in *Input) string {
	if in.Usage == nil || (in.Usage.From == "" && in.Usage.To == "") {
		return ""
	}
	return fmt.Sprintf(" between %s and %s", in.Usage.From, in.Usage.To)
}

// --- DOC003 deprecated-in-use ---

func checkDeprecatedInUse(in *Input) ([]Finding, string) {
	if len(in.Profiles) == 0 {
		return nil, "no profiles were supplied"
	}
	ix := newIndex(in.Catalog)
	var out []Finding
	for _, p := range in.Profiles {
		for _, id := range p.Plugins {
			e, ok := ix.find(id)
			if !ok || e.Status != "deprecated" {
				continue
			}
			f := Finding{Plugin: entryID(e), Profile: p.Name}
			f.Message = fmt.Sprintf("profile %s includes the deprecated plugin %s", txt(p.Name), txt(entryID(e)))
			if e.SupersededBy != "" {
				f.Message += fmt.Sprintf("; use %s instead", txt(e.SupersededBy))
				f.Hint = fmt.Sprintf("in the profile's [plugins] include list, replace %q with the replacement", entryID(e))
			} else {
				f.Hint = "remove it from the profile; no replacement is named"
			}
			out = append(out, f)
		}
	}
	return out, ""
}

// --- DOC004 review, DOC005 owner ---

func checkReview(in *Input) ([]Finding, string) {
	now := in.now().UTC().Truncate(24 * time.Hour)
	maxAge := time.Duration(in.maxAge()) * 24 * time.Hour
	var out []Finding
	for _, e := range in.Catalog.Plugins {
		if e.Status == "deprecated" {
			continue
		}
		id := entryID(e)
		if e.ReviewBy == "" {
			out = append(out, Finding{Plugin: id, Message: fmt.Sprintf("%s has no review_by date", txt(id)),
				Hint: `add review_by = "YYYY-MM-DD" to its sidecar`})
			continue
		}
		d, err := time.Parse("2006-01-02", e.ReviewBy)
		if err != nil {
			out = append(out, Finding{Plugin: id, Message: fmt.Sprintf("%s has a review_by that is not a date (%s)", txt(id), txt(e.ReviewBy)),
				Hint: `use the form review_by = "YYYY-MM-DD"`})
			continue
		}
		if !now.After(d) {
			continue
		}
		days := int(now.Sub(d).Hours() / 24)
		f := Finding{Plugin: id, Message: fmt.Sprintf("%s was due for review on %s (%d days ago)", txt(id), e.ReviewBy, days),
			Hint: "review the plugin and move review_by forward"}
		if now.Sub(d) <= maxAge {
			f.Severity = Info
		}
		out = append(out, f)
	}
	return out, ""
}

func checkOwner(in *Input) ([]Finding, string) {
	var out []Finding
	for _, e := range in.Catalog.Plugins {
		if strings.TrimSpace(e.Owner) == "" {
			id := entryID(e)
			out = append(out, Finding{Plugin: id, Message: fmt.Sprintf("%s has no owner", txt(id)),
				Hint: `add owner = "team-or-person" to its sidecar`})
		}
	}
	return out, ""
}

// --- DOC006 standalone skills ---

func checkStandaloneSkills(in *Input) ([]Finding, string) {
	if in.StandaloneSkills == nil {
		return nil, "the skills directory was not read"
	}
	if len(in.Profiles) == 0 {
		return nil, "no profiles were supplied"
	}
	mentioned := map[string]bool{}
	for _, p := range in.Profiles {
		for _, s := range p.StandaloneOff {
			mentioned[s] = true
		}
		for _, s := range p.NameOnly {
			mentioned[s] = true
		}
	}
	var missing, unsafe []string
	seen := map[string]bool{}
	for _, s := range in.StandaloneSkills {
		if mentioned[s] || seen[s] {
			continue
		}
		seen[s] = true
		if skillRe.MatchString(s) && len(s) <= 128 {
			missing = append(missing, s)
		} else {
			unsafe = append(unsafe, txt(s))
		}
	}
	sort.Strings(missing)
	sort.Strings(unsafe)
	var out []Finding
	if len(missing) > 0 {
		quoted := make([]string, len(missing))
		for i, s := range missing {
			quoted[i] = fmt.Sprintf("%q", s)
		}
		out = append(out, Finding{
			Message: fmt.Sprintf("%d standalone skill(s) are not mentioned by any profile and stay visible in all of them: %s",
				len(missing), strings.Join(missing, ", ")),
			Hint: fmt.Sprintf("add to the profiles that should hide them:\n[skills]\noff = [%s]", strings.Join(quoted, ", ")),
		})
	}
	if len(unsafe) > 0 {
		out = append(out, Finding{
			Message: fmt.Sprintf("%d standalone skill(s) have unusual names and are not mentioned by any profile: %s", len(unsafe), strings.Join(unsafe, ", ")),
			Hint:    "rename them, then list them in skills.off",
		})
	}
	return out, ""
}

// --- DOC007 missing upstream ---

func checkMissingUpstream(in *Input) ([]Finding, string) {
	if in.Installed == nil {
		return nil, "installed plugins were not read"
	}
	known := map[string]bool{}
	ix := newIndex(in.Catalog)
	for _, e := range in.Catalog.Plugins {
		known[e.Marketplace] = true
	}
	var out []Finding
	for _, p := range in.Installed {
		if p.Marketplace == "" || !known[p.Marketplace] {
			continue
		}
		if _, ok := ix.byID[p.ID]; ok {
			continue
		}
		f := Finding{Plugin: p.ID, Message: fmt.Sprintf("%s is installed but marketplace %s no longer lists it", txt(p.ID), txt(p.Marketplace))}
		if pluginIDRe.MatchString(p.ID) && !p.RequiredByOrg {
			f.Hint = "claude plugin uninstall " + p.ID
		}
		out = append(out, f)
	}
	return out, ""
}

// --- DOC008 not installed ---

func checkNotInstalled(in *Input) ([]Finding, string) {
	if in.Installed == nil {
		return nil, "installed plugins were not read"
	}
	have := map[string]bool{}
	for _, p := range in.Installed {
		have[p.ID] = true
	}
	var out []Finding
	for _, p := range in.Profiles {
		for _, id := range p.Plugins {
			if have[id] {
				continue
			}
			f := Finding{Plugin: id, Profile: p.Name, Message: fmt.Sprintf("profile %s includes %s, which is not installed", txt(p.Name), txt(id))}
			if pluginIDRe.MatchString(id) {
				f.Hint = "claude plugin install " + id
			}
			out = append(out, f)
		}
	}
	return out, ""
}

// --- DOC009 forced by policy ---

func checkForced(in *Input) ([]Finding, string) {
	if in.Installed == nil {
		return nil, "installed plugins were not read"
	}
	var out []Finding
	for _, p := range in.Installed {
		if !p.RequiredByOrg {
			continue
		}
		var masking []string
		for _, pv := range in.Profiles {
			if masks(pv, p.ID) {
				masking = append(masking, pv.Name)
			}
		}
		sort.Strings(masking)
		msg := fmt.Sprintf("%s is forced on by managed policy and cannot be masked", txt(p.ID))
		if len(masking) > 0 {
			msg += fmt.Sprintf("; profiles that leave it out still run with it: %s", txt(strings.Join(masking, ", ")))
		}
		out = append(out, Finding{Plugin: p.ID, Message: msg})
	}
	return out, ""
}

// masks reports whether a profile would mask a plugin: it excludes it, or it
// runs in allow-only mode (the default) and does not include it.
func masks(p ProfileView, id string) bool {
	for _, x := range p.Exclude {
		if x == id {
			return true
		}
	}
	if p.Mode == "" || p.Mode == "allow-only" {
		for _, x := range p.Plugins {
			if x == id {
				return false
			}
		}
		return true
	}
	return false
}

// --- DOC010 platform review ---

// Lint rules that say a plugin's hooks or MCP definitions are not routed to
// the platform owners.
var coverageCodes = map[string]bool{"CAT042": true, "CAT044": true}

func checkPlatformReview(in *Input) ([]Finding, string) {
	if in.Lint == nil {
		return nil, "no lint report was supplied"
	}
	noCodeowners := false
	uncovered := map[string]string{}
	for _, f := range in.Lint.Findings {
		if f.Code == "CAT043" {
			noCodeowners = true
		}
		if coverageCodes[f.Code] && f.Plugin != "" {
			if _, ok := uncovered[f.Plugin]; !ok {
				uncovered[f.Plugin] = f.Code
			}
		}
	}
	var out []Finding
	for _, e := range in.Catalog.Plugins {
		if !e.NeedsPlatformReview {
			continue
		}
		code, bad := uncovered[e.Name]
		if !bad && !noCodeowners {
			continue
		}
		what := what(e)
		why := "CODEOWNERS does not route it to the platform owners"
		if !bad {
			why = "the repository has no CODEOWNERS file"
		}
		id := entryID(e)
		out = append(out, Finding{Plugin: id,
			Message: fmt.Sprintf("%s ships %s but %s (%s)", txt(id), what, why, codeOrDash(code, bad)),
			Hint:    "give the platform team ownership of the plugin's hooks and .mcp.json in CODEOWNERS and set lint.platform_owners"})
	}
	return out, ""
}

func codeOrDash(code string, bad bool) string {
	if !bad {
		return "CAT043"
	}
	return code
}

func what(e catalog.Entry) string {
	switch {
	case e.HasHooks && e.HasMCP:
		return "hooks and MCP servers"
	case e.HasHooks:
		return "hooks"
	default:
		return "MCP servers"
	}
}

// --- DOC011 protected plugins masked by a profile ---

func checkProtectedMasked(in *Input) ([]Finding, string) {
	if in.Org == nil {
		return nil, "no org config was supplied"
	}
	var out []Finding
	for _, id := range in.Org.Protect.Plugins {
		for _, p := range in.Profiles {
			if !masks(p, id) {
				continue
			}
			f := Finding{Plugin: id, Profile: p.Name,
				Message: fmt.Sprintf("profile %s would mask %s, which the org config protects", txt(p.Name), txt(id))}
			if profileRe.MatchString(p.Name) && pluginIDRe.MatchString(id) {
				f.Hint = fmt.Sprintf("add %q to plugins.include of profile %s", id, p.Name)
			}
			out = append(out, f)
		}
	}
	return out, ""
}

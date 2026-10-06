package lint

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ccshelf/ccshelf/internal/catalog/safepath"
	"github.com/ccshelf/ccshelf/internal/catalog/sidecar"
	"github.com/ccshelf/ccshelf/internal/marketplace"
	"github.com/ccshelf/ccshelf/internal/orgconfig"
)

// Run loads the repository at root and checks it against cfg.
func Run(root string, cfg *orgconfig.Config, opt Options) (*Report, error) {
	d, err := LoadData(root, cfg)
	if err != nil {
		return nil, err
	}
	return Check(d, cfg, opt), nil
}

// checker accumulates findings.
type checker struct {
	d   *Data
	cfg *orgconfig.Config
	opt Options
	out []Finding
}

func (c *checker) add(sev Severity, code, msg string, ref *PluginRef, file string, line int, hint string) {
	f := Finding{Severity: sev, Code: code, Message: msg, File: file, Line: line, Hint: hint}
	if ref != nil {
		f.Plugin = ref.Plugin.Name
		if f.File == "" {
			f.File, f.Line = ref.MarketplaceFile, ref.Line
		}
	}
	c.out = append(c.out, f)
}

// q quotes untrusted text for a message, truncated.
func q(s string) string {
	const max = 80
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = string(r[:max]) + "..."
	}
	return fmt.Sprintf("%q", s)
}

// Check applies every rule to loaded data and returns the sorted report.
func Check(d *Data, cfg *orgconfig.Config, opt Options) *Report {
	c := &checker{d: d, cfg: cfg, opt: opt, out: append([]Finding(nil), d.Findings...)}
	names := map[string]*PluginRef{}
	for i := range d.Plugins {
		if !d.Plugins[i].Dup {
			names[d.Plugins[i].Plugin.Name] = &d.Plugins[i]
		}
	}
	for i := range d.Plugins {
		ref := &d.Plugins[i]
		c.entry(ref)
		c.source(ref)
		c.metadata(ref, names)
		c.relevance(ref)
		c.dependencies(ref, names)
		c.ownership(ref)
	}
	c.orphanSidecars(names)
	c.codeownersGeneral()
	c.profiles()
	r := &Report{Findings: c.out}
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	r.Sort()
	return r
}

// entry checks the marketplace entry itself.
func (c *checker) entry(ref *PluginRef) {
	p := ref.Plugin
	if ref.Dup {
		c.add(Error, "CAT002", "duplicate plugin name "+q(p.Name), ref, "", 0, "plugin names must be unique across the marketplaces of one catalog")
	}
	if reason, isErr := marketplace.ReservedNameCheck(p.Name); reason != "" {
		if isErr {
			c.add(Error, "CAT003", fmt.Sprintf("plugin %s: %s", q(p.Name), reason), ref, "", 0, "claude plugin validate rejects this name")
		} else {
			c.add(Warning, "CAT004", fmt.Sprintf("plugin %s: %s", q(p.Name), reason), ref, "", 0, "")
		}
	}
	desc := strings.TrimSpace(p.Description)
	switch {
	case desc == "":
		hint := "describe what the plugin does in one or two sentences"
		if !p.Source.IsLocal() {
			hint = "external-source plugins need a description because their contents cannot be inspected"
		}
		c.add(Error, "CAT005", fmt.Sprintf("plugin %s has no description", q(p.Name)), ref, "", 0, hint)
	case utf8.RuneCountInString(desc) < c.cfg.Lint.MinDescriptionLength:
		c.add(Warning, "CAT006", fmt.Sprintf("plugin %s: description has %d characters, the minimum is %d",
			q(p.Name), utf8.RuneCountInString(desc), c.cfg.Lint.MinDescriptionLength), ref, "", 0, "")
	}
	if p.Author.IsZero() {
		c.add(Warning, "CAT007", fmt.Sprintf("plugin %s has no author", q(p.Name)), ref, "", 0, "")
	}
}

// source checks the local path or notes an external source, then the
// inspection result (hooks, MCP).
func (c *checker) source(ref *PluginRef) {
	p := ref.Plugin
	if !p.Source.IsLocal() {
		c.add(Info, "CAT031", fmt.Sprintf("plugin %s comes from %s; its contents cannot be inspected", q(p.Name), q(p.Source.Summary())), ref, "", 0, "")
		return
	}
	if ref.InfoErr != nil {
		err := ref.InfoErr
		switch {
		case errors.Is(err, safepath.ErrEscape):
			c.add(Error, "CAT032", fmt.Sprintf("plugin %s: source %s is not a path inside the repository: %v", q(p.Name), q(p.Source.Declared), err), ref, "", 0,
				"use a relative path such as ./plugins/<name>")
		case errors.Is(err, fs.ErrNotExist):
			c.add(Error, "CAT030", fmt.Sprintf("plugin %s: source directory %s does not exist", q(p.Name), q(p.Source.Declared)), ref, "", 0, "")
		default:
			c.add(Error, "CAT033", fmt.Sprintf("plugin %s: %v", q(p.Name), err), ref, "", 0, "")
		}
		return
	}
	if ref.Info == nil {
		return
	}
	if ref.Info.HasHooks {
		c.add(Info, "CAT040", fmt.Sprintf("plugin %s ships hooks", q(p.Name)), ref, "", 0, "hooks run commands on developers' machines; they need platform review")
	}
	if ref.Info.HasMCP {
		c.add(Info, "CAT041", fmt.Sprintf("plugin %s ships MCP servers", q(p.Name)), ref, "", 0, "MCP servers run code on developers' machines; they need platform review")
	}
}

// metadata checks the sidecar rules for one plugin.
func (c *checker) metadata(ref *PluginRef, names map[string]*PluginRef) {
	if ref.Dup {
		return
	}
	p := ref.Plugin
	sc := c.d.Sidecars[p.Name]
	single := c.cfg.Catalog.MetadataSource == orgconfig.SourceMarketplace

	// Taxonomy applies to the native fields, with or without a sidecar.
	if tx := c.d.Taxonomy; tx != nil {
		if p.Category != "" && !tx.HasCategory(p.Category) && !(ref.IsBundle() && p.Category == "profile") {
			c.add(Error, "CAT021", fmt.Sprintf("plugin %s: category %s is not in %s", q(p.Name), q(p.Category), tx.File), ref, "", 0, "add it to the taxonomy or use an existing category")
		}
		for _, t := range p.Tags {
			if !tx.HasTag(t) {
				c.add(Error, "CAT022", fmt.Sprintf("plugin %s: tag %s is not in %s", q(p.Name), q(t), tx.File), ref, "", 0, "add it to the taxonomy or use an existing tag")
			}
		}
	}

	if sc == nil {
		if ref.IsBundle() {
			return
		}
		if single {
			c.add(Error, "CAT010", fmt.Sprintf("plugin %s has no metadata object", q(p.Name)), ref, "", 0, "single-file mode reads owner, status and the other fields from the entry's metadata")
		} else {
			c.add(Error, "CAT010", fmt.Sprintf("plugin %s has no sidecar", q(p.Name)), ref, "", 0, "create "+path.Join(sidecar.Dir, p.Name+".toml"))
		}
		return
	}
	at := func(field string) (string, int) { return sc.File, sc.LineOf(field) }
	addAt := func(sev Severity, code, field, msg, hint string) {
		f, l := at(field)
		c.add(sev, code, msg, ref, f, l, hint)
	}

	status := sc.Status
	empty := func(field string) bool {
		switch field {
		case "owner":
			return strings.TrimSpace(sc.Owner) == ""
		case "status":
			return sc.Status == ""
		case "when_to_use":
			return len(sc.WhenToUse) == 0
		case "avoid_when":
			return len(sc.AvoidWhen) == 0
		case "overlaps_with":
			return len(sc.OverlapsWith) == 0
		case "superseded_by":
			return sc.SupersededBy == ""
		case "review_by":
			return sc.ReviewBy == ""
		case "support":
			return strings.TrimSpace(sc.Support) == ""
		case "docs":
			return sc.Docs == ""
		}
		return false
	}
	for _, f := range c.cfg.Lint.Require {
		if f == "review_by" && status == sidecar.StatusActive {
			continue // reported by CAT018
		}
		if empty(f) {
			addAt(Error, "CAT013", f, fmt.Sprintf("plugin %s: required field %s is missing or empty", q(p.Name), f), "")
		}
	}
	if status != "" && !sidecar.ValidStatus(status) {
		addAt(Error, "CAT014", "status", fmt.Sprintf("plugin %s: status %s is not active, experimental or deprecated", q(p.Name), q(status)), "")
	}
	if status == sidecar.StatusDeprecated {
		for _, f := range c.cfg.Lint.RequireWhenDeprecated {
			if empty(f) {
				addAt(Error, "CAT015", f, fmt.Sprintf("plugin %s is deprecated and needs %s", q(p.Name), f), "")
			}
		}
	}
	if sc.SupersededBy != "" {
		c.supersession(ref, sc, names, addAt)
	}
	for _, o := range sc.OverlapsWith {
		if o == p.Name {
			addAt(Error, "CAT023", "overlaps_with", fmt.Sprintf("plugin %s lists itself in overlaps_with", q(p.Name)), "")
		} else if names[o] == nil {
			addAt(Error, "CAT023", "overlaps_with", fmt.Sprintf("plugin %s: overlaps_with names %s, which is not in the marketplace", q(p.Name), q(o)), "")
		}
	}
	c.review(ref, sc, status, addAt)
	if sc.Docs != "" && !IsHTTPURL(sc.Docs) {
		addAt(Error, "CAT028", "docs", fmt.Sprintf("plugin %s: docs %s is not an http or https URL", q(p.Name), q(sc.Docs)), "only http and https links are rendered in the catalog")
	}
}

func (c *checker) supersession(ref *PluginRef, sc *sidecar.Sidecar, names map[string]*PluginRef, addAt func(Severity, string, string, string, string)) {
	p := ref.Plugin
	target := sc.SupersededBy
	switch {
	case target == p.Name:
		addAt(Error, "CAT017", "superseded_by", fmt.Sprintf("plugin %s is superseded by itself", q(p.Name)), "")
		return
	case names[target] == nil:
		addAt(Error, "CAT016", "superseded_by", fmt.Sprintf("plugin %s: superseded_by names %s, which is not in the marketplace", q(p.Name), q(target)), "")
		return
	}
	if ts := c.d.Sidecars[target]; ts != nil && ts.Status == sidecar.StatusDeprecated {
		// Walk the chain to say whether it loops back.
		cur, seen := target, map[string]bool{p.Name: true}
		loop := false
		for cur != "" && !seen[cur] {
			seen[cur] = true
			next := c.d.Sidecars[cur]
			if next == nil || next.Status != sidecar.StatusDeprecated {
				break
			}
			cur = next.SupersededBy
		}
		if cur == p.Name {
			loop = true
		}
		msg := fmt.Sprintf("plugin %s: superseded_by %s is itself deprecated", q(p.Name), q(target))
		if loop {
			msg = fmt.Sprintf("plugin %s: superseded_by %s leads back to the plugin (cycle)", q(p.Name), q(target))
		}
		addAt(Error, "CAT017", "superseded_by", msg, "point to the plugin that replaces the whole chain")
	}
}

func (c *checker) review(ref *PluginRef, sc *sidecar.Sidecar, status string, addAt func(Severity, string, string, string, string)) {
	p := ref.Plugin
	date, present, err := sc.ReviewDate()
	switch {
	case err != nil:
		addAt(Error, "CAT018", "review_by", fmt.Sprintf("plugin %s: %v", q(p.Name), err), "")
		return
	case !present:
		if status == sidecar.StatusActive {
			addAt(Error, "CAT018", "review_by", fmt.Sprintf("plugin %s is active and needs a review_by date", q(p.Name)), "")
		}
		return
	}
	if status == sidecar.StatusDeprecated {
		return
	}
	now := c.opt.now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if !date.Before(today) {
		return
	}
	age := int(today.Sub(date).Hours() / 24)
	if age > c.cfg.Lint.MaxReviewAgeDays {
		addAt(Warning, "CAT019", "review_by", fmt.Sprintf("plugin %s: review_by %s is %d days ago, the limit is %d (stale)", q(p.Name), sc.ReviewBy, age, c.cfg.Lint.MaxReviewAgeDays),
			"confirm the entry is still right and set a new review_by date")
	} else {
		addAt(Info, "CAT020", "review_by", fmt.Sprintf("plugin %s: review_by %s passed %d days ago", q(p.Name), sc.ReviewBy, age), "")
	}
}

func (c *checker) orphanSidecars(names map[string]*PluginRef) {
	if c.cfg.Catalog.MetadataSource == orgconfig.SourceMarketplace {
		return
	}
	for name, sc := range c.d.Sidecars {
		if names[name] == nil {
			c.out = append(c.out, Finding{Severity: Warning, Code: "CAT011", Message: fmt.Sprintf("sidecar for %s has no marketplace entry", q(name)),
				File: sc.File, Line: sc.Line, Plugin: name})
		}
	}
}

// Relevance limits (marketplace reference).
var relevanceLimits = []struct {
	name       string
	maxEntries int
	maxLen     int
}{
	{"cwd", 10, 256}, {"cli", 10, 64}, {"hosts", 20, 128}, {"filesRead", 10, 256}, {"manifestDeps", 10, 256},
}

func (c *checker) relevance(ref *PluginRef) {
	rel := ref.Plugin.Relevance
	if rel == nil {
		return
	}
	p := ref.Plugin
	lists := map[string][]string{"cwd": rel.Signals.Cwd, "cli": rel.Signals.CLI, "hosts": rel.Signals.Hosts, "filesRead": rel.Signals.FilesRead}
	for _, l := range relevanceLimits {
		var entries []string
		if l.name == "manifestDeps" {
			for _, m := range rel.Signals.ManifestDeps {
				entries = append(entries, m.Pattern)
			}
		} else {
			entries = lists[l.name]
		}
		if len(entries) > l.maxEntries {
			c.add(Error, "CAT025", fmt.Sprintf("plugin %s: relevance.signals.%s has %d entries, the limit is %d", q(p.Name), l.name, len(entries), l.maxEntries), ref, "", 0, "")
		}
		for _, e := range entries {
			if n := utf8.RuneCountInString(e); n > l.maxLen {
				c.add(Error, "CAT025", fmt.Sprintf("plugin %s: a relevance.signals.%s entry has %d characters, the limit is %d", q(p.Name), l.name, n, l.maxLen), ref, "", 0, "")
			}
		}
	}
	for _, m := range rel.Signals.ManifestDeps {
		if m.Pattern == "" {
			continue
		}
		feature := NonRE2Feature(m.Pattern)
		_, err := regexp.Compile(m.Pattern)
		switch {
		case feature != "":
			c.add(Warning, "CAT027", fmt.Sprintf("plugin %s: pattern %s uses %s, which Go's RE2 lacks", q(p.Name), q(m.Pattern), feature), ref, "", 0,
				"fine if Claude Code evaluates it as a JavaScript regular expression; this tool cannot check it")
		case err != nil:
			c.add(Error, "CAT026", fmt.Sprintf("plugin %s: pattern %s is not a valid regular expression: %v", q(p.Name), q(m.Pattern), err), ref, "", 0, "")
		}
	}
}

// NonRE2Feature names a regular-expression feature that JavaScript supports
// and Go's RE2 does not, or returns "" when none is found. It scans the
// pattern textually and understands backslash escapes; it does not parse
// character classes, so it can report a false positive inside [...].
func NonRE2Feature(p string) string {
	for i := 0; i < len(p); i++ {
		switch ch := p[i]; ch {
		case '\\':
			if i+1 < len(p) {
				n := p[i+1]
				if n >= '1' && n <= '9' {
					return "a backreference"
				}
				if n == 'k' && i+2 < len(p) && p[i+2] == '<' {
					return "a named backreference"
				}
				i++
			}
		case '(':
			rest := p[i:]
			for _, f := range []struct{ prefix, name string }{
				{"(?=", "lookahead"}, {"(?!", "negative lookahead"}, {"(?<=", "lookbehind"}, {"(?<!", "negative lookbehind"}, {"(?>", "an atomic group"},
			} {
				if strings.HasPrefix(rest, f.prefix) {
					return f.name
				}
			}
		case '*', '+', '?', '}':
			if i+1 < len(p) && p[i+1] == '+' && (ch != '}' || strings.Contains(p[:i], "{")) {
				return "a possessive quantifier"
			}
		}
	}
	return ""
}

func (c *checker) dependencies(ref *PluginRef, names map[string]*PluginRef) {
	if ref.Dup {
		return
	}
	p := ref.Plugin
	deps := append([]marketplace.Dependency(nil), p.Dependencies...)
	if ref.Info != nil {
		deps = append(deps, ref.Info.Dependencies...)
	}
	local := map[string]bool{}
	other := map[string]map[string]bool{}
	for _, m := range c.d.Marketplaces {
		set := map[string]bool{}
		for _, mp := range m.Plugins {
			set[mp.Name] = true
		}
		if m.Name == ref.MarketplaceName {
			local = set
		}
		other[m.Name] = set
	}
	var allowed []string
	for _, m := range c.d.Marketplaces {
		if m.Name == ref.MarketplaceName {
			allowed = m.AllowCrossMarketplaceDependenciesOn
		}
	}
	seenDep := map[marketplace.Dependency]bool{}
	for _, dep := range deps {
		dep.Version = ""
		if seenDep[dep] {
			continue
		}
		seenDep[dep] = true
		if dep.Name == "" {
			c.add(Error, "CAT024", fmt.Sprintf("plugin %s has a dependency without a name", q(p.Name)), ref, "", 0, "")
			continue
		}
		switch {
		case dep.Marketplace == "" || dep.Marketplace == ref.MarketplaceName:
			if !local[dep.Name] {
				c.add(Error, "CAT024", fmt.Sprintf("plugin %s depends on %s, which is not in the marketplace", q(p.Name), q(dep.Name)), ref, "", 0, "")
			}
		case other[dep.Marketplace] != nil:
			if !other[dep.Marketplace][dep.Name] {
				c.add(Error, "CAT024", fmt.Sprintf("plugin %s depends on %s@%s, which is not in that marketplace", q(p.Name), q(dep.Name), q(dep.Marketplace)), ref, "", 0, "")
			}
		default:
			ok := false
			for _, a := range allowed {
				if a == dep.Marketplace {
					ok = true
				}
			}
			if !ok {
				c.add(Error, "CAT024", fmt.Sprintf("plugin %s depends on %s in marketplace %s, which is not listed in allowCrossMarketplaceDependenciesOn", q(p.Name), q(dep.Name), q(dep.Marketplace)), ref, "", 0,
					"add the marketplace to allowCrossMarketplaceDependenciesOn or remove the dependency")
			}
		}
	}
}

// ownership applies the CODEOWNERS rules that concern one plugin.
func (c *checker) ownership(ref *PluginRef) {
	dir := ref.PluginDir()
	if dir == "" || dir == "." || ref.InfoErr != nil || ref.Dup {
		return
	}
	p := ref.Plugin
	plat := c.cfg.Lint.PlatformOwners
	hasHooks := ref.Info != nil && ref.Info.HasHooks
	hasMCP := ref.Info != nil && ref.Info.HasMCP
	co := c.d.Owners
	if co == nil {
		if len(plat) > 0 && (hasHooks || hasMCP) {
			c.add(Error, "CAT042", fmt.Sprintf("plugin %s ships hooks or MCP servers but there is no CODEOWNERS file", q(p.Name)), ref, "", 0, "add a CODEOWNERS rule that gives the platform team these paths")
		}
		return
	}
	probe := dir + "/.claude-plugin/plugin.json"
	owners := co.Owners(probe)
	if len(owners) == 0 {
		c.add(Warning, "CAT044", fmt.Sprintf("plugin %s: directory %s/ is not covered by CODEOWNERS", q(p.Name), dir), ref, "", 0, "add a line such as /"+dir+"/ @team")
	} else if sc := c.d.Sidecars[p.Name]; sc != nil && sc.Owner != "" && !containsFold(owners, sc.Owner) {
		c.out = append(c.out, Finding{Severity: Warning, Code: "CAT046", Plugin: p.Name, File: sc.File, Line: sc.LineOf("owner"),
			Message: fmt.Sprintf("plugin %s: owner %s is not among the CODEOWNERS owners of %s/ (%s)", q(p.Name), q(sc.Owner), dir, strings.Join(owners, ", "))})
	}
	if len(plat) > 0 {
		check := func(present bool, rel, what string) {
			if !present {
				return
			}
			got := co.Owners(dir + "/" + rel)
			for _, g := range got {
				if containsFold(plat, g) {
					return
				}
			}
			c.add(Error, "CAT042", fmt.Sprintf("plugin %s ships %s but CODEOWNERS does not give a platform owner %s/%s (owners: %s)", q(p.Name), what, dir, rel, ownersText(got)), ref, "", 0,
				"add a rule for /"+dir+"/"+rel+" after the team rule, because the last matching rule wins")
		}
		check(hasHooks, "hooks/hooks.json", "hooks")
		check(hasMCP, ".mcp.json", "MCP servers")
	}
}

func ownersText(o []string) string {
	if len(o) == 0 {
		return "none"
	}
	return strings.Join(o, ", ")
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func (c *checker) codeownersGeneral() {
	co := c.d.Owners
	if co == nil {
		if len(c.d.Plugins) > 0 {
			c.out = append(c.out, Finding{Severity: Warning, Code: "CAT043", Message: "no CODEOWNERS file found (looked in .github/, the root and docs/)",
				Hint: "CODEOWNERS routes review of plugins, the catalog metadata and workflows"})
		}
		return
	}
	for _, is := range co.Issues {
		c.out = append(c.out, Finding{Severity: Warning, Code: "CAT047", Message: is.Message, File: c.d.OwnersPath, Line: is.Line, Hint: "GitHub ignores this line"})
	}
	if !co.Covers(".github/CODEOWNERS") {
		c.out = append(c.out, Finding{Severity: Warning, Code: "CAT045", Message: "/.github/ is not covered by CODEOWNERS", File: c.d.OwnersPath,
			Hint: "without it a plugin team can edit workflows and CODEOWNERS itself"})
	}
}

// profiles checks the profile manifest and bundle entry correspondence using
// file names only.
func (c *checker) profiles() {
	profileSet := map[string]bool{}
	for _, n := range c.d.Profiles {
		profileSet[n] = true
	}
	byName := map[string]*PluginRef{}
	for i := range c.d.Plugins {
		if !c.d.Plugins[i].Dup {
			byName[c.d.Plugins[i].Plugin.Name] = &c.d.Plugins[i]
		}
	}
	for _, n := range c.d.Profiles {
		ref := byName[BundlePrefix+n]
		file := path.Join(c.cfg.Profiles.Dir, n+".toml")
		if ref == nil {
			c.out = append(c.out, Finding{Severity: Error, Code: "CAT050", Plugin: BundlePrefix + n, File: file,
				Message: fmt.Sprintf("profile %s has no bundle entry %s in the marketplace", q(n), q(BundlePrefix+n)),
				Hint:    `add {"name": "` + BundlePrefix + n + `", "source": "./bundles/` + BundlePrefix + n + `", "category": "profile"} to marketplace.json`})
		}
	}
	for i := range c.d.Plugins {
		ref := &c.d.Plugins[i]
		if ref.Dup {
			continue
		}
		name := ref.Plugin.Name
		isBundle := ref.IsBundle()
		src := ref.Plugin.Source
		underBundles := src.IsLocal() && (src.Path == "bundles" || strings.HasPrefix(src.Path, "bundles/"))
		if !isBundle && !underBundles {
			continue
		}
		if isBundle && !profileSet[strings.TrimPrefix(name, BundlePrefix)] {
			c.add(Error, "CAT051", fmt.Sprintf("bundle entry %s has no profile manifest %s", q(name), path.Join(c.cfg.Profiles.Dir, strings.TrimPrefix(name, BundlePrefix)+".toml")), ref, "", 0, "")
		}
		want := "bundles/" + name
		if !isBundle || !src.IsLocal() || src.Path != want {
			c.add(Error, "CAT052", fmt.Sprintf("bundle entry %s must have the source ./%s, found %s", q(name), want, q(src.Summary())), ref, "", 0, "")
		}
		if ref.Plugin.Version != "" {
			c.add(Error, "CAT053", fmt.Sprintf("bundle entry %s sets version %s; leave it out so the commit SHA is the version", q(name), q(ref.Plugin.Version)), ref, "", 0, "decision D-17")
		}
	}
}

// IsHTTPURL reports whether s is an absolute http or https URL with a host
// and no control characters or spaces.
func IsHTTPURL(s string) bool {
	if s == "" || len(s) > 2048 {
		return false
	}
	for _, r := range s {
		if r <= ' ' || r == 0x7f {
			return false
		}
	}
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil
}

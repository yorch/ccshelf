package scaffold

import (
	"fmt"
	"strings"

	"github.com/yorch/ccshelf/internal/ui"
)

// addConfig plans ccshelf.toml.
func (b *builder) addConfig() {
	b.file(pathConfig, GroupConfig, kindPlain, func(strict bool) ([]byte, bool) {
		pl, ok := b.platformForText(strict)
		if !ok {
			return nil, false
		}
		return b.renderConfig(pl), true
	}, nil)
}

// renderConfig writes ccshelf.toml. Every string goes through tomlString.
func (b *builder) renderConfig(platform []string) []byte {
	if b.p.ProfilesOnly {
		return b.renderProfilesOnlyConfig(platform)
	}
	var w strings.Builder
	w.WriteString("# Org configuration for ccshelf. \"ccshelf catalog init\" generated this file. Edit it freely.\n")
	w.WriteString("# Every key is optional. See the ccshelf documentation (catalog and org repo).\n\n")
	w.WriteString("[lint]\n")
	w.WriteString("# Sidecar fields every plugin needs. A missing one is a lint error. Add \"review_by\" to\n")
	w.WriteString("# make every active plugin carry a review date.\n")
	w.WriteString("require = [\"owner\", \"status\"]\n")
	w.WriteString("# CODEOWNERS owners that must own everything that runs code or shapes the catalog (hooks,\n")
	w.WriteString("# MCP, LSP, plugin manifests that declare them, workflows, profiles, bundles, this file).\n")
	w.WriteString("platform_owners = " + tomlArray(platform) + "\n")
	w.WriteString("require_when_deprecated = [\"superseded_by\"]\n")
	w.WriteString("max_review_age_days = 180\n")
	w.WriteString("# Create this file to restrict plugin categories and tags to a list. Without it, the rule is off.\n")
	w.WriteString("# taxonomy = \"catalog/taxonomy.toml\"\n\n")
	w.WriteString("[catalog]\n")
	if org := b.p.org(b.name); org != "" {
		w.WriteString("title = " + tomlString(org+" plugin catalog") + "\n")
	} else {
		w.WriteString("# title = \"My organization plugin catalog\"\n")
	}
	w.WriteString("metadata_source = \"sidecar\"\n")
	w.WriteString("marketplaces = [" + tomlString(pathMarketplace) + "]\n\n")
	w.WriteString("[profiles]\n")
	w.WriteString("dir = \"profiles\"\n")
	w.WriteString("mcp_registry = \"mcp/registry.toml\"\n\n")
	w.WriteString("# Controls that no profile may mask. Fill these in once you know them.\n")
	w.WriteString("[protect]\n")
	mk := b.name
	if mk == "" {
		mk = "marketplace"
	}
	w.WriteString("# plugins = " + tomlArray([]string{"audit-logger@" + mk}) + "\n")
	w.WriteString("# mcp = [\"plugin:audit:audit\"]\n")
	return []byte(w.String())
}

// renderProfilesOnlyConfig writes the ccshelf.toml of a repo without a
// marketplace: the catalog is switched off, and only the keys that still apply
// are written.
func (b *builder) renderProfilesOnlyConfig(platform []string) []byte {
	var w strings.Builder
	w.WriteString("# Org configuration for ccshelf. \"ccshelf catalog init --profiles-only\" generated this file. Edit it freely.\n")
	w.WriteString("# Every key is optional. See the ccshelf documentation (catalog and org repo).\n\n")
	w.WriteString("[lint]\n")
	w.WriteString("# CODEOWNERS owners that must own everything that shapes what runs on developer machines\n")
	w.WriteString("# (profiles, the MCP registry, prompts, workflows, this file).\n")
	w.WriteString("platform_owners = " + tomlArray(platform) + "\n\n")
	w.WriteString("[catalog]\n")
	w.WriteString("# This repo holds profiles only: no plugin marketplace, no sidecars, no bundles and no catalog.\n")
	w.WriteString("# ccshelf lint checks the profiles, the MCP registry and ownership. The compile and catalog build commands refuse to run.\n")
	w.WriteString("enabled = false\n\n")
	w.WriteString("[profiles]\n")
	w.WriteString("dir = \"profiles\"\n")
	w.WriteString("mcp_registry = \"mcp/registry.toml\"\n\n")
	w.WriteString("# Controls that no profile may mask. Fill these in once you know them.\n")
	w.WriteString("[protect]\n")
	w.WriteString("# plugins = " + tomlArray([]string{"audit-logger@your-marketplace"}) + "\n")
	w.WriteString("# mcp = [\"plugin:audit:audit\"]\n")
	return []byte(w.String())
}

type mpAuthor struct {
	Name string `json:"name"`
}

type mpPlugin struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	Description string   `json:"description"`
	Author      mpAuthor `json:"author"`
}

type mpOwner struct {
	Name string `json:"name"`
}

type mpFile struct {
	Name        string     `json:"name"`
	Owner       mpOwner    `json:"owner"`
	Description string     `json:"description"`
	Plugins     []mpPlugin `json:"plugins"`
}

// addMarketplace plans .claude-plugin/marketplace.json, which is never rewritten.
func (b *builder) addMarketplace() error {
	b.file(pathMarketplace, GroupMarketplace, kindNever, func(strict bool) ([]byte, bool) {
		if b.name == "" {
			if strict {
				b.need("--marketplace-name", "the name of the marketplace, lower case letters, digits and hyphens")
			}
			return nil, false
		}
		org := b.p.org(b.name)
		f := mpFile{Name: b.name, Owner: mpOwner{Name: org}, Description: org + " Claude Code plugins.", Plugins: []mpPlugin{}}
		for _, t := range b.targets {
			if t.Dir == "" {
				continue
			}
			desc := t.Description
			if desc == "" {
				desc = Placeholder + ": describe what " + t.Name + " does"
			}
			author := t.Author
			if author == "" {
				author = org
			}
			f.Plugins = append(f.Plugins, mpPlugin{Name: t.Name, Source: "./" + t.Dir, Description: desc, Author: mpAuthor{Name: author}})
		}
		data, err := jsonIndent(f)
		return data, err == nil
	}, nil)
	return nil
}

// defaultOwner returns the owner for sidecars that no CODEOWNERS line names.
func (b *builder) defaultOwner(strict bool) (string, bool) {
	if b.p.Owner != "" {
		return b.p.Owner, true
	}
	if pl := b.platform(); len(pl) > 0 {
		return pl[0], true
	}
	if strict {
		if b.willWrite(pathConfig, GroupConfig) || b.willWrite(b.coPath, GroupCodeowners) {
			b.need("--platform-owners", "who owns what runs code on developer machines or shapes the catalog (also the default plugin owner)")
		} else {
			b.need("--owner", "the default owner of the plugin sidecars: @user, @org/team or an email address")
		}
	}
	return "", false
}

// targetOwner is the owner of a plugin: the one an existing CODEOWNERS file
// names for its directory, else the default.
func (b *builder) targetOwner(t target, strict bool) (string, bool) {
	if b.co != nil && t.Dir != "" {
		for _, o := range b.co.Owners(t.Dir + "/README.md") {
			if ValidOwner(o) {
				return o, true
			}
		}
	}
	return b.defaultOwner(strict)
}

// addSidecars plans one stub per target plugin.
func (b *builder) addSidecars() {
	if !b.p.Enabled(GroupSidecars) {
		return
	}
	existing := map[string]string{} // lower-cased name -> name, of the sidecars already there
	if ents, err := b.fs.ReadDir(sidecarDir); err == nil {
		for _, e := range ents {
			existing[strings.ToLower(e.Name())] = e.Name()
		}
	}
	for _, t := range b.targets {
		path := sidecarDir + "/" + t.Name + ".toml"
		if have, ok := existing[strings.ToLower(path[len(sidecarDir)+1:])]; ok && have != t.Name+".toml" {
			b.plan.Entries = append(b.plan.Entries, Entry{
				Path: path, Group: GroupSidecars, Action: actionConflict,
				Reason: "differs only in letter case from the existing " + sidecarDir + "/" + ui.SanitizeLine(have) + " (one file on Windows and macOS): rename one of them",
			})
			continue
		}
		b.file(path, GroupSidecars, kindPlain, func(strict bool) ([]byte, bool) {
			owner, ok := b.targetOwner(t, strict)
			if !ok {
				return nil, false
			}
			if strict {
				b.addTodoOnce("fill in the catalog sidecars: replace every " + Placeholder + " value (ccshelf lint reports them as CAT048) and set status to active once an owner has reviewed the entry")
			}
			return sidecarStub(t.Name, owner, b.cfg.Lint.Require), true
		}, nil)
	}
}

// sidecarStub writes the placeholder sidecar of a plugin. Fields that
// lint.require demands and that can carry a placeholder get one; the others
// (a date, a plugin name, a URL) are left out, so that lint says exactly what
// is missing.
func sidecarStub(name, owner string, require []string) []byte {
	req := map[string]bool{}
	for _, r := range require {
		req[r] = true
	}
	var w strings.Builder
	w.WriteString("# Catalog metadata of " + name + ", generated by \"ccshelf catalog init\".\n")
	w.WriteString("# Replace every " + Placeholder + " value: ccshelf lint warns (CAT048) until none is left.\n")
	w.WriteString("owner = " + tomlString(owner) + "\n")
	w.WriteString("# A new entry is experimental until its owner has reviewed it: set active (and review_by) then.\n")
	w.WriteString("status = \"experimental\"\n")
	w.WriteString("when_to_use = " + tomlArray([]string{Placeholder + ": when someone should pick this plugin"}) + "\n")
	if req["avoid_when"] {
		w.WriteString("avoid_when = " + tomlArray([]string{Placeholder + ": when they should not"}) + "\n")
	}
	if req["support"] {
		w.WriteString("support = " + tomlString(Placeholder+": where to ask for help") + "\n")
	}
	return []byte(w.String())
}

type ruleKind int

const (
	ruleCatchAll ruleKind = iota
	ruleTeam
	rulePlatform
)

type coRule struct {
	kind    ruleKind
	pattern string
	owners  []string
	// probes are paths the rule must own for an existing CODEOWNERS file to
	// count as covering it; a pattern with "plugins/*" is probed once per
	// plugin directory.
	probes []string
}

func (r coRule) line() string {
	return fmt.Sprintf("%-34s %s", r.pattern, strings.Join(r.owners, " "))
}

// platformRules are the rules that the platform owners hold, in the order of
// the starter template; the last four come last on purpose, because GitHub
// applies the last matching rule and a plugin team's directory rule must not
// win over them.
func platformRules(pl, dirs []string, profilesOnly bool) (general, exec []coRule) {
	g := func(pattern, probe string) coRule {
		r := coRule{kind: rulePlatform, pattern: pattern, owners: pl}
		if !strings.Contains(probe, "plugins/x/") {
			r.probes = []string{probe}
			return r
		}
		for _, d := range dirs {
			r.probes = append(r.probes, strings.Replace(probe, "plugins/x", d, 1))
		}
		if len(r.probes) == 0 {
			r.probes = []string{probe}
		}
		return r
	}
	if profilesOnly {
		return []coRule{
			g("/profiles/", "profiles/x.toml"),
			g("/mcp/", "mcp/registry.toml"),
			g("/prompts/", "prompts/x.md"),
			g("/ccshelf.toml", "ccshelf.toml"),
			g("/docs/", "docs/x.md"),
			g("/.github/", ".github/workflows/x.yml"),
		}, nil
	}
	general = []coRule{
		g("/.claude-plugin/marketplace.json", ".claude-plugin/marketplace.json"),
		g("/bundles/", "bundles/profile-x/.claude-plugin/plugin.json"),
		g("/catalog/", "catalog/plugins/x.toml"),
		g("/profiles/", "profiles/x.toml"),
		g("/mcp/", "mcp/registry.toml"),
		g("/prompts/", "prompts/x.md"),
		g("/ccshelf.toml", "ccshelf.toml"),
		g("/docs/", "docs/x.md"),
		g("/.github/", ".github/workflows/x.yml"),
	}
	exec = []coRule{
		g("/plugins/*/hooks/", "plugins/x/hooks/hooks.json"),
		g("/plugins/*/.mcp.json", "plugins/x/.mcp.json"),
		g("/plugins/*/.lsp.json", "plugins/x/.lsp.json"),
		g("/plugins/*/.claude-plugin/", "plugins/x/.claude-plugin/plugin.json"),
	}
	return general, exec
}

// codeownersRules builds the full rule list for the platform owners pl.
func (b *builder) codeownersRules(pl []string, strict bool) (catchAll coRule, team []coRule, general, exec []coRule, ok bool) {
	catchAll = coRule{kind: ruleCatchAll, pattern: "*", owners: pl, probes: []string{"ccshelf-new-file.txt"}}
	var dirs []string
	for _, t := range b.targets {
		if t.Dir != "" {
			dirs = append(dirs, t.Dir)
		}
	}
	for _, t := range b.targets {
		if t.Dir == "" {
			continue
		}
		owner, ok := b.targetOwner(t, strict)
		if !ok {
			return catchAll, nil, nil, nil, false
		}
		team = append(team, coRule{kind: ruleTeam, pattern: "/" + t.Dir + "/", owners: []string{owner}, probes: []string{t.Dir + "/README.md"}})
	}
	general, exec = platformRules(pl, dirs, b.p.ProfilesOnly)
	return catchAll, team, general, exec, true
}

// addCodeowners plans .github/CODEOWNERS (or the CODEOWNERS file that exists
// at another location GitHub reads).
func (b *builder) addCodeowners() {
	gen := func(strict bool) ([]byte, bool) {
		pl, ok := b.platformForText(strict)
		if !ok {
			return nil, false
		}
		ca, team, general, exec, ok := b.codeownersRules(pl, strict)
		if !ok {
			return nil, false
		}
		var w strings.Builder
		w.WriteString("# \"ccshelf catalog init\" generated this file. Edit it freely: nothing regenerates it.\n")
		if b.p.ProfilesOnly {
			w.WriteString("# GitHub applies the LAST matching pattern, so the catch-all comes first and narrower rules\n")
			w.WriteString("# come after it.\n\n")
		} else {
			w.WriteString("# GitHub applies the LAST matching pattern, so the catch-all comes first, the team rules\n")
			w.WriteString("# after it, and the platform team's narrower rules for code that runs on developer\n")
			w.WriteString("# machines come last and win.\n\n")
		}
		w.WriteString("# Catch-all: anything not matched below (new top-level files, new directories) is the platform team's.\n")
		w.WriteString(ca.line() + "\n\n")
		if b.p.ProfilesOnly {
			w.WriteString("# The platform team reviews everything that shapes what runs on developers' machines: profiles,\n")
			w.WriteString("# the MCP registry, prompts, workflows and this configuration.\n")
			for _, r := range general {
				w.WriteString(r.line() + "\n")
			}
			return []byte(w.String()), true
		}
		w.WriteString("# Each team owns its plugin source: add \"/plugins/<name>/ @your-team\" for every plugin.\n")
		for _, r := range team {
			w.WriteString(r.line() + "\n")
		}
		w.WriteString("\n# The platform team reviews everything that shapes what people see and what runs on their machines.\n")
		for _, r := range general {
			w.WriteString(r.line() + "\n")
		}
		w.WriteString("\n# Hooks, MCP and LSP servers and plugin manifests (which can declare them inline) are code on\n")
		w.WriteString("# developer machines: platform-reviewed, and last on purpose.\n")
		for _, r := range exec {
			w.WriteString(r.line() + "\n")
		}
		return []byte(w.String()), true
	}
	rules := func() []byte {
		pl := b.platform()
		if len(pl) == 0 || b.co == nil {
			return nil
		}
		ca, team, general, exec, ok := b.codeownersRules(pl, false)
		if !ok {
			return nil
		}
		covered := func(r coRule) bool {
			for _, probe := range r.probes {
				got := b.co.Owners(probe)
				ok := len(got) > 0
				if r.kind == rulePlatform {
					ok = false
					for _, g := range got {
						for _, p := range pl {
							ok = ok || strings.EqualFold(g, p)
						}
					}
				}
				if !ok {
					return false
				}
			}
			return true
		}
		var missing []string
		var top string
		for _, r := range append(append(append([]coRule{ca}, team...), general...), exec...) {
			switch {
			case covered(r):
			case r.kind == ruleCatchAll:
				top = r.line()
			default:
				missing = append(missing, r.line())
			}
		}
		if len(missing) == 0 && top == "" {
			return nil
		}
		head := suggestionHeads[1] + " adding to " + ui.SanitizeLine(b.coPath) + ".\n" +
			"# GitHub applies the LAST matching rule: append the rules below at the end, with the rules for\n" +
			"# hooks, MCP servers and plugin manifests last.\n"
		if top != "" {
			head += "#\n# Put this catch-all at the TOP of the file, not at the end (it would override every rule above it):\n#   " + top + "\n"
		}
		if len(missing) == 0 {
			return []byte(head)
		}
		return []byte(head + "\n" + strings.Join(missing, "\n") + "\n")
	}
	if b.co == nil {
		rules = nil
	}
	b.file(b.coPath, GroupCodeowners, kindRules, gen, rules)
}

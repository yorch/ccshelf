package lint

import (
	"sort"
	"time"
)

// Severity of a finding.
type Severity string

// Severities, from worst to mildest.
const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Info    Severity = "info"
)

// Finding is one lint result.
type Finding struct {
	Severity Severity `json:"severity"`
	// Code is the rule id, CAT followed by three digits.
	Code    string `json:"code"`
	Message string `json:"message"`
	// File is the repo-relative, slash-separated file, when known.
	File string `json:"file,omitempty"`
	// Line is 1-based, 0 when unknown.
	Line   int    `json:"line,omitempty"`
	Plugin string `json:"plugin,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// Report is the result of a lint run.
type Report struct {
	Findings []Finding `json:"findings"`
}

// Counts is the number of findings per severity.
type Counts struct {
	Errors, Warnings, Infos int
}

// Counts tallies the findings.
func (r *Report) Counts() Counts {
	var c Counts
	for _, f := range r.Findings {
		switch f.Severity {
		case Error:
			c.Errors++
		case Warning:
			c.Warnings++
		default:
			c.Infos++
		}
	}
	return c
}

// HasErrors reports whether any finding has error severity.
func (r *Report) HasErrors() bool { return r.Counts().Errors > 0 }

// Sort orders findings by file, line, code, plugin and message.
func (r *Report) Sort() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		switch {
		case a.File != b.File:
			return a.File < b.File
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Code != b.Code:
			return a.Code < b.Code
		case a.Plugin != b.Plugin:
			return a.Plugin < b.Plugin
		default:
			return a.Message < b.Message
		}
	})
}

// Options tune a run.
type Options struct {
	// Now is the clock for review dates; time.Now when nil.
	Now func() time.Time
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Rule documents one lint rule.
type Rule struct {
	Code        string   `json:"code"`
	Severity    Severity `json:"severity"`
	Description string   `json:"description"`
}

// Rules returns every rule, ordered by code. A rule whose severity depends on
// the data lists its usual severity.
func Rules() []Rule {
	out := make([]Rule, len(rules))
	copy(out, rules)
	return out
}

var rules = []Rule{
	{"CAT001", Error, "A marketplace file listed in ccshelf.toml cannot be read or parsed."},
	{"CAT002", Error, "Two marketplace entries use the same plugin name."},
	{"CAT003", Error, "Plugin name starts with a reserved prefix (claude-, anthropic-, cc-plugin-)."},
	{"CAT004", Warning, "Plugin name contains the whole word \"claude\"."},
	{"CAT005", Error, "Marketplace entry has no description (external-source plugins need one because their contents cannot be inspected)."},
	{"CAT006", Warning, "Description is shorter than lint.min_description_length."},
	{"CAT007", Warning, "Marketplace entry has no author."},
	{"CAT008", Error, "The taxonomy file cannot be read or is malformed."},
	{"CAT009", Error, "Plugin name has characters Claude Code rejects (error), or is not kebab-case or is longer than 64 characters (warning)."},
	{"CAT010", Error, "Marketplace entry has no sidecar (or, in single-file mode, no metadata)."},
	{"CAT011", Warning, "Sidecar has no marketplace entry."},
	{"CAT012", Error, "Sidecar is unreadable or malformed (unknown key, wrong type, bad name)."},
	{"CAT013", Error, "A sidecar field required by lint.require is missing or empty."},
	{"CAT014", Error, "Status is not active, experimental or deprecated."},
	{"CAT015", Error, "A deprecated plugin lacks a field required by lint.require_when_deprecated."},
	{"CAT016", Error, "superseded_by names a plugin that does not exist."},
	{"CAT017", Error, "superseded_by points to the plugin itself, to a deprecated plugin, or forms a cycle."},
	{"CAT018", Error, "review_by is not YYYY-MM-DD, or an active plugin has none while lint.require lists review_by."},
	{"CAT019", Warning, "review_by is older than lint.max_review_age_days before today (stale)."},
	{"CAT020", Info, "review_by has passed but is within lint.max_review_age_days."},
	{"CAT021", Error, "Category is not in the taxonomy."},
	{"CAT022", Error, "Tag is not in the taxonomy."},
	{"CAT023", Error, "overlaps_with names a plugin that does not exist (or the plugin itself)."},
	{"CAT024", Error, "A dependency names a plugin that does not exist, or a cross-marketplace dependency that is not allowed."},
	{"CAT025", Error, "Relevance signals exceed the limits (cwd 10 x 256, cli 10 x 64, hosts 20 x 128, filesRead 10 x 256, manifestDeps 10 x 256)."},
	{"CAT026", Error, "A relevance manifestDeps pattern is not a valid regular expression."},
	{"CAT027", Warning, "A relevance pattern uses features Go's RE2 lacks (lookaround, backreferences, possessive quantifiers)."},
	{"CAT028", Error, "docs is not an http or https URL."},
	{"CAT030", Error, "A local plugin source directory does not exist."},
	{"CAT031", Info, "External-source plugin: its contents cannot be inspected."},
	{"CAT032", Error, "A local plugin source path is absolute, contains .., or leaves the repository."},
	{"CAT033", Error, "A local plugin's plugin.json cannot be read or is malformed."},
	{"CAT040", Info, "Plugin ships hooks, which run on developers' machines."},
	{"CAT041", Info, "Plugin ships MCP or LSP servers, which run on developers' machines."},
	{"CAT042", Error, "CODEOWNERS does not give a platform owner the files that run code: hooks, .mcp.json, .lsp.json, plugin.json declaring hooks or servers, declared config paths (error), and scripts they reference (warning). Needs lint.platform_owners."},
	{"CAT043", Warning, "No CODEOWNERS file was found."},
	{"CAT044", Warning, "A plugin directory, or a directory beside the plugin directories without a marketplace entry, is not covered by CODEOWNERS."},
	{"CAT045", Warning, "/.github/ is not covered by CODEOWNERS, or workflows, CODEOWNERS, ccshelf.toml, profiles, bundles, catalog, the MCP registry or a marketplace file lack a platform owner (needs lint.platform_owners)."},
	{"CAT046", Warning, "The sidecar owner is not among the CODEOWNERS owners of the plugin directory."},
	{"CAT047", Warning, "A CODEOWNERS line is invalid and GitHub ignores it."},
	{"CAT050", Error, "A profile manifest that resolves to plugins has no profile-<name> bundle entry in the marketplace."},
	{"CAT051", Error, "A profile-<name> bundle entry has no profile manifest."},
	{"CAT052", Error, "A bundle entry's source is not ./bundles/profile-<name>."},
	{"CAT053", Error, "A bundle entry sets version (the commit SHA is the version, decision D-17)."},
	{"CAT060", Error, "A file the lint needs cannot be read safely (outside the repo, too large, not a regular file)."},
}

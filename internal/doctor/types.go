package doctor

import (
	"sort"
	"time"

	"github.com/yorch/ccshelf/internal/analytics"
	"github.com/yorch/ccshelf/internal/catalog"
	"github.com/yorch/ccshelf/internal/catalog/lint"
	"github.com/yorch/ccshelf/internal/claude"
	"github.com/yorch/ccshelf/internal/orgconfig"
)

// Severity of a finding. The values match the lint package.
type Severity string

// Severities, from worst to mildest.
const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Info    Severity = "info"
)

// Finding is one doctor result.
type Finding struct {
	Severity Severity `json:"severity"`
	// Code is DOC followed by three digits (or the code of a policy finding).
	Code string `json:"code"`
	// Check is the short name of the check, such as "overlap".
	Check   string `json:"check,omitempty"`
	Message string `json:"message"`
	// Plugin is the plugin id, when the finding is about one plugin.
	Plugin string `json:"plugin,omitempty"`
	// Profile is the profile name, when the finding is about one profile.
	Profile string `json:"profile,omitempty"`
	// Hint says what to do, for example the install command or a TOML snippet.
	Hint string `json:"hint,omitempty"`
}

// ProfileView is what doctor needs to know about a resolved profile. The CLI
// fills it from the profile package.
type ProfileView struct {
	Name string
	// Kind is personal, org or project.
	Kind string
	// Source is the id of the source the profile came from.
	Source string
	// Plugins are the resolved plugin includes, as name@marketplace.
	Plugins []string
	// Exclude are the resolved plugin excludes.
	Exclude []string
	// Mode is the plugins.mode ("allow-only", "additive"); empty means
	// allow-only, the default.
	Mode          string
	StandaloneOff []string
	NameOnly      []string
	// Abstract is true for a profile that resolves to no plugins: only a
	// base for others, never launched, so it is left out of the checks about
	// what a launched profile does.
	Abstract bool
	// HideConnectors is true when the profile sets claudeai_connectors to
	// "none".
	HideConnectors bool
	// StrictMCP is true when the profile sets mcp.strict.
	StrictMCP bool
}

// Input is everything the checks look at. Only Catalog is required. A check
// whose input is missing is skipped and listed in Report.Skipped.
type Input struct {
	Catalog *catalog.Catalog
	// NoCatalogReason says why Catalog is nil, for the DOC000 skip entry; empty
	// gives the generic reason.
	NoCatalogReason string
	// Lint is the lint report of the org data repo (optional).
	Lint     *lint.Report
	Profiles []ProfileView
	// Installed are the plugins `claude plugin list` reports. nil means "not
	// read" (the checks that need it are skipped). An empty non-nil slice
	// means "read, and nothing is installed".
	Installed []claude.Plugin
	// StandaloneSkills are the names found in the user's skills directory.
	// nil means "not read", and an empty non-nil slice means "none found".
	StandaloneSkills []string
	// Usage is optional usage data.
	Usage *analytics.Usage
	// Org is the org config (optional). It supplies the protected plugins.
	Org *orgconfig.Config
	// Policy are findings from the policy package, copied into the report.
	Policy []Finding
	// Now is the clock. It is time.Now when zero.
	Now time.Time
	// ReviewMaxAgeDays is how long after review_by a plugin is "stale".
	// It is 180 when zero.
	ReviewMaxAgeDays int
}

func (in *Input) now() time.Time {
	if in.Now.IsZero() {
		return time.Now()
	}
	return in.Now
}

func (in *Input) maxAge() int {
	if in.ReviewMaxAgeDays <= 0 {
		return 180
	}
	return in.ReviewMaxAgeDays
}

// Report is the result of Run.
type Report struct {
	Findings []Finding `json:"findings"`
	// Skipped names the checks that did not run for lack of input, with the
	// reason.
	Skipped []Skip `json:"skipped"`
}

// Skip is a check that did not run.
type Skip struct {
	Code   string `json:"code"`
	Check  string `json:"check"`
	Reason string `json:"reason"`
}

// Counts is the number of findings per severity.
type Counts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Infos    int `json:"infos"`
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

// Rule documents one check.
type Rule struct {
	Code        string   `json:"code"`
	Check       string   `json:"check"`
	Severity    Severity `json:"severity"`
	Description string   `json:"description"`
}

// Rules returns every check, ordered by code. A check whose severity depends
// on the data lists its usual severity.
func Rules() []Rule {
	out := make([]Rule, 0, len(checks))
	for _, c := range checks {
		out = append(out, Rule{Code: c.code, Check: c.name, Severity: c.severity, Description: c.desc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

package scaffold

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yorch/ccshelf/internal/ui"
)

// Mode says whether the target is a new repository or an existing one.
type Mode string

// Modes of a plan.
const (
	// ModeNew is an empty or missing directory (a directory that holds only
	// .git counts as empty).
	ModeNew Mode = "new"
	// ModeAdopt is a directory with content: nothing existing is replaced.
	ModeAdopt Mode = "adopt"
)

// Group is a set of generated files that can be skipped as a whole.
type Group string

// Groups, in the order the plan lists them.
const (
	GroupConfig         Group = "config"
	GroupMarketplace    Group = "marketplace"
	GroupSidecars       Group = "sidecars"
	GroupCodeowners     Group = "codeowners"
	GroupWorkflows      Group = "workflows"
	GroupReadme         Group = "readme"
	GroupGitattributes  Group = "gitattributes"
	GroupGitignore      Group = "gitignore"
	GroupExampleProfile Group = "example-profile"
)

// DefaultGroups returns the groups that are generated unless skipped. The
// example profile is not among them: it is written only on request.
func DefaultGroups() []Group {
	return []Group{GroupConfig, GroupMarketplace, GroupSidecars, GroupCodeowners, GroupWorkflows, GroupReadme, GroupGitattributes, GroupGitignore}
}

// Params are the user's choices. The zero value is valid except that some
// values are needed depending on what gets generated (see MissingError).
type Params struct {
	// Mode forces new or adopt; empty detects it from the target.
	Mode Mode
	// MarketplaceName is the marketplace "name" (--marketplace-name).
	MarketplaceName string
	// Org is the display name of the organization (--org). It defaults to the
	// marketplace name.
	Org string
	// Owner is the default owner of sidecar stubs (--owner). It defaults to the
	// first platform owner.
	Owner string
	// PlatformOwners own everything that runs code or shapes the catalog
	// (--platform-owners).
	PlatformOwners []string
	// CcshelfRef is the full 40-hex commit SHA of the action, or a release
	// tag vX.Y.Z that names the version only (--ccshelf-ref).
	CcshelfRef string
	// CcshelfVersion is the release tag the action installs (--ccshelf-version).
	CcshelfVersion string
	// RunnerLabel is the fallback of runs-on (--runner-label).
	RunnerLabel string
	// Skip holds the groups not to generate.
	Skip map[Group]bool
	// ExampleProfile also writes profiles/example.toml.sample.
	ExampleProfile bool
	// Force replaces existing files that differ, after saving <file>.bak.
	Force bool
}

// Enabled reports whether group g is generated.
func (p *Params) Enabled(g Group) bool {
	if g == GroupExampleProfile {
		return p.ExampleProfile
	}
	return !p.Skip[g]
}

// FieldError is an invalid value; Flag names the flag that carries it.
type FieldError struct {
	Flag string
	Msg  string
}

func (e *FieldError) Error() string { return fmt.Sprintf("%s: %s", e.Flag, e.Msg) }

// MissingError lists the flags whose values are needed for the plan and were
// not given. The command asks for them on a terminal and otherwise exits 2.
type MissingError struct {
	Flags []string
	// Hints says what each flag is for, parallel to Flags.
	Hints []string
}

func (e *MissingError) Error() string {
	return "missing required flag " + strings.Join(e.Flags, ", ")
}

// Patterns of the user values. They are deliberately narrower than what
// Claude Code or GitHub accept: the values end up in TOML, JSON, YAML,
// CODEOWNERS and Markdown, and a character that needs quoting in any of them
// is not worth supporting.
var (
	// MarketplaceNameRe is the pattern of --marketplace-name.
	MarketplaceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	ownerHandleRe     = regexp.MustCompile(`^@[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)?$`)
	ownerEmailRe      = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}$`)
	shaRe             = regexp.MustCompile(`^[0-9a-f]{40}$`)
	versionRe         = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z][0-9A-Za-z.-]{0,30})?$`)
	runnerLabelRe     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// DefaultRunnerLabel is the runs-on fallback when --runner-label is not given.
const DefaultRunnerLabel = "ubuntu-latest"

// maxOwners bounds --platform-owners.
const maxOwners = 20

// ValidOwner reports whether s is an owner as this package writes it into
// CODEOWNERS and ccshelf.toml: @user, @org/team or an email address.
func ValidOwner(s string) bool {
	return len(s) <= 100 && (ownerHandleRe.MatchString(s) || ownerEmailRe.MatchString(s))
}

// ValidMarketplaceName reports whether s is acceptable as --marketplace-name.
func ValidMarketplaceName(s string) bool { return MarketplaceNameRe.MatchString(s) }

// ValidRef reports whether s is a full commit SHA or a vX.Y.Z tag.
func ValidRef(s string) bool { return shaRe.MatchString(s) || versionRe.MatchString(s) }

// ValidVersion reports whether s is a release tag vX.Y.Z.
func ValidVersion(s string) bool { return versionRe.MatchString(s) }

// ValidRunnerLabel reports whether s is acceptable as --runner-label.
func ValidRunnerLabel(s string) bool { return runnerLabelRe.MatchString(s) }

// ValidOrg reports whether s is acceptable as --org: one line of 1 to 100
// characters without control, invisible or bidirectional formatting
// characters, and no leading or trailing space.
func ValidOrg(s string) bool {
	return s != "" && utf8.RuneCountInString(s) <= 100 && s == strings.TrimSpace(s) && !ui.HasControl(s)
}

// Validate checks every value that is set and returns the first problem as a
// *FieldError, or nil. It also normalizes: the owner list loses duplicates.
func (p *Params) Validate() error {
	switch p.Mode {
	case "", ModeNew, ModeAdopt:
	default:
		return &FieldError{"--mode", fmt.Sprintf("%q is not new or adopt", ui.SanitizeLine(string(p.Mode)))}
	}
	if p.MarketplaceName != "" && !ValidMarketplaceName(p.MarketplaceName) {
		return &FieldError{"--marketplace-name", fmt.Sprintf("%q must match %s (lower case letters, digits and hyphens, at most 64 characters)", ui.SanitizeLine(p.MarketplaceName), MarketplaceNameRe)}
	}
	if p.Org != "" && !ValidOrg(p.Org) {
		return &FieldError{"--org", "must be one line of at most 100 characters, without control or invisible characters and without leading or trailing space"}
	}
	if p.Owner != "" && !ValidOwner(p.Owner) {
		return &FieldError{"--owner", fmt.Sprintf("%q is not @user, @org/team or an email address", ui.SanitizeLine(p.Owner))}
	}
	if len(p.PlatformOwners) > maxOwners {
		return &FieldError{"--platform-owners", fmt.Sprintf("at most %d owners", maxOwners)}
	}
	seen := map[string]bool{}
	owners := make([]string, 0, len(p.PlatformOwners))
	for _, o := range p.PlatformOwners {
		if !ValidOwner(o) {
			return &FieldError{"--platform-owners", fmt.Sprintf("%q is not @user, @org/team or an email address", ui.SanitizeLine(o))}
		}
		if !seen[o] {
			seen[o] = true
			owners = append(owners, o)
		}
	}
	p.PlatformOwners = owners
	if p.CcshelfRef != "" && !ValidRef(p.CcshelfRef) {
		return &FieldError{"--ccshelf-ref", fmt.Sprintf("%q must be a full 40-character lower-case commit SHA or a release tag such as v0.1.0", ui.SanitizeLine(p.CcshelfRef))}
	}
	if p.CcshelfVersion != "" && !ValidVersion(p.CcshelfVersion) {
		return &FieldError{"--ccshelf-version", fmt.Sprintf("%q must be a release tag such as v0.1.0", ui.SanitizeLine(p.CcshelfVersion))}
	}
	if versionRe.MatchString(p.CcshelfRef) && p.CcshelfVersion != "" && p.CcshelfVersion != p.CcshelfRef {
		return &FieldError{"--ccshelf-version", "differs from the release tag given as --ccshelf-ref"}
	}
	if p.RunnerLabel != "" && !ValidRunnerLabel(p.RunnerLabel) {
		return &FieldError{"--runner-label", fmt.Sprintf("%q must match %s", ui.SanitizeLine(p.RunnerLabel), runnerLabelRe)}
	}
	return nil
}

// pin is the resolved state of the ccshelf Action reference.
type pin struct {
	// SHA is the commit of the action; zeroSHA when unknown.
	SHA string
	// Version is the release tag; "v0.0.0" when unknown.
	Version string
	// Pinned is true when both are real.
	Pinned bool
	// NeedSHA and NeedVersion say what is still a placeholder.
	NeedSHA, NeedVersion bool
}

const (
	zeroSHA     = "0000000000000000000000000000000000000000"
	unknownTag  = "v0.0.0"
	placeholder = "TODO(ccshelf)"
)

func (p *Params) pin() pin {
	var out pin
	switch {
	case shaRe.MatchString(p.CcshelfRef):
		out.SHA = p.CcshelfRef
	default:
		out.SHA, out.NeedSHA = zeroSHA, true
	}
	switch {
	case versionRe.MatchString(p.CcshelfRef):
		out.Version = p.CcshelfRef
	case p.CcshelfVersion != "":
		out.Version = p.CcshelfVersion
	default:
		out.Version, out.NeedVersion = unknownTag, true
	}
	out.Pinned = !out.NeedSHA && !out.NeedVersion
	return out
}

func (p *Params) runner() string {
	if p.RunnerLabel != "" {
		return p.RunnerLabel
	}
	return DefaultRunnerLabel
}

func (p *Params) org(marketplaceName string) string {
	if p.Org != "" {
		return p.Org
	}
	return marketplaceName
}

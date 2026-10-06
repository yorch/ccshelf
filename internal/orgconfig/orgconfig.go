package orgconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/ccshelf/ccshelf/internal/catalog/safepath"
)

// ErrInvalid is wrapped by every error about a ccshelf.toml that exists but
// cannot be used (it does not parse, fails validation, or points outside its
// root). The launcher treats it as fatal: guessing what the file meant could
// mask a protected control.
var ErrInvalid = errors.New("invalid org config")

// FileName is the org config file name at the repository root.
const FileName = "ccshelf.toml"

// MaxFileSize caps the size of ccshelf.toml.
const MaxFileSize = 1 << 20

// Metadata source values for Catalog.MetadataSource.
const (
	// SourceSidecar reads catalog metadata from catalog/plugins/<name>.toml.
	SourceSidecar = "sidecar"
	// SourceMarketplace reads it from each entry's metadata object
	// (single-file mode).
	SourceMarketplace = "marketplace"
)

// Lint configures the catalog lint rules.
type Lint struct {
	// Require lists sidecar fields every plugin needs.
	Require []string `toml:"require"`
	// RequireWhenDeprecated lists sidecar fields needed for deprecated plugins.
	RequireWhenDeprecated []string `toml:"require_when_deprecated"`
	// MaxReviewAgeDays is how long past review_by an entry may be before it is
	// reported stale.
	MaxReviewAgeDays int `toml:"max_review_age_days"`
	// Taxonomy is the repo-relative path of the taxonomy file.
	Taxonomy string `toml:"taxonomy"`
	// MinDescriptionLength is the shortest accepted marketplace description.
	MinDescriptionLength int `toml:"min_description_length"`
	// PlatformOwners are the CODEOWNERS owners that must cover plugin hooks and
	// .mcp.json files; empty disables that rule.
	PlatformOwners []string `toml:"platform_owners"`
}

// Catalog configures the catalog build.
type Catalog struct {
	Title          string   `toml:"title"`
	MetadataSource string   `toml:"metadata_source"`
	Marketplaces   []string `toml:"marketplaces"`
	// GitData enables last-change, contributor and tag data (needs git).
	GitData bool `toml:"git_data"`
	// ReleaseTagPattern is the git glob (git describe --match) that selects
	// release tags for the changed-since-tag data; it never selects the
	// per-plugin tags <plugin>--v<version>. Default "v[0-9]*".
	ReleaseTagPattern string `toml:"release_tag_pattern"`
	// BaseURL is the public URL of the catalog site, used for links only.
	BaseURL string `toml:"base_url"`
}

// Profiles locates profile manifests.
type Profiles struct {
	Dir         string `toml:"dir"`
	MCPRegistry string `toml:"mcp_registry"`
}

// Protect lists controls that no profile may mask (SR3).
type Protect struct {
	// Plugins are plugin ids such as "audit-log@acme-tools".
	Plugins []string `toml:"plugins"`
	// MCP are MCP server labels such as "plugin:audit:audit" or
	// "claude.ai Shopify".
	MCP []string `toml:"mcp"`
}

// Config is the parsed org config.
type Config struct {
	Lint     Lint     `toml:"lint"`
	Catalog  Catalog  `toml:"catalog"`
	Profiles Profiles `toml:"profiles"`
	Protect  Protect  `toml:"protect"`
}

// SidecarFields returns the field names a sidecar can carry, which are the
// only valid values of lint.require and lint.require_when_deprecated.
func SidecarFields() []string {
	return []string{"owner", "status", "when_to_use", "avoid_when", "overlaps_with", "superseded_by", "review_by", "support", "docs"}
}

// Default returns the configuration used when ccshelf.toml is absent or leaves
// a key out.
func Default() *Config {
	return &Config{
		Lint: Lint{
			Require:               []string{"owner", "status"},
			RequireWhenDeprecated: []string{"superseded_by"},
			MaxReviewAgeDays:      180,
			Taxonomy:              "catalog/taxonomy.toml",
			MinDescriptionLength:  30,
		},
		Catalog: Catalog{
			MetadataSource: SourceSidecar,
			Marketplaces:   []string{marketplaceDefault},

			ReleaseTagPattern: "v[0-9]*",
		},
		Profiles: Profiles{Dir: "profiles", MCPRegistry: "mcp/registry.toml"},
	}
}

const marketplaceDefault = ".claude-plugin/marketplace.json"

// Load reads <root>/ccshelf.toml. A missing file returns Default(). The
// result is validated, including that every path stays inside root. Use Find
// when the caller must tell a missing file from a present one.
func Load(root string) (*Config, error) {
	cfg, _, err := Find(root)
	return cfg, err
}

// Find is Load that also reports whether the file exists. A missing file
// returns Default() and found == false, which is not an error: callers that
// rely on [protect] (the launcher) use found to warn that nothing is
// protected instead of silently using the defaults. The result is validated,
// including that every path stays inside root.
func Find(root string) (cfg *Config, found bool, err error) {
	data, err := safepath.ReadFile(root, FileName, MaxFileSize)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", FileName, err)
	}
	cfg, err = Parse(data)
	if err != nil {
		return nil, true, fmt.Errorf("%w: %s: %w", ErrInvalid, FileName, err)
	}
	if err := cfg.CheckPaths(root); err != nil {
		return nil, true, fmt.Errorf("%w: %s: %w", ErrInvalid, FileName, err)
	}
	return cfg, true, nil
}

// Parse decodes ccshelf.toml bytes strictly over the defaults and validates
// the result lexically (see Validate).
func Parse(data []byte) (*Config, error) {
	cfg := Default()
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		var sm *toml.StrictMissingError
		if errors.As(err, &sm) {
			return nil, fmt.Errorf("unknown key or section:\n%s", sm.String())
		}
		var de *toml.DecodeError
		if errors.As(err, &de) {
			row, col := de.Position()
			msg := strings.TrimPrefix(de.Error(), "toml: ")
			if key := de.Key(); len(key) > 0 {
				msg = strings.Join(key, ".") + ": " + msg
			}
			return nil, fmt.Errorf("line %d, column %d: %s", row, col, msg)
		}
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

var (
	pluginIDRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}@[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	mcpLabelRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._:@/-]{0,255}$`)
	ownerRe     = regexp.MustCompile(`^(@[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)?|[^@\s]+@[^@\s]+\.[^@\s]+)$`)
	fieldNameRe = regexp.MustCompile(`^[a-z_]+$`)
)

// ValidOwner reports whether s is a CODEOWNERS style owner: @user, @org/team
// or an email address.
func ValidOwner(s string) bool { return len(s) <= 256 && ownerRe.MatchString(s) }

// Validate checks values and paths lexically, without touching the file
// system: known field names, positive limits, enum values, relative
// forward-slash paths without "..", valid plugin ids and MCP labels. Use
// CheckPaths for the symlink check.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	known := map[string]bool{}
	for _, f := range SidecarFields() {
		known[f] = true
	}
	checkFields := func(key string, fields []string) {
		seen := map[string]bool{}
		for _, f := range fields {
			switch {
			case !fieldNameRe.MatchString(f) || !known[f]:
				add("%s: unknown sidecar field %q (valid: %s)", key, f, strings.Join(SidecarFields(), ", "))
			case seen[f]:
				add("%s: %q is listed twice", key, f)
			}
			seen[f] = true
		}
	}
	checkFields("lint.require", c.Lint.Require)
	checkFields("lint.require_when_deprecated", c.Lint.RequireWhenDeprecated)
	if c.Lint.MaxReviewAgeDays <= 0 {
		add("lint.max_review_age_days: must be positive, got %d", c.Lint.MaxReviewAgeDays)
	}
	if c.Lint.MinDescriptionLength < 0 {
		add("lint.min_description_length: must not be negative, got %d", c.Lint.MinDescriptionLength)
	}
	if c.Lint.Taxonomy != "" {
		if err := safepath.CheckRel(c.Lint.Taxonomy); err != nil {
			add("lint.taxonomy: %v", err)
		}
	}
	for _, o := range c.Lint.PlatformOwners {
		if !ValidOwner(o) {
			add("lint.platform_owners: %q is not @user, @org/team or an email address", o)
		}
	}

	switch c.Catalog.MetadataSource {
	case SourceSidecar, SourceMarketplace:
	default:
		add("catalog.metadata_source: must be %q or %q, got %q", SourceSidecar, SourceMarketplace, c.Catalog.MetadataSource)
	}
	if len(c.Catalog.Marketplaces) == 0 {
		add("catalog.marketplaces: needs at least one file")
	}
	seenM := map[string]bool{}
	for _, m := range c.Catalog.Marketplaces {
		if err := safepath.CheckRel(m); err != nil {
			add("catalog.marketplaces: %v", err)
		} else if !strings.HasSuffix(m, ".json") {
			add("catalog.marketplaces: %q must be a .json file", m)
		}
		if seenM[m] {
			add("catalog.marketplaces: %q is listed twice", m)
		}
		seenM[m] = true
	}
	if len(c.Catalog.Title) > 200 || strings.ContainsAny(c.Catalog.Title, "\x00\r\n") {
		add("catalog.title: must be one line of at most 200 characters")
	}
	if !validReleaseTagPattern(c.Catalog.ReleaseTagPattern) {
		add("catalog.release_tag_pattern: %q must be a git glob of 1 to 100 letters, digits and . _ - + / * ? [ ] !, not starting with -", c.Catalog.ReleaseTagPattern)
	}
	if c.Catalog.BaseURL != "" {
		u, err := url.Parse(c.Catalog.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			add("catalog.base_url: must be an http or https URL, got %q", c.Catalog.BaseURL)
		}
	}

	for _, kp := range [][2]string{{"profiles.dir", c.Profiles.Dir}, {"profiles.mcp_registry", c.Profiles.MCPRegistry}} {
		key, p := kp[0], kp[1]
		if p == "" {
			add("%s: must not be empty", key)
		} else if err := safepath.CheckRel(p); err != nil {
			add("%s: %v", key, err)
		} else if c, bad := unplainComponent(p); bad {
			// The launcher's directory source refuses these (profile.Layout), so
			// the validator must too: a value accepted here but refused there
			// would make a valid org config unusable.
			add("%s: %q has the component %q; use plain components (no empty or dot-prefixed ones, so write \"profiles\", not \"./profiles\")", key, p, c)
		}
	}

	for _, id := range c.Protect.Plugins {
		if !pluginIDRe.MatchString(id) {
			add("protect.plugins: %q is not a plugin id of the form name@marketplace", id)
		}
	}
	for _, l := range c.Protect.MCP {
		if !mcpLabelRe.MatchString(l) {
			add("protect.mcp: %q is not a valid MCP server label", l)
		}
	}
	return errors.Join(errs...)
}

// CheckPaths verifies that every configured path stays inside root once
// symlinks are resolved. Paths that do not exist yet are accepted.
func (c *Config) CheckPaths(root string) error {
	paths := [][2]string{
		{"lint.taxonomy", c.Lint.Taxonomy},
		{"profiles.dir", c.Profiles.Dir},
		{"profiles.mcp_registry", c.Profiles.MCPRegistry},
	}
	var errs []error
	for _, kp := range paths {
		key, p := kp[0], kp[1]
		if p == "" {
			continue
		}
		if _, err := safepath.Resolve(root, p); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key, err))
		}
	}
	for _, m := range c.Catalog.Marketplaces {
		if _, err := safepath.Resolve(root, m); err != nil {
			errs = append(errs, fmt.Errorf("catalog.marketplaces: %w", err))
		}
	}
	return errors.Join(errs...)
}

// IsProtectedPlugin reports whether the plugin id is listed under
// [protect] plugins.
func (c *Config) IsProtectedPlugin(id string) bool { return contains(c.Protect.Plugins, id) }

// IsProtectedMCP reports whether the MCP server label is listed under
// [protect] mcp.
func (c *Config) IsProtectedMCP(label string) bool { return contains(c.Protect.MCP, label) }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// unplainComponent returns the first slash separated component of p that is
// empty or starts with ".", and whether there is one. It agrees with
// profile.Layout, which refuses the same components.
func unplainComponent(p string) (string, bool) {
	for _, comp := range strings.Split(p, "/") {
		if comp == "" || strings.HasPrefix(comp, ".") {
			return comp, true
		}
	}
	return "", false
}

var releasePatternRe = regexp.MustCompile(`^[A-Za-z0-9*?\[\]!._/+-]{1,100}$`)

func validReleaseTagPattern(s string) bool {
	return releasePatternRe.MatchString(s) && !strings.HasPrefix(s, "-") && !strings.Contains(s, "..")
}

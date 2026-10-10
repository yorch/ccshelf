package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/yorch/ccshelf/internal/config/tomlkeys"
)

// MaxFileSize is the largest config file Load accepts.
const MaxFileSize = 256 << 10

// Source types.
const (
	SourceDir    = "dir"
	SourceGit    = "git"
	SourcePlugin = "plugin"
)

// Trust.OnChange values. "allow" is deliberately not accepted (SR2).
const (
	OnChangePrompt = "prompt"
	OnChangeFail   = "fail"
)

// UI values.
const (
	ColorAuto   = "auto"
	ColorAlways = "always"
	ColorNever  = "never"

	InteractiveAuto  = "auto"
	InteractiveNever = "never"
)

// SourceTypes returns the accepted values of sources.type.
func SourceTypes() []string { return []string{SourceDir, SourceGit, SourcePlugin} }

// OnChangeModes returns the accepted values of trust.on_change.
func OnChangeModes() []string { return []string{OnChangePrompt, OnChangeFail} }

// ColorModes returns the accepted values of ui.color.
func ColorModes() []string { return []string{ColorAuto, ColorAlways, ColorNever} }

// InteractiveModes returns the accepted values of ui.interactive.
func InteractiveModes() []string { return []string{InteractiveAuto, InteractiveNever} }

// Config is the user configuration (config.toml).
type Config struct {
	DefaultAccount string             `toml:"default_account,omitempty"`
	Sources        []SourceConfig     `toml:"sources,omitempty"`
	Catalog        Catalog            `toml:"catalog"`
	Trust          Trust              `toml:"trust"`
	Accounts       map[string]Account `toml:"accounts,omitempty"`
	Claude         Claude             `toml:"claude"`
	UI             UI                 `toml:"ui"`
	Update         Update             `toml:"update,omitempty"`
}

// Catalog configures optional remote catalog retrieval for discovery commands.
type Catalog struct {
	RemoteURL string `toml:"remote_url,omitempty"`
}

// SourceConfig describes one profile source. The order of sources does not
// matter. profile.Resolve decides collisions and personal shadowing. For git
// sources, Path is the folder inside the repository.
type SourceConfig struct {
	Type string `toml:"type"`
	Name string `toml:"name,omitempty"`
	Path string `toml:"path,omitempty"`
	URL  string `toml:"url,omitempty"`
	Ref  string `toml:"ref,omitempty"`
	// Branch is, for git sources, a branch to track instead of a tag or a
	// commit (D-55). It is mutually exclusive with Ref. Trust stays per
	// commit: every new commit on the branch needs trust again.
	Branch string `toml:"branch,omitempty"`
	Plugin string `toml:"plugin,omitempty"`
	// Marketplace is, for plugin sources, the source (owner/repo or a git
	// URL) the plugin's marketplace must have been added from (SR2). The
	// "@marketplace" in a plugin id is only a local alias that anyone can
	// reuse, so without this a lookalike marketplace could supply the profiles.
	Marketplace string `toml:"marketplace,omitempty"`
}

// Trust holds the trust settings (SR2).
type Trust struct {
	RequirePin           bool   `toml:"require_pin"`
	OnChange             string `toml:"on_change"`
	TrustProjectProfiles bool   `toml:"trust_project_profiles"`
	// BranchCheckInterval is a Go duration string (for example "24h"): how
	// often a run checks a tracked branch for a new commit. Empty means 24h.
	BranchCheckInterval string `toml:"branch_check_interval,omitempty"`
}

// EffectiveBranchCheckInterval returns trust.branch_check_interval with the
// default applied. An invalid value (Validate reports it) falls back to the
// default.
func (t Trust) EffectiveBranchCheckInterval() time.Duration {
	if t.BranchCheckInterval == "" {
		return DefaultBranchCheckInterval
	}
	d, err := ParseUpdateInterval(t.BranchCheckInterval)
	if err != nil || d < MinUpdateInterval || d > MaxUpdateInterval {
		return DefaultBranchCheckInterval
	}
	return d
}

// DefaultBranchCheckInterval is used when trust.branch_check_interval is empty.
const DefaultBranchCheckInterval = 24 * time.Hour

// BranchRefPrefix starts the trust identity of a branch source. A tag name
// never holds ":", so a branch and a tag with the same name never collide.
const BranchRefPrefix = "branch:"

// TrustRef returns the ref that the trust lockfile records for a git source:
// the tag or commit, or "branch:<name>" for a branch source.
func (s SourceConfig) TrustRef() string {
	if s.Branch != "" {
		return BranchRefPrefix + s.Branch
	}
	return s.Ref
}

// ValidateBranch reports why name is not an acceptable branch to track, or
// nil. It applies the rules of "git check-ref-format --branch" without running
// git, and refuses full ref names (refs/..., heads/..., remotes/...) and HEAD.
func ValidateBranch(name string) error {
	switch {
	case name == "":
		return errors.New("a branch name is required")
	case len(name) > 200:
		return errors.New("a branch name is at most 200 characters")
	case strings.HasPrefix(name, "-"):
		return fmt.Errorf("branch %q must not start with '-'", name)
	case name == "@" || strings.EqualFold(name, "HEAD"):
		return fmt.Errorf("%q is not a branch name", name)
	case strings.Contains(name, ".."), strings.Contains(name, "@{"), strings.Contains(name, "//"):
		return fmt.Errorf("branch %q contains \"..\", \"@{\" or \"//\"", name)
	case strings.HasPrefix(name, "/"), strings.HasSuffix(name, "/"), strings.HasSuffix(name, "."):
		return fmt.Errorf("branch %q must not start with \"/\" or end with \"/\" or \".\"", name)
	case strings.IndexFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || strings.ContainsRune("~^:?*[\\", r)
	}) >= 0:
		return fmt.Errorf("branch %q contains a space, a control character or one of ~ ^ : ? * [ \\", name)
	}
	first, _, _ := strings.Cut(strings.ToLower(name), "/")
	if first == "refs" || ((first == "heads" || first == "remotes") && strings.Contains(name, "/")) {
		return fmt.Errorf("branch %q looks like a full ref name. Give the branch name only", name)
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return fmt.Errorf("branch %q has a part that starts with \".\" or ends with \".lock\"", name)
		}
	}
	return nil
}

// Account names a separate Claude Code configuration directory.
type Account struct {
	ConfigDir string `toml:"config_dir"`
}

// Claude locates the claude binary. Empty means search PATH.
type Claude struct {
	Path string `toml:"path,omitempty"`
}

// UI holds presentation settings.
type UI struct {
	Color       string `toml:"color"`
	Interactive string `toml:"interactive"`
}

// Default returns the configuration used when no file exists.
func Default() *Config {
	return &Config{
		Trust: Trust{RequirePin: true, OnChange: OnChangePrompt},
		UI:    UI{Color: ColorAuto, Interactive: InteractiveAuto},
	}
}

var (
	accountNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	sourceNameRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	pluginIDRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9._-]*$`)
	shaRe         = regexp.MustCompile(`^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)
	hexRe         = regexp.MustCompile(`^[0-9a-fA-F]+$`)
	// tagRe is the syntax of a pinned tag name. It has no "/", so a tag can
	// never be written as a path such as refs/heads/main.
	tagRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	// refCharsRe is the syntax allowed for a ref when pins are not required.
	refCharsRe = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)
)

// ValidAccountName reports whether s is a legal account name.
func ValidAccountName(s string) bool { return accountNameRe.MatchString(s) }

// reservedRefs are names that denote a moving target (a branch, a symbolic
// ref or a conventional "newest" label) and are never accepted as a pin.
var reservedRefs = map[string]bool{
	"head": true, "fetch_head": true, "orig_head": true, "merge_head": true,
	"main": true, "master": true, "develop": true, "development": true, "dev": true, "trunk": true,
	"release": true, "latest": true, "stable": true, "next": true, "origin": true,
}

// reservedRefLeaders are leading words that make a ref look like a full ref
// name (refs/heads/x, heads/x, remotes/origin/x) or a remote-tracking one.
var reservedRefLeaders = map[string]bool{"refs": true, "heads": true, "remotes": true, "origin": true}

// ValidatePin reports why ref is not an acceptable pin, or nil. A pin is a
// full commit id (40 hex digits, or 64 for SHA-256 repositories) or a tag name
// matching ^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$.
//
// ValidatePin rejects:
//
//   - anything containing "/" (so nobody can ever spell refs/..., heads/...
//     or origin/...)
//   - a name whose first word (up to the first . _ + or -) is refs, heads,
//     remotes or origin
//   - the names HEAD, FETCH_HEAD, ORIG_HEAD, MERGE_HEAD, main, master,
//     develop, dev, trunk, release, latest, stable and next, in any case
//   - ".." and a trailing "." or ".lock"
//   - hex strings of 7 to 39 digits, which are ambiguous abbreviations of a
//     commit id
//
// This is a syntax gate, not the security boundary. The git source fetches
// refs/tags/<ref> only, never a branch, and records the peeled commit SHA that
// the tag points to. The trust check compares that SHA, so it detects a tag
// that moves, whatever its name.
func ValidatePin(ref string) error {
	if ref == "" {
		return errors.New("a pinned ref (tag or commit SHA) is required")
	}
	if shaRe.MatchString(ref) {
		return nil
	}
	if strings.Contains(ref, "/") {
		return fmt.Errorf("ref %q contains \"/\": a pin is a tag name or a full commit SHA, never a branch or a full ref path (to track a branch, use branch instead of ref)", ref)
	}
	if !tagRe.MatchString(ref) {
		return fmt.Errorf("ref %q contains characters that are not allowed: a pin is a tag name (letters, digits and . _ + - only) or a full 40-hex commit SHA", ref)
	}
	lower := strings.ToLower(ref)
	first := lower
	if i := strings.IndexAny(lower, "._+-"); i >= 0 {
		first = lower[:i]
	}
	switch {
	case reservedRefs[lower] || reservedRefLeaders[first]:
		return fmt.Errorf("ref %q looks like a branch or a moving reference. Pin a tag or a full 40-hex commit SHA.%s", ref, branchHint(lower))
	case strings.Contains(ref, ".."), strings.HasSuffix(ref, "."), strings.HasSuffix(lower, ".lock"):
		return fmt.Errorf("ref %q is not a valid git tag name", ref)
	case hexRe.MatchString(ref) && len(ref) >= 7 && len(ref) < 40:
		return fmt.Errorf("ref %q looks like an abbreviated commit id, which is ambiguous. Use the full 40-hex SHA or a tag", ref)
	}
	return nil
}

// branchHint suggests the branch key for the usual names of a default branch.
// It names only a fixed list, so no input is echoed back.
func branchHint(lower string) string {
	switch lower {
	case "main", "master", "develop", "development", "dev", "trunk":
		return fmt.Sprintf(" To track the branch, use branch = %q.", lower)
	}
	return ""
}

// Load reads and validates the configuration at path. A missing file yields
// Default(). Unknown keys and invalid values are errors.
func Load(path string) (*Config, error) {
	raw, err := ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default(), nil
		}
		return nil, err
	}
	return Parse(raw, path)
}

// ReadFile reads the raw bytes of the configuration file at path, refusing a
// file larger than MaxFileSize. A missing file wraps fs.ErrNotExist.
func ReadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening config %s: %w", path, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	if len(raw) > MaxFileSize {
		return nil, fmt.Errorf("config %s is larger than %d bytes", path, MaxFileSize)
	}
	return raw, nil
}

// Parse decodes and validates configuration text. name is only a label for
// errors (a file name). Parse reads nothing from disk.
func Parse(raw []byte, name string) (*Config, error) { return parse(raw, name, true) }

// ParseUnexpanded is Parse for a command that rewrites the file. It validates
// ~ and variables in account directories and claude.path as they would
// expand. But the returned Config keeps the text that the user wrote, so
// writing it back changes nothing that the user did not ask to change.
func ParseUnexpanded(raw []byte, name string) (*Config, error) { return parse(raw, name, false) }

func parse(raw []byte, name string, expand bool) (*Config, error) {
	cfg := Default()
	dec := toml.NewDecoder(bytes.NewReader(raw)).DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("config %s: %w", name, describeDecode(err))
	}
	// The decoder matches keys case-insensitively; only the documented
	// spelling is accepted, so a reviewer never sees two spellings of one key.
	issues, err := tomlkeys.Check(raw, Config{})
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", name, err)
	}
	if len(issues) > 0 {
		errs := make([]error, len(issues))
		for i, is := range issues {
			errs[i] = fmt.Errorf("%s: %s", is.Path, is)
		}
		return nil, fmt.Errorf("config %s: %w", name, errors.Join(errs...))
	}
	if !expand {
		if err := cfg.ValidateUnexpanded(); err != nil {
			return nil, fmt.Errorf("config %s: %w", name, err)
		}
		return cfg, nil
	}
	if err := cfg.expand(); err != nil {
		return nil, fmt.Errorf("config %s: %w", name, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", name, err)
	}
	return cfg, nil
}

// ValidateUnexpanded is Validate for a Config whose paths still hold ~ and
// variables as written (see ParseUnexpanded): it checks an expanded copy.
func (c *Config) ValidateUnexpanded() error {
	cp := *c
	cp.Accounts = make(map[string]Account, len(c.Accounts))
	for k, v := range c.Accounts {
		cp.Accounts[k] = v
	}
	if err := cp.expand(); err != nil {
		return err
	}
	return cp.Validate()
}

func describeDecode(err error) error {
	var sm *toml.StrictMissingError
	if errors.As(err, &sm) {
		var errs []error
		for i := range sm.Errors {
			e := &sm.Errors[i]
			row, _ := e.Position()
			errs = append(errs, fmt.Errorf("line %d: unknown key %q", row, strings.Join(e.Key(), ".")))
		}
		return errors.Join(errs...)
	}
	var de *toml.DecodeError
	if errors.As(err, &de) {
		row, col := de.Position()
		return fmt.Errorf("line %d, column %d: %s", row, col, de.Error())
	}
	return err
}

// expand resolves ~ and variables in account directories and the claude path.
func (c *Config) expand() error {
	for name, a := range c.Accounts {
		d, err := ExpandPath(a.ConfigDir)
		if err != nil {
			return fmt.Errorf("accounts.%s.config_dir: %w", name, err)
		}
		a.ConfigDir = d
		c.Accounts[name] = a
	}
	if c.Claude.Path != "" {
		p, err := ExpandPath(c.Claude.Path)
		if err != nil {
			return fmt.Errorf("claude.path: %w", err)
		}
		c.Claude.Path = p
	}
	return nil
}

// ResolvedPath returns the source path with ~ and variables expanded. For git
// sources, the path is inside the repository, and ResolvedPath returns it
// unchanged.
func (s SourceConfig) ResolvedPath() (string, error) {
	if s.Type == SourceDir {
		p, err := ExpandPath(s.Path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("source path %q expands to the relative path %q. It must be absolute", s.Path, p)
		}
		return p, nil
	}
	return s.Path, nil
}

// Validate checks every rule documented on the package.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if !contains(OnChangeModes(), c.Trust.OnChange) {
		if c.Trust.OnChange == "allow" {
			add("trust.on_change: %q is not accepted: trust is never auto-accepted (SR2)", "allow")
		} else {
			add("trust.on_change: %q is not one of %v", c.Trust.OnChange, OnChangeModes())
		}
	}
	if !contains(ColorModes(), c.UI.Color) {
		add("ui.color: %q is not one of %v", c.UI.Color, ColorModes())
	}
	if !contains(InteractiveModes(), c.UI.Interactive) {
		add("ui.interactive: %q is not one of %v", c.UI.Interactive, InteractiveModes())
	}
	if strings.ContainsRune(c.Claude.Path, 0) {
		add("claude.path: contains a NUL byte")
	}
	c.Update.validate(add)
	if c.Trust.BranchCheckInterval != "" {
		d, err := ParseUpdateInterval(c.Trust.BranchCheckInterval)
		switch {
		case err != nil:
			add("trust.branch_check_interval: %q is not a duration such as 24h", c.Trust.BranchCheckInterval)
		case d < MinUpdateInterval:
			add("trust.branch_check_interval: %q is shorter than the minimum %s", c.Trust.BranchCheckInterval, MinUpdateInterval)
		case d > MaxUpdateInterval:
			add("trust.branch_check_interval: %q is longer than the maximum %s", c.Trust.BranchCheckInterval, MaxUpdateInterval)
		}
	}
	if c.Catalog.RemoteURL != "" {
		u, err := url.Parse(c.Catalog.RemoteURL)
		switch {
		case err != nil:
			add("catalog.remote_url: %v", err)
		case u.Scheme != "https" || u.Host == "":
			add("catalog.remote_url: must be an absolute HTTPS URL")
		case u.User != nil || u.RawQuery != "" || u.Fragment != "":
			add("catalog.remote_url: credentials, query strings and fragments are not allowed")
		}
	}

	for i, s := range c.Sources {
		p := fmt.Sprintf("sources[%d]", i)
		if s.Name != "" && !sourceNameRe.MatchString(s.Name) {
			add("%s.name: %q must match %s", p, s.Name, sourceNameRe)
		}
		switch s.Type {
		case SourceDir:
			if s.Path == "" {
				add("%s: dir sources need path", p)
			} else if err := checkDirSourcePath(s.Path); err != nil {
				add("%s.path: %v", p, err)
			}
			if s.URL != "" || s.Ref != "" || s.Branch != "" || s.Plugin != "" || s.Marketplace != "" {
				add("%s: dir sources take only path", p)
			}
		case SourceGit:
			c.validateGit(p, s, add)
		case SourcePlugin:
			if !pluginIDRe.MatchString(s.Plugin) {
				add("%s.plugin: %q must be name@marketplace", p, s.Plugin)
			}
			if s.URL != "" || s.Ref != "" || s.Branch != "" {
				add("%s: plugin sources take only plugin, path and marketplace", p)
			}
			if s.Marketplace != "" {
				if err := ValidateMarketplaceSource(s.Marketplace); err != nil {
					add("%s.marketplace: %v", p, err)
				} else if k := credentialMarker(s.Marketplace); k != "" {
					add("%s.marketplace: looks like it embeds a credential (%s...). Never put tokens in the configuration", p, k)
				}
			}
			if s.Path != "" {
				if err := relInside(s.Path); err != nil {
					add("%s.path: %v", p, err)
				}
			}
		default:
			add("%s.type: %q is not one of %v", p, s.Type, SourceTypes())
		}
	}

	def, _ := DefaultClaudeDir()
	names := make([]string, 0, len(c.Accounts))
	for n := range c.Accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	var defCanon string
	if def != "" {
		defCanon = canonicalPath(def)
	}
	canon := map[string]string{}
	for _, n := range names {
		a := c.Accounts[n]
		if !accountNameRe.MatchString(n) {
			add("accounts.%s: name must match %s", n, accountNameRe)
		}
		switch {
		case a.ConfigDir == "":
			add("accounts.%s.config_dir: required", n)
		case !filepath.IsAbs(a.ConfigDir):
			add("accounts.%s.config_dir: %q must be an absolute path", n, a.ConfigDir)
		default:
			cp := canonicalPath(a.ConfigDir)
			canon[n] = cp
			switch {
			case def != "" && (samePath(a.ConfigDir, def) || samePath(cp, defCanon)):
				add("accounts.%s.config_dir: must not be Claude Code's default directory %s", n, def)
			case def != "" && nested(cp, defCanon):
				add("accounts.%s.config_dir: %q must not contain or sit inside Claude Code's default directory %s", n, a.ConfigDir, def)
			}
		}
	}
	for i, n := range names {
		for _, m := range names[i+1:] {
			a, b := canon[n], canon[m]
			switch {
			case a == "" || b == "":
			case samePath(a, b):
				add("accounts.%s.config_dir: the same directory as account %q. Accounts must not share a configuration directory", m, n)
			case nested(a, b):
				add("accounts.%s.config_dir: %q and account %q's directory are nested. Accounts must not contain one another", m, c.Accounts[m].ConfigDir, n)
			}
		}
	}
	if c.DefaultAccount != "" {
		if _, ok := c.Accounts[c.DefaultAccount]; !ok {
			add("default_account: %q is not a configured account (known: %s)", c.DefaultAccount, knownList(c.Accounts))
		}
	}
	return errors.Join(errs...)
}

func (c *Config) validateGit(p string, s SourceConfig, add func(string, ...any)) {
	if err := ValidateGitURL(s.URL); err != nil {
		add("%s.url: %v", p, err)
	}
	for field, v := range map[string]string{"url": s.URL, "ref": s.Ref, "branch": s.Branch, "path": s.Path, "name": s.Name} {
		if k := credentialMarker(v); k != "" {
			add("%s.%s: looks like it embeds a credential (%s...). Never put tokens in the configuration", p, field, k)
		}
	}
	if s.Plugin != "" {
		add("%s: git sources do not take plugin", p)
	}
	if s.Marketplace != "" {
		add("%s: git sources do not take marketplace", p)
	}
	switch {
	case s.Ref != "" && s.Branch != "":
		add("%s: use ref or branch, not both", p)
	case s.Ref == "" && s.Branch == "":
		// With pins required this is a load error (as before). With pins off
		// it is one unavailable source (gitsource.New reports it), so the
		// personal profiles and "config source pin" keep working.
		if c.Trust.RequirePin {
			add("%s: a pinned ref is required. Add ref = \"<tag>\" or branch = \"<name>\"", p)
		}
	case s.Branch != "":
		// An explicit branch is allowed even with trust.require_pin: the
		// config says in plain words that the source moves (D-55).
		if err := ValidateBranch(s.Branch); err != nil {
			add("%s.branch: %v", p, err)
		}
	case c.Trust.RequirePin:
		if err := ValidatePin(s.Ref); err != nil {
			add("%s.ref: %v", p, err)
		}
	default:
		if !refCharsRe.MatchString(s.Ref) || strings.HasPrefix(s.Ref, "-") || strings.Contains(s.Ref, "..") {
			add("%s.ref: %q contains characters that are not allowed", p, s.Ref)
		}
	}
	if s.Path != "" {
		if err := relInside(s.Path); err != nil {
			add("%s.path: %v", p, err)
		}
	}
}

// relInside checks that p is a relative, clean folder path inside a repository.
func relInside(p string) error {
	if strings.ContainsRune(p, 0) || strings.Contains(p, `\`) {
		return fmt.Errorf("%q must use forward slashes and no control characters", p)
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) || (len(p) > 1 && p[1] == ':') {
		return fmt.Errorf("%q must be relative", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("%q must not contain a \"..\" segment", p)
		}
	}
	if path.Clean(p) == ".." {
		return fmt.Errorf("%q escapes the repository", p)
	}
	return nil
}

// credentialMarkers are prefixes of well-known access tokens.
var credentialMarkers = []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "xoxb-", "xoxp-"}

// credentialMarker returns the token prefix found anywhere in s, or "".
func credentialMarker(s string) string {
	l := strings.ToLower(s)
	for _, m := range credentialMarkers {
		if strings.Contains(l, m) {
			return m
		}
	}
	return ""
}

var (
	// scpLikeRe is the scp-like git address user@host:path.
	scpLikeRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9.-]*:[A-Za-z0-9._~/][A-Za-z0-9._~/+-]*$`)
	// helperRe matches git's <helper>:: remote-helper transports (ext::, fd::).
	helperRe = regexp.MustCompile(`^[A-Za-z0-9+.-]+::`)
)

// ownerRepoRe is the owner/repo shorthand of a GitHub marketplace source.
var ownerRepoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateMarketplaceSource checks the expected marketplace source of a plugin
// source: the owner/repo shorthand Claude Code accepts for GitHub, or a git URL
// valid under [ValidateGitURL] (no credentials, no helper transports).
func ValidateMarketplaceSource(raw string) error {
	if ownerRepoRe.MatchString(raw) && !strings.Contains(raw, "..") {
		return nil
	}
	if raw == "" {
		return errors.New("must not be empty")
	}
	if err := ValidateGitURL(raw); err != nil {
		return fmt.Errorf("%w (use owner/repo or a git URL)", err)
	}
	return nil
}

// ValidateGitURL reports why url is not an acceptable git source address, or
// nil. It accepts exactly three forms:
//
//   - https://host/path (no userinfo of any kind, no query or fragment)
//   - ssh://[user@]host/path (a user name only, never a password)
//   - the scp-like user@host:path
//
// It refuses everything else: file: and bare local paths, ext:: fd:: and any
// <helper>:: transport, git://, http://, a leading "-" (an option for git),
// whitespace and control characters.
func ValidateGitURL(raw string) error {
	switch {
	case raw == "":
		return errors.New("git sources need url")
	case strings.HasPrefix(raw, "-"):
		return errors.New("must not start with '-'")
	case strings.IndexFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0:
		return errors.New("must not contain whitespace, control or invisible formatting characters")
	case helperRe.MatchString(raw):
		return fmt.Errorf("the %q remote-helper transport is not allowed", raw[:strings.Index(raw, "::")+2])
	case strings.HasPrefix(strings.ToLower(raw), "file:"):
		return errors.New(`the "file:" transport is not allowed`)
	case strings.HasPrefix(raw, "https://"):
		u, err := url.Parse(raw)
		switch {
		case err != nil || u.Host == "" || u.Hostname() == "":
			return fmt.Errorf("%q is not a valid https URL", raw)
		case u.User != nil:
			return errors.New("an https URL must not contain user information (no user name, password or token)")
		case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#"):
			return errors.New("must not contain a query or a fragment")
		case u.Path == "" || u.Path == "/":
			return errors.New("needs a repository path: https://host/path")
		}
		return nil
	case strings.HasPrefix(raw, "ssh://"):
		u, err := url.Parse(raw)
		switch {
		case err != nil || u.Hostname() == "":
			return fmt.Errorf("%q is not a valid ssh URL", raw)
		case u.User != nil:
			if _, hasPw := u.User.Password(); hasPw || strings.Contains(u.User.String(), ":") {
				return errors.New("an ssh URL must not contain a password")
			}
		}
		switch {
		case u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#"):
			return errors.New("must not contain a query or a fragment")
		case u.Path == "" || u.Path == "/":
			return errors.New("needs a repository path: ssh://[user@]host/path")
		}
		return nil
	case scpLikeRe.MatchString(raw):
		return nil
	}
	return errors.New("must be https://host/path, ssh://[user@]host/path or user@host:path")
}

// checkDirSourcePath rejects a dir source path that could be satisfied by a
// relative location. A relative path resolves against the working directory,
// which for a cloned repository is attacker-controlled, and the source would
// then be treated as the user's own (personal) directory.
func checkDirSourcePath(p string) error {
	switch {
	case strings.ContainsRune(p, 0):
		return errors.New("contains a NUL byte")
	case p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`):
		return nil
	case strings.HasPrefix(p, "$"):
		if v := cwdVariable(p); v != "" {
			return fmt.Errorf("%q starts with $%s, the directory you run ccshelf from: a cloned repository could then supply profiles as if they were yours (SR2). Use an absolute path, or ~", p, v)
		}
		return nil // expanded later; ResolvedPath requires an absolute result
	case filepath.IsAbs(p):
		return nil
	}
	return fmt.Errorf("%q must be absolute or start with ~ (a relative path would depend on the working directory)", p)
}

// cwdVariable returns PWD or OLDPWD when p starts with that variable ($PWD,
// ${PWD}, ${PWD:-x}), and "" otherwise.
func cwdVariable(p string) string {
	rest := strings.TrimPrefix(p, "$")
	rest = strings.TrimPrefix(rest, "{")
	n := 0
	for n < len(rest) && (rest[n] == '_' || rest[n] >= '0' && rest[n] <= '9' || rest[n] >= 'A' && rest[n] <= 'Z' || rest[n] >= 'a' && rest[n] <= 'z') {
		n++
	}
	if name := rest[:n]; name == "PWD" || name == "OLDPWD" {
		return name
	}
	return ""
}

// samePath reports whether a and b name the same path on the running OS.
func samePath(a, b string) bool { return samePathFor(runtime.GOOS, a, b) }

// foldsCase reports whether goos has case-insensitive file systems by default.
func foldsCase(goos string) bool { return goos == "windows" || goos == "darwin" }

func samePathFor(goos, a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if foldsCase(goos) {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// nested reports whether one of a and b is a proper ancestor of the other.
func nested(a, b string) bool { return nestedFor(runtime.GOOS, a, b) }

func nestedFor(goos, a, b string) bool {
	return strictlyInside(goos, a, b) || strictlyInside(goos, b, a)
}

// strictlyInside reports whether child is below parent (and not equal to it).
func strictlyInside(goos, parent, child string) bool {
	parent, child = filepath.Clean(parent), filepath.Clean(child)
	if foldsCase(goos) {
		parent, child = strings.ToLower(parent), strings.ToLower(child)
	}
	if parent == child {
		return false
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// canonicalPath resolves symlinks in p as far as p exists: the longest
// existing ancestor is passed through EvalSymlinks and the rest is appended
// unchanged. A directory that does not exist yet therefore still compares
// correctly with one that does.
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	cur := p
	for {
		if eval, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(eval, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func knownList(m map[string]Account) string {
	if len(m) == 0 {
		return "none configured"
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Encode validates cfg and returns the TOML text Save would write.
func Encode(cfg *Config) ([]byte, error) {
	if cfg == nil {
		return nil, errors.New("saving config: nil config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("saving config: %w", err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	return data, nil
}

// EncodeUnexpanded is Encode for a Config that holds paths as written (see
// ParseUnexpanded).
func EncodeUnexpanded(cfg *Config) ([]byte, error) {
	if cfg == nil {
		return nil, errors.New("saving config: nil config")
	}
	if err := cfg.ValidateUnexpanded(); err != nil {
		return nil, fmt.Errorf("saving config: %w", err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	return data, nil
}

// Save validates cfg and writes it to path atomically with mode 0600, creating
// the directory with mode 0700. It refuses to replace a symlink.
func Save(path string, cfg *Config) error {
	data, err := Encode(cfg)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// writeAtomic writes data to path through a temporary file in the same
// directory (mode 0600) and a rename. It refuses to replace a symlink.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlink %s", path)
	}
	tmpName, err := writeTempFile(dir, data)
	if err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

// writeTempFile writes data to a new, synced file in dir (mode 0600) and
// returns its name. The caller removes the file, or renames it into place.
func writeTempFile(dir string, data []byte) (string, error) {
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return "", fmt.Errorf("creating temporary file in %s: %w", dir, err)
	}
	name := tmp.Name()
	fail := func(err error) (string, error) {
		tmp.Close()
		_ = os.Remove(name)
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(fmt.Errorf("writing %s: %w", name, err))
	}
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		return fail(fmt.Errorf("setting permissions on %s: %w", name, err))
	}
	if err := tmp.Sync(); err != nil {
		return fail(fmt.Errorf("syncing %s: %w", name, err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("closing %s: %w", name, err)
	}
	return name, nil
}

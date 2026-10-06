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

	toml "github.com/pelletier/go-toml/v2"
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
	Trust          Trust              `toml:"trust"`
	Accounts       map[string]Account `toml:"accounts,omitempty"`
	Claude         Claude             `toml:"claude"`
	UI             UI                 `toml:"ui"`
}

// SourceConfig describes one profile source. Sources are listed most specific
// first. For git sources Path is the folder inside the repository.
type SourceConfig struct {
	Type   string `toml:"type"`
	Name   string `toml:"name,omitempty"`
	Path   string `toml:"path,omitempty"`
	URL    string `toml:"url,omitempty"`
	Ref    string `toml:"ref,omitempty"`
	Plugin string `toml:"plugin,omitempty"`
}

// Trust holds the trust settings (SR2).
type Trust struct {
	RequirePin           bool   `toml:"require_pin"`
	OnChange             string `toml:"on_change"`
	TrustProjectProfiles bool   `toml:"trust_project_profiles"`
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
	pluginIDRe    = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+$`)
	shaRe         = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
	refCharsRe    = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)
)

// ValidAccountName reports whether s is a legal account name.
func ValidAccountName(s string) bool { return accountNameRe.MatchString(s) }

var branchLike = map[string]bool{
	"main": true, "master": true, "head": true, "develop": true,
	"development": true, "trunk": true, "dev": true,
}

// ValidatePin reports why ref is not an acceptable pin (a tag or a full
// commit id), or nil.
func ValidatePin(ref string) error {
	if ref == "" {
		return errors.New("a pinned ref (tag or commit SHA) is required")
	}
	if shaRe.MatchString(ref) {
		return nil
	}
	if !refCharsRe.MatchString(ref) || strings.HasPrefix(ref, "-") || strings.Contains(ref, "..") {
		return fmt.Errorf("ref %q contains characters that are not allowed", ref)
	}
	l := strings.ToLower(ref)
	if branchLike[l] || strings.HasPrefix(l, "origin/") || strings.HasPrefix(l, "refs/heads/") || strings.HasPrefix(l, "refs/remotes/") {
		return fmt.Errorf("ref %q looks like a branch; pin a tag or a 40-hex commit SHA", ref)
	}
	return nil
}

// Load reads and validates the configuration at path. A missing file yields
// Default(). Unknown keys and invalid values are errors.
func Load(path string) (*Config, error) {
	cfg := Default()
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cfg, nil
		}
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
	dec := toml.NewDecoder(bytes.NewReader(raw)).DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, describeDecode(err))
	}
	if err := cfg.expand(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
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
// sources the path is inside the repository and is returned unchanged.
func (s SourceConfig) ResolvedPath() (string, error) {
	if s.Type == SourceDir {
		return ExpandPath(s.Path)
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

	for i, s := range c.Sources {
		p := fmt.Sprintf("sources[%d]", i)
		if s.Name != "" && !sourceNameRe.MatchString(s.Name) {
			add("%s.name: %q must match %s", p, s.Name, sourceNameRe)
		}
		switch s.Type {
		case SourceDir:
			if s.Path == "" {
				add("%s: dir sources need path", p)
			}
			if s.URL != "" || s.Ref != "" || s.Plugin != "" {
				add("%s: dir sources take only path", p)
			}
		case SourceGit:
			c.validateGit(p, s, add)
		case SourcePlugin:
			if !pluginIDRe.MatchString(s.Plugin) {
				add("%s.plugin: %q must be name@marketplace", p, s.Plugin)
			}
			if s.URL != "" || s.Ref != "" {
				add("%s: plugin sources take only plugin and path", p)
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
		case def != "" && samePath(a.ConfigDir, def):
			add("accounts.%s.config_dir: must not be Claude Code's default directory %s", n, def)
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
	switch {
	case s.URL == "":
		add("%s.url: git sources need url", p)
	case strings.HasPrefix(s.URL, "-"):
		add("%s.url: must not start with '-'", p)
	case strings.HasPrefix(strings.ToLower(s.URL), "ext::") || strings.HasPrefix(strings.ToLower(s.URL), "file:"):
		add("%s.url: the %q transport is not allowed", p, strings.SplitN(s.URL, ":", 2)[0])
	case strings.ContainsAny(s.URL, "\x00\n\r"):
		add("%s.url: contains control characters", p)
	default:
		if u, err := url.Parse(s.URL); err == nil && u.User != nil {
			if _, hasPw := u.User.Password(); hasPw {
				add("%s.url: must not embed a password", p)
			}
		}
	}
	if s.Plugin != "" {
		add("%s: git sources do not take plugin", p)
	}
	if c.Trust.RequirePin {
		if err := ValidatePin(s.Ref); err != nil {
			add("%s.ref: %v", p, err)
		}
	} else if s.Ref != "" {
		if !refCharsRe.MatchString(s.Ref) || strings.HasPrefix(s.Ref, "-") {
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

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
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

// Save validates cfg and writes it to path atomically with mode 0600, creating
// the directory with mode 0700. It refuses to replace a symlink.
func Save(path string, cfg *Config) error {
	if cfg == nil {
		return errors.New("saving config: nil config")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("refusing to replace symlink %s", path)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("writing %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		tmp.Close()
		cleanup()
		return fmt.Errorf("setting permissions on %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("syncing %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

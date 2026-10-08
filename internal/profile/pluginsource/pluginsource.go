package pluginsource

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yorch/ccshelf/internal/claude"
	"github.com/yorch/ccshelf/internal/orgconfig"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
)

// DefaultPath is the profiles folder inside the plugin when Options.Path is empty.
const DefaultPath = "profiles"

// ErrNotPrepared is the error that Names, Open and Root return before Prepare.
var ErrNotPrepared = errors.New("plugin source is not prepared. Call Prepare first")

// ErrNotInstalled is returned when the plugin is not installed.
var ErrNotInstalled = errors.New("plugin is not installed")

// Options configures a Source.
type Options struct {
	// Plugin is the plugin id, name@marketplace.
	Plugin string
	// Path is the profiles folder inside the plugin. Default "profiles".
	Path string
	// Installed lists installed plugins. The caller injects it (usually a
	// wrapper around claude.ListInstalled) so tests never run claude.
	Installed func(ctx context.Context) ([]claude.Plugin, error)
	// MarketplaceSource returns the identity of the real source the named
	// marketplace was added from, in the canonical, kind-tagged form of
	// claude.Marketplace.Identity ("github:owner/repo", "git:host/path", a hash
	// for a local directory). The "@marketplace" part of a plugin id
	// is only a local alias: anyone can add a marketplace under any name, so
	// without this the id proves nothing about where the plugin came from.
	// When set, the source is bound into ID and Locator, so the trust record
	// is keyed to it and a plugin of the same name from elsewhere is a new
	// source. The caller injects it (a wrapper around the marketplace listing)
	// so tests never run claude.
	MarketplaceSource func(ctx context.Context, marketplace string) (string, error)
	// ExpectedMarketplace is the source the organization says the marketplace
	// must come from. When set, MarketplaceSource is required and Prepare
	// fails unless the identity of the real source equals what the expected
	// source stands for (claude.ExpectedIdentity: the kind, the host without
	// case, no trailing slash or ".git", the same repository over ssh or
	// https, and no ref or sub-path).
	ExpectedMarketplace string
}

// Source is a profile source backed by an installed plugin. It satisfies
// profile.Source (kind KindOrg) once Prepare has succeeded.
type Source struct {
	opts Options
	path string // cleaned profiles folder, slash separated

	mu      sync.Mutex
	version string
	market  string // the verified marketplace source, "" when not checked
	inner   profile.Source
	cfg     *orgconfig.Config
	found   bool
}

var (
	_ profile.Source          = (*Source)(nil)
	_ profile.RegistryLocator = (*Source)(nil)
)

// New validates opts. It does no I/O.
func New(opts Options) (*Source, error) {
	name, mkt := claude.SplitID(opts.Plugin)
	if name == "" || mkt == "" || strings.ContainsAny(opts.Plugin, " \t\r\n\x00") || strings.HasPrefix(opts.Plugin, "-") {
		return nil, fmt.Errorf("plugin %q must look like name@marketplace", opts.Plugin)
	}
	if opts.Installed == nil {
		return nil, errors.New("the Installed function is required")
	}
	p := opts.Path
	if p == "" {
		p = DefaultPath
	}
	if strings.ContainsAny(p, "\\:\x00") || strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("path %q must be relative with forward slashes", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return nil, fmt.Errorf("path %q must not contain \"..\"", p)
		}
	}
	p = filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	if p == "." {
		return nil, fmt.Errorf("path %q must name a folder below the plugin", opts.Path)
	}
	if path.Base(p) != DefaultPath {
		// prompts/ and mcp/registry.toml sit next to a folder named "profiles";
		// the profile package decides that from the name and a wrapper cannot
		// pass the decision on, so another name would be misread.
		return nil, fmt.Errorf("path %q: the profiles folder must be named %q (prompts/ and mcp/ sit next to it)", opts.Path, DefaultPath)
	}
	if opts.ExpectedMarketplace != "" && opts.MarketplaceSource == nil {
		return nil, errors.New("ExpectedMarketplace needs MarketplaceSource to check it against")
	}
	return &Source{opts: opts, path: p}, nil
}

// Plugin returns the plugin id.
func (s *Source) Plugin() string { return s.opts.Plugin }

// ProtectedPluginIDs returns the plugin that carries the profiles. The
// settings spec must never mask it.
func (s *Source) ProtectedPluginIDs() []string { return []string{s.opts.Plugin} }

// ID returns "plugin:<name@marketplace>", followed by " from <source>" once
// Prepare has verified the real marketplace source. It contains no machine
// path.
func (s *Source) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.market != "" {
		return "plugin:" + s.opts.Plugin + " from " + s.market
	}
	return "plugin:" + s.opts.Plugin
}

// Locator is the same as ID. The version is not part of a plugin's identity,
// but the real marketplace source is.
func (s *Source) Locator() string { return s.ID() }

// Ref returns the installed plugin version, or "" before Prepare. It lets the
// trust package see a version change as a change of ref.
func (s *Source) Ref() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

// Kind returns profile.KindOrg.
func (s *Source) Kind() profile.Kind { return profile.KindOrg }

// Commit returns "plugin:<version>", or "" before Prepare.
func (s *Source) Commit() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inner == nil {
		return ""
	}
	if s.version == "" {
		return "plugin:unversioned"
	}
	return "plugin:" + s.version
}

// Root returns the folder that holds profiles/ as the underlying directory
// source reports it ("" before Prepare, or when that source was refused).
func (s *Source) Root() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inner == nil {
		return ""
	}
	return s.inner.Root()
}

// OrgConfig returns the org config (ccshelf.toml) found next to the profiles
// folder inside the plugin, and whether the plugin has one. It returns
// (nil, false) before Prepare.
func (s *Source) OrgConfig() (cfg *orgconfig.Config, found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg, s.found
}

// RegistryPath returns the MCP registry file below Root
// (profile.RegistryLocator): profiles.mcp_registry of the plugin's org config,
// else mcp/registry.toml.
func (s *Source) RegistryPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cfg != nil {
		return s.cfg.Profiles.MCPRegistry
	}
	return profile.DefaultRegistryPath
}

// Names lists the profiles in the plugin.
func (s *Source) Names() ([]string, error) {
	in, err := s.prepared()
	if err != nil {
		return nil, err
	}
	return in.Names()
}

// Open reads and validates one profile from the plugin.
func (s *Source) Open(name string) (*profile.File, error) {
	in, err := s.prepared()
	if err != nil {
		return nil, err
	}
	f, err := in.Open(name)
	if err != nil {
		return nil, err
	}
	f.Source = s
	return f, nil
}

func (s *Source) prepared() (profile.Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inner == nil {
		return nil, ErrNotPrepared
	}
	return s.inner, nil
}

// Prepare locates the installed plugin and confines the profiles folder to its
// install directory.
func (s *Source) Prepare(ctx context.Context) error {
	plugins, err := s.opts.Installed(ctx)
	if err != nil {
		return fmt.Errorf("listing installed plugins: %w", err)
	}
	var found *claude.Plugin
	for i := range plugins {
		if plugins[i].ID == s.opts.Plugin {
			found = &plugins[i]
			break
		}
	}
	if found != nil {
		if err := s.checkInstalled(found); err != nil {
			return err
		}
	}
	market, err := s.checkMarketplace(ctx)
	if err != nil {
		return err
	}
	if found == nil || found.InstallPath == "" {
		return fmt.Errorf("%w: %s (it carries the shared profiles). Install it with: /plugin install %s", ErrNotInstalled, s.opts.Plugin, s.opts.Plugin)
	}
	if !filepath.IsAbs(found.InstallPath) {
		return fmt.Errorf("plugin %s reports a relative install path", s.opts.Plugin)
	}
	installDir, err := filepath.EvalSymlinks(found.InstallPath)
	if err != nil {
		return fmt.Errorf("plugin %s: install directory is not readable (reinstall it with /plugin install %s): %w", s.opts.Plugin, s.opts.Plugin, err)
	}
	if fi, err := os.Stat(installDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("plugin %s: install path is not a directory", s.opts.Plugin)
	}
	profilesDir := filepath.Join(installDir, filepath.FromSlash(s.path))
	root := filepath.Dir(profilesDir)
	if err := confine(installDir, root, "source root"); err != nil {
		return err
	}
	if err := confine(installDir, profilesDir, "profiles folder"); err != nil {
		return err
	}
	cfg, cfgFound := orgconfig.Default(), false
	if _, err := os.Stat(root); err == nil {
		// A broken org config fails closed: guessing what it meant could mask
		// a protected control. The profiles folder stays where Options.Path
		// says; only the MCP registry location is taken from the config.
		if cfg, cfgFound, err = orgconfig.Find(root); err != nil {
			return fmt.Errorf("plugin %s: org config: %w", s.opts.Plugin, err)
		}
	}
	inner := profile.DirSource(profile.KindOrg, profilesDir)
	if cfg.Profiles.MCPRegistry != profile.DefaultRegistryPath {
		inner = profile.DirSourceAt(profile.KindOrg, root, profile.Layout{Profiles: path.Base(s.path), Registry: cfg.Profiles.MCPRegistry})
	}
	s.mu.Lock()
	s.version = found.Version
	s.market = market
	s.inner = inner
	s.cfg, s.found = cfg, cfgFound
	s.mu.Unlock()
	return nil
}

// confine requires that p, if it exists, resolves to a place inside dir.
func confine(dir, p, what string) error {
	eval, err := filepath.EvalSymlinks(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("resolving the %s: %w", what, err)
	}
	rel, err := filepath.Rel(dir, eval)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("the %s resolves outside the plugin's install directory", what)
	}
	return nil
}

// checkInstalled requires that the plugin is enabled and installed at user or
// managed scope. A project or local plugin comes from the repository the
// session starts in (SR3: a project never supplies the shared profiles), and a
// disabled plugin is not what the user set up.
func (s *Source) checkInstalled(p *claude.Plugin) error {
	switch strings.ToLower(p.Scope) {
	case "user", "managed":
	default:
		return fmt.Errorf("plugin %s is installed at scope %q. A shared profile source must be installed at user or managed scope", s.opts.Plugin, ui.SanitizeLine(p.Scope))
	}
	if !p.Enabled {
		return fmt.Errorf("plugin %s is installed but not enabled. Enable it with: /plugin enable %s", s.opts.Plugin, s.opts.Plugin)
	}
	return nil
}

// matchesExpected reports whether src, the identity of the marketplace as
// claude.Marketplace.Identity gives it, is the one the organization expects (an
// owner/repo shorthand or a git URL, see claude.ExpectedIdentity). The source
// kinds must agree, the host is the only part compared without case, and a
// marketplace added at a ref or sub-path never equals an expected source that
// names none. An expected source that cannot be read matches nothing.
func matchesExpected(src, want string) bool {
	id, err := claude.ExpectedIdentity(want)
	return err == nil && id == src
}

// checkMarketplace looks up the real source of the plugin's marketplace and
// compares it with the expected one. It returns the source to bind into the
// identity, or "" when no lookup was configured.
func (s *Source) checkMarketplace(ctx context.Context) (string, error) {
	if s.opts.MarketplaceSource == nil {
		return "", nil
	}
	_, mkt := claude.SplitID(s.opts.Plugin)
	src, err := s.opts.MarketplaceSource(ctx, mkt)
	if err != nil {
		return "", fmt.Errorf("finding the source of marketplace %q: %w (add the organization's marketplace with: /plugin marketplace add <source>)", ui.SanitizeLine(mkt), err)
	}
	src = strings.TrimSpace(src)
	if src == "" || ui.HasControl(src) {
		return "", fmt.Errorf("marketplace %q reports no usable source", ui.SanitizeLine(mkt))
	}
	if want := s.opts.ExpectedMarketplace; want != "" && !matchesExpected(src, want) {
		return "", fmt.Errorf("marketplace %q was added from %q, not from the expected %q. Remove it with /plugin marketplace remove %s and add the expected one with /plugin marketplace add %s", ui.SanitizeLine(mkt), ui.SanitizeLine(src), ui.SanitizeLine(want), ui.SanitizeLine(mkt), ui.SanitizeLine(want))
	}
	return src, nil
}

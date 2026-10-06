package pluginsource

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ccshelf/ccshelf/internal/claude"
	"github.com/ccshelf/ccshelf/internal/profile"
)

// DefaultPath is the profiles folder inside the plugin when Options.Path is empty.
const DefaultPath = "profiles"

// ErrNotPrepared is returned by Names, Open and Root before Prepare.
var ErrNotPrepared = errors.New("plugin source is not prepared; call Prepare first")

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
}

// Source is a profile source backed by an installed plugin. It satisfies
// profile.Source (kind KindOrg) once Prepare has succeeded.
type Source struct {
	opts Options
	path string // cleaned profiles folder, slash separated

	mu      sync.Mutex
	version string
	root    string
	inner   profile.Source
}

var _ profile.Source = (*Source)(nil)

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
	return &Source{opts: opts, path: p}, nil
}

// Plugin returns the plugin id.
func (s *Source) Plugin() string { return s.opts.Plugin }

// ProtectedPluginIDs returns the plugin that carries the profiles. The
// settings spec must never mask it.
func (s *Source) ProtectedPluginIDs() []string { return []string{s.opts.Plugin} }

// ID returns "plugin:<name@marketplace>".
func (s *Source) ID() string { return "plugin:" + s.opts.Plugin }

// Locator is the same as ID; the version is not part of a plugin's identity.
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

// Root returns the folder that holds profiles/, or "" before Prepare.
func (s *Source) Root() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root
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
	if found == nil || found.InstallPath == "" {
		return fmt.Errorf("%w: %s (it carries the shared profiles); install it with: /plugin install %s", ErrNotInstalled, s.opts.Plugin, s.opts.Plugin)
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
	s.mu.Lock()
	s.version = found.Version
	s.root = root
	s.inner = profile.DirSource(profile.KindOrg, profilesDir)
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

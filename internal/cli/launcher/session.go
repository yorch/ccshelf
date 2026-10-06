package launcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ccshelf/ccshelf/internal/account"
	"github.com/ccshelf/ccshelf/internal/claude"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/config"
	"github.com/ccshelf/ccshelf/internal/orgconfig"
	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/profile/gitsource"
	"github.com/ccshelf/ccshelf/internal/profile/pluginsource"
	"github.com/ccshelf/ccshelf/internal/trust"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// PreparedSource is a profile source that must be prepared (fetched and
// verified) before use. gitsource.Source and pluginsource.Source satisfy it.
type PreparedSource interface {
	profile.Source
	// Prepare makes the source usable.
	Prepare(ctx context.Context) error
}

// GitFactory builds a git source from options. Tests replace it so that no
// repository is cloned.
type GitFactory func(opts gitsource.Options) (PreparedSource, error)

func defaultGit(opts gitsource.Options) (PreparedSource, error) {
	s, err := gitsource.New(opts)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// projectState is what is known about the working directory's .ccshelf folder.
type projectState struct {
	// Root is the real path of the directory that holds .ccshelf, or "".
	Root string
	// Present is true when a .ccshelf folder was found.
	Present bool
	// Allowed is true when project profiles may be loaded (SR2).
	Allowed bool
	// Reason says why project profiles are not loaded, when Present.
	Reason string
}

// session is the resolved shared state of one command invocation.
type session struct {
	l       *launcher
	cc      *clicore.Context
	cfg     *config.Config
	cfgPath string
	// choice and env are the account choice and the child environment.
	choice config.AccountChoice
	env    []string
	cwd    string
	bin    string

	sources          []profile.Source
	protectedPlugins []string
	protectedMCP     []string
	hasPluginSource  bool
	proj             projectState
}

// loadConfig reads the configuration named by --config or the default path.
func loadConfig(cc *clicore.Context) (*config.Config, string, error) {
	p, err := configPath(cc)
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(p)
	if err != nil {
		return nil, "", fmt.Errorf("configuration %s: %w", p, err)
	}
	return cfg, p, nil
}

// envMap turns NAME=value entries into a map.
func envMap(list []string) map[string]string {
	m := map[string]string{}
	for _, e := range list {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = v
		}
	}
	return m
}

// accountEnv resolves the account for a profile account name and returns the
// environment additions.
func accountEnv(cc *clicore.Context, cfg *config.Config, profileAccount string) (config.AccountChoice, map[string]string, error) {
	choice, err := config.ResolveAccount(cc.G.Account, profileAccount, cfg, cc.Getenv)
	if err != nil {
		return choice, nil, ui.Usage(fmt.Errorf("account: %w", err))
	}
	extra := map[string]string{}
	if choice.SetEnv {
		list, err := account.Env(cfg, choice.Name)
		if err != nil {
			return choice, nil, fmt.Errorf("account %s: %w", choice.Name, err)
		}
		for k, v := range envMap(list) {
			extra[k] = v
		}
	}
	return choice, extra, nil
}

// printAccountNotes shows what an account choice ignored or overrode, so the
// choice is never silent.
func printAccountNotes(cc *clicore.Context, choice config.AccountChoice) {
	for _, n := range choice.Notes {
		warnf(cc, "%s", n)
	}
}

// open loads the configuration, resolves the initial account (the profile's
// own account is applied later by run), and builds the sources. When prepare
// is false git and plugin sources are not fetched or located (used by
// commands that only need local names).
func (l *launcher) open(ctx context.Context, cc *clicore.Context, prepare, needClaude bool) (*session, error) {
	cfg, path, err := loadConfig(cc)
	if err != nil {
		return nil, err
	}
	cwd, err := cc.Getwd()
	if err != nil {
		return nil, fmt.Errorf("working directory: %w", err)
	}
	choice, extra, err := accountEnv(cc, cfg, "")
	if err != nil {
		return nil, err
	}
	s := &session{l: l, cc: cc, cfg: cfg, cfgPath: path, choice: choice, cwd: cwd,
		env: claude.Env(cc.Environ(), extra)}
	if needClaude {
		if _, err := s.locate(); err != nil {
			return nil, ui.Failure(err)
		}
	}
	if err := s.buildSources(ctx, prepare); err != nil {
		return nil, err
	}
	return s, nil
}

// locate finds the claude binary: --claude, then claude.path from the
// configuration, then the usual search (see claude.Locate).
func (s *session) locate() (string, error) {
	if s.bin != "" {
		return s.bin, nil
	}
	override := s.cc.G.ClaudePath
	if override == "" {
		override = s.cfg.Claude.Path
	}
	bin, err := claude.Locate(override)
	if err != nil {
		if errors.Is(err, claude.ErrNotFound) {
			return "", withHint(err, "install Claude Code, or pass --claude <path> or set claude.path in the configuration")
		}
		return "", err
	}
	s.bin = bin
	return bin, nil
}

// findProject walks up from dir to the nearest directory holding .ccshelf
// (never the user's home directory, which is not a project).
func findProject(dir string) string {
	home, _ := os.UserHomeDir()
	for {
		if dir != home {
			if fi, err := os.Lstat(filepath.Join(dir, trust.ProjectFolder)); err == nil && fi.IsDir() {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func detectProject(cfg *config.Config, cwd string) projectState {
	root := findProject(cwd)
	if root == "" {
		return projectState{}
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	st := projectState{Root: root, Present: true}
	if !cfg.Trust.TrustProjectProfiles {
		st.Reason = "project profiles are off (trust.trust_project_profiles is false)"
		return st
	}
	pp, err := config.ProjectTrustPath()
	if err != nil {
		st.Reason = fmt.Sprintf("project trust file: %v", err)
		return st
	}
	ps, err := trust.OpenProjects(pp)
	if err != nil {
		st.Reason = fmt.Sprintf("project trust file: %v", err)
		return st
	}
	ok, err := trust.ProjectAllowed(cfg, ps, root)
	switch {
	case err != nil:
		st.Reason = fmt.Sprintf("the .ccshelf folder cannot be trusted: %v", err)
	case !ok:
		st.Reason = "the .ccshelf folder is not trusted (or changed since); review it, then run: ccshelf trust --project"
	default:
		st.Allowed = true
	}
	return st
}

// sameDir reports whether two directories are the same place (best effort,
// resolving symlinks that exist).
func sameDir(a, b string) bool {
	norm := func(p string) string {
		p = filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return norm(a) == norm(b)
}

// buildSources assembles the source list: the personal directory first, then
// the configured sources in order, then the trusted project folder.
func (s *session) buildSources(ctx context.Context, prepare bool) error {
	personal, err := profile.PersonalDir()
	if err != nil {
		return fmt.Errorf("personal profiles directory: %w", err)
	}
	s.sources = append(s.sources, profile.DirSource(profile.KindPersonal, personal))
	newGit := s.l.opt.NewGit
	if newGit == nil {
		newGit = defaultGit
	}
	for i, sc := range s.cfg.Sources {
		switch sc.Type {
		case config.SourceDir:
			p, err := sc.ResolvedPath()
			if err != nil {
				return fmt.Errorf("sources[%d]: %w", i, err)
			}
			if sameDir(p, personal) {
				continue
			}
			s.sources = append(s.sources, profile.DirSource(profile.KindOrg, p))
		case config.SourceGit:
			if !prepare {
				continue
			}
			g, err := newGit(gitsource.Options{URL: sc.URL, Ref: sc.Ref, Subpath: sc.Path, RequirePin: s.cfg.Trust.RequirePin})
			if err != nil {
				return fmt.Errorf("sources[%d] (git %s): %w", i, ui.Sanitize(sc.URL), err)
			}
			if err := g.Prepare(ctx); err != nil {
				return fmt.Errorf("sources[%d] (git %s): %w", i, ui.Sanitize(sc.URL), err)
			}
			s.sources = append(s.sources, g)
		case config.SourcePlugin:
			s.hasPluginSource = true
			if !prepare {
				continue
			}
			ps, err := pluginsource.New(pluginsource.Options{Plugin: sc.Plugin, Path: sc.Path, Installed: s.listInstalledFor})
			if err != nil {
				return fmt.Errorf("sources[%d] (plugin %s): %w", i, ui.Sanitize(sc.Plugin), err)
			}
			if err := ps.Prepare(ctx); err != nil {
				return fmt.Errorf("sources[%d] (plugin %s): %w", i, ui.Sanitize(sc.Plugin), err)
			}
			s.protectedPlugins = append(s.protectedPlugins, ps.ProtectedPluginIDs()...)
			s.sources = append(s.sources, ps)
		default:
			return fmt.Errorf("sources[%d]: unknown type %q", i, sc.Type)
		}
	}
	s.proj = detectProject(s.cfg, s.cwd)
	if s.proj.Allowed {
		s.sources = append(s.sources, profile.DirSource(profile.KindProject, filepath.Join(s.proj.Root, trust.ProjectFolder, "profiles")))
	}
	if !prepare {
		return nil
	}
	return s.collectProtected()
}

// listInstalledFor lists installed plugins for the plugin source (never
// cached: a source needs the live answer).
func (s *session) listInstalledFor(ctx context.Context) ([]claude.Plugin, error) {
	bin, err := s.locate()
	if err != nil {
		return nil, err
	}
	return claude.ListInstalled(ctx, bin, s.cwd, s.env)
}

// collectProtected reads the protected plugins and MCP labels (SR3) declared
// by the org config of every shared source. A broken org config fails closed:
// guessing would risk masking a protected control.
func (s *session) collectProtected() error {
	seen := map[string]bool{}
	for _, src := range s.sources {
		if src.Kind() != profile.KindOrg || src.Root() == "" || seen[src.Root()] {
			continue
		}
		seen[src.Root()] = true
		oc, err := orgconfig.Load(src.Root())
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("org config of source %s: %w", ui.Sanitize(profile.PortableSourceID(src)), err)
		}
		s.protectedPlugins = append(s.protectedPlugins, oc.Protect.Plugins...)
		s.protectedMCP = append(s.protectedMCP, oc.Protect.MCP...)
	}
	s.protectedPlugins = uniqSorted(s.protectedPlugins)
	s.protectedMCP = uniqSorted(s.protectedMCP)
	return nil
}

func uniqSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	m := map[string]bool{}
	for _, x := range in {
		m[x] = true
	}
	out := make([]string, 0, len(m))
	for x := range m {
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

// resolve resolves a profile across the sources and maps the failures to exit
// codes: not found is a usage error (2), an untrusted project is a trust error
// (4), the rest are failures (1).
func (s *session) resolve(name string) (*profile.Resolved, error) {
	r, err := profile.Resolve(name, s.sources, profile.ResolveOptions{AllowProject: s.proj.Allowed})
	if err == nil {
		return r, nil
	}
	switch {
	case errors.Is(err, profile.ErrNotFound):
		hint := "ccshelf ls lists the available profiles"
		if s.proj.Present && !s.proj.Allowed {
			hint += "; this directory has a .ccshelf folder that is not loaded: " + s.proj.Reason
		}
		return nil, ui.Usage(withHint(err, "%s", hint))
	case errors.Is(err, profile.ErrProjectNotTrusted):
		return nil, ui.TrustRequired(withHint(err, "review the folder, then run: ccshelf trust --project"))
	}
	return nil, ui.Failure(err)
}

// sourceLabel maps a source id to a machine-independent label.
func (s *session) sourceLabels() map[string]string {
	m := map[string]string{}
	for _, src := range s.sources {
		m[src.ID()] = profile.PortableSourceID(src)
	}
	return m
}

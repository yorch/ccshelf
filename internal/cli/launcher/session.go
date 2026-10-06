package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/ccshelf/ccshelf/internal/account"
	"github.com/ccshelf/ccshelf/internal/cache"
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

// cachedPreparer is implemented by git sources that can be prepared from a
// verified cached checkout of a known commit without any network access
// (gitsource.Source). Sources that do not implement it are always prepared
// with Prepare.
type cachedPreparer interface {
	PrepareCached(ctx context.Context, commit string) error
}

// orgConfigSource is implemented by sources that read the org config
// (ccshelf.toml) themselves, from the pinned tree they verified.
type orgConfigSource interface {
	OrgConfig() (cfg *orgconfig.Config, found bool)
}

// sourceFailure is a shared source that could not be loaded in this run.
type sourceFailure struct {
	label string
	err   error
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

	sources []profile.Source
	// protectedPlugins and protectedMCP are what the settings must never mask:
	// the declared lists plus, for a source that is unavailable, the last
	// known ones. pinPlugins and pinMCP are the declared lists only; they are
	// what the trust closure pins.
	protectedPlugins []string
	protectedMCP     []string
	pinPlugins       []string
	pinMCP           []string
	assumedPlugins   []string
	assumedMCP       []string
	hasPluginSource  bool
	proj             projectState

	// refresh makes git sources re-resolve their tag on the remote instead of
	// using the commit the trust lockfile pinned (trust, ls --refresh).
	refresh bool
	// failed lists the shared sources that could not be loaded; their profiles
	// are unavailable but nothing else is affected.
	failed []sourceFailure
	// gitIDs holds the ids of the prepared git sources, which are the ones
	// expected to carry a ccshelf.toml.
	gitIDs map[string]bool
	// lock caches the trust lockfile entries read for the cached commits.
	lock       []trust.Entry
	lockLoaded bool
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

// openOpts says what open prepares.
type openOpts struct {
	// prepare fetches and verifies the git sources and locates the plugin
	// sources. Without it only local names are available.
	prepare bool
	// needClaude locates the claude binary.
	needClaude bool
	// refresh re-resolves git tags on the remote even when the trust lockfile
	// pins a cached commit (the trust command and ls --refresh).
	refresh bool
}

// open loads the configuration, resolves the initial account (the profile's
// own account is applied later by run), and builds the sources. When prepare
// is false git and plugin sources are not fetched or located (used by
// commands that only need local names).
func (l *launcher) open(ctx context.Context, cc *clicore.Context, prepare, needClaude bool) (*session, error) {
	return l.openWith(ctx, cc, openOpts{prepare: prepare, needClaude: needClaude})
}

// openWith is open with all the options.
func (l *launcher) openWith(ctx context.Context, cc *clicore.Context, o openOpts) (*session, error) {
	cfg, cfgPath, err := loadConfig(cc)
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
	s := &session{
		l: l, cc: cc, cfg: cfg, cfgPath: cfgPath, choice: choice, cwd: cwd,
		env: claude.Env(cc.Environ(), extra), refresh: o.refresh, gitIDs: map[string]bool{},
	}
	if o.needClaude {
		if _, err := s.locate(); err != nil {
			return nil, ui.Failure(err)
		}
	}
	if err := s.buildSources(ctx, o.prepare); err != nil {
		return nil, err
	}
	if o.prepare {
		s.pruneCache()
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
func findProject(dir, goos string) string {
	home, _ := os.UserHomeDir()
	for {
		if !samePath(dir, home, goos) {
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

func detectProject(cfg *config.Config, cwd, goos string) projectState {
	root := findProject(cwd, goos)
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

// caseInsensitiveOS reports whether paths are compared without regard to case
// on goos (the default file systems of Windows and macOS are case-insensitive).
func caseInsensitiveOS(goos string) bool { return goos == "windows" || goos == "darwin" }

// samePath compares two cleaned paths, ignoring case on Windows and macOS. An
// empty path equals nothing.
func samePath(a, b, goos string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if caseInsensitiveOS(goos) {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// sameDir reports whether two directories are the same place (best effort,
// resolving symlinks that exist, and ignoring case on Windows and macOS).
func sameDir(a, b, goos string) bool {
	norm := func(p string) string {
		p = filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return samePath(norm(a), norm(b), goos)
}

// buildSources assembles the source list: the personal directory first, then
// the configured sources in order, then the trusted project folder. A shared
// source that cannot be loaded is reported as a warning and left out (see
// failSource); the others still load.
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
			if sameDir(p, personal, s.cc.GOOS) {
				continue
			}
			src := s.orgDirSource(i, p)
			if prepare {
				s.addShared(fmt.Sprintf("sources[%d] (dir %s)", i, ui.Sanitize(p)), src, "")
			} else {
				s.sources = append(s.sources, src)
			}
		case config.SourceGit:
			if !prepare {
				continue
			}
			label := fmt.Sprintf("sources[%d] (git %s)", i, ui.Sanitize(sc.URL))
			g, err := s.prepareGit(ctx, newGit, sc)
			if err != nil {
				if errors.Is(err, orgconfig.ErrInvalid) {
					return fmt.Errorf("%s: %w", label, err)
				}
				s.failSource(label, err, "git:"+sc.URL)
				continue
			}
			if s.addShared(label, g, "git:"+sc.URL) {
				s.gitIDs[g.ID()] = true
			}
		case config.SourcePlugin:
			s.hasPluginSource = true
			if !prepare {
				continue
			}
			label := fmt.Sprintf("sources[%d] (plugin %s)", i, ui.Sanitize(sc.Plugin))
			ps, err := pluginsource.New(pluginsource.Options{Plugin: sc.Plugin, Path: sc.Path, Installed: s.listInstalledFor})
			if err != nil {
				s.failSource(label, err, "")
				continue
			}
			// The plugin that carries the profiles is protected even when it
			// cannot be read this time.
			s.protectedPlugins = append(s.protectedPlugins, ps.ProtectedPluginIDs()...)
			if err := ps.Prepare(ctx); err != nil {
				if errors.Is(err, orgconfig.ErrInvalid) {
					return fmt.Errorf("%s: %w", label, err)
				}
				s.failSource(label, err, "")
				continue
			}
			s.addShared(label, ps, "")
		default:
			return fmt.Errorf("sources[%d]: unknown type %q", i, sc.Type)
		}
	}
	s.proj = detectProject(s.cfg, s.cwd, s.cc.GOOS)
	if s.proj.Allowed {
		s.sources = append(s.sources, profile.DirSource(profile.KindProject, filepath.Join(s.proj.Root, trust.ProjectFolder, "profiles")))
	}
	if !prepare {
		return nil
	}
	return s.collectProtected()
}

// failSource records that a shared source cannot be used in this run and
// says so. Its profiles are unavailable; every other source, and the personal
// profiles, are unaffected. Protection never fails open for what is known: a
// source that was trusted before keeps its last known protected plugins and
// MCP servers (from the trust lockfile) enforced for every profile; only a
// source that was never trusted or loaded, of which nothing is known, is
// simply left out. locator is the lockfile key of the source ("" when it has
// none).
func (s *session) failSource(label string, err error, locator string) {
	s.failed = append(s.failed, sourceFailure{label: label, err: err})
	plugins, mcp := s.lastKnownProtected(locator)
	msg := "nothing is known about the plugins and MCP servers it protects, so none are enforced for it"
	if len(plugins)+len(mcp) > 0 {
		s.assumedPlugins = append(s.assumedPlugins, plugins...)
		s.assumedMCP = append(s.assumedMCP, mcp...)
		msg = "the protected plugins and MCP servers recorded when it was last trusted are still enforced"
	}
	warnf(s.cc, "%s is unavailable: %s; its profiles are not available in this run; %s", label, ui.SanitizeLine(err.Error()), msg)
}

// lastKnownProtected returns the protected plugins and MCP labels pinned in
// the trust lockfile entries that involve the source with the given locator.
// The entries pin every protected control of their closure, so the result may
// include controls of other sources; that only protects more.
func (s *session) lastKnownProtected(locator string) (plugins, mcp []string) {
	if locator == "" {
		return nil, nil
	}
	for _, e := range s.lockEntries() {
		involved := e.Source == locator
		for _, r := range e.Sources {
			involved = involved || r.Source == locator
		}
		if !involved {
			continue
		}
		for _, it := range e.Items {
			switch it.Kind {
			case itemProtectPlugin:
				plugins = append(plugins, it.Name)
			case itemProtectMCP:
				mcp = append(mcp, it.Name)
			}
		}
	}
	return plugins, mcp
}

// failedSummary describes the unavailable sources for an error message.
func (s *session) failedSummary() string {
	parts := make([]string, 0, len(s.failed))
	for _, f := range s.failed {
		parts = append(parts, fmt.Sprintf("%s is unavailable: %s", f.label, ui.SanitizeLine(f.err.Error())))
	}
	return strings.Join(parts, "; ")
}

// addShared adds a prepared shared source, after checking that its profiles
// can be listed; a source that cannot is reported like any other failure. It
// reports whether the source was added.
func (s *session) addShared(label string, src profile.Source, locator string) bool {
	if _, err := src.Names(); err != nil {
		s.failSource(label, err, locator)
		return false
	}
	s.sources = append(s.sources, src)
	return true
}

// prepareGit builds and prepares one git source. When the trust lockfile
// already pins the commit of its tag and a verified checkout of it is cached,
// the source is prepared from the cache without contacting the remote: what
// runs is exactly what was trusted, and an unreachable remote does not matter.
// The tag is re-resolved on the remote only for the commands that review
// (trust, ls --refresh), for a ref that is not a tag or SHA pin
// (trust.require_pin = false), and when nothing usable is cached.
func (s *session) prepareGit(ctx context.Context, newGit GitFactory, sc config.SourceConfig) (PreparedSource, error) {
	g, err := newGit(gitsource.Options{URL: sc.URL, Ref: sc.Ref, Subpath: sc.Path, RequirePin: s.cfg.Trust.RequirePin})
	if err != nil {
		return nil, err
	}
	if commit := s.lockedCommit(sc); commit != "" {
		if cp, ok := g.(cachedPreparer); ok && cp.PrepareCached(ctx, commit) == nil {
			return g, nil
		}
		// Not cached, or the cached copy failed its checks: Prepare fetches
		// again, or reports the problem with the folder to delete.
	}
	if err := g.Prepare(ctx); err != nil {
		return nil, err
	}
	return g, nil
}

var fullSHA = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// lockedCommit returns the commit that the trust lockfile recorded for the tag
// of sc, or "" when the cache must not be used for it: after --refresh, when
// refs may move (require_pin is off), when the ref is a commit SHA already,
// or when nothing is recorded. When several records differ (a tag that moved
// between two acceptances) the most recent one wins; a profile accepted
// against the other commit then needs a new review, as it should.
func (s *session) lockedCommit(sc config.SourceConfig) string {
	if s.refresh || !s.cfg.Trust.RequirePin || fullSHA.MatchString(strings.ToLower(sc.Ref)) {
		return ""
	}
	locator := "git:" + sc.URL
	var best string
	var bestAt time.Time
	for _, e := range s.lockEntries() {
		recs := e.Sources
		if len(recs) == 0 {
			recs = []trust.SourceRecord{{Source: e.Source, Ref: e.Ref, Commit: e.Commit}}
		}
		for _, r := range recs {
			if r.Source == locator && r.Ref == sc.Ref && fullSHA.MatchString(r.Commit) && (best == "" || e.AcceptedAt.After(bestAt)) {
				best, bestAt = r.Commit, e.AcceptedAt
			}
		}
	}
	return best
}

// lockEntries returns the trust lockfile entries (none when it cannot be read:
// the trust check reports that problem itself).
func (s *session) lockEntries() []trust.Entry {
	if s.lockLoaded {
		return s.lock
	}
	s.lockLoaded = true
	p, err := config.LockfilePath()
	if err != nil {
		return nil
	}
	st, err := trust.Open(p)
	if err != nil {
		return nil
	}
	s.lock = st.List()
	return s.lock
}

// orgDirSource builds the source of a configured directory. When the folder is
// the profiles folder of an org data repository, the org config
// (ccshelf.toml) found in the repository root says where the MCP registry is
// (profiles.mcp_registry), as it does for the lint; the profiles folder is the
// one the user configured, and a disagreement with profiles.dir is reported.
func (s *session) orgDirSource(i int, p string) profile.Source {
	def := profile.DirSource(profile.KindOrg, p)
	root := filepath.Dir(p)
	for level := 0; level < 3; level++ {
		cfg, found, err := orgconfig.Find(root)
		if err == nil && found {
			rel, rerr := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			switch {
			case rerr == nil && rel == path.Clean(cfg.Profiles.Dir):
				return profile.DirSourceAt(profile.KindOrg, root, profile.Layout{Profiles: cfg.Profiles.Dir, Registry: cfg.Profiles.MCPRegistry})
			case level == 0 && filepath.Base(p) == "profiles":
				warnf(s.cc, "sources[%d]: the org config in %s says profiles.dir = %q, but this source uses the folder %q; using the folder from the configuration",
					i, ui.Sanitize(root), cfg.Profiles.Dir, rel)
				return profile.DirSourceAt(profile.KindOrg, root, profile.Layout{Profiles: "profiles", Registry: cfg.Profiles.MCPRegistry})
			}
		}
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	return def
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

// orgConfigOf returns the org config of a shared source and whether the source
// has one. Sources that read it themselves (git, plugin) answer from the
// verified tree; for the others it is read from the root of the source, which
// is confined to that root.
func orgConfigOf(src profile.Source) (cfg *orgconfig.Config, found bool, err error) {
	if oc, ok := src.(orgConfigSource); ok {
		if cfg, found = oc.OrgConfig(); cfg != nil {
			return cfg, found, nil
		}
	}
	if src.Root() == "" {
		return orgconfig.Default(), false, nil
	}
	return orgconfig.Find(src.Root())
}

// collectProtected reads the protected plugins and MCP labels (SR3) declared
// by the org config of every shared source. A broken org config is an error
// that stops the command: guessing what it meant could mask a protected
// control. A git source whose repository has no ccshelf.toml gets a warning,
// so that a repository that forgot the file is not mistaken for one that
// protects nothing; an empty ccshelf.toml is the way to say so.
func (s *session) collectProtected() error {
	for _, src := range s.sources {
		if src.Kind() != profile.KindOrg || src.Root() == "" {
			continue
		}
		oc, found, err := orgConfigOf(src)
		if err != nil {
			return fmt.Errorf("org config of source %s: %w", ui.Sanitize(profile.PortableSourceID(src)), err)
		}
		if !found && s.gitIDs[src.ID()] {
			warnf(s.cc, "source %s has no %s: no protected plugins or MCP servers are declared by it (add a %s, which may be empty, to the repository to say so)",
				ui.Sanitize(profile.PortableSourceID(src)), orgconfig.FileName, orgconfig.FileName)
		}
		s.protectedPlugins = append(s.protectedPlugins, oc.Protect.Plugins...)
		s.protectedMCP = append(s.protectedMCP, oc.Protect.MCP...)
	}
	// What the closure pins is what is declared now; the last-known lists of
	// unavailable sources only guard the settings.
	s.pinPlugins = uniqSorted(s.protectedPlugins)
	s.pinMCP = uniqSorted(s.protectedMCP)
	s.protectedPlugins = uniqSorted(append(s.protectedPlugins, s.assumedPlugins...))
	s.protectedMCP = uniqSorted(append(s.protectedMCP, s.assumedMCP...))
	return nil
}

// Closure item kinds for the protected controls (SR3). The protect lists live in
// each org source's ccshelf.toml, outside the profile files, so they are pinned
// here: an edit of a list (a plugin or MCP server dropped from protection)
// changes the closure hash and needs a review like any other risky change.
const (
	itemProtectPlugin = "protect-plugin"
	itemProtectMCP    = "protect-mcp"
)

// pinProtected adds the protected plugins and MCP labels to the closure of r
// and recomputes its hash. The trust check, the accept command and the run
// pipeline all resolve through session.resolve, so they agree on one hash.
// A closure without any protected control is left as Resolve built it.
func (s *session) pinProtected(r *profile.Resolved) {
	if len(s.pinPlugins) == 0 && len(s.pinMCP) == 0 {
		return
	}
	items := append([]profile.ClosureItem(nil), r.Closure.Items...)
	add := func(kind string, names []string) {
		for _, n := range names {
			items = append(items, profile.ClosureItem{Kind: kind, Name: n, Digest: profile.DigestBytes([]byte(kind + "\x00" + n)), Risky: true})
		}
	}
	add(itemProtectPlugin, s.pinPlugins)
	add(itemProtectMCP, s.pinMCP)
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Digest < b.Digest
	})
	r.Closure = profile.Closure{Hash: profile.HashItems(items), Items: items}
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
		s.pinProtected(r)
		return r, nil
	}
	switch {
	case errors.Is(err, profile.ErrNotFound) && len(s.failed) > 0:
		// The profile may be in a source that could not be loaded.
		return nil, ui.Failure(withHint(fmt.Errorf("%w; %s", err, s.failedSummary()),
			"fix the source, or run again when it is reachable; profiles of the other sources are not affected"))
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

// pruneStamp is the cache file whose content is the time of the last pruning.
var pruneStamp = "prune-" + strings.Repeat("0", 32) + ".stamp"

// pruneInterval is how often the cache is pruned at most.
const pruneInterval = 24 * time.Hour

// pruneCache removes cache files and git checkouts that have not been used
// for cache.DefaultMaxAge, at most once a day (a stamp file records the last
// time). The checkouts that the trust lockfile pins are kept whatever their
// age: they are what a run without network uses. It is best effort and never
// fails a run.
func (s *session) pruneCache() {
	dir, err := cache.Dir()
	if err != nil {
		return
	}
	now := time.Now()
	if s.cc.Now != nil {
		now = s.cc.Now()
	}
	if b, err := cache.ReadFile(dir, pruneStamp); err == nil {
		if last, perr := time.Parse(time.RFC3339, string(b)); perr == nil && now.Sub(last) < pruneInterval && !last.After(now) {
			return
		}
	}
	// Stamp first: a pruning that is interrupted must not repeat on every run.
	if err := cache.WriteReplace(dir, pruneStamp, []byte(now.UTC().Format(time.RFC3339))); err != nil {
		return
	}
	pinned := map[string]bool{}
	base := filepath.Join(dir, "git")
	for _, e := range s.lockEntries() {
		for _, r := range e.Sources {
			if url, ok := strings.CutPrefix(r.Source, "git:"); ok && fullSHA.MatchString(r.Commit) {
				pinned[filepath.Clean(gitsource.CheckoutDir(base, url, r.Commit))] = true
			}
		}
		if url, ok := strings.CutPrefix(e.Source, "git:"); ok && fullSHA.MatchString(e.Commit) {
			pinned[filepath.Clean(gitsource.CheckoutDir(base, url, e.Commit))] = true
		}
	}
	_, _ = cache.PruneDir(dir, cache.DefaultMaxAge, func(p string) bool { return pinned[filepath.Clean(p)] })
}

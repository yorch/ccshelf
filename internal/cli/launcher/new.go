package launcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cache"
	"github.com/yorch/ccshelf/internal/claude"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
)

type newFlags struct {
	scope                                  string
	scopeGiven, yes, contentGiven          bool
	from, plugins, exclude, skillsOff, mcp []string
	description, owner, model, effort      string
}

// given reports whether any content flag was set (name aside), so that the
// wizard opens only when the command line said nothing about the profile.
func (f *newFlags) given() bool {
	return f.contentGiven || len(f.from) > 0 || len(f.plugins) > 0 || len(f.exclude) > 0 || len(f.skillsOff) > 0 || len(f.mcp) > 0 ||
		f.description != "" || f.owner != "" || f.model != "" || f.effort != ""
}

// record adds every flag that is set to the equivalent command, in one place
// so that no flag can be left out of it.
func (f *newFlags) record(rec *ui.Recorder) {
	for _, p := range f.from {
		rec.Flag("--from", p)
	}
	for _, p := range f.plugins {
		rec.Flag("--plugin", p)
	}
	for _, p := range f.exclude {
		rec.Flag("--exclude-plugin", p)
	}
	for _, p := range f.skillsOff {
		rec.Flag("--skill-off", p)
	}
	for _, p := range f.mcp {
		rec.Flag("--mcp", p)
	}
	for _, kv := range []struct{ flag, val string }{
		{"--description", f.description}, {"--owner", f.owner}, {"--model", f.model}, {"--effort", f.effort},
	} {
		if kv.val != "" {
			rec.Flag(kv.flag, kv.val)
		}
	}
}

func (l *launcher) newCmd() *cobra.Command {
	var f newFlags
	c := &cobra.Command{
		Use:   "new [name]",
		Short: "Create a user or project profile",
		Long: `Create a separate TOML file in your user profiles directory (default), or
with --scope project in the Git root/.ccshelf/profiles (cwd outside Git).
--from names a parent profile to extend (repeatable). With no content flags,
a terminal offers a wizard and prints its equivalent flag command. Creation
never enables or trusts project profiles. --yes confirms creation only.`,
		Example: `  ccshelf new sre-night --from base --plugin sre-kit@acme
  ccshelf new notes --description "Writing and notes" --skill-off legacy-helper`,
		Args: cobra.MaximumNArgs(1),
	}
	c.Flags().StringVar(&f.scope, "scope", "user", "profile location: user or project")
	c.Flags().BoolVar(&f.yes, "yes", false, "skip creation confirmation only (never accepts trust)")
	c.Flags().StringArrayVar(&f.from, "from", nil, "parent profile to extend (repeatable)")
	c.Flags().StringArrayVar(&f.plugins, "plugin", nil, "plugin to include, name@marketplace (repeatable)")
	c.Flags().StringArrayVar(&f.exclude, "exclude-plugin", nil, "plugin to always mask, name@marketplace (repeatable)")
	c.Flags().StringArrayVar(&f.skillsOff, "skill-off", nil, "standalone skill to turn off (repeatable)")
	c.Flags().StringArrayVar(&f.mcp, "mcp", nil, "MCP server from the registry to add (repeatable)")
	c.Flags().StringVar(&f.description, "description", "", "one-line description")
	c.Flags().StringVar(&f.owner, "owner", "", "owner (team or person)")
	c.Flags().StringVar(&f.model, "model", "", "default model")
	c.Flags().StringVar(&f.effort, "effort", "", "default effort level")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, cmd *cobra.Command, args []string) error {
		f.scopeGiven = cmd.Flags().Changed("scope")
		for _, flag := range []string{"from", "plugin", "exclude-plugin", "skill-off", "mcp", "description", "owner", "model", "effort"} {
			f.contentGiven = f.contentGiven || cmd.Flags().Changed(flag)
		}
		return l.newProfile(ctx, cc, first(args), &f)
	})
	return c
}

func (l *launcher) newProfile(ctx context.Context, cc *clicore.Context, name string, f *newFlags) error {
	if f.scope != "user" && f.scope != "project" {
		return ui.Usage(errors.New("--scope must be user or project"))
	}
	interactive := canPrompt(cc) && cc.Getenv("CI") == ""
	fullWizard := interactive && !f.given()
	asked := false
	if name == "" {
		if !interactive {
			return ui.Usage(withHint(errors.New("missing argument <name>"), "run: ccshelf new <name> [--from <profile>] [--plugin <id>]"))
		}
		n, err := cc.Prompt.Input(ctx, "Profile name", "", func(s string) error {
			if !profile.ValidName(s) {
				return errors.New("use lower-case letters, digits and hyphens, starting with a letter or digit")
			}
			return nil
		})
		if err != nil {
			return err
		}
		name, asked = n, true
	}
	if !profile.ValidName(name) {
		return ui.Usage(fmt.Errorf("invalid profile name %q: use lower-case letters, digits and hyphens, starting with a letter or digit", ui.Sanitize(name)))
	}
	if fullWizard && !f.scopeGiven {
		i, err := cc.Prompt.Select(ctx, ui.Question{Title: "Profile location", Options: []ui.Option{
			{Label: "User config (personal)", Value: "user"}, {Label: "Repository-local (project; off and untrusted by default)", Value: "project"},
		}, Default: 0, HasDefault: true})
		if err != nil {
			return err
		}
		f.scope = []string{"user", "project"}[i]
	}
	needSources := len(f.from) > 0 || fullWizard
	s, err := l.open(ctx, cc, needSources)
	if err != nil {
		return err
	}
	dir, err := profile.PersonalDir()
	kind := profile.KindPersonal
	if f.scope == "project" {
		dir, err = profile.ProjectProfilesDir(s.cwd)
		kind = profile.KindProject
	}
	if err != nil {
		return fmt.Errorf("profiles destination: %w", err)
	}
	if kind == profile.KindPersonal {
		if err := profile.CheckPersonalDestination(dir); err != nil {
			return ui.Failure(err)
		}
	} else if err := profile.CheckDestination(dir); err != nil {
		return ui.Failure(err)
	}
	target := filepath.Join(dir, name+".toml")
	if _, err := os.Lstat(target); err == nil {
		if kind == profile.KindPersonal {
			return ui.Failure(withHint(fmt.Errorf("profile %q already exists: %s", name, target), "edit it with: ccshelf edit %s", name))
		}
		return ui.Failure(fmt.Errorf("profile %q already exists: %s", name, target))
	} else if !errors.Is(err, os.ErrNotExist) {
		return ui.Failure(err)
	}
	if err := s.newNamespaces(ctx, needSources, dir); err != nil {
		return err
	}
	if err := s.checkNewName(name, f.scope); err != nil {
		return err
	}
	if fullWizard {
		if err := s.wizardNew(ctx, f); err != nil {
			return err
		}
		asked = true
	}

	for _, c := range []struct {
		flag string
		ids  []string
	}{{"--plugin", f.plugins}, {"--exclude-plugin", f.exclude}} {
		for _, id := range c.ids {
			if !validPluginID(id) {
				return ui.Usage(fmt.Errorf("%s: %q is not a plugin id of the form name@marketplace (letters, digits, '.', '_' and '-', not starting with '-')", c.flag, ui.SanitizeLine(id)))
			}
		}
	}
	m := profile.Manifest{
		Name: name, Description: f.description, Owner: f.owner, Extends: f.from,
		Plugins: profile.Plugins{Include: f.plugins, Exclude: f.exclude},
		Skills:  profile.Skills{Off: f.skillsOff},
		MCP:     profile.MCP{Servers: f.mcp},
		Session: profile.Session{Model: f.model, Effort: f.effort},
	}
	raw, err := toml.Marshal(m)
	if err != nil {
		return ui.Failure(fmt.Errorf("encoding the profile: %w", err))
	}
	raw = append([]byte("# Created by ccshelf new. See docs for every key.\n"), raw...)
	if _, err := profile.Parse(raw, name+".toml"); err != nil {
		return ui.Usage(fmt.Errorf("the profile would be invalid: %w", err))
	}
	// Resolve the candidate from memory using the normal origin/parent/MCP
	// rules. AllowProject here validates only; it never grants runtime trust.
	candidate := &newProfileSource{Source: profile.DirSource(kind, dir), name: name, raw: raw, target: target}
	sources := append([]profile.Source(nil), s.sources...)
	replaced := false
	for i, src := range sources {
		if src.ID() == candidate.ID() {
			sources[i] = candidate
			replaced = true
		}
	}
	if !replaced {
		sources = append(sources, candidate)
	}
	if _, err := profile.Resolve(name, sources, profile.ResolveOptions{AllowProject: kind == profile.KindProject || s.proj.Allowed}); err != nil {
		return fmt.Errorf("the new profile does not resolve (nothing was kept): %w", err)
	}
	if fullWizard {
		fmt.Fprintln(cc.Streams.Err, "Destination:", ui.SanitizeLine(target))
		fmt.Fprintln(cc.Streams.Err, "Profile summary:\n"+ui.Sanitize(strings.TrimSpace(string(raw))))
		if !f.yes {
			ok, err := cc.Prompt.Confirm(ctx, "Create this profile? (does not grant trust)", false)
			if err != nil {
				return err
			}
			if !ok {
				return ui.Failure(errors.New("canceled: nothing was written"))
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeNewProfileFile(kind, target, raw); err != nil {
		return ui.Failure(err)
	}
	okf(cc, "created %s", target)
	if kind == profile.KindProject {
		home, _ := os.UserHomeDir()
		if sameDir(filepath.Dir(filepath.Dir(dir)), home, cc.GOOS) {
			warnf(cc, "Creation does not enable or trust project profiles. The launcher does not discover the home directory as a project; move .ccshelf into a project folder before explicitly enabling and reviewing it.")
		} else {
			warnf(cc, "Creation does not enable or trust project profiles. They are off by default until explicitly enabled and reviewed. Set trust.trust_project_profiles = true, then review with: ccshelf trust --project. Review the resolved profile with: ccshelf trust %s", name)
		}
	}
	if asked {
		rec := ui.NewRecorder("new", name)
		rec.Flag("--scope", f.scope)
		rec.Bool("--yes")
		rec.Bool("--no-interactive")
		f.record(rec)
		printEquivalent(cc, rec)
	}
	return nil
}

// wizardNew asks for what is missing: a description, a parent, plugins, skills
// to hide and MCP servers.
func (s *session) wizardNew(ctx context.Context, f *newFlags) error {
	cc := s.cc
	desc, err := cc.Prompt.Input(ctx, "Description (one line, may be empty)", "", func(v string) error {
		if strings.ContainsAny(v, "\r\n") {
			return errors.New("one line only")
		}
		return nil
	})
	if err != nil {
		return err
	}
	f.description = strings.TrimSpace(desc)

	picker := *s
	if f.scope == "user" && !s.proj.Allowed {
		picker.sources = nil
		for _, src := range s.sources {
			if src.Kind() != profile.KindProject {
				picker.sources = append(picker.sources, src)
			}
		}
	}
	opts, err := picker.profileOptions()
	if err != nil {
		return err
	}
	if len(opts) > 0 {
		i, err := cc.Prompt.MultiSelect(ctx, ui.Question{Title: "Profiles to extend (pick none for a fresh profile)", Options: opts, Default: -1, Filterable: true})
		if err != nil {
			return err
		}
		for _, k := range i {
			f.from = append(f.from, opts[k].Value)
		}
	}

	if ids := s.installedIDs(ctx); len(ids) > 0 {
		options := make([]ui.Option, len(ids))
		for i, id := range ids {
			options[i] = ui.Option{Label: id, Value: id}
		}
		picks, err := cc.Prompt.MultiSelect(ctx, ui.Question{Title: "Plugins to include", Options: options, Default: -1, Filterable: true})
		if err != nil {
			return err
		}
		for _, k := range picks {
			f.plugins = append(f.plugins, ids[k])
		}
	}

	skills, err := cc.Prompt.Input(ctx, "Standalone skills to turn off (comma separated, empty for none)", "", nil)
	if err != nil {
		return err
	}
	for _, sk := range strings.Split(skills, ",") {
		if sk = strings.TrimSpace(sk); sk != "" {
			f.skillsOff = append(f.skillsOff, sk)
		}
	}

	if names := s.registryNames(); f.scope != "project" && len(names) > 0 {
		options := make([]ui.Option, len(names))
		for i, n := range names {
			options[i] = ui.Option{Label: n, Value: n}
		}
		picks, err := cc.Prompt.MultiSelect(ctx, ui.Question{Title: "MCP servers to add", Options: options, Default: -1, Filterable: true})
		if err != nil {
			return err
		}
		for _, k := range picks {
			f.mcp = append(f.mcp, names[k])
		}
	}
	return nil
}

// installedIDs lists the installed plugin ids for the wizard; any problem is a
// warning and an empty list, because the wizard can still work without it.
func (s *session) installedIDs(ctx context.Context) []string {
	bin, err := s.locate()
	if err != nil {
		warnf(s.cc, "skipping the plugin list: %v", err)
		return nil
	}
	cdir, err := cache.Dir()
	if err != nil {
		warnf(s.cc, "skipping the plugin list: %v", err)
		return nil
	}
	ic := &claude.InstalledCache{Dir: cdir, Now: s.cc.Now}
	list, _, err := ic.List(ctx, bin, s.cwd, s.env)
	if err != nil {
		warnf(s.cc, "skipping the plugin list: %v", err)
		return nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, p := range list {
		if !seen[p.ID] {
			seen[p.ID] = true
			ids = append(ids, p.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// registryNames lists MCP server names from the registries of the sources.
func (s *session) registryNames() []string {
	seen := map[string]bool{}
	for _, src := range s.sources {
		// A profile finds MCP servers only in the registry of its own source,
		// so a personal profile can use only the personal registry.
		if src.Kind() != profile.KindPersonal || src.Root() == "" {
			continue
		}
		reg, err := profile.LoadRegistry(filepath.Join(src.Root(), "mcp", "registry.toml"))
		if err != nil {
			continue
		}
		for n := range reg {
			seen[n] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// writeNewFile keeps the exclusive project writer shared by new and its
// regression tests.
func writeNewFile(path string, data []byte) error { return profile.WriteNewProfile(path, data) }

// writeNewProfileFile selects the exclusive writer for the destination kind.
// A personal root may be reached through symlinks, as the reader allows;
// project profiles keep the strict symlink-refusing writer.
func writeNewProfileFile(kind profile.Kind, path string, data []byte) error {
	if kind == profile.KindPersonal {
		return profile.WriteNewPersonalProfile(path, data)
	}
	return profile.WriteNewProfile(path, data)
}

// newProfileSource overlays only the candidate on its normal directory source.
// It exposes the same registry/root so Resolve applies the usual origin rules.
type newProfileSource struct {
	profile.Source
	name, target string
	raw          []byte
}

// Names includes the candidate in its destination's existing namespace.
func (s *newProfileSource) Names() ([]string, error) {
	names, err := s.Source.Names()
	if err != nil {
		return nil, err
	}
	return append(names, s.name), nil
}

// Open validates the candidate's bytes, or delegates a parent's file read.
func (s *newProfileSource) Open(name string) (*profile.File, error) {
	if name != s.name {
		return s.Source.Open(name)
	}
	m, err := profile.Parse(s.raw, name+".toml")
	if err != nil {
		return nil, err
	}
	return &profile.File{Name: name, Path: s.target, Raw: s.raw, Manifest: m, Source: s}, nil
}

// newNamespaces checks local and verified cached namespaces without fetching
// merely to check a new name. Existing parent/wizard preparation is preserved.
func (s *session) newNamespaces(ctx context.Context, prepared bool, destination string) error {
	if !prepared {
		local := s.sources
		s.sources = nil
		for _, src := range local {
			if src.Kind() == profile.KindOrg {
				s.addShared(src.ID(), src)
			} else {
				s.sources = append(s.sources, src)
			}
		}
		factory := s.l.opt.NewGit
		if factory == nil {
			factory = defaultGit
		}
		for i, sc := range s.cfg.Sources {
			switch sc.Type {
			case config.SourceGit:
				label := fmt.Sprintf("sources[%d] (git %s)", i, ui.Sanitize(sc.URL))
				g, err := factory(gitOptions(sc, s.cfg.Trust.RequirePin))
				if err == nil {
					err = s.prepareNewCached(ctx, g, sc)
				}
				if err != nil {
					s.failSource(label, err, nil)
					continue
				}
				s.addShared(label, g)
			case config.SourcePlugin:
				s.failSource(fmt.Sprintf("sources[%d] (plugin %s)", i, ui.Sanitize(sc.Plugin)), errors.New("plugin namespaces have no verified offline cache, so use the full terminal new wizard or --from <parent> to prepare sources as part of content gathering"), nil)
			}
		}
	}
	// Include existing disabled project names as well as the intended namespace.
	dirs := []string{destination}
	if root := findProject(s.cwd, s.cc.GOOS); root != "" {
		dirs = append(dirs, filepath.Join(root, ".ccshelf", "profiles"))
	}
	projectDir, err := profile.ProjectProfilesDir(s.cwd)
	if err != nil {
		// An unresolvable project namespace (for example an unsafe .git marker)
		// only narrows what is known about existing names: project creation
		// fails closed in checkNewName, user creation proceeds with a warning.
		s.unavailableNamespace("project profiles", err)
	} else {
		dirs = append(dirs, projectDir)
	}
	personal, err := profile.PersonalDir()
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if sameDir(dir, personal, s.cc.GOOS) {
			continue
		}
		if err := profile.CheckDestination(dir); err != nil {
			if sameDir(dir, destination, s.cc.GOOS) {
				return ui.Failure(err)
			}
			s.unavailableNamespace(dir, err)
			continue
		}
		src := profile.DirSource(profile.KindProject, dir)
		found := false
		for _, existing := range s.sources {
			if existing.ID() == src.ID() {
				found = true
				break
			}
		}
		if !found {
			s.sources = append(s.sources, src)
		}
	}
	return nil
}

// unavailableNamespace records a namespace that cannot be inspected for
// existing profile names. It never fetches or prepares anything: the name may
// collide in a namespace that cannot be read, so project creation fails closed
// in checkNewName while user creation proceeds with this warning.
func (s *session) unavailableNamespace(label string, err error) {
	s.failed = append(s.failed, sourceFailure{label: label, err: err})
	s.warn("%s cannot be inspected for existing profile names: %s", label, ui.SanitizeLine(err.Error()))
}

func (s *session) prepareNewCached(ctx context.Context, g PreparedSource, sc config.SourceConfig) error {
	cp, ok := g.(cachedPreparer)
	if !ok {
		return errors.New("no verified cached namespace (prepare the source explicitly)")
	}
	commits := s.lockedCommits(sc)
	if fullSHA.MatchString(strings.ToLower(sc.Ref)) {
		commits = append([]string{strings.ToLower(sc.Ref)}, commits...)
	}
	if cl, ok := g.(cachedLister); ok {
		cached, err := cl.CachedCommits()
		if err == nil {
			commits = append(commits, cached...)
		}
	}
	for _, commit := range commits {
		if err := cp.PrepareCached(ctx, commit); err == nil {
			return nil
		}
	}
	return errors.New("no usable verified cached namespace (prepare the source explicitly)")
}

func (s *session) checkNewName(name, scope string) error {
	for _, src := range s.sources {
		names, err := src.Names()
		if err != nil {
			return ui.Failure(err)
		}
		for _, existing := range names {
			if existing == name {
				return ui.Failure(fmt.Errorf("profile %q already exists in %s, and creation would shadow an existing name", name, src.ID()))
			}
		}
	}
	if scope == "project" && len(s.failed) > 0 {
		return ui.Failure(withHint(fmt.Errorf("cannot prove project profile %q does not shadow an unavailable namespace: %s", name, s.failedSummary()), "prepare and inspect git sources explicitly with ccshelf ls (and ccshelf trust <profile> when required), then retry. Plugin sources have no offline namespace cache: use the full terminal new wizard or --from <parent> for normal source preparation. No source was fetched or trusted solely for this collision check"))
	}
	return nil
}

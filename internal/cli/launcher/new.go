package launcher

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cache"
	"github.com/yorch/ccshelf/internal/claude"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
)

type newFlags struct {
	from, plugins, exclude, skillsOff, mcp []string
	description, owner, model, effort      string
}

// given reports whether any content flag was set (name aside), so that the
// wizard opens only when the command line said nothing about the profile.
func (f *newFlags) given() bool {
	return len(f.from) > 0 || len(f.plugins) > 0 || len(f.exclude) > 0 || len(f.skillsOff) > 0 || len(f.mcp) > 0 ||
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
		Short: "Create a personal profile",
		Long: `Create a personal profile file in your profiles directory. --from names a parent
profile to extend (repeatable). In a terminal, anything not given as a flag is
asked for, and the equivalent flag command is printed at the end.`,
		Example: `  ccshelf new sre-night --from base --plugin sre-kit@acme
  ccshelf new notes --description "Writing and notes" --skill-off legacy-helper`,
		Args: cobra.MaximumNArgs(1),
	}
	c.Flags().StringArrayVar(&f.from, "from", nil, "parent profile to extend (repeatable)")
	c.Flags().StringArrayVar(&f.plugins, "plugin", nil, "plugin to include, name@marketplace (repeatable)")
	c.Flags().StringArrayVar(&f.exclude, "exclude-plugin", nil, "plugin to always mask, name@marketplace (repeatable)")
	c.Flags().StringArrayVar(&f.skillsOff, "skill-off", nil, "standalone skill to turn off (repeatable)")
	c.Flags().StringArrayVar(&f.mcp, "mcp", nil, "MCP server from the registry to add (repeatable)")
	c.Flags().StringVar(&f.description, "description", "", "one-line description")
	c.Flags().StringVar(&f.owner, "owner", "", "owner (team or person)")
	c.Flags().StringVar(&f.model, "model", "", "default model")
	c.Flags().StringVar(&f.effort, "effort", "", "default effort level")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		return l.newProfile(ctx, cc, first(args), &f)
	})
	return c
}

func (l *launcher) newProfile(ctx context.Context, cc *clicore.Context, name string, f *newFlags) error {
	interactive := canPrompt(cc)
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
	dir, err := profile.PersonalDir()
	if err != nil {
		return fmt.Errorf("personal profiles directory: %w", err)
	}
	target := filepath.Join(dir, name+".toml")
	if _, err := os.Lstat(target); err == nil {
		return ui.Failure(withHint(fmt.Errorf("profile %q already exists: %s", name, target), "edit it with: ccshelf edit %s", name))
	}

	needSources := len(f.from) > 0 || interactive
	s, err := l.open(ctx, cc, needSources, false)
	if err != nil {
		return err
	}
	noFlags := !f.given()
	if interactive && noFlags {
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
	if err := writeNewFile(target, raw); err != nil {
		return ui.Failure(err)
	}
	// Resolve it once: a missing parent or unknown MCP server is reported now,
	// and the file we just created is removed again so nothing half-valid stays.
	if _, err := s.resolve(name); err != nil {
		_ = os.Remove(target)
		return fmt.Errorf("the new profile does not resolve (nothing was kept): %w", err)
	}
	okf(cc, "created %s", target)
	if asked {
		rec := ui.NewRecorder("new", name)
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

	opts, err := s.profileOptions()
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

	if names := s.registryNames(); len(names) > 0 {
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

// writeNewFile creates path exclusively with mode 0600 in a directory of mode
// 0700. It never replaces an existing file or follows a symlink at path.
func writeNewFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is the user's own profiles directory
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists", path)
		}
		return fmt.Errorf("creating %s: %w", path, err)
	}
	if _, err := bytes.NewReader(data).WriteTo(f); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

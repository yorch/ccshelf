package orgcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/catalog"
	"github.com/yorch/ccshelf/internal/catalog/lint"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/marketplace"
	"github.com/yorch/ccshelf/internal/orgconfig"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
)

// Options holds what internal/cli injects into the org commands.
type Options struct {
	// Catalog finds the catalog data of the configured org source for search
	// and recommend when the working directory is not an org data repo. Nil
	// means there is none.
	Catalog clicore.CatalogProvider
}

// Commands returns the org and catalog commands: lint, compile, catalog
// (with the build subcommand), search, recommend and doctor, without a
// configured org source to fall back on.
func Commands(get clicore.Provider) []*cobra.Command {
	return CommandsWith(get, Options{})
}

// CommandsWith is Commands with the injected pieces of opt.
func CommandsWith(get clicore.Provider, opt Options) []*cobra.Command {
	return []*cobra.Command{
		newLint(get),
		newCompile(get),
		newCatalog(get),
		newSearch(get, opt),
		newRecommend(get, opt),
		newDoctor(get),
	}
}

// repo is an opened org data repo.
type repo struct {
	root string
	cfg  *orgconfig.Config
}

// openRepo finds the root (--root or the working directory) and loads
// ccshelf.toml from it.
func openRepo(c *clicore.Context) (*repo, error) {
	root := c.G.Root
	if root == "" {
		wd, err := c.Getwd()
		if err != nil {
			return nil, fmt.Errorf("finding the current directory: %w", err)
		}
		root = wd
	} else if !filepath.IsAbs(root) {
		if wd, err := c.Getwd(); err == nil {
			root = filepath.Join(wd, root)
		}
	}
	root = filepath.Clean(root)
	st, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("org data repo root: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("org data repo root %s is not a directory", root)
	}
	cfg, err := orgconfig.Load(root)
	if err != nil {
		return nil, fmt.Errorf("loading the org config: %w", err)
	}
	return &repo{root: root, cfg: cfg}, nil
}

// isOrgRepo reports whether the repo's first configured marketplace file can
// be read, which is what makes a directory an org data repo (the check
// requireMarketplace applies to a catalog build).
func (r *repo) isOrgRepo() bool {
	if len(r.cfg.Catalog.Marketplaces) == 0 {
		return false
	}
	_, err := marketplace.LoadFile(r.root, r.cfg.Catalog.Marketplaces[0])
	return err == nil
}

// openCatalogRepo is openRepo for the commands that only read the catalog
// (search, recommend). An explicit --root, or a working directory that is an
// org data repo, is used as before. Otherwise the catalog data of the user's
// configured org source is used (see clicore.CatalogProvider), so a developer
// whose organization is reached through a git source needs no checkout; src
// then describes it. When there is nothing to fall back on, the working
// directory repo is returned and the caller reports "not an org data repo"
// with a hint that names the options.
func openCatalogRepo(ctx context.Context, c *clicore.Context, opt Options) (r *repo, src string, err error) {
	r, err = openRepo(c)
	if err != nil || c.G.Root != "" || r.isOrgRepo() || opt.Catalog == nil {
		return r, "", err
	}
	cd, perr := opt.Catalog(ctx, c)
	if perr != nil {
		if errors.Is(perr, clicore.ErrNoCatalog) {
			return r, "", nil
		}
		return nil, "", fmt.Errorf("reading the configured org source: %w", perr)
	}
	cfg, err := orgconfig.Load(cd.Root)
	if err != nil {
		return nil, "", fmt.Errorf("loading the org config of %s: %w", ui.SanitizeLine(cd.Source), err)
	}
	return &repo{root: cd.Root, cfg: cfg}, cd.Source, nil
}

// noteSource tells, on stderr and only in text mode, which configured org
// source a catalog command read when it did not read a directory.
func noteSource(c *clicore.Context, src string) {
	if src == "" || c.Mode.JSON {
		return
	}
	fmt.Fprintf(errw(c), "note: reading the cached catalog of %s\n", ui.SanitizeLine(src))
}

// notOrgRepo wraps the "not an org data repo" failure of a catalog command
// with the ways out.
func notOrgRepo(err error, withSource bool, extra ...string) error {
	if err == nil {
		return nil
	}
	hint := "run it inside an org data repo, or pass --root <dir>" + strings.Join(extra, "")
	if withSource {
		hint += "; or configure the organization's git source in config.toml and run \"ccshelf ls\" once so that its catalog is cached"
	}
	return withHintErr(err, hint)
}

type hintErr struct {
	err  error
	hint string
}

func (e *hintErr) Error() string { return e.err.Error() }
func (e *hintErr) Unwrap() error { return e.err }

// Hint returns the advice shown after the error.
func (e *hintErr) Hint() string { return e.hint }

func withHintErr(err error, hint string) error { return &hintErr{err: err, hint: hint} }

// profilesDir is the absolute directory of the profile manifests.
func (r *repo) profilesDir() string {
	return filepath.Join(r.root, filepath.FromSlash(r.cfg.Profiles.Dir))
}

func (r *repo) source() profile.Source {
	return profile.DirSource(profile.KindOrg, r.profilesDir())
}

// profileProblem is a profile that could not be loaded or resolved.
type profileProblem struct {
	Name string
	Err  error
}

// resolveAll resolves every profile of the repo, sorted by name. Profiles that
// fail are returned as problems, not as an error.
func (r *repo) resolveAll() ([]*profile.Resolved, []profileProblem, error) {
	src := r.source()
	names, err := src.Names()
	if err != nil {
		return nil, nil, fmt.Errorf("listing profiles: %w", err)
	}
	sort.Strings(names)
	var ok []*profile.Resolved
	var bad []profileProblem
	for _, n := range names {
		res, err := profile.Resolve(n, []profile.Source{src}, profile.ResolveOptions{})
		if err != nil {
			bad = append(bad, profileProblem{n, err})
			continue
		}
		ok = append(ok, res)
	}
	return ok, bad, nil
}

// openProfiles opens (parses and validates) every profile without resolving
// extends; invalid ones are skipped. It feeds catalog and recommend rows.
func (r *repo) openProfiles() ([]*profile.Manifest, error) {
	src := r.source()
	names, err := src.Names()
	if err != nil {
		return nil, fmt.Errorf("listing profiles: %w", err)
	}
	sort.Strings(names)
	var out []*profile.Manifest
	for _, n := range names {
		f, err := src.Open(n)
		if err != nil || f == nil || f.Manifest == nil {
			continue
		}
		out = append(out, f.Manifest)
	}
	return out, nil
}

func (r *repo) catalogProfiles() ([]catalog.ProfileInfo, error) {
	ms, err := r.openProfiles()
	if err != nil {
		return nil, err
	}
	out := make([]catalog.ProfileInfo, 0, len(ms))
	for _, m := range ms {
		st := m.Status
		if st == "" {
			st = profile.StatusActive
		}
		out = append(out, catalog.ProfileInfo{Name: m.Name, Description: m.Description, Owner: m.Owner, Status: st, WhenToUse: append([]string{}, m.WhenToUse...)})
	}
	return out, nil
}

// profileProblemFindings turns profile problems into lint findings (PRF001),
// dropping exact duplicates (a broken parent breaks every child).
func (r *repo) profileFindings(bad []profileProblem, good []*profile.Resolved) []lint.Finding {
	var out []lint.Finding
	seen := map[string]bool{}
	add := func(f lint.Finding) {
		k := fmt.Sprintf("%s|%s|%d|%s", f.Severity, f.File, f.Line, f.Message)
		if !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	rel := func(name string) string { return r.cfg.Profiles.Dir + "/" + name + ".toml" }
	for _, p := range bad {
		var ve *profile.ValidationError
		if errors.As(p.Err, &ve) && len(ve.Problems) > 0 {
			file := rel(p.Name)
			if base := filepath.Base(ve.File); base == "registry.toml" {
				file = r.cfg.Profiles.MCPRegistry
			} else if b := strings.TrimSuffix(base, ".toml"); b != base && profile.ValidName(b) {
				file = rel(b)
			}
			for _, pr := range ve.Problems {
				msg := pr.Message
				if pr.Field != "" {
					msg = pr.Field + ": " + msg
				}
				add(lint.Finding{Severity: lint.Error, Code: "PRF001", Message: msg, File: file, Line: pr.Line})
			}
			continue
		}
		add(lint.Finding{Severity: lint.Error, Code: "PRF001", Message: p.Err.Error(), File: rel(p.Name)})
	}
	for _, res := range good {
		for _, w := range res.Warnings {
			add(lint.Finding{Severity: lint.Warning, Code: "PRF002", Message: w, File: rel(res.Name)})
		}
		// The documented Windows launcher (cmd /c npx -y pkg@1.2.3) is not a
		// warning; it is shown as info so that it stays visible.
		names := make([]string, 0, len(res.MCP))
		for n := range res.MCP {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			for _, note := range profile.MCPNotes(res.MCP[n]) {
				add(lint.Finding{Severity: lint.Info, Code: "PRF002", Message: note, File: rel(res.Name)})
			}
		}
	}
	return out
}

// hostingMarketplace returns the name of the marketplace that hosts the
// profile bundles: the first configured marketplace that lists a profile-*
// plugin, else the first configured one.
func (r *repo) hostingMarketplace() (string, error) {
	var first string
	for i, rel := range r.cfg.Catalog.Marketplaces {
		m, err := marketplace.LoadFile(r.root, rel)
		if err != nil {
			if i == 0 {
				return "", err
			}
			continue
		}
		if i == 0 {
			first = m.Name
		}
		for _, p := range m.Plugins {
			if strings.HasPrefix(p.Name, lint.BundlePrefix) {
				return m.Name, nil
			}
		}
	}
	if first == "" {
		return "", errors.New("no marketplace is configured (catalog.marketplaces)")
	}
	return first, nil
}

// requireMarketplace fails with exit 1 when the repo is not an org data repo:
// a marketplace file listed in ccshelf.toml (by default
// .claude-plugin/marketplace.json) cannot be read. lint and compile fail in
// that case; the commands that read the catalog do the same, so a wrong
// --root or working directory is never mistaken for an empty catalog.
func requireMarketplace(r *repo, rep *lint.Report) error {
	for _, f := range rep.Findings {
		if f.Code == "CAT001" {
			return ui.Failure(fmt.Errorf("%s is not an org data repo: %s", ui.SanitizeLine(r.root), ui.SanitizeLine(f.Message)))
		}
	}
	return nil
}

// out writes s to the command's stdout.
func out(c *clicore.Context) io.Writer { return c.Streams.Out }

// errw is the command's stderr.
func errw(c *clicore.Context) io.Writer { return c.Streams.Err }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// noArgs rejects positional arguments as a usage error (exit 2).
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return ui.Usage(fmt.Errorf("%s takes no arguments, got %q", cmd.CommandPath(), args[0]))
	}
	return nil
}

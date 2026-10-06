package orgcmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/catalog"
	"github.com/ccshelf/ccshelf/internal/catalog/lint"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/marketplace"
	"github.com/ccshelf/ccshelf/internal/orgconfig"
	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// Commands returns the org and catalog commands: lint, compile, catalog
// (with the build subcommand), search, recommend and doctor.
func Commands(get clicore.Provider) []*cobra.Command {
	return []*cobra.Command{
		newLint(get),
		newCompile(get),
		newCatalog(get),
		newSearch(get),
		newRecommend(get),
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

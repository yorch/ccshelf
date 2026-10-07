package orgcmd

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/catalog"
	"github.com/yorch/ccshelf/internal/catalog/site"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/ui"
)

// defaultOut is the --out default, relative to the working directory.
const defaultOut = "dist/catalog"

// markdownName is the generated Markdown catalog.
const markdownName = "CATALOG.md"

// buildJSON is the data of `catalog build --json` (kind "catalog-build").
type buildJSON struct {
	Out   string   `json:"out"`
	Files []string `json:"files"`
	// Pruned lists the stale site files --no-site removed.
	Pruned   []string    `json:"pruned,omitempty"`
	Plugins  int         `json:"plugins"`
	Profiles int         `json:"profiles"`
	Lint     lintSummary `json:"lint"`
}

type lintSummary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Infos    int `json:"infos"`
}

func newCatalog(get clicore.Provider) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Set up the org data repo and build its plugin catalog",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newCatalogInit(get), newCatalogBuild(get))
	return cmd
}

func newCatalogBuild(get clicore.Provider) *cobra.Command {
	var outDir string
	var noSite, gitData, stamp bool
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Write catalog.json, CATALOG.md and the static site",
		Long: `Build the catalog from the org data repo and write it into --out (default
` + defaultOut + `, relative to the current directory): catalog.json, CATALOG.md and, unless
--no-site, the static site (index.html, app.js, style.css; with --no-site the
files of an earlier site build are removed from --out). Nothing is written
outside --out, and the output is published files: directories 0755, files 0644.

--out must not be the repo root or contain it, must not lie inside the repo's
source directories (profiles, bundles, catalog, plugins, .github and so on)
and must not pass through a symbolic link below the working directory or the
repo root. A directory that is not an org data repo (its marketplace file
cannot be read or parsed) is an error, exit 1, and nothing is written.

The catalog has no timestamp unless --timestamp is given, so the output is
reproducible. Lint findings are printed to stderr; a repo with lint errors
still produces its catalog (for previews) but the command exits 1.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := get()
			if err != nil {
				return err
			}
			r, err := openRepo(c)
			if err != nil {
				return err
			}
			target, err := resolveOut(c, r, outDir)
			if err != nil {
				return err
			}
			profs, err := r.catalogProfiles()
			if err != nil {
				return err
			}
			cat, rep, err := catalog.BuildContext(cmd.Context(), r.root, r.cfg, catalog.Options{Now: c.Now, GitData: gitData, Profiles: profs})
			if err != nil {
				return fmt.Errorf("building the catalog: %w", err)
			}
			good, bad, err := r.resolveAll()
			if err != nil {
				return err
			}
			if err := requireMarketplace(r, rep); err != nil {
				return err
			}
			dropAbstractBundles(rep, good)
			rep.Findings = append(rep.Findings, r.profileFindings(bad, good)...)
			rep.Sort()
			if !stamp {
				cat.GeneratedAt = ""
			}
			files, err := renderFiles(cat, !noSite)
			if err != nil {
				return err
			}
			var prune []string
			if noSite {
				prune = staleSiteFiles
			}
			pruned, err := writeOutput(target, files, prune)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(files))
			for _, f := range files {
				names = append(names, f.name)
			}
			sort.Strings(names)
			n := rep.Counts()
			data := buildJSON{
				Out: target.dest, Files: names, Pruned: pruned, Plugins: len(cat.Plugins), Profiles: len(cat.Profiles),
				Lint: lintSummary{Errors: n.Errors, Warnings: n.Warnings, Infos: n.Infos},
			}
			if c.Mode.JSON {
				if err := ui.WriteJSON(out(c), "catalog-build", data); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(out(c), "catalog: %s, %s, written to %s\n", plural(data.Plugins, "plugin", "plugins"), plural(data.Profiles, "profile", "profiles"), ui.SanitizeLine(target.dest))
				for _, nme := range names {
					fmt.Fprintf(out(c), "  %s\n", nme)
				}
				for _, nme := range pruned {
					fmt.Fprintf(out(c), "  removed the stale %s\n", nme)
				}
			}
			if len(rep.Findings) > 0 {
				_ = writeLint(errw(c), rep, formatText)
			}
			if n.Errors > 0 {
				return ui.Failure(fmt.Errorf("the catalog was written, but lint found %s", plural(n.Errors, "error", "errors")))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outDir, "out", "", "output directory (default "+defaultOut+")")
	cmd.Flags().BoolVar(&noSite, "no-site", false, "write only catalog.json and CATALOG.md")
	cmd.Flags().BoolVar(&gitData, "git-data", false, "add last-change and contributor data from git")
	cmd.Flags().BoolVar(&stamp, "timestamp", false, "stamp generated_at into the output")
	return cmd
}

type outFile struct {
	name string
	data []byte
}

func renderFiles(cat *catalog.Catalog, withSite bool) ([]outFile, error) {
	var files []outFile
	if withSite {
		sf, err := site.Render(cat)
		if err != nil {
			return nil, fmt.Errorf("rendering the site: %w", err)
		}
		for _, f := range sf {
			files = append(files, outFile{f.Name, f.Data})
		}
	} else {
		js, err := catalog.JSON(cat)
		if err != nil {
			return nil, err
		}
		files = append(files, outFile{"catalog.json", js})
	}
	files = append(files, outFile{markdownName, catalog.Markdown(cat)})
	return files, nil
}

// searchJSON is the data of `search --json` (kind "search").
type searchJSON struct {
	// Source says where the catalog came from when it was not the working
	// directory or --root: the configured org source.
	Source  string      `json:"source,omitempty"`
	Query   string      `json:"query"`
	Matches []matchJSON `json:"matches"`
}

type matchJSON struct {
	Name        string   `json:"name"`
	Marketplace string   `json:"marketplace"`
	Status      string   `json:"status,omitempty"`
	Category    string   `json:"category,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Score       int      `json:"score"`
	Fields      []string `json:"fields"`
	Description string   `json:"description"`
}

func newSearch(get clicore.Provider, opt Options) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search the plugin catalog of the org data repo",
		Long: `Search the catalog of the org data repo (--root, default the current
directory). Every word of the query must match a field (name, display name,
tags, category, when_to_use, description or owner); better matches come first,
ties by name.

Outside an org data repo and without --root, [catalog].remote_url in the user
config takes precedence and retrieves catalog.json over HTTPS for this search.
Otherwise search reads a configured org source from its local directory or
verified git cache. A query that matches nothing in a real catalog is not an
error.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return ui.Usage(errors.New("search needs a query, for example: ccshelf search figma"))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := get()
			if err != nil {
				return err
			}
			if limit < 0 {
				return ui.Usage(errors.New("--limit must not be negative"))
			}
			r, src, err := openCatalogRepo(cmd.Context(), c, opt)
			if err != nil {
				return err
			}
			cat := r.catalog
			if cat == nil {
				built, rep, err := catalog.BuildContext(cmd.Context(), r.root, r.cfg, catalog.Options{Now: c.Now})
				if err != nil {
					return fmt.Errorf("building the catalog: %w", err)
				}
				if err := requireMarketplace(r, rep); err != nil {
					return notOrgRepo(err, opt.Catalog != nil)
				}
				cat = built
			}
			noteSource(c, src)
			query := strings.Join(args, " ")
			ms := catalog.Search(cat, query, limit)
			data := searchJSON{Source: src, Query: query, Matches: []matchJSON{}}
			for _, m := range ms {
				data.Matches = append(data.Matches, matchJSON{
					Name: m.Entry.Name, Marketplace: m.Entry.Marketplace, Status: m.Entry.Status,
					Category: m.Entry.Category, Owner: m.Entry.Owner, Score: m.Score,
					Fields: append([]string{}, m.Fields...), Description: m.Entry.Description,
				})
			}
			if c.Mode.JSON {
				return ui.WriteJSON(out(c), "search", data)
			}
			return writeSearchText(out(c), c.Mode, data)
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 10, "show at most this many matches (0 for all)")
	return cmd
}

func writeSearchText(w io.Writer, mode ui.Mode, d searchJSON) error {
	if len(d.Matches) == 0 {
		return ui.EmptyState(w, fmt.Sprintf("no plugin matches %q", d.Query), "try fewer or broader words; search matches every word in your query")
	}
	rows := make([][]string, 0, len(d.Matches))
	for _, m := range d.Matches {
		id := m.Name
		if m.Marketplace != "" {
			id += "@" + m.Marketplace
		}
		rows = append(rows, []string{id, m.Status, m.Description})
	}
	return ui.Table(w, []string{"PLUGIN", "STATUS", "DESCRIPTION"}, rows, mode)
}

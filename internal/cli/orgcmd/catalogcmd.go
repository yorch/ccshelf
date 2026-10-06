package orgcmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/catalog"
	"github.com/ccshelf/ccshelf/internal/catalog/site"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// defaultOut is the --out default, relative to the working directory.
const defaultOut = "dist/catalog"

// markdownName is the generated Markdown catalog.
const markdownName = "CATALOG.md"

// buildJSON is the data of `catalog build --json` (kind "catalog-build").
type buildJSON struct {
	Out      string      `json:"out"`
	Files    []string    `json:"files"`
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
		Short: "Build the plugin catalog of the org data repo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newCatalogBuild(get))
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
--no-site, the static site (index.html, app.js, style.css). Nothing is written
outside --out.

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
			dest, err := resolveOut(c, r, outDir)
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
			if err := writeFiles(dest, files); err != nil {
				return err
			}
			names := make([]string, 0, len(files))
			for _, f := range files {
				names = append(names, f.name)
			}
			sort.Strings(names)
			n := rep.Counts()
			data := buildJSON{Out: dest, Files: names, Plugins: len(cat.Plugins), Profiles: len(cat.Profiles),
				Lint: lintSummary{Errors: n.Errors, Warnings: n.Warnings, Infos: n.Infos}}
			if c.Mode.JSON {
				if err := ui.WriteJSON(out(c), "catalog-build", data); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(out(c), "catalog: %s, %s, written to %s\n", plural(data.Plugins, "plugin", "plugins"), plural(data.Profiles, "profile", "profiles"), ui.SanitizeLine(dest))
				for _, nme := range names {
					fmt.Fprintf(out(c), "  %s\n", nme)
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

// resolveOut makes the output directory absolute and refuses the repo root.
func resolveOut(c *clicore.Context, r *repo, outDir string) (string, error) {
	if outDir == "" {
		outDir = defaultOut
	}
	dest := outDir
	if !filepath.IsAbs(dest) {
		wd, err := c.Getwd()
		if err != nil {
			return "", fmt.Errorf("finding the current directory: %w", err)
		}
		dest = filepath.Join(wd, dest)
	}
	dest = filepath.Clean(dest)
	if same(dest, r.root) {
		return "", ui.Usage(errors.New("--out must not be the repo root: pick a separate output directory"))
	}
	if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("--out %s is a symbolic link; refusing to write through it", dest)
	}
	return dest, nil
}

func same(a, b string) bool {
	if a == b {
		return true
	}
	ea, errA := filepath.EvalSymlinks(a)
	eb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ea == eb
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

// writeFiles writes each file atomically (temporary file, then rename) into
// dir, creating dir with mode 0700. It refuses to replace anything that is not
// a regular file.
func writeFiles(dir string, files []outFile) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	for _, f := range files {
		target := filepath.Join(dir, f.name)
		if fi, err := os.Lstat(target); err == nil {
			if !fi.Mode().IsRegular() {
				return fmt.Errorf("refusing to overwrite %s: not a regular file", target)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspecting %s: %w", target, err)
		}
		if err := writeAtomic(dir, f.name, f.data); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating a temporary file for %s: %w", name, err)
	}
	tmpName := tmp.Name()
	fail := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return fail(fmt.Errorf("writing %s: %w", name, err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing %s: %w", name, err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replacing %s: %w", name, err)
	}
	return nil
}

// searchJSON is the data of `search --json` (kind "search").
type searchJSON struct {
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

func newSearch(get clicore.Provider) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search the plugin catalog of the org data repo",
		Long: `Search the catalog of the org data repo (--root, default the current
directory) locally and offline. Every word of the query must match a field
(name, display name, tags, category, when_to_use, description or owner);
better matches come first, ties by name.`,
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
			r, err := openRepo(c)
			if err != nil {
				return err
			}
			cat, _, err := catalog.BuildContext(cmd.Context(), r.root, r.cfg, catalog.Options{Now: c.Now})
			if err != nil {
				return fmt.Errorf("building the catalog: %w", err)
			}
			query := strings.Join(args, " ")
			ms := catalog.Search(cat, query, limit)
			data := searchJSON{Query: query, Matches: []matchJSON{}}
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
		_, err := fmt.Fprintf(w, "no plugin matches %q\n", ui.SanitizeLine(d.Query))
		return err
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

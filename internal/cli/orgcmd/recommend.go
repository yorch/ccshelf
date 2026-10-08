package orgcmd

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/catalog"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/marketplace"
	"github.com/yorch/ccshelf/internal/recommend"
	"github.com/yorch/ccshelf/internal/ui"
)

// recommendJSON is the data of `recommend --json` (kind "recommend").
type recommendJSON struct {
	// Source says where the catalog came from when it was not the working
	// directory or --root: the configured org source.
	Source          string                     `json:"source,omitempty"`
	Dir             string                     `json:"dir"`
	Recommendations []recommend.Recommendation `json:"recommendations"`
}

func newRecommend(get clicore.Provider, opt Options) *cobra.Command {
	var dir string
	var limit int
	cmd := &cobra.Command{
		Use:   "recommend",
		Short: "Suggest plugins and profiles for a project directory (rule based)",
		Long: `Look at a project directory (--dir, default the current directory) and suggest
plugins and profiles of the org data repo (--root, default the current
directory) whose relevance signals and when_to_use text match it. The rules
are deterministic and there is no model call. Only file names and a few small
manifest files of the project are read.

Outside an org data repo (the marketplace file of ccshelf.toml cannot be read)
and without --root, [catalog].remote_url in the user config is retrieved over
HTTPS when set; otherwise it uses the catalog data of the organization's
source from its local directory or verified git cache. Remote JSON can
recommend profiles; plugin recommendations need marketplace relevance rules
from the org data repo.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			target := dir
			if target == "" || !filepath.IsAbs(target) {
				wd, err := c.Getwd()
				if err != nil {
					return fmt.Errorf("finding the current directory: %w", err)
				}
				target = filepath.Join(wd, target)
			}
			sig, err := recommend.Collect(cmd.Context(), target)
			if err != nil {
				return fmt.Errorf("reading the project directory: %w", err)
			}
			if r.catalog != nil {
				noteSource(c, src)
				profs := make([]recommend.ProfileInfo, 0, len(r.catalog.Profiles))
				for _, m := range r.catalog.Profiles {
					profs = append(profs, recommend.ProfileInfo{Name: m.Name, Status: m.Status, WhenToUse: m.WhenToUse})
				}
				recs := recommend.ForCatalog(r.catalog, sig, profs)
				if limit > 0 && len(recs) > limit {
					recs = recs[:limit]
				}
				if recs == nil {
					recs = []recommend.Recommendation{}
				}
				data := recommendJSON{Source: src, Dir: sig.Cwd, Recommendations: recs}
				if c.Mode.JSON {
					return ui.WriteJSON(out(c), "recommend", data)
				}
				return writeRecommendText(out(c), c.Mode, data)
			}
			if err := requireCatalog(r, "recommend"); err != nil {
				return err
			}
			cat, lrep, err := catalog.BuildContext(cmd.Context(), r.root, r.cfg, catalog.Options{Now: c.Now})
			if err != nil {
				return fmt.Errorf("building the catalog: %w", err)
			}
			if err := requireMarketplace(r, lrep); err != nil {
				return notOrgRepo(err, opt.Catalog != nil)
			}
			noteSource(c, src)
			var mkts []*marketplace.Marketplace
			for _, rel := range r.cfg.Catalog.Marketplaces {
				if m, err := marketplace.LoadFile(r.root, rel); err == nil {
					mkts = append(mkts, m)
				}
			}
			ms, err := r.openProfiles()
			if err != nil {
				return err
			}
			profs := make([]recommend.ProfileInfo, 0, len(ms))
			for _, m := range ms {
				profs = append(profs, recommend.ProfileInfo{Name: m.Name, Status: m.Status, WhenToUse: m.WhenToUse, AvoidWhen: m.AvoidWhen, SupersededBy: m.SupersededBy})
			}
			recs := recommend.ForCatalog(cat, sig, profs, recommend.WithRelevance(recommend.RelevanceOf(mkts...)))
			if limit > 0 && len(recs) > limit {
				recs = recs[:limit]
			}
			if recs == nil {
				recs = []recommend.Recommendation{}
			}
			data := recommendJSON{Source: src, Dir: sig.Cwd, Recommendations: recs}
			if c.Mode.JSON {
				return ui.WriteJSON(out(c), "recommend", data)
			}
			return writeRecommendText(out(c), c.Mode, data)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "project directory to look at (default the current directory)")
	cmd.Flags().IntVar(&limit, "limit", 10, "show at most this many suggestions (0 for all)")
	return cmd
}

func writeRecommendText(w io.Writer, mode ui.Mode, d recommendJSON) error {
	if len(d.Recommendations) == 0 {
		return ui.EmptyState(w, "no suggestions for "+d.Dir, "browse the catalog with: ccshelf search <query>")
	}
	rows := make([][]string, 0, len(d.Recommendations))
	for _, r := range d.Recommendations {
		rows = append(rows, []string{r.Kind, r.Name, fmt.Sprintf("%.1f", r.Score), strings.Join(r.Why, "; ")})
	}
	return ui.Table(w, []string{"KIND", "NAME", "SCORE", "WHY"}, rows, mode)
}

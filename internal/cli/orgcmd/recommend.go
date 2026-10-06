package orgcmd

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/catalog"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/marketplace"
	"github.com/ccshelf/ccshelf/internal/recommend"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// recommendJSON is the data of `recommend --json` (kind "recommend").
type recommendJSON struct {
	Dir             string                     `json:"dir"`
	Recommendations []recommend.Recommendation `json:"recommendations"`
}

func newRecommend(get clicore.Provider) *cobra.Command {
	var dir string
	var limit int
	cmd := &cobra.Command{
		Use:   "recommend",
		Short: "Suggest plugins and profiles for a project directory (rule based, offline)",
		Long: `Look at a project directory (--dir, default the current directory) and suggest
plugins and profiles of the org data repo (--root, default the current
directory) whose relevance signals and when_to_use text match it. The rules
are deterministic; there is no model call and no network access. Only file
names and a few small manifest files of the project are read.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
			cat, _, err := catalog.BuildContext(cmd.Context(), r.root, r.cfg, catalog.Options{Now: c.Now})
			if err != nil {
				return fmt.Errorf("building the catalog: %w", err)
			}
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
			data := recommendJSON{Dir: sig.Cwd, Recommendations: recs}
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
		_, err := fmt.Fprintf(w, "no suggestions for %s\n", ui.SanitizeLine(d.Dir))
		return err
	}
	rows := make([][]string, 0, len(d.Recommendations))
	for _, r := range d.Recommendations {
		rows = append(rows, []string{r.Kind, r.Name, fmt.Sprintf("%.1f", r.Score), strings.Join(r.Why, "; ")})
	}
	return ui.Table(w, []string{"KIND", "NAME", "SCORE", "WHY"}, rows, mode)
}

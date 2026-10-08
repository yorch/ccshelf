package launcher

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
)

type lsRow struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Status      string   `json:"status,omitempty"`
	Kind        string   `json:"kind"`
	Source      string   `json:"source"`
	Tracks      string   `json:"tracks,omitempty"`
	Shadows     []string `json:"shadows"`
	Conflict    []string `json:"conflict"`
	Error       string   `json:"error,omitempty"`
}

func (l *launcher) lsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the profiles of all sources",
		Long: `List the profiles of every source: your personal directory, the configured
sources and, only when trusted, the repository's .ccshelf folder. Invalid
profiles are listed with their error. Use --json for a stable machine-readable form.`,
		Args: cobra.NoArgs,
	}
	var refresh bool
	c.Flags().BoolVar(&refresh, "refresh", false, "re-resolve git tags and branches on the remote instead of using the commits the trust lockfile pins")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, _ []string) error {
		s, err := l.openWith(ctx, cc, openOpts{prepare: true, refresh: refresh})
		if err != nil {
			return err
		}
		list, err := profile.List(s.sources)
		if err != nil {
			return ui.Failure(err)
		}
		labels := s.sourceLabels()
		tracks := map[string]string{}
		for _, src := range s.sources {
			if tr := sourceTracks(src); tr != "" {
				tracks[src.ID()] = tr
			}
		}
		rows := make([]lsRow, 0, len(list))
		for _, p := range list {
			label := labels[p.Source]
			if label == "" {
				label = p.Source
			}
			shadows := make([]string, 0, len(p.Shadows))
			for _, x := range p.Shadows {
				shadows = append(shadows, labelOr(labels, x))
			}
			conflict := make([]string, 0, len(p.Conflict))
			for _, x := range p.Conflict {
				conflict = append(conflict, labelOr(labels, x))
			}
			rows = append(rows, lsRow{
				Name: p.Name, Description: ui.Sanitize(p.Description), Owner: ui.Sanitize(p.Owner), Status: p.Status,
				Kind: p.Kind.String(), Source: ui.Sanitize(label), Tracks: ui.Sanitize(tracks[p.Source]), Shadows: shadows, Conflict: conflict, Error: ui.Sanitize(p.Err),
			})
		}
		if cc.Mode.JSON {
			return ui.WriteJSON(cc.Streams.Out, "profiles", rows)
		}
		if s.proj.Present && !s.proj.Allowed {
			warnf(cc, "this directory has a .ccshelf folder that is not loaded: %s", s.proj.Reason)
		}
		if len(rows) == 0 {
			return ui.EmptyState(cc.Streams.Out, "no profiles found", "create one with: ccshelf new <name>. To configure shared sources: ccshelf init --help")
		}
		table := make([][]string, 0, len(rows))
		for _, r := range rows {
			desc := r.Description
			status := r.Status
			switch {
			case r.Error != "":
				status, desc = "invalid", r.Error
			case len(r.Conflict) > 0:
				status, desc = "conflict", "name exists in: "+join(r.Conflict)
			case len(r.Shadows) > 0:
				desc += " (shadows " + join(r.Shadows) + ")"
			}
			kind := r.Kind
			if r.Tracks != "" {
				kind += " (" + r.Tracks + ")"
			}
			table = append(table, []string{r.Name, status, kind, r.Owner, desc})
		}
		return ui.Table(cc.Streams.Out, []string{"NAME", "STATUS", "KIND", "OWNER", "DESCRIPTION"}, table, cc.Mode)
	})
	return c
}

func labelOr(labels map[string]string, id string) string {
	if l, ok := labels[id]; ok {
		return l
	}
	return id
}

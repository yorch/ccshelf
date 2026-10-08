package orgcmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/catalog/lint"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
)

// Output formats of lint.
const (
	formatText   = "text"
	formatJSON   = "json"
	formatGitHub = "github"
)

// lintJSON is the data of `lint --json` (kind "lint"); the envelope is
// ui.WriteJSON's, like every other command.
type lintJSON struct {
	Summary  lintSummary    `json:"summary"`
	Findings []lint.Finding `json:"findings"`
}

func newLint(get clicore.Provider) *cobra.Command {
	var format string
	var strict bool
	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Check the org data repo: marketplace, sidecars, ownership and profiles",
		Long: `Check the org data repo (--root, default the current directory) against its
ccshelf.toml: marketplace entries, catalog sidecars, taxonomy, review dates,
CODEOWNERS coverage of hooks and MCP servers, profile manifests and their
profile-* bundle entries.

Formats: text (default), json (same as the global --json: the common
{"version","kind":"lint","data":{"summary","findings"}} envelope) and github (workflow
annotations, one ::error/::warning/::notice line per finding). The exit code
is 1 when there is any error finding (with --strict, also any warning).`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := get()
			if err != nil {
				return err
			}
			if c.G.JSON {
				format = formatJSON
			}
			switch format {
			case formatText, formatJSON, formatGitHub:
			default:
				return ui.Usage(fmt.Errorf("unknown --format %q (use text, json or github)", format))
			}
			r, err := openRepo(c)
			if err != nil {
				return err
			}
			rep, err := lintRepo(c, r)
			if err != nil {
				return err
			}
			if err := writeLint(out(c), rep, format); err != nil {
				return err
			}
			n := rep.Counts()
			switch {
			case n.Errors > 0:
				return ui.Failure(fmt.Errorf("lint found %s", plural(n.Errors, "error", "errors")))
			case strict && n.Warnings > 0:
				return ui.Failure(fmt.Errorf("lint found %s (--strict)", plural(n.Warnings, "warning", "warnings")))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", formatText, "output format: text, json or github")
	cmd.Flags().BoolVar(&strict, "strict", false, "fail on warnings too")
	return cmd
}

// lintRepo runs the catalog lint and checks every profile manifest.
func lintRepo(c *clicore.Context, r *repo) (*lint.Report, error) {
	rep, err := lint.Run(r.root, r.cfg, lint.Options{Now: c.Now})
	if err != nil {
		return nil, fmt.Errorf("lint: %w", err)
	}
	good, bad, err := r.resolveAll()
	if err != nil {
		return nil, err
	}
	dropAbstractBundles(rep, good)
	rep.Findings = append(rep.Findings, r.profileFindings(bad, good)...)
	rep.Sort()
	return rep, nil
}

func writeLint(w io.Writer, rep *lint.Report, format string) error {
	switch format {
	case formatJSON:
		n := rep.Counts()
		data := lintJSON{Summary: lintSummary{Errors: n.Errors, Warnings: n.Warnings, Infos: n.Infos}, Findings: rep.Findings}
		if data.Findings == nil {
			data.Findings = []lint.Finding{}
		}
		return ui.WriteJSON(w, "lint", data)
	case formatGitHub:
		_, err := io.WriteString(w, lint.FormatGitHub(rep))
		return err
	}
	_, err := io.WriteString(w, lint.FormatText(rep))
	return err
}

// dropAbstractBundles removes CAT050 for abstract profiles: compile skips a
// profile that resolves to no plugins, so it has no bundle and the finding
// would be a false alarm.
func dropAbstractBundles(rep *lint.Report, good []*profile.Resolved) {
	abstract := map[string]bool{}
	for _, res := range good {
		if len(res.Merged.Plugins.Include) == 0 {
			abstract[lint.BundlePrefix+res.Name] = true
		}
	}
	kept := rep.Findings[:0]
	for _, f := range rep.Findings {
		if f.Code == "CAT050" && abstract[f.Plugin] {
			continue
		}
		kept = append(kept, f)
	}
	rep.Findings = kept
}

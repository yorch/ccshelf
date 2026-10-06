package orgcmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/bundles"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// compileJSON is the data of `compile --json` (kind "compile").
type compileJSON struct {
	Check   bool         `json:"check"`
	Changed bool         `json:"changed"`
	Bundles []bundleJSON `json:"bundles"`
	Skipped []string     `json:"skipped"`
	Drift   driftJSON    `json:"drift"`
	Notes   []string     `json:"notes"`
	Written []string     `json:"written"`
}

type bundleJSON struct {
	Profile           string   `json:"profile"`
	Path              string   `json:"path"`
	CrossMarketplaces []string `json:"cross_marketplaces"`
}

type driftJSON struct {
	Missing  []string `json:"missing"`
	Modified []string `json:"modified"`
	Stale    []string `json:"stale"`
	Extra    []string `json:"extra"`
	Diff     string   `json:"diff,omitempty"`
}

func newCompile(get clicore.Provider) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "compile",
		Short: "Generate the profile-* bundle plugins from the profile manifests",
		Long: `Compile every profile of the org data repo into a profile bundle: a plugin
under bundles/profile-<name>/ whose dependencies are the profile's resolved
plugins. Profiles that resolve to no plugins (abstract bases) are skipped.

The whole bundles/ tree is generated output: files that are not produced by a
profile are removed. With --check nothing is written; the command prints the
differences and exits 1 when the committed bundles are stale.`,
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
			return runCompile(c, r, check)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "write nothing; exit 1 when the bundles are stale")
	return cmd
}

func runCompile(c *clicore.Context, r *repo, check bool) error {
	good, bad, err := r.resolveAll()
	if err != nil {
		return err
	}
	if len(bad) > 0 {
		for _, p := range bad {
			fmt.Fprintf(errw(c), "profile %s: %s\n", ui.SanitizeLine(p.Name), ui.Sanitize(p.Err.Error()))
		}
		return ui.Failure(fmt.Errorf("cannot compile: %s invalid (see ccshelf lint)", plural(len(bad), "profile is", "profiles are")))
	}
	mkt, err := r.hostingMarketplace()
	if err != nil {
		return fmt.Errorf("finding the hosting marketplace: %w", err)
	}
	in := make([]bundles.Input, 0, len(good))
	for _, res := range good {
		in = append(in, bundles.Input{Profile: res.Name, Marketplace: mkt, Plugins: res.Merged.Plugins.Include})
	}
	res, err := bundles.Compile(in)
	if err != nil {
		return fmt.Errorf("compiling bundles: %w", err)
	}
	drift, err := bundles.Check(r.root, res.Files)
	if err != nil {
		return fmt.Errorf("comparing bundles with the repo: %w", err)
	}
	data := compileJSON{
		Check:   check,
		Changed: drift.HasDrift(),
		Bundles: []bundleJSON{},
		Skipped: nonNil(res.Skipped),
		Notes:   []string{},
		Written: []string{},
		Drift: driftJSON{
			Missing: nonNil(drift.Missing), Modified: nonNil(drift.Modified),
			Stale: nonNil(drift.Stale), Extra: nonNil(drift.Extra), Diff: drift.Diff,
		},
	}
	for _, b := range res.Bundles {
		data.Bundles = append(data.Bundles, bundleJSON{Profile: b.Profile, Path: b.Path, CrossMarketplaces: nonNil(b.CrossMarketplaces)})
		if len(b.CrossMarketplaces) > 0 {
			data.Notes = append(data.Notes, fmt.Sprintf("bundle %s depends on plugins of %s: list them in allowCrossMarketplaceDependenciesOn of marketplace %s",
				b.Profile, strings.Join(b.CrossMarketplaces, ", "), mkt))
		}
	}
	if !check && drift.HasDrift() {
		if err := bundles.Write(r.root, res.Files, bundles.WriteOptions{PruneStale: true}); err != nil {
			return fmt.Errorf("writing bundles: %w", err)
		}
		for _, f := range res.Files {
			data.Written = append(data.Written, f.Path)
		}
	}
	if c.Mode.JSON {
		if err := ui.WriteJSON(out(c), "compile", data); err != nil {
			return err
		}
	} else if err := writeCompileText(out(c), data); err != nil {
		return err
	}
	if check && drift.HasDrift() {
		return ui.Failure(errors.New("the bundles are stale: run ccshelf compile and commit the result"))
	}
	return nil
}

func writeCompileText(w io.Writer, d compileJSON) error {
	var b strings.Builder
	switch {
	case d.Check && d.Changed:
		b.WriteString("bundles are stale:\n")
		for _, g := range []struct {
			label string
			paths []string
		}{{"missing", d.Drift.Missing}, {"modified", d.Drift.Modified}, {"stale", d.Drift.Stale}, {"extra", d.Drift.Extra}} {
			for _, p := range g.paths {
				fmt.Fprintf(&b, "  %-8s %s\n", g.label, ui.SanitizeLine(p))
			}
		}
		if d.Drift.Diff != "" {
			b.WriteString("\n")
			b.WriteString(ui.Sanitize(d.Drift.Diff))
			if !strings.HasSuffix(d.Drift.Diff, "\n") {
				b.WriteString("\n")
			}
		}
	case d.Check:
		fmt.Fprintf(&b, "bundles are up to date (%s)\n", plural(len(d.Bundles), "bundle", "bundles"))
	case d.Changed:
		fmt.Fprintf(&b, "wrote %s\n", plural(len(d.Bundles), "bundle", "bundles"))
		for _, x := range d.Bundles {
			fmt.Fprintf(&b, "  %s\n", ui.SanitizeLine(x.Path))
		}
	default:
		fmt.Fprintf(&b, "bundles are up to date (%s), nothing written\n", plural(len(d.Bundles), "bundle", "bundles"))
	}
	if len(d.Skipped) > 0 {
		fmt.Fprintf(&b, "skipped (no plugins): %s\n", strings.Join(d.Skipped, ", "))
	}
	for _, n := range d.Notes {
		fmt.Fprintf(&b, "note: %s\n", ui.SanitizeLine(n))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

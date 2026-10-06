package orgcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/analytics"
	"github.com/ccshelf/ccshelf/internal/catalog"
	"github.com/ccshelf/ccshelf/internal/claude"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/doctor"
	"github.com/ccshelf/ccshelf/internal/policy"
	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// detectPolicy is policy.Detect; tests replace it so they never read the
// machine's managed settings.
var detectPolicy = policy.Detect

// Policy finding codes added by doctor --policy.
const (
	codePolicyUnreadable = "POL001"
	codePolicyBlocked    = "POL002"
	codePolicyExcluded   = "POL003"
	codePolicyForced     = "POL004"
	checkPolicy          = "policy"
)

// doctorJSON is the data of `doctor --json` (kind "doctor"). The envelope is
// ui.WriteJSON's; the data mirrors doctor.Report plus the capability matrix.
type doctorJSON struct {
	Summary      doctor.Counts    `json:"summary"`
	Findings     []doctor.Finding `json:"findings"`
	Skipped      []doctor.Skip    `json:"skipped"`
	Capabilities []capabilityJSON `json:"capabilities,omitempty"`
	PolicySource []policy.Source  `json:"policy_sources,omitempty"`
}

type capabilityJSON struct {
	ID     string   `json:"id"`
	State  string   `json:"state"`
	Reason string   `json:"reason"`
	Items  []string `json:"items,omitempty"`
}

func newDoctor(get clicore.Provider) *cobra.Command {
	var usePolicy, installed, usageAPI bool
	var usageFile string
	var usageDays int
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the org's catalog and profiles for overlap, staleness and policy conflicts",
		Long: `Analyze the org data repo (--root, default the current directory): overlapping
plugins, plugins in no profile, deprecated plugins in use, stale or missing
review dates and owners, protected plugins that a profile masks, and plugins
that need platform review.

Checks that need more input are skipped and listed as skipped, never silently
passed: --installed reads 'claude plugin list' (read only), --usage-file reads
an OpenTelemetry JSONL export, --usage-api asks the Enterprise Analytics API
(needs the admin key in CCSHELF_ANALYTICS_KEY; this is the only network call),
and --policy reads the machine's managed Claude Code policy (read only,
never bypassed) and prints the capability matrix.

Exit code 1 for error findings; with --policy, 3 when managed policy blocks
something a profile needs or could not be read.`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := get()
			if err != nil {
				return err
			}
			if usageFile != "" && usageAPI {
				return ui.Usage(errors.New("use only one of --usage-file and --usage-api"))
			}
			if usageDays < 1 || usageDays > 365 {
				return ui.Usage(errors.New("--usage-days must be between 1 and 365"))
			}
			r, err := openRepo(c)
			if err != nil {
				return err
			}
			return runDoctor(cmd.Context(), c, r, doctorOptions{policy: usePolicy, installed: installed, usageAPI: usageAPI, usageFile: usageFile, usageDays: usageDays})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&usePolicy, "policy", false, "also read managed Claude Code policy and print the capability matrix")
	f.BoolVar(&installed, "installed", false, "read the installed plugins with 'claude plugin list'")
	f.StringVar(&usageFile, "usage-file", "", "OpenTelemetry JSONL export to read plugin usage from")
	f.BoolVar(&usageAPI, "usage-api", false, "read plugin usage from the Enterprise Analytics API")
	f.IntVar(&usageDays, "usage-days", 30, "usage window in days for --usage-api")
	return cmd
}

type doctorOptions struct {
	policy, installed, usageAPI bool
	usageFile                   string
	usageDays                   int
}

func runDoctor(ctx context.Context, c *clicore.Context, r *repo, o doctorOptions) error {
	cat, lrep, err := catalog.BuildContext(ctx, r.root, r.cfg, catalog.Options{Now: c.Now})
	if err != nil {
		return fmt.Errorf("building the catalog: %w", err)
	}
	good, bad, err := r.resolveAll()
	if err != nil {
		return err
	}
	dropAbstractBundles(lrep, good)
	src := profile.PortableSourceID(r.source())
	views := make([]doctor.ProfileView, 0, len(good))
	for _, res := range good {
		m := res.Merged
		views = append(views, doctor.ProfileView{
			Name: res.Name, Kind: res.Kind.String(), Source: src,
			Plugins: m.Plugins.Include, Exclude: m.Plugins.Exclude, Mode: m.Plugins.Mode,
			StandaloneOff: m.Skills.Off, NameOnly: m.Skills.NameOnly,
		})
	}
	in := doctor.Input{
		Catalog: cat, Lint: lrep, Profiles: views, Org: r.cfg,
		Now: c.Now(), ReviewMaxAgeDays: r.cfg.Lint.MaxReviewAgeDays,
	}
	for _, f := range r.profileFindings(bad, nil) {
		in.Policy = append(in.Policy, doctor.Finding{Severity: doctor.Severity(f.Severity), Code: f.Code, Check: "profile", Message: f.File + ": " + f.Message})
	}
	if o.installed {
		bin, err := claude.Locate(c.G.ClaudePath)
		if err != nil {
			return fmt.Errorf("--installed: %w", err)
		}
		wd, _ := c.Getwd()
		in.Installed, err = claude.ListInstalled(ctx, bin, wd, claude.Env(c.Environ(), nil))
		if err != nil {
			return fmt.Errorf("--installed: %w", err)
		}
		if in.Installed == nil {
			in.Installed = []claude.Plugin{}
		}
	}
	if in.Usage, err = loadUsage(ctx, c, o); err != nil {
		return err
	}

	var matrix *policy.Matrix
	policyBlocked := false
	if o.policy {
		pol, err := detectPolicy(ctx, policy.Options{GOOS: c.GOOS})
		if err != nil {
			return fmt.Errorf("reading managed policy: %w", err)
		}
		matrix = policy.Evaluate(pol, in.Installed)
		pf := policyFindings(pol, matrix, views)
		in.Policy = append(in.Policy, pf...)
		for _, f := range pf {
			if f.Severity == doctor.Error {
				policyBlocked = true
			}
		}
	}

	rep := doctor.Run(in)
	if c.Mode.JSON {
		data := doctorJSON{Summary: rep.Counts(), Findings: rep.Findings, Skipped: rep.Skipped}
		if data.Findings == nil {
			data.Findings = []doctor.Finding{}
		}
		if data.Skipped == nil {
			data.Skipped = []doctor.Skip{}
		}
		if matrix != nil {
			data.Capabilities = capabilities(matrix)
			data.PolicySource = matrix.Sources
		}
		if err := ui.WriteJSON(out(c), "doctor", data); err != nil {
			return err
		}
	} else {
		if err := rep.WriteText(out(c)); err != nil {
			return err
		}
		if matrix != nil {
			if err := writeMatrix(out(c), c.Mode, matrix); err != nil {
				return err
			}
		}
	}
	switch {
	case policyBlocked:
		return ui.Policy(errors.New("managed policy blocks something this org's profiles need, or could not be read (see the policy findings)"))
	case rep.HasErrors():
		return ui.Failure(fmt.Errorf("doctor found %s", plural(rep.Counts().Errors, "error", "errors")))
	}
	return nil
}

func loadUsage(ctx context.Context, c *clicore.Context, o doctorOptions) (*analytics.Usage, error) {
	switch {
	case o.usageFile != "":
		f, err := os.Open(o.usageFile)
		if err != nil {
			return nil, fmt.Errorf("--usage-file: %w", err)
		}
		defer f.Close()
		u, err := analytics.ParseOTelJSONL(f)
		if err != nil {
			return nil, fmt.Errorf("--usage-file %s: %w", o.usageFile, err)
		}
		return u, nil
	case o.usageAPI:
		to := c.Now().UTC().Truncate(24 * time.Hour)
		u, err := analytics.Fetch(ctx, analytics.Options{
			Getenv: c.Getenv, Now: c.Now, To: to, From: to.AddDate(0, 0, -o.usageDays),
		})
		if err != nil {
			if errors.Is(err, analytics.ErrNoKey) {
				return nil, ui.MissingFlags("set "+analytics.DefaultKeyEnv+" to an admin key, or use --usage-file", "--usage-file")
			}
			return nil, fmt.Errorf("--usage-api: %w", err)
		}
		return u, nil
	}
	return nil, nil
}

// policyFindings turns the detected policy into doctor findings. Errors (exit
// code 3) are an unreadable policy and a profile that includes a plugin the
// policy blocks; masking attempts on forced plugins are warnings.
func policyFindings(pol *policy.Policy, m *policy.Matrix, views []doctor.ProfileView) []doctor.Finding {
	var out []doctor.Finding
	if pol.Unreadable {
		msg := "a managed policy source exists but could not be read, so what it restricts is not known"
		if len(pol.Unknown) > 0 {
			msg += ": " + pol.Unknown[0]
		}
		out = append(out, doctor.Finding{Severity: doctor.Error, Code: codePolicyUnreadable, Check: checkPolicy, Message: msg,
			Hint: "fix the file's permissions or contents; ccshelf never guesses around a policy it cannot read"})
	}
	blocked := toSet(m.BlockedPlugins())
	forced := m.LockedPlugins()
	for _, v := range views {
		inc := toSet(v.Plugins)
		exc := toSet(v.Exclude)
		for _, id := range v.Plugins {
			if blocked[id] {
				out = append(out, doctor.Finding{Severity: doctor.Error, Code: codePolicyBlocked, Check: checkPolicy, Plugin: id, Profile: v.Name,
					Message: fmt.Sprintf("profile %s includes %s, which managed policy blocks (enabledPlugins false)", v.Name, id),
					Hint:    "remove it from the profile; policy cannot be overridden"})
			}
		}
		for _, id := range forced {
			switch {
			case exc[id]:
				out = append(out, doctor.Finding{Severity: doctor.Warning, Code: codePolicyExcluded, Check: checkPolicy, Plugin: id, Profile: v.Name,
					Message: fmt.Sprintf("profile %s excludes %s, but managed policy force-enables it, so it stays on", v.Name, id)})
			case !inc[id] && (v.Mode == "" || v.Mode == profile.ModeAllowOnly):
				out = append(out, doctor.Finding{Severity: doctor.Info, Code: codePolicyForced, Check: checkPolicy, Plugin: id, Profile: v.Name,
					Message: fmt.Sprintf("%s is force-enabled by managed policy and stays on in profile %s although the profile does not list it", id, v.Name)})
			}
		}
	}
	return out
}

func toSet(s []string) map[string]bool {
	m := make(map[string]bool, len(s))
	for _, x := range s {
		m[x] = true
	}
	return m
}

func capabilities(m *policy.Matrix) []capabilityJSON {
	var out []capabilityJSON
	for _, id := range policy.AllFeatures {
		f, ok := m.Features[id]
		if !ok {
			continue
		}
		out = append(out, capabilityJSON{ID: string(f.ID), State: string(f.State), Reason: f.Reason, Items: f.Items})
	}
	return out
}

func writeMatrix(w io.Writer, mode ui.Mode, m *policy.Matrix) error {
	var b strings.Builder
	b.WriteString("\ncapability matrix (managed policy):\n")
	rows := [][]string{}
	for _, c := range capabilities(m) {
		reason := c.Reason
		if len(c.Items) > 0 {
			reason += " (" + strings.Join(c.Items, ", ") + ")"
		}
		rows = append(rows, []string{c.ID, c.State, reason})
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return err
	}
	if err := ui.Table(w, []string{"FEATURE", "STATE", "REASON"}, rows, mode); err != nil {
		return err
	}
	b.Reset()
	for _, s := range m.Sources {
		state := "absent"
		if s.Present {
			state = "present"
			if s.Used {
				state = "in use"
			}
		}
		fmt.Fprintf(&b, "source %s: %s (%s)\n", s.Kind, ui.SanitizeLine(s.Location), state)
	}
	for _, u := range m.Unknown {
		fmt.Fprintf(&b, "unknown: %s\n", ui.SanitizeLine(u))
	}
	for _, x := range m.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", ui.SanitizeLine(x))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

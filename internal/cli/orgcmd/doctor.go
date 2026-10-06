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
	codePolicyUnreadable  = "POL001"
	codePolicyBlocked     = "POL002"
	codePolicyExcluded    = "POL003"
	codePolicyForced      = "POL004"
	codePolicyNeedBlocked = "POL005"
	codePolicyMarketplace = "POL006"
	codePolicyNeedUnknown = "POL007"
	checkPolicy           = "policy"
)

// maxSkillEntries caps the entries read from --skills-dir.
const maxSkillEntries = 5000

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
	var usePolicy, installed, usageAPI, strict bool
	var usageFile, skillsDir string
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
--skills-dir lists the standalone skills in a directory (read only; each
subdirectory with a SKILL.md is a skill; ccshelf never reads ~/.claude on its
own), and --policy reads the machine's managed Claude Code policy (read only,
never bypassed) and prints the capability matrix.

With --policy every profile's needs (extra MCP servers, strict MCP, dropping
user settings, a system prompt file, hiding claude.ai connectors, and the
marketplaces of its plugins) are checked against the policy that could be
read. Exit code 3 when the policy blocks something a profile needs (POL002,
POL005, POL006); a feature whose state is unknown is a warning, never a
failure. A policy that exists but could not be read is a warning, and exit 3
only with --strict.

Exit code 1 for error findings, and when the directory is not an org data
repo (its marketplace file cannot be read).`,
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
			return runDoctor(cmd.Context(), c, r, doctorOptions{policy: usePolicy, installed: installed, usageAPI: usageAPI, usageFile: usageFile, usageDays: usageDays, skillsDir: skillsDir, strict: strict})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&usePolicy, "policy", false, "also read managed Claude Code policy and print the capability matrix")
	f.BoolVar(&installed, "installed", false, "read the installed plugins with 'claude plugin list'")
	f.StringVar(&usageFile, "usage-file", "", "OpenTelemetry JSONL export to read plugin usage from")
	f.BoolVar(&usageAPI, "usage-api", false, "read plugin usage from the Enterprise Analytics API")
	f.IntVar(&usageDays, "usage-days", 30, "usage window in days for --usage-api and --usage-file")
	f.StringVar(&skillsDir, "skills-dir", "", "directory of standalone skills to check against the profiles (read only)")
	f.BoolVar(&strict, "strict", false, "with --policy: exit 3 also when a managed policy exists but could not be read")
	return cmd
}

type doctorOptions struct {
	policy, installed, usageAPI, strict bool
	usageFile, skillsDir                string
	usageDays                           int
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
	if err := requireMarketplace(r, lrep); err != nil {
		return err
	}
	dropAbstractBundles(lrep, good)
	src := profile.PortableSourceID(r.source())
	views := make([]doctor.ProfileView, 0, len(good))
	profs := make([]policyProfile, 0, len(good))
	for _, res := range good {
		m := res.Merged
		strict := m.MCP.Strict != nil && *m.MCP.Strict
		hide := m.MCP.ClaudeAIConnectors == profile.ConnectorsNone
		v := doctor.ProfileView{
			Name: res.Name, Kind: res.Kind.String(), Source: src,
			Plugins: m.Plugins.Include, Exclude: m.Plugins.Exclude, Mode: m.Plugins.Mode,
			StandaloneOff: m.Skills.Off, NameOnly: m.Skills.NameOnly,
			Abstract: len(m.Plugins.Include) == 0, HideConnectors: hide, StrictMCP: strict,
		}
		views = append(views, v)
		// The same needs the launcher hands to policy.Matrix.Plan.
		profs = append(profs, policyProfile{view: v, onBlocked: m.Policy.OnBlocked, needs: policy.Needs{
			ExtraMCPServers:        strict || len(res.MCP) > 0,
			StrictMCPConfig:        strict,
			DropUserSettingSources: !m.InheritsUserSettings(),
			AppendSystemPromptFile: len(res.Prompt) > 0,
			HideConnectors:         hide,
		}})
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
	if o.skillsDir != "" {
		if in.StandaloneSkills, err = readSkillNames(o.skillsDir); err != nil {
			return fmt.Errorf("--skills-dir: %w", err)
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
		pf := policyFindings(pol, matrix, profs, o.strict)
		in.Policy = append(in.Policy, pf...)
		for _, f := range pf {
			if f.Severity == doctor.Error {
				policyBlocked = true
			}
		}
	}

	rep := doctor.Run(in)
	if c.Mode.JSON {
		pl := rep.Payload()
		data := doctorJSON{Summary: pl.Summary, Findings: pl.Findings, Skipped: pl.Skipped}
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
		return ui.Policy(errors.New("managed policy blocks something this org's profiles need, or (with --strict) could not be read; see the policy findings"))
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
		// The window ends after today (UTC) and starts --usage-days earlier.
		end := c.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
		u, err := analytics.ParseOTelJSONLWindow(f, analytics.OTelOptions{From: end.AddDate(0, 0, -o.usageDays), To: end})
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
				return nil, ui.Usage(fmt.Errorf("--usage-api needs an admin key: set the environment variable %s (the key is read from the environment only), or use --usage-file with an OpenTelemetry export", analytics.DefaultKeyEnv))
			}
			return nil, fmt.Errorf("--usage-api: %w", err)
		}
		return u, nil
	}
	return nil, nil
}

// readSkillNames lists the standalone skills in dir: its subdirectories (or
// symbolic links to directories) that hold a SKILL.md. It only reads
// directory entries and stats SKILL.md; it never opens a skill file. The
// result is never nil, so "none found" differs from "not read".
func readSkillNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	if len(entries) > maxSkillEntries {
		return nil, fmt.Errorf("%s has more than %d entries", dir, maxSkillEntries)
	}
	names := []string{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !(e.IsDir() || e.Type()&os.ModeSymlink != 0) {
			continue
		}
		if fi, err := os.Stat(filepath.Join(dir, name, "SKILL.md")); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// policyProfile is a resolved profile with what it asks of the launcher.
type policyProfile struct {
	view      doctor.ProfileView
	needs     policy.Needs
	onBlocked string
}

// neededFeatures lists the capabilities a profile's needs map to, in the
// order policy.Matrix.Plan evaluates them.
func neededFeatures(n policy.Needs) []policy.FeatureID {
	var out []policy.FeatureID
	for _, x := range []struct {
		need bool
		id   policy.FeatureID
	}{
		{n.HideConnectors, policy.HideConnectors},
		{n.ExtraMCPServers, policy.AddMCPConfig},
		{n.StrictMCPConfig, policy.StrictMCPConfig},
		{n.DropUserSettingSources, policy.SettingSources},
		{n.AppendSystemPromptFile, policy.AppendSystemPromptFile},
	} {
		if x.need {
			out = append(out, x.id)
		}
	}
	return out
}

// marketplaceBlocked decides from evidence whether managed policy keeps the
// named marketplace out. It knows a marketplace by the source that
// extraKnownMarketplaces gives it, so a marketplace policy does not describe
// is never called blocked.
func marketplaceBlocked(pol *policy.Policy, name string) (bool, string) {
	if pol.StrictKnownMarketplaces != nil && len(pol.StrictKnownMarketplaces) == 0 {
		return true, "strictKnownMarketplaces is empty, which allows no marketplace"
	}
	src, known := pol.ExtraKnownMarketplaces[name]
	if !known {
		return false, ""
	}
	for _, b := range pol.BlockedMarketplaces {
		if b == src {
			return true, "its source is listed in blockedMarketplaces"
		}
	}
	if len(pol.StrictKnownMarketplaces) > 0 {
		for _, a := range pol.StrictKnownMarketplaces {
			if a == src || strings.HasSuffix(a.Kind, "Pattern") {
				return false, "" // allowed, or a pattern this check does not evaluate
			}
		}
		return true, "its source is not among strictKnownMarketplaces"
	}
	return false, ""
}

// policyFindings turns the detected policy into doctor findings.
//
// Errors (exit code 3) are a profile that includes a plugin the policy turns
// off (POL002), a profile that needs a launcher feature the policy blocks
// (POL005, from policy.Matrix.Plan) and a profile whose plugins come from a
// marketplace the policy blocks (POL006). A policy that exists but could not
// be read is a warning (POL001) and an error only when strict is set. A needed
// feature whose state is unknown is a warning (POL007), never a failure.
// Masking attempts on forced plugins are warnings and notes.
func policyFindings(pol *policy.Policy, m *policy.Matrix, profs []policyProfile, strict bool) []doctor.Finding {
	var out []doctor.Finding
	if pol.Unreadable {
		sev := doctor.Warning
		if strict {
			sev = doctor.Error
		}
		msg := "a managed policy source exists but could not be read, so what it restricts is not known"
		if len(pol.Unknown) > 0 {
			msg += ": " + pol.Unknown[0]
		}
		out = append(out, doctor.Finding{
			Severity: sev, Code: codePolicyUnreadable, Check: checkPolicy, Message: msg,
			Hint: "fix the file's permissions or contents; ccshelf never guesses around a policy it cannot read (--strict makes this exit 3)",
		})
	}
	blocked := toSet(m.BlockedPlugins())
	forced := m.LockedPlugins()
	for _, p := range profs {
		v := p.view
		inc := toSet(v.Plugins)
		exc := toSet(v.Exclude)
		for _, id := range v.Plugins {
			if blocked[id] {
				out = append(out, doctor.Finding{
					Severity: doctor.Error, Code: codePolicyBlocked, Check: checkPolicy, Plugin: id, Profile: v.Name,
					Message: fmt.Sprintf("profile %s includes %s, which managed policy blocks (enabledPlugins false)", v.Name, id),
					Hint:    "remove it from the profile; policy cannot be overridden",
				})
			}
		}
		for _, id := range forced {
			switch {
			case exc[id]:
				out = append(out, doctor.Finding{
					Severity: doctor.Warning, Code: codePolicyExcluded, Check: checkPolicy, Plugin: id, Profile: v.Name,
					Message: fmt.Sprintf("profile %s excludes %s, but managed policy force-enables it, so it stays on", v.Name, id),
				})
			case !v.Abstract && !inc[id] && (v.Mode == "" || v.Mode == profile.ModeAllowOnly):
				out = append(out, doctor.Finding{
					Severity: doctor.Info, Code: codePolicyForced, Check: checkPolicy, Plugin: id, Profile: v.Name,
					Message: fmt.Sprintf("%s is force-enabled by managed policy and stays on in profile %s although the profile does not list it", id, v.Name),
				})
			}
		}
		if v.Abstract {
			continue // a base profile is never launched
		}
		out = append(out, needFindings(m, p)...)
		out = append(out, marketplaceFindings(pol, v)...)
	}
	return out
}

// needFindings checks one profile's launcher needs with Matrix.Plan.
func needFindings(m *policy.Matrix, p policyProfile) []doctor.Finding {
	var out []doctor.Finding
	applied, err := m.Plan(p.needs, "warn")
	if err != nil {
		return []doctor.Finding{{
			Severity: doctor.Warning, Code: codePolicyNeedUnknown, Check: checkPolicy, Profile: p.view.Name,
			Message: fmt.Sprintf("profile %s: the policy check could not be evaluated: %v", p.view.Name, err),
		}}
	}
	outcome := "the launcher drops it with a warning (policy.on_blocked = \"warn\")"
	if p.onBlocked == "fail" {
		outcome = "the launcher refuses to start the profile (policy.on_blocked = \"fail\")"
	}
	for _, id := range applied.Dropped {
		out = append(out, doctor.Finding{
			Severity: doctor.Error, Code: codePolicyNeedBlocked, Check: checkPolicy, Profile: p.view.Name,
			Message: fmt.Sprintf("profile %s needs %s, which managed policy blocks (%s); %s", p.view.Name, id, m.Features[id].Reason, outcome),
			Hint:    "change the profile so it does not need this feature; policy cannot be overridden",
		})
	}
	// Features that are unknown for the same reason share one finding.
	var reasons []string
	byReason := map[string][]string{}
	for _, id := range neededFeatures(p.needs) {
		if f := m.Features[id]; f.State == policy.Unknown {
			if _, ok := byReason[f.Reason]; !ok {
				reasons = append(reasons, f.Reason)
			}
			byReason[f.Reason] = append(byReason[f.Reason], string(id))
		}
	}
	for _, reason := range reasons {
		out = append(out, doctor.Finding{
			Severity: doctor.Warning, Code: codePolicyNeedUnknown, Check: checkPolicy, Profile: p.view.Name,
			Message: fmt.Sprintf("profile %s needs %s, which managed policy may block (state unknown): %s", p.view.Name, strings.Join(byReason[reason], ", "), reason),
		})
	}
	return out
}

// marketplaceFindings reports plugins of a profile that come from a
// marketplace managed policy blocks.
func marketplaceFindings(pol *policy.Policy, v doctor.ProfileView) []doctor.Finding {
	byMarket := map[string][]string{}
	for _, id := range v.Plugins {
		if i := strings.LastIndex(id, "@"); i >= 0 && i < len(id)-1 {
			byMarket[id[i+1:]] = append(byMarket[id[i+1:]], id)
		}
	}
	names := make([]string, 0, len(byMarket))
	for n := range byMarket {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []doctor.Finding
	for _, n := range names {
		if bad, why := marketplaceBlocked(pol, n); bad {
			out = append(out, doctor.Finding{
				Severity: doctor.Error, Code: codePolicyMarketplace, Check: checkPolicy, Profile: v.Name,
				Message: fmt.Sprintf("profile %s includes %s from marketplace %s, which managed policy blocks (%s)", v.Name, strings.Join(byMarket[n], ", "), n, why),
				Hint:    "use plugins from an allowed marketplace; policy cannot be overridden",
			})
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

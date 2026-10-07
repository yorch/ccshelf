package orgcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/catalog/codeowners"
	"github.com/yorch/ccshelf/internal/catalog/gitdata"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/scaffold"
	"github.com/yorch/ccshelf/internal/ui"
)

// initFlags are the flags of "catalog init".
type initFlags struct {
	marketplaceName, org, owner string
	platformOwners              []string
	ccshelfRef, ccshelfVersion  string
	runnerLabel, mode, sidecars string
	defaultBranch               string
	exampleProfile              bool
	skip                        map[scaffold.Group]*bool
	dryRun, yes, force          bool
	writeSuggestions, gitInit   bool
	quiet                       bool
}

// initEntryJSON is one planned file in `catalog init --json`.
type initEntryJSON struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
	// Suggestion is the text for a needs-merge file (what --write-suggestions
	// writes), cut at maxSuggestionBytes.
	Suggestion string `json:"suggestion,omitempty"`
}

// maxSuggestionBytes bounds a suggestion in the plan output; the file written
// by --write-suggestions is always complete.
const maxSuggestionBytes = 4096

// maxSuggestionLines bounds the suggestion lines printed per file in text mode.
const maxSuggestionLines = 20

// initJSON is the data of `catalog init --json` (kind "catalog-init").
type initJSON struct {
	Dir     string          `json:"dir"`
	Mode    string          `json:"mode"`
	DryRun  bool            `json:"dry_run"`
	Entries []initEntryJSON `json:"entries"`
	// Written, Overwritten, Backups and Suggestions list what a real run did.
	Written     []string `json:"written"`
	Overwritten []string `json:"overwritten"`
	Backups     []string `json:"backups"`
	Suggestions []string `json:"suggestions"`
	// Refused lists suggestion files that were not written because a file of
	// that name exists and ccshelf did not write it.
	Refused []string `json:"refused"`
	GitInit bool     `json:"git_init"`
	Notes   []string `json:"notes"`
	Todo    []string `json:"todo"`
}

// skipGroups are the --no-<group> flags: the flag name and the group.
var skipGroups = []struct {
	flag  string
	group scaffold.Group
	help  string
}{
	{"no-config", scaffold.GroupConfig, "do not generate ccshelf.toml"},
	{"no-marketplace", scaffold.GroupMarketplace, "do not generate .claude-plugin/marketplace.json"},
	{"no-sidecars", scaffold.GroupSidecars, "do not generate catalog/plugins/<name>.toml (same as --sidecars none)"},
	{"no-codeowners", scaffold.GroupCodeowners, "do not generate .github/CODEOWNERS"},
	{"no-workflows", scaffold.GroupWorkflows, "do not generate the .github/workflows files"},
	{"no-readme", scaffold.GroupReadme, "do not generate README.md"},
	{"no-gitattributes", scaffold.GroupGitattributes, "do not generate .gitattributes"},
	{"no-gitignore", scaffold.GroupGitignore, "do not generate .gitignore"},
}

func newCatalogInit(get clicore.Provider) *cobra.Command {
	f := &initFlags{skip: map[scaffold.Group]*bool{}}
	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Set up a new org data repo, or add the missing pieces to an existing marketplace repo",
		Long: `Bootstrap an org data repo in dir (default: --root, else the current directory).
This is the organization's setup; "ccshelf init" is each developer's own.

Modes (detected, or forced with --mode):
  new    an empty or missing directory (a lone .git counts as empty).
  adopt  a directory with content, such as a marketplace repo that has
         .claude-plugin/marketplace.json and/or plugins/. Nothing existing is
         changed: the command prints a plan and only creates files that are
         missing. An existing file is reported as skip-exists (and as
         up to date when it already equals what would be written), or as
         needs-merge for CODEOWNERS, .gitattributes and .gitignore, with the
         lines to add; --write-suggestions saves them next to the file as
         <name>.ccshelf-suggested. A marketplace.json is never rewritten.
         --force replaces the other differing files after saving <file>.bak.

Generated files (each group can be left out with a --no-<group> flag):
  ccshelf.toml, .claude-plugin/marketplace.json (a skeleton; in adopt mode it
  lists the plugins found under plugins/), catalog/plugins/<name>.toml (a stub
  per plugin: the owner from CODEOWNERS or --owner, status experimental, and
  TODO(ccshelf) placeholders that ccshelf lint reports as warnings, CAT048),
  .github/CODEOWNERS, .github/workflows/{validate,catalog,release}.yml,
  README.md, .gitattributes and .gitignore. ccshelf has no built-in profiles:
  profiles/example.toml.sample, an all-comment sample, is written only with
  --example-profile.

The workflows call the ccshelf action pinned by full commit SHA. Pass
--ccshelf-ref <40-hex SHA> and --ccshelf-version <vX.Y.Z> to pin it (a tag
given as --ccshelf-ref sets the version only). Without them the workflows
contain a placeholder and fail with a clear message until you pin them.

Flags are the contract: every value can be passed as a flag. In a terminal,
the values that are missing are asked for, the plan is shown, and the
equivalent flag command is printed. Without a terminal, a missing value is a
usage error (exit 2) naming the flag, and writing needs --yes (or --dry-run to
print the plan). Nothing is committed, pushed or fetched; --git-init only runs
git init when the directory is not a repository yet.`,
		Example: `  ccshelf catalog init ./acme-claude --marketplace-name acme --org "Acme Corp" --platform-owners @acme/platform --yes
  ccshelf catalog init . --platform-owners @acme/platform --dry-run
  ccshelf catalog init . --mode adopt --platform-owners @acme/platform --write-suggestions --yes`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return ui.Usage(fmt.Errorf("catalog init takes at most one directory, got %d arguments", len(args)))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := get()
			if err != nil {
				return err
			}
			return runCatalogInit(cmd.Context(), c, cmd, f, args)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.marketplaceName, "marketplace-name", "", "marketplace name (lower case letters, digits and hyphens)")
	fl.StringVar(&f.org, "org", "", "display name of the organization (default: the marketplace name)")
	fl.StringVar(&f.owner, "owner", "", "default owner of the plugin sidecars (default: the first platform owner)")
	fl.StringSliceVar(&f.platformOwners, "platform-owners", nil, "CODEOWNERS owners of everything that runs code or shapes the catalog (@user, @org/team or an email address; repeatable)")
	fl.StringVar(&f.ccshelfRef, "ccshelf-ref", "", "full 40-hex commit SHA of the ccshelf action to pin, or a release tag vX.Y.Z (sets the version only)")
	fl.StringVar(&f.ccshelfVersion, "ccshelf-version", "", "ccshelf release tag the action installs, such as v0.1.0")
	fl.StringVar(&f.runnerLabel, "runner-label", "", "fallback of runs-on in the workflows when the RUNNER_LABEL variable is unset (default "+scaffold.DefaultRunnerLabel+")")
	fl.StringVar(&f.defaultBranch, "default-branch", "", "default branch the catalog workflow publishes from (default: read from an existing repository, else main)")
	fl.StringVar(&f.mode, "mode", "", "new or adopt (default: detected from the directory)")
	fl.StringVar(&f.sidecars, "sidecars", "stub", "sidecar files for the plugins found: stub or none")
	fl.BoolVar(&f.exampleProfile, "example-profile", false, "also write profiles/example.toml.sample, an all-comment sample")
	for _, sg := range skipGroups {
		v := new(bool)
		f.skip[sg.group] = v
		fl.BoolVar(v, sg.flag, false, sg.help)
	}
	fl.BoolVar(&f.dryRun, "dry-run", false, "print the plan and write nothing")
	fl.BoolVar(&f.yes, "yes", false, "write without asking for confirmation (never accepts trust)")
	fl.BoolVar(&f.force, "force", false, "replace existing files that differ, after saving <file>.bak (never marketplace.json)")
	fl.BoolVar(&f.writeSuggestions, "write-suggestions", false, "write <name>.ccshelf-suggested files for the files that need a merge")
	fl.BoolVar(&f.quiet, "quiet", false, "do not print the suggested lines of the files that need a merge (they stay in --json and in --write-suggestions)")
	fl.BoolVar(&f.gitInit, "git-init", false, "run git init when the directory is not a git repository (nothing else of git)")
	return cmd
}

// params builds the scaffold parameters from the flags.
func (f *initFlags) params() (scaffold.Params, error) {
	p := scaffold.Params{
		Mode: scaffold.Mode(f.mode), MarketplaceName: f.marketplaceName, Org: f.org, Owner: f.owner,
		PlatformOwners: append([]string(nil), f.platformOwners...),
		CcshelfRef:     f.ccshelfRef, CcshelfVersion: f.ccshelfVersion, RunnerLabel: f.runnerLabel, DefaultBranch: f.defaultBranch,
		Skip: map[scaffold.Group]bool{}, ExampleProfile: f.exampleProfile, Force: f.force,
	}
	for g, v := range f.skip {
		if *v {
			p.Skip[g] = true
		}
	}
	switch f.sidecars {
	case "stub", "":
	case "none":
		p.Skip[scaffold.GroupSidecars] = true
	default:
		return p, ui.Usage(fmt.Errorf("--sidecars: %q is not stub or none", ui.SanitizeLine(f.sidecars)))
	}
	if err := p.Validate(); err != nil {
		return p, ui.Usage(err)
	}
	return p, nil
}

// ownDirs are the directories that catalog init never writes into:
// Claude Code's own (~/.claude and $CLAUDE_CONFIG_DIR) and ccshelf's
// configuration and cache directories, derived from the command's
// environment.
func ownDirs(c *clicore.Context) []string {
	home := homeDir(c)
	var out []string
	add := func(parts ...string) {
		for _, p := range parts {
			if p == "" {
				return
			}
		}
		if d := filepath.Join(parts...); filepath.IsAbs(d) {
			out = append(out, d)
		}
	}
	add(home, ".claude")
	if d := c.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(d) {
		out = append(out, d)
	}
	if c.GOOS == "windows" {
		add(c.Getenv("APPDATA"), "ccshelf")
		add(c.Getenv("LOCALAPPDATA"), "ccshelf")
		add(home, "AppData", "Roaming", "ccshelf")
		add(home, "AppData", "Local", "ccshelf")
		return out
	}
	if x := c.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(x) {
		add(x, "ccshelf")
	}
	if x := c.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(x) {
		add(x, "ccshelf")
	}
	add(home, ".config", "ccshelf")
	add(home, ".cache", "ccshelf")
	return out
}

// homeDir returns the user's home directory from the command's environment.
func homeDir(c *clicore.Context) string {
	if c.GOOS == "windows" {
		return c.Getenv("USERPROFILE")
	}
	return c.Getenv("HOME")
}

func runCatalogInit(ctx context.Context, c *clicore.Context, cmd *cobra.Command, f *initFlags, args []string) error {
	p, err := f.params()
	if err != nil {
		return err
	}
	arg := c.G.Root
	if len(args) == 1 {
		arg = args[0]
	}
	wd, err := c.Getwd()
	if err != nil {
		return fmt.Errorf("finding the current directory: %w", err)
	}
	tgt, err := scaffold.ResolveTarget(scaffold.TargetOptions{Arg: arg, Wd: wd, Home: homeDir(c), Forbidden: ownDirs(c)})
	if err != nil {
		var te *scaffold.TargetError
		if errors.As(err, &te) && te.Usage {
			return ui.Usage(err)
		}
		return ui.Failure(err)
	}
	fsys, closer, err := tgt.Open()
	if err != nil {
		return asFailure(err)
	}
	defer func() { _ = closer.Close() }()
	detectBranch(ctx, tgt, &p)

	interactive := canPromptInit(c)
	asked := false
	var plan *scaffold.Plan
	for {
		plan, err = scaffold.Build(fsys, p)
		var me *scaffold.MissingError
		if errors.As(err, &me) {
			if !interactive {
				flags, hints := append([]string(nil), me.Flags...), append([]string(nil), me.Hints...)
				if !f.yes && !f.dryRun {
					flags = append(flags, "--yes")
				}
				return ui.MissingFlags(strings.Join(hints, "; "), flags...)
			}
			if err := askMissing(ctx, c, tgt, &p, me); err != nil {
				return err
			}
			asked = true
			continue
		}
		if err != nil {
			return asFailure(err)
		}
		if interactive && plan.NeedsPin && !cmd.Flags().Changed("ccshelf-ref") && !cmd.Flags().Changed("ccshelf-version") && p.CcshelfRef == "" && p.CcshelfVersion == "" {
			if err := askPin(ctx, c, &p); err != nil {
				return err
			}
			asked = true
			p2 := p
			if plan2, err2 := scaffold.Build(fsys, p2); err2 == nil {
				plan = plan2
			}
		}
		break
	}

	data := initJSON{
		Dir: tgt.Dir, Mode: string(plan.Mode), DryRun: f.dryRun,
		Written: []string{}, Overwritten: []string{}, Backups: []string{}, Suggestions: []string{}, Refused: []string{},
	}
	for _, e := range plan.Entries {
		data.Entries = append(data.Entries, initEntryJSON{Path: e.Path, Action: string(e.Action), Reason: e.Reason, Suggestion: boundedSuggestion(e.Suggestion)})
	}
	if data.Entries == nil {
		data.Entries = []initEntryJSON{}
	}
	gitWanted := f.gitInit && !plan.HasGit
	if gitWanted {
		if top := gitdata.EnclosingWorkTree(ctx, tgt.Dir); top != "" {
			plan.Notes = append(plan.Notes, fmt.Sprintf("--git-init would create a repository inside the work tree of %s (a nested repository, which that repository will see as an untracked directory or a submodule candidate); if that is not what you want, leave --git-init out and put the directory elsewhere", ui.SanitizeLine(top)))
		}
	}
	data.Notes = append([]string{}, plan.Notes...)
	data.Todo = append([]string{}, plan.Todos...)

	if !c.Mode.JSON {
		printInitPlan(out(c), tgt.Dir, plan, f)
	}

	if f.dryRun {
		return finishInit(c, tgt, plan, f, p, asked, &data, nil, false)
	}
	if !f.yes {
		if !interactive {
			return ui.MissingFlags("confirm writing the files; --dry-run prints the plan and writes nothing", "--yes")
		}
		ok, err := c.Prompt.Confirm(ctx, "Write these files?", false)
		if err != nil {
			return err
		}
		if !ok {
			return ui.Failure(errors.New("canceled: nothing was written"))
		}
	}

	var res *scaffold.Result
	if plan.Changes() || (f.writeSuggestions && plan.Count(scaffold.ActionMerge) > 0) || gitWanted {
		wfs, wcloser, err := tgt.Create()
		if err != nil {
			return asFailure(err)
		}
		defer func() { _ = wcloser.Close() }()
		res, err = scaffold.Apply(wfs, plan, scaffold.ApplyOptions{WriteSuggestions: f.writeSuggestions})
		if err != nil {
			return ui.Failure(&applyFailure{err: err, res: res})
		}
	}
	if gitWanted {
		if err := gitdata.Init(ctx, tgt.Dir); err != nil {
			return ui.Failure(withHintErr(fmt.Errorf("git init: %w", err), "the files were written; run git init yourself"))
		}
		data.GitInit = true
	}
	return finishInit(c, tgt, plan, f, p, asked, &data, res, gitWanted)
}

// detectBranch fills the default branch from the repository in the target
// when the user gave none: read-only, through the hardened git helper, and only
// when the target itself is the root of a repository (a parent repository is
// never read). A name that is not a plain branch name is ignored.
func detectBranch(ctx context.Context, tgt *scaffold.Target, p *scaffold.Params) {
	if p.DefaultBranch != "" || !tgt.Exists || p.Skip[scaffold.GroupWorkflows] {
		return
	}
	if _, err := os.Lstat(filepath.Join(tgt.Dir, ".git")); err != nil {
		return
	}
	if b, src := gitdata.DefaultBranch(ctx, tgt.Dir); scaffold.ValidBranch(b) {
		p.DefaultBranch, p.BranchSource = b, src
	}
}

func asFailure(err error) error {
	var fe *scaffold.FieldError
	if errors.As(err, &fe) {
		return ui.Usage(err)
	}
	var te *scaffold.TargetError
	if errors.As(err, &te) && te.Usage {
		return ui.Usage(err)
	}
	return ui.Failure(err)
}

// canPromptInit says whether the wizard may ask questions: a terminal, and
// neither --no-interactive nor --json.
func canPromptInit(c *clicore.Context) bool {
	if c.G.NoInteractive || c.Mode.JSON {
		return false
	}
	_, non := c.Prompt.(ui.NonInteractive)
	return !non
}

func printInitPlan(w io.Writer, dir string, plan *scaffold.Plan, f *initFlags) {
	fmt.Fprintf(w, "catalog init: %s (mode: %s)\n", ui.SanitizeLine(dir), plan.Mode)
	if len(plan.Entries) == 0 {
		fmt.Fprintln(w, "  nothing is selected to generate")
	}
	for _, e := range plan.Entries {
		line := fmt.Sprintf("  %-11s %s", e.Action, ui.SanitizeLine(e.Path))
		if e.Reason != "" {
			line += "  (" + ui.SanitizeLine(e.Reason) + ")"
		}
		fmt.Fprintln(w, line)
		if !f.quiet && e.Action == scaffold.ActionMerge {
			printSuggestion(w, e.Suggestion)
		}
	}
	if f.gitInit {
		if plan.HasGit {
			fmt.Fprintln(w, "  git init is not needed: .git exists")
		} else {
			fmt.Fprintln(w, "  git init   .git")
		}
	}
	for _, n := range plan.Notes {
		fmt.Fprintf(w, "note: %s\n", ui.SanitizeLine(n))
	}
	if n := plan.Count(scaffold.ActionMerge); n > 0 && !f.writeSuggestions {
		fmt.Fprintf(w, "note: %s need a merge by hand; --write-suggestions saves the lines to add as <name>%s\n", plural(n, "file", "files"), scaffold.SuggestionSuffix)
	}
}

// boundedSuggestion returns the suggestion as text, cut at maxSuggestionBytes.
func boundedSuggestion(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if len(b) > maxSuggestionBytes {
		cut := strings.ToValidUTF8(string(b[:maxSuggestionBytes]), "")
		return cut + "\n# ... cut: the file written by --write-suggestions is complete\n"
	}
	return string(b)
}

// printSuggestion prints the suggested lines of a needs-merge file, indented,
// at most maxSuggestionLines lines of at most 200 characters each.
func printSuggestion(w io.Writer, s []byte) {
	lines := strings.Split(strings.TrimRight(string(s), "\n"), "\n")
	for i, l := range lines {
		if i == maxSuggestionLines {
			fmt.Fprintf(w, "      ... %d more lines (--write-suggestions saves all of them)\n", len(lines)-i)
			return
		}
		l = ui.SanitizeLine(l)
		if len(l) > 200 {
			l = strings.ToValidUTF8(l[:200], "") + "..."
		}
		fmt.Fprintf(w, "      | %s\n", l)
	}
}

// applyFailure is the error of a write that stopped half way. The text names
// the files that were written and the ones rolled back; the JSON error carries
// both lists.
type applyFailure struct {
	err error
	res *scaffold.Result
}

func (e *applyFailure) Error() string { return e.err.Error() }
func (e *applyFailure) Unwrap() error { return e.err }

// Hint says what is left on disk.
func (e *applyFailure) Hint() string {
	kept := len(e.res.Created) - len(e.res.RolledBack)
	switch {
	case len(e.res.Created) == 0:
		return "nothing new was written; fix the problem and run the command again"
	case kept <= 0:
		return fmt.Sprintf("the %s written before the failure %s removed again (rolled back: %s); fix the problem and run the command again", plural(len(e.res.Created), "file", "files"), pluralVerb(len(e.res.Created)), listFiles(e.res.RolledBack))
	}
	return fmt.Sprintf("written before the failure: %s; rolled back: %s; %d stay (run the command again to see what is left)", listFiles(e.res.Created), listFiles(e.res.RolledBack), kept)
}

// ErrorData is the machine-readable part of the failure (--json).
func (e *applyFailure) ErrorData() map[string]any {
	nz := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	return map[string]any{"written": nz(e.res.Created), "rolled_back": nz(e.res.RolledBack), "overwritten": nz(e.res.Overwritten), "backups": nz(e.res.Backups)}
}

func pluralVerb(n int) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

func listFiles(files []string) string {
	if len(files) == 0 {
		return "none"
	}
	const most = 12
	shown := files
	if len(shown) > most {
		shown = shown[:most]
	}
	out := strings.Join(shown, ", ")
	if len(files) > most {
		out += fmt.Sprintf(" and %d more", len(files)-most)
	}
	return out
}

// finishInit prints the outcome, the next steps and the equivalent command.
func finishInit(c *clicore.Context, tgt *scaffold.Target, plan *scaffold.Plan, f *initFlags, p scaffold.Params, asked bool, data *initJSON, res *scaffold.Result, gitDone bool) error {
	if res != nil {
		data.Written = append(data.Written, res.Created...)
		data.Overwritten = append(data.Overwritten, res.Overwritten...)
		data.Backups = append(data.Backups, res.Backups...)
		data.Suggestions = append(data.Suggestions, res.Suggestions...)
		data.Refused = append(data.Refused, res.Refused...)
		for _, r := range res.Refused {
			data.Notes = append(data.Notes, r+" exists and was not written by ccshelf: it was not replaced (move it away and run the command again)")
		}
	}
	if c.Mode.JSON {
		return ui.WriteJSON(out(c), "catalog-init", *data)
	}
	w := out(c)
	switch {
	case f.dryRun:
		fmt.Fprintln(w, "dry run: nothing was written")
	case res == nil || (len(res.Created) == 0 && len(res.Overwritten) == 0 && len(res.Suggestions) == 0 && !gitDone):
		fmt.Fprintln(w, "nothing to do: everything is already set up (no file was changed)")
	default:
		fmt.Fprintf(w, "wrote %s", plural(len(res.Created)+len(res.Overwritten), "file", "files"))
		if n := len(res.Backups); n > 0 {
			fmt.Fprintf(w, ", saved %s as .bak", plural(n, "backup", "backups"))
		}
		if n := len(res.Suggestions); n > 0 {
			fmt.Fprintf(w, ", wrote %s", plural(n, "suggestion", "suggestions"))
		}
		if gitDone {
			fmt.Fprint(w, ", ran git init")
		}
		fmt.Fprintln(w)
	}
	changed := res != nil && (len(res.Created) > 0 || len(res.Overwritten) > 0 || len(res.Suggestions) > 0 || gitDone)
	if f.dryRun || changed {
		for _, t := range plan.Todos {
			fmt.Fprintf(w, "todo: %s\n", ui.SanitizeLine(t))
		}
	}
	if changed {
		fmt.Fprintln(w, "next steps:")
		for i, s := range nextSteps(tgt, plan, gitDone) {
			fmt.Fprintf(w, "  %d. %s\n", i+1, s)
		}
	}
	if asked {
		rec := equivalent(tgt, f, p)
		if err := rec.Print(errw(c), c.GOOS); err != nil {
			fmt.Fprintf(errw(c), "cannot print the equivalent command: %v\n", err)
		}
	}
	return nil
}

func nextSteps(tgt *scaffold.Target, plan *scaffold.Plan, gitDone bool) []string {
	cd := ""
	if tgt.Dir != "" {
		cd = "cd " + quoteForSteps(tgt.Dir) + ", then "
	}
	var steps []string
	if files := plan.MarkerFiles(); len(files) > 0 {
		steps = append(steps, "replace every "+scaffold.Placeholder+" in the files written: "+strings.Join(files, ", ")+
			" (ccshelf lint lists the ones in the marketplace descriptions and the catalog sidecars as CAT048 warnings; it does not read the workflows or the README, so search those yourself)")
	}
	if plan.NeedsPin {
		steps = append(steps, "pin the ccshelf action in "+strings.Join(plan.PinFiles, " and ")+": a full commit SHA and the release tag, then delete the guard job and its needs line (until then the guard job fails and the other jobs are skipped)")
	}
	steps = append(steps,
		cd+"run: ccshelf lint",
		"run: ccshelf compile --check  (it needs profiles/*.toml only once you add profiles)",
		"run: ccshelf catalog build --out dist/catalog")
	if !plan.HasGit && !gitDone {
		steps = append(steps, "create the repository: git init, then commit and push it to your Git host (this command never commits or pushes)")
	}
	steps = append(steps, "set up the repository rulesets: code-owner review on the default branch, protected v* tags, and the github-pages environment limited to the default branch")
	if plan.Count(scaffold.ActionMerge) > 0 {
		steps = append(steps, "merge the suggested lines into the files marked needs-merge")
	}
	return steps
}

func quoteForSteps(s string) string {
	if q, err := ui.QuotePOSIX(s); err == nil {
		return q
	}
	return "<the directory>"
}

// equivalent builds the flag command that repeats an interactive run.
func equivalent(tgt *scaffold.Target, f *initFlags, p scaffold.Params) *ui.Recorder {
	rec := ui.NewRecorder("catalog", "init", tgt.Dir)
	str := func(name, v string) {
		if v != "" {
			rec.Flag(name, v)
		}
	}
	str("--mode", string(p.Mode))
	str("--marketplace-name", p.MarketplaceName)
	str("--org", p.Org)
	str("--owner", p.Owner)
	for _, o := range p.PlatformOwners {
		rec.Flag("--platform-owners", o)
	}
	str("--ccshelf-ref", p.CcshelfRef)
	str("--ccshelf-version", p.CcshelfVersion)
	str("--runner-label", p.RunnerLabel)
	str("--default-branch", p.DefaultBranch)
	if f.sidecars == "none" {
		rec.Flag("--sidecars", "none")
	}
	if p.ExampleProfile {
		rec.Bool("--example-profile")
	}
	for _, sg := range skipGroups {
		if p.Skip[sg.group] && !(sg.group == scaffold.GroupSidecars && f.sidecars == "none") {
			rec.Bool("--" + sg.flag)
		}
	}
	for _, b := range []struct {
		on   bool
		name string
	}{{f.force, "--force"}, {f.writeSuggestions, "--write-suggestions"}, {f.gitInit, "--git-init"}} {
		if b.on {
			rec.Bool(b.name)
		}
	}
	if f.dryRun {
		rec.Bool("--dry-run")
	} else {
		rec.Bool("--yes")
	}
	return rec
}

var nonNameRe = regexp.MustCompile(`[^a-z0-9]+`)

// defaultMarketplaceName derives a marketplace name from the directory name.
func defaultMarketplaceName(dir string) string {
	n := nonNameRe.ReplaceAllString(strings.ToLower(filepath.Base(dir)), "-")
	n = strings.Trim(n, "-")
	if len(n) > 64 {
		n = strings.Trim(n[:64], "-")
	}
	if scaffold.ValidMarketplaceName(n) {
		return n
	}
	return ""
}

// guessOwners proposes platform owners from what is already there, read-only:
// the catch-all rule of an existing CODEOWNERS file, else @<org>/platform for
// the organization in the URL of the remote "origin". The user confirms or
// edits it, so a wrong guess costs nothing.
func guessOwners(ctx context.Context, tgt *scaffold.Target) string {
	if !tgt.Exists {
		return ""
	}
	if f, _, err := codeowners.Find(tgt.Dir); err == nil && f != nil {
		if owners := f.Owners("ccshelf-new-file.txt"); len(owners) > 0 {
			var ok []string
			for _, o := range owners {
				if scaffold.ValidOwner(o) {
					ok = append(ok, o)
				}
			}
			if len(ok) > 0 {
				return strings.Join(ok, ", ")
			}
		}
	}
	if org := remoteOrg(gitdata.RemoteURL(ctx, tgt.Dir)); org != "" {
		return "@" + org + "/platform"
	}
	return ""
}

var scpLikeRe = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:([A-Za-z0-9._-]+)/`)

// remoteOrg extracts the organization (the first path segment) from a git
// remote URL, or "" when it cannot be read as one.
func remoteOrg(remote string) string {
	var seg string
	if m := scpLikeRe.FindStringSubmatch(remote); m != nil {
		seg = m[1]
	} else if u, err := url.Parse(remote); err == nil && u.Host != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 {
			seg = parts[0]
		}
	}
	if seg == "" || !scaffold.ValidOwner("@"+seg) {
		return ""
	}
	return seg
}

// askMissing asks, in a terminal, for the values Build said are missing.
func askMissing(ctx context.Context, c *clicore.Context, tgt *scaffold.Target, p *scaffold.Params, me *scaffold.MissingError) error {
	hints := map[string]string{}
	for i, flag := range me.Flags {
		hints[flag] = ui.SanitizeLine(me.Hints[i])
	}
	// A fixed order, whatever order the plan found the gaps in; unknown flags last.
	order := []string{"--marketplace-name", "--platform-owners", "--owner"}
	for _, flag := range me.Flags {
		if flag != order[0] && flag != order[1] && flag != order[2] {
			order = append(order, flag)
		}
	}
	for _, flag := range order {
		hint, ok := hints[flag]
		if !ok {
			continue
		}
		switch flag {
		case "--marketplace-name":
			v, err := c.Prompt.Input(ctx, "Marketplace name ("+hint+")", defaultMarketplaceName(tgt.Dir), func(s string) error {
				if !scaffold.ValidMarketplaceName(s) {
					return fmt.Errorf("use lower case letters, digits and hyphens, at most 64 characters")
				}
				return nil
			})
			if err != nil {
				return err
			}
			p.MarketplaceName = strings.TrimSpace(v)
		case "--platform-owners", "--owner":
			def := guessOwners(ctx, tgt)
			if flag == "--owner" {
				def = ""
			}
			v, err := c.Prompt.Input(ctx, strings.TrimPrefix(flag, "--")+" ("+hint+"); several owners separated by commas", def, func(s string) error {
				return validOwnerList(s, flag == "--platform-owners")
			})
			if err != nil {
				return err
			}
			owners := splitOwners(v)
			if flag == "--owner" {
				p.Owner = owners[0]
			} else {
				p.PlatformOwners = owners
			}
		default:
			return ui.MissingFlags(hint, flag)
		}
	}
	return nil
}

func splitOwners(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

func validOwnerList(s string, many bool) error {
	owners := splitOwners(s)
	if len(owners) == 0 {
		return errors.New("give at least one owner")
	}
	if !many && len(owners) > 1 {
		return errors.New("give one owner")
	}
	for _, o := range owners {
		if !scaffold.ValidOwner(o) {
			return fmt.Errorf("%q is not @user, @org/team or an email address", ui.SanitizeLine(o))
		}
	}
	return nil
}

// askPin asks for the ccshelf action pin; empty answers leave the placeholder.
func askPin(ctx context.Context, c *clicore.Context, p *scaffold.Params) error {
	sha, err := c.Prompt.Input(ctx, "Full commit SHA of the ccshelf action to pin (empty to pin later; the workflows fail until you do)", "", func(s string) error {
		if s == "" || scaffold.ValidRef(s) {
			return nil
		}
		return errors.New("use a full 40-character lower-case commit SHA or a release tag such as v0.1.0")
	})
	if err != nil {
		return err
	}
	p.CcshelfRef = strings.TrimSpace(sha)
	if p.CcshelfRef == "" || scaffold.ValidVersion(p.CcshelfRef) {
		return nil
	}
	ver, err := c.Prompt.Input(ctx, "Release tag of that commit, such as v0.1.0 (empty to set it later)", "", func(s string) error {
		if s == "" || scaffold.ValidVersion(s) {
			return nil
		}
		return errors.New("use a release tag such as v0.1.0")
	})
	if err != nil {
		return err
	}
	p.CcshelfVersion = strings.TrimSpace(ver)
	return nil
}

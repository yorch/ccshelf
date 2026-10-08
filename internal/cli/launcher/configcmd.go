package launcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
)

func (l *launcher) configCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Show and change the configuration file",
		Long: `Show the effective configuration, and add, pin or remove profile sources and
change a few settings in place, without editing config.toml by hand. Every
subcommand works with flags alone. In a terminal, "ccshelf config" alone opens
a menu, and the command shows a change and asks you to confirm it (default no)
before it writes it.

The command writes a change by encoding the file again. Comments and layout
are lost, and the command keeps the previous file as config.toml.bak. When
there is no terminal, a change that weakens a security setting needs --yes.
These changes weaken a security setting:
  - turning off pinning
  - trusting project profiles
  - installing updates automatically
  - adding a source that tracks a branch, is not pinned or has a path that is
    a variable
  - changing claude.path or the update source
Nothing here fetches a source or records trust. Only "ccshelf init" creates
the file.

Settings you can change with "config set": ` + strings.Join(config.SettingKeys(), ", ") + `.
Anything else (accounts, claude.path, the update source) has its own command or
needs "ccshelf config edit".`,
		Example: `  ccshelf config show
  ccshelf config source add --git-url git@ghe.example.com:acme/claude-marketplace.git --ref v2026.10.1
  ccshelf config source pin 1 --ref v2026.11.0
  ccshelf config set update.mode notify
  ccshelf config edit`,
		Args: cobra.NoArgs,
	}
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, cmd *cobra.Command, _ []string) error {
		if !canPrompt(cc) {
			cmd.SetOut(cc.Streams.Err)
			_ = cmd.Help()
			return ui.Usage(errors.New("config needs a subcommand when there is no terminal (or with --no-interactive)"))
		}
		return l.configMenu(ctx, cc)
	})
	c.AddCommand(l.configPathCmd(), l.configShowCmd(), l.configSourceCmd(), l.configSetCmd(), l.configUnsetCmd(), l.configEditCmd())
	return c
}

func (l *launcher) configPathCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "path",
		Short: "Print the configuration file path in use",
		Long:  "Print the path of config.toml (the --config file when given). The file need not exist.",
		Args:  cobra.NoArgs,
	}
	c.RunE = l.do(func(_ context.Context, cc *clicore.Context, _ *cobra.Command, _ []string) error {
		path, err := configPath(cc)
		if err != nil {
			return err
		}
		_, statErr := os.Lstat(path)
		if cc.Mode.JSON {
			return ui.WriteJSON(cc.Streams.Out, "config-path", struct {
				Path   string `json:"path"`
				Exists bool   `json:"exists"`
			}{path, statErr == nil})
		}
		fmt.Fprintln(cc.Streams.Out, path)
		return nil
	})
	return c
}

// ---- show -----------------------------------------------------------------

type configSourceRow struct {
	Number int    `json:"number"`
	Type   string `json:"type"`
	Name   string `json:"name,omitempty"`
	URL    string `json:"url,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Branch string `json:"branch,omitempty"`
	// TrustedCommit is, for a branch source, the commit the trust lockfile
	// recorded last; a run uses it without network access.
	TrustedCommit string `json:"trusted_commit,omitempty"`
	Path          string `json:"path,omitempty"`
	Plugin        string `json:"plugin,omitempty"`
	Marketplace   string `json:"marketplace,omitempty"`
}

func sourceRows(cfg *config.Config) []configSourceRow {
	rows := make([]configSourceRow, 0, len(cfg.Sources))
	for i, s := range cfg.Sources {
		row := configSourceRow{Number: i + 1, Type: s.Type, Name: s.Name, URL: s.URL, Ref: s.Ref, Branch: s.Branch, Path: s.Path, Plugin: s.Plugin, Marketplace: s.Marketplace}
		if s.Type == config.SourceGit && s.Branch != "" {
			row.TrustedCommit = trustedBranchCommit(s)
		}
		rows = append(rows, row)
	}
	return rows
}

// trustedBranchCommit returns the commit the trust lockfile recorded last for a
// branch source, or "" (none, or the lockfile cannot be read).
func trustedBranchCommit(sc config.SourceConfig) string {
	s := &session{}
	if c := s.lockedCommits(sc); len(c) > 0 {
		return c[0]
	}
	return ""
}

// branchLabel describes what a branch source runs: "branch main @ 1a2b3c4".
func branchLabel(branch, commit string) string {
	if commit == "" {
		return "branch " + branch + " (no trusted commit yet)"
	}
	return "branch " + branch + " @ " + commit[:min(7, len(commit))]
}

type configDoc struct {
	Path           string            `json:"path"`
	Exists         bool              `json:"exists"`
	Sources        []configSourceRow `json:"sources"`
	Trust          configTrust       `json:"trust"`
	Update         configUpdate      `json:"update"`
	Catalog        configCatalog     `json:"catalog"`
	Accounts       []configAccount   `json:"accounts"`
	DefaultAccount string            `json:"default_account,omitempty"`
	Claude         configClaude      `json:"claude"`
	UI             configUI          `json:"ui"`
}

type configTrust struct {
	RequirePin           bool   `json:"require_pin"`
	OnChange             string `json:"on_change"`
	TrustProjectProfiles bool   `json:"trust_project_profiles"`
	BranchCheckInterval  string `json:"branch_check_interval"`
}

type configUpdate struct {
	Mode               string   `json:"mode"`
	Interval           string   `json:"interval"`
	BaseURL            string   `json:"base_url,omitempty"`
	CosignIdentityRepo string   `json:"cosign_identity_repo,omitempty"`
	AssetHosts         []string `json:"asset_hosts"`
}

type configCatalog struct {
	RemoteURL string `json:"remote_url,omitempty"`
}

type configAccount struct {
	Name      string `json:"name"`
	ConfigDir string `json:"config_dir"`
}

type configClaude struct {
	Path string `json:"path,omitempty"`
}

type configUI struct {
	Color       string `json:"color"`
	Interactive string `json:"interactive"`
}

func configData(path string, exists bool, cfg *config.Config) configDoc {
	d := configDoc{
		Path: path, Exists: exists, Sources: sourceRows(cfg),
		Trust: configTrust{cfg.Trust.RequirePin, cfg.Trust.OnChange, cfg.Trust.TrustProjectProfiles, config.FormatInterval(cfg.Trust.EffectiveBranchCheckInterval())},
		Update: configUpdate{
			Mode: cfg.Update.EffectiveMode(), Interval: config.FormatInterval(cfg.Update.EffectiveInterval()),
			BaseURL: cfg.Update.BaseURL, CosignIdentityRepo: cfg.Update.CosignIdentityRepo,
			AssetHosts: append([]string{}, cfg.Update.AssetHosts...),
		},
		Catalog:        configCatalog{cfg.Catalog.RemoteURL},
		Accounts:       []configAccount{},
		DefaultAccount: cfg.DefaultAccount,
		Claude:         configClaude{cfg.Claude.Path},
		UI:             configUI{cfg.UI.Color, cfg.UI.Interactive},
	}
	names := make([]string, 0, len(cfg.Accounts))
	for n := range cfg.Accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d.Accounts = append(d.Accounts, configAccount{n, cfg.Accounts[n].ConfigDir})
	}
	return d
}

// loadForShow loads the configuration for a read-only command: a missing file
// shows the defaults.
func loadForShow(cc *clicore.Context) (cfg *config.Config, path string, exists bool, err error) {
	path, err = configPath(cc)
	if err != nil {
		return nil, "", false, err
	}
	if _, serr := os.Stat(path); serr != nil {
		if errors.Is(serr, fs.ErrNotExist) {
			return config.Default(), path, false, nil
		}
		return nil, "", false, ui.Failure(fmt.Errorf("checking %s: %w", ui.SanitizeLine(path), serr))
	}
	cfg, err = config.Load(path)
	if err != nil {
		return nil, "", false, ui.Failure(withHint(err, "fix it with: ccshelf config edit"))
	}
	return cfg, path, true, nil
}

func (l *launcher) configShowCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration",
		Long: `Print the sources (numbered from 1, with the numbers that config source pin
and rm take), trust, update, catalog, accounts and ui settings. The command
marks a value that the file does not set with (default). A missing file shows
the defaults.`,
		Args: cobra.NoArgs,
	}
	c.RunE = l.do(func(_ context.Context, cc *clicore.Context, _ *cobra.Command, _ []string) error {
		cfg, path, exists, err := loadForShow(cc)
		if err != nil {
			return err
		}
		if cc.Mode.JSON {
			return ui.WriteJSON(cc.Streams.Out, "config", configData(path, exists, cfg))
		}
		printConfig(cc, path, exists, cfg)
		return nil
	})
	return c
}

func printConfig(cc *clicore.Context, path string, exists bool, cfg *config.Config) {
	w := cc.Streams.Out
	p := func(format string, a ...any) { fmt.Fprintf(w, format+"\n", a...) }
	s := ui.SanitizeLine
	def := func(v string, isDefault bool) string {
		if isDefault {
			return s(v) + " (default)"
		}
		return s(v)
	}
	p("Config file: %s", s(path))
	if !exists {
		p("  (the file does not exist, so the defaults are shown. Create it with: ccshelf init)")
	}
	p("Sources:")
	if len(cfg.Sources) == 0 {
		p("  (none configured. Add one with: ccshelf config source add)")
	}
	for i, src := range cfg.Sources {
		p("  %d. %s %s", i+1, src.Type, s(src.SourceLocation()))
		field := func(k, v string) {
			if v != "" {
				p("       %s: %s", k, s(v))
			}
		}
		switch src.Type {
		case config.SourceGit:
			field("ref", src.Ref)
			if src.Branch != "" {
				field("tracks", branchLabel(src.Branch, trustedBranchCommit(src)))
			}
			field("path", src.Path)
		case config.SourcePlugin:
			field("marketplace", src.Marketplace)
			field("path", src.Path)
		}
		field("name", src.Name)
	}
	for _, grp := range []struct {
		title string
		keys  []string
	}{
		{"Trust:", []string{"trust.require_pin", "trust.on_change", "trust.trust_project_profiles", "trust.branch_check_interval"}},
		{"Update:", []string{"update.mode", "update.interval"}},
		{"Catalog:", []string{"catalog.remote_url"}},
	} {
		p("%s", grp.title)
		for _, k := range grp.keys {
			v, set := cfg.GetSetting(k)
			name := k[strings.Index(k, ".")+1:]
			if v == "" {
				v = "(default)"
				p("  %s: %s", name, v)
				continue
			}
			p("  %s: %s", name, def(v, !set))
		}
	}
	for _, kv := range [][2]string{{"base_url", cfg.Update.BaseURL}, {"cosign_identity_repo", cfg.Update.CosignIdentityRepo}, {"asset_hosts", strings.Join(cfg.Update.AssetHosts, ", ")}} {
		if kv[1] != "" {
			p("  %s: %s", kv[0], s(kv[1]))
		}
	}
	p("Accounts:")
	if len(cfg.Accounts) == 0 {
		p("  (none)")
	}
	names := make([]string, 0, len(cfg.Accounts))
	for n := range cfg.Accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		mark := ""
		if n == cfg.DefaultAccount {
			mark = " (default account)"
		}
		p("  %s: %s%s", s(n), s(cfg.Accounts[n].ConfigDir), mark)
	}
	if cfg.DefaultAccount == "" {
		v, _ := cfg.GetSetting("default_account")
		_ = v
		p("  default_account: (default)")
	}
	if cfg.Claude.Path != "" {
		p("Claude:")
		p("  path: %s", s(cfg.Claude.Path))
	}
	p("UI:")
	for _, k := range []string{"ui.color", "ui.interactive"} {
		v, set := cfg.GetSetting(k)
		p("  %s: %s", k[strings.Index(k, ".")+1:], def(v, !set))
	}
}

// ---- source ---------------------------------------------------------------

func (l *launcher) configSourceCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "source",
		Short: "List, add, pin and remove profile sources",
		Long: `Change the [[sources]] of the configuration. Sources are numbered from 1 in the
order of "config source ls". The order carries no meaning. The command fetches
nothing and records no trust. ccshelf fetches shared profiles when you first
use them, and they need your trust then.`,
		Args: cobra.NoArgs,
	}
	c.AddCommand(l.configSourceLsCmd(), l.configSourceAddCmd(), l.configSourcePinCmd(), l.configSourceRmCmd())
	return c
}

func (l *launcher) configSourceLsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the configured profile sources",
		Long:    "List the [[sources]] with the numbers that config source pin and rm take. ccshelf always searches your personal profiles directory, and the list does not show it.",
		Args:    cobra.NoArgs,
	}
	c.RunE = l.do(func(_ context.Context, cc *clicore.Context, _ *cobra.Command, _ []string) error {
		cfg, _, _, err := loadForShow(cc)
		if err != nil {
			return err
		}
		rows := sourceRows(cfg)
		if cc.Mode.JSON {
			return ui.WriteJSON(cc.Streams.Out, "config-sources", rows)
		}
		if len(rows) == 0 {
			return ui.EmptyState(cc.Streams.Out, "no profile sources configured", "to add one: ccshelf config source add --git-url <url> --ref <tag>  (or --branch <name>)")
		}
		table := make([][]string, 0, len(rows))
		for i, r := range rows {
			ref := r.Ref
			if r.Branch != "" {
				ref = branchLabel(r.Branch, r.TrustedCommit)
			}
			table = append(table, []string{strconv.Itoa(r.Number), r.Type, cfg.Sources[i].SourceLocation(), ref, r.Path})
		}
		return ui.Table(cc.Streams.Out, []string{"#", "TYPE", "SOURCE", "REF", "PATH"}, table, cc.Mode)
	})
	return c
}

type sourceAddFlags struct {
	gitURL, ref, branch, path, dir, plugin, marketplace string
	yes                                                 bool
}

func (l *launcher) configSourceAddCmd() *cobra.Command {
	var f sourceAddFlags
	c := &cobra.Command{
		Use:   "add",
		Short: "Add a profile source (git, dir or plugin)",
		Long: `Add one profile source with exactly one of these flags:
  - --git-url, with --ref or --branch, and --path for the folder inside the
    repository
  - --dir, an absolute directory
  - --plugin (name@marketplace), with --marketplace and --path

The command checks the source with the rules of the configuration file and
refuses an exact duplicate. For a duplicate, use "config source pin" or
"config source rm". The command fetches and trusts nothing.

In a terminal, without any of those flags, the command asks the questions.

A source with --branch follows a branch, which can move. A run still uses the
commit you trusted, and every new commit needs your trust again. Adding one
weakens a setting and needs --yes without a terminal. The same applies to a git
source that is not pinned to a tag or full commit. That source is possible only
while trust.require_pin is off.`,
		Example: `  ccshelf config source add --git-url git@ghe.example.com:acme/claude-marketplace.git --ref v2026.10.1
  ccshelf config source add --git-url git@ghe.example.com:acme/claude-marketplace.git --branch main --yes
  ccshelf config source add --dir ~/team-profiles
  ccshelf config source add --plugin org-profiles@acme --marketplace acme/claude-marketplace`,
		Args: cobra.NoArgs,
	}
	fl := c.Flags()
	fl.StringVar(&f.gitURL, "git-url", "", "git source: repository URL")
	fl.StringVar(&f.ref, "ref", "", "git source: tag or full commit id to pin to")
	fl.StringVar(&f.branch, "branch", "", "git source: branch to track, instead of --ref")
	fl.StringVar(&f.path, "path", "", "git or plugin source: folder inside the repository or plugin (git default: profiles)")
	fl.StringVar(&f.dir, "dir", "", "dir source: absolute directory of profiles")
	fl.StringVar(&f.plugin, "plugin", "", "plugin source: name@marketplace")
	fl.StringVar(&f.marketplace, "marketplace", "", "plugin source: the marketplace source the plugin must come from (owner/repo or git URL)")
	fl.BoolVar(&f.yes, "yes", false, "confirm the write (and a weakening change), but never accept trust")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, cmd *cobra.Command, _ []string) error {
		return l.sourceAdd(ctx, cc, cmd, &f)
	})
	return c
}

func (l *launcher) sourceAdd(ctx context.Context, cc *clicore.Context, cmd *cobra.Command, f *sourceAddFlags) error {
	kinds := 0
	for _, v := range []string{f.gitURL, f.dir, f.plugin} {
		if v != "" {
			kinds++
		}
	}
	// Flags are checked before the file is read, so contradictory flags are a
	// usage error whether or not a configuration exists.
	var src config.SourceConfig
	var err error
	switch {
	case kinds > 1:
		return ui.Usage(errors.New("give exactly one of --git-url, --dir or --plugin"))
	case kinds == 1:
		if src, err = sourceFromFlags(cmd, f); err != nil {
			return err
		}
	case !canPrompt(cc):
		return ui.Usage(ui.MissingFlags("give exactly one of --git-url (with --ref or --branch), --dir or --plugin", "--git-url|--dir|--plugin"))
	}
	file, err := openConfigFile(cc)
	if err != nil {
		return err
	}
	cfg := file.clone()
	var rec *ui.Recorder
	if kinds == 0 {
		if src, rec, err = askSource(ctx, cc, cfg); err != nil {
			return err
		}
	}
	if err := cfg.ValidateSource(src); err != nil {
		return ui.Usage(withHint(fmt.Errorf("source: %s", strings.ReplaceAll(err.Error(), "sources[0]", fmt.Sprintf("source %d", len(cfg.Sources)+1))), "%s", sourceHint(src)))
	}
	for i, ex := range cfg.Sources {
		if config.SameSource(ex, src) {
			return ui.Usage(withHint(fmt.Errorf("source %d already is %s %s", i+1, ex.Type, ui.SanitizeLine(ex.SourceLocation())),
				"to change its ref or branch, use: ccshelf config source pin %d --ref <ref> or --branch <name>. To replace it: ccshelf config source rm %d. To add another folder of the same repository, give a different --path", i+1, i+1))
		}
	}
	cfg.Sources = append(cfg.Sources, src)
	return l.commitConfig(ctx, cc, &writePlan{
		file: file, next: cfg, yes: f.yes, rec: rec,
		note: "Nothing is fetched or trusted here. ccshelf fetches profiles from a shared source when you first use them, and they need your trust then.",
	})
}

func sourceHint(src config.SourceConfig) string {
	switch src.Type {
	case config.SourceGit:
		return "a git source needs a remote URL (https://, ssh:// or git@host:path) and --ref (a tag or full commit id) or --branch. Use --dir for a local folder"
	case config.SourceDir:
		return "a dir source is an absolute path, or one starting with ~"
	}
	return "a plugin source is name@marketplace, and --marketplace is owner/repo or a git URL"
}

func sourceFromFlags(cmd *cobra.Command, f *sourceAddFlags) (config.SourceConfig, error) {
	changed := func(n string) bool { return cmd.Flags().Changed(n) }
	switch {
	case f.gitURL != "":
		for _, bad := range []string{"marketplace"} {
			if changed(bad) {
				return config.SourceConfig{}, ui.Usage(fmt.Errorf("--%s goes with --plugin, not --git-url", bad))
			}
		}
		if err := config.ValidateGitURL(f.gitURL); err != nil {
			return config.SourceConfig{}, ui.Usage(withHint(fmt.Errorf("--git-url: %w", err), "%s", sourceHint(config.SourceConfig{Type: config.SourceGit})))
		}
		if f.ref != "" && f.branch != "" {
			return config.SourceConfig{}, ui.Usage(errors.New("give --ref or --branch, not both"))
		}
		p := f.path
		if !changed("path") {
			p = "profiles"
		}
		return config.SourceConfig{Type: config.SourceGit, URL: f.gitURL, Ref: f.ref, Branch: f.branch, Path: p}, nil
	case f.dir != "":
		if changed("ref") || changed("branch") || changed("path") || changed("marketplace") {
			return config.SourceConfig{}, ui.Usage(errors.New("--dir takes no --ref, --branch, --path or --marketplace"))
		}
		return config.SourceConfig{Type: config.SourceDir, Path: f.dir}, nil
	}
	if changed("ref") || changed("branch") {
		return config.SourceConfig{}, ui.Usage(errors.New("--ref and --branch go with --git-url, not --plugin"))
	}
	if !validPluginID(f.plugin) {
		return config.SourceConfig{}, ui.Usage(withHint(fmt.Errorf("--plugin %q must be name@marketplace", ui.SanitizeLine(f.plugin)), "%s", sourceHint(config.SourceConfig{Type: config.SourcePlugin})))
	}
	return config.SourceConfig{Type: config.SourcePlugin, Plugin: f.plugin, Marketplace: f.marketplace, Path: f.path}, nil
}

// parseSourceNumber turns the argument of pin and rm into an index.
func parseSourceNumber(cfg *config.Config, arg string) (int, error) {
	n, err := strconv.Atoi(arg)
	if err != nil {
		return 0, ui.Usage(withHint(fmt.Errorf("%q is not a source number", ui.SanitizeLine(arg)), "run: ccshelf config source ls"))
	}
	if n < 1 || n > len(cfg.Sources) {
		return 0, ui.Usage(withHint(fmt.Errorf("there is no source number %d (there are %d)", n, len(cfg.Sources)), "run: ccshelf config source ls"))
	}
	return n - 1, nil
}

func (l *launcher) configSourcePinCmd() *cobra.Command {
	var ref, branch string
	var yes bool
	c := &cobra.Command{
		Use:   "pin [n]",
		Short: "Change the pinned ref or the tracked branch of git source number n",
		Long: `Change the tag or full commit id that git source number n (see config source ls)
is pinned to, or make it track a branch with --branch. Give --ref or --branch,
not both. A source can switch between a tag or commit and a branch. A branch can
move, so a switch to a branch weakens a setting and needs --yes without a
terminal. The next run that needs a profile from that source fetches the new
ref. When its commit differs from the one you trusted, the profile needs your
trust again. This command never records trust. Pinning to something that is not
a tag or full commit is possible only while trust.require_pin is off, and it
needs --yes without a terminal.`,
		Example: "  ccshelf config source pin 1 --ref v2026.11.0\n  ccshelf config source pin 1 --branch main --yes",
		Args:    cobra.MaximumNArgs(1),
	}
	c.Flags().StringVar(&ref, "ref", "", "the new tag or full commit id")
	c.Flags().StringVar(&branch, "branch", "", "the branch to track, instead of --ref")
	c.Flags().BoolVar(&yes, "yes", false, "confirm the write (and a weakening change), but never accept trust")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		return l.sourcePin(ctx, cc, args, ref, branch, yes)
	})
	return c
}

// sourcePin is "config source pin"; without an argument it asks.
func (l *launcher) sourcePin(ctx context.Context, cc *clicore.Context, args []string, ref, branch string, yes bool) error {
	if ref != "" && branch != "" {
		return ui.Usage(errors.New("give --ref or --branch, not both"))
	}
	file, err := openConfigFile(cc)
	if err != nil {
		return err
	}
	cfg := file.clone()
	asked := false
	var idx int
	if len(args) == 1 {
		if idx, err = parseSourceNumber(cfg, args[0]); err != nil {
			return err
		}
	} else {
		if !canPrompt(cc) {
			return ui.Usage(withHint(errors.New("missing argument <n>"), "run: ccshelf config source pin <n> --ref <ref>  (or --branch <name>; ccshelf config source ls lists the numbers)"))
		}
		if idx, err = pickSource(ctx, cc, cfg, "Pin which source?", config.SourceGit); err != nil {
			return err
		}
		asked = true
	}
	src := &cfg.Sources[idx]
	if src.Type != config.SourceGit {
		return ui.Usage(withHint(fmt.Errorf("source %d is a %s source, and only git sources have a ref or a branch", idx+1, src.Type), "run: ccshelf config source ls"))
	}
	if ref == "" && branch == "" {
		if !canPrompt(cc) {
			return ui.Usage(ui.MissingFlags("the tag or full commit id to pin to, or the branch to track", "--ref|--branch"))
		}
		current := src.Ref
		if src.Branch != "" {
			current = config.BranchRefPrefix + src.Branch
		}
		answer, err := cc.Prompt.Input(ctx, pinPrompt("New tag or full commit id"), current, validatePinOrBranch(refValidator(cfg)))
		if err != nil {
			return err
		}
		ref, branch = splitPinInput(answer)
		asked = true
	}
	if branch != "" {
		if err := config.ValidateBranch(branch); err != nil {
			return ui.Usage(fmt.Errorf("--branch: %w", err))
		}
		src.Ref, src.Branch = "", branch
	} else {
		if cfg.Trust.RequirePin {
			if err := config.ValidatePin(ref); err != nil {
				return ui.Usage(fmt.Errorf("--ref: %w", err))
			}
		}
		src.Ref, src.Branch = ref, ""
	}
	var rec *ui.Recorder
	if asked {
		rec = ui.NewRecorder("config", "source", "pin", strconv.Itoa(idx+1))
		if branch != "" {
			rec.Flag("--branch", branch)
		} else {
			rec.Flag("--ref", ref)
		}
		rec.Bool("--yes")
	}
	return l.commitConfig(ctx, cc, &writePlan{
		file: file, next: cfg, yes: yes, rec: rec,
		note: "Nothing is fetched or trusted here. If the new ref or branch resolves to a different commit, profiles from this source need your trust again on the next run.",
	})
}

func refValidator(cfg *config.Config) func(string) error {
	return func(s string) error {
		if cfg.Trust.RequirePin {
			return config.ValidatePin(s)
		}
		if s == "" {
			return errors.New("a ref is required")
		}
		return nil
	}
}

func (l *launcher) configSourceRmCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:     "rm [n]",
		Aliases: []string{"remove"},
		Short:   "Remove source number n",
		Long: `Remove the source with number n (see config source ls). Profiles from it no
longer appear. The command keeps the trust records already made for its
profiles as they are, and does not delete them.`,
		Example: "  ccshelf config source rm 2",
		Args:    cobra.MaximumNArgs(1),
	}
	c.Flags().BoolVar(&yes, "yes", false, "confirm the write, but never accept trust")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		return l.sourceRm(ctx, cc, args, yes)
	})
	return c
}

// sourceRm is "config source rm"; without an argument it asks.
func (l *launcher) sourceRm(ctx context.Context, cc *clicore.Context, args []string, yes bool) error {
	file, err := openConfigFile(cc)
	if err != nil {
		return err
	}
	cfg := file.clone()
	var rec *ui.Recorder
	var idx int
	if len(args) == 1 {
		if idx, err = parseSourceNumber(cfg, args[0]); err != nil {
			return err
		}
	} else {
		if !canPrompt(cc) {
			return ui.Usage(withHint(errors.New("missing argument <n>"), "run: ccshelf config source rm <n>  (ccshelf config source ls lists the numbers)"))
		}
		if idx, err = pickSource(ctx, cc, cfg, "Remove which source?", ""); err != nil {
			return err
		}
		rec = ui.NewRecorder("config", "source", "rm", strconv.Itoa(idx+1))
		rec.Bool("--yes")
	}
	cfg.Sources = append(cfg.Sources[:idx:idx], cfg.Sources[idx+1:]...)
	return l.commitConfig(ctx, cc, &writePlan{file: file, next: cfg, yes: yes, rec: rec})
}

// ---- set and unset --------------------------------------------------------

// refuseKey explains where a key that "config set" does not accept is changed.
func refuseKey(key string) error {
	hint := "change it with: ccshelf config edit"
	switch {
	case key == "accounts" || strings.HasPrefix(key, "accounts."):
		hint = "accounts have their own commands: ccshelf account add, ls and rm"
	case key == "sources" || strings.HasPrefix(key, "sources"):
		hint = "sources have their own commands: ccshelf config source add, pin and rm"
	}
	return ui.Usage(withHint(fmt.Errorf("%s cannot be changed with config set (the settings you can change are: %s)", ui.SanitizeLine(key), strings.Join(config.SettingKeys(), ", ")), "%s", hint))
}

func (l *launcher) configSetCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "set [key] [value]",
		Short: "Change one setting",
		Long: `Change one setting of the configuration. The keys are: ` + strings.Join(config.SettingKeys(), ", ") + `.
Values:
  - true or false
  - trust.on_change: prompt or fail
  - update.mode: off, notify or install
  - update.interval: a duration such as 24h (1h to one year)
  - trust.branch_check_interval: a duration such as 24h (1h to one year)
  - catalog.remote_url: an https URL
  - default_account: an account name
  - ui.color: auto, always or never
  - ui.interactive: auto or never
The command refuses anything else. Accounts and sources have their own
commands, and the rest needs "config edit".

Turning trust.require_pin off, trust.trust_project_profiles on, or update.mode to
install weakens a security setting and needs --yes without a terminal.`,
		Example: `  ccshelf config set update.mode notify
  ccshelf config set update.interval 48h
  ccshelf config set trust.on_change fail`,
		Args: cobra.MaximumNArgs(2),
	}
	c.Flags().BoolVar(&yes, "yes", false, "confirm the write (and a weakening change), but never accept trust")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		return l.setOrUnset(ctx, cc, args, yes, false)
	})
	return c
}

func (l *launcher) configUnsetCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "unset [key]",
		Short: "Put one setting back to its default",
		Long: `Put one setting back to its default (the same keys as config set). The
command removes the key from the file where it can. Otherwise it writes the key
with its default.`,
		Example: "  ccshelf config unset update.interval",
		Args:    cobra.MaximumNArgs(1),
	}
	c.Flags().BoolVar(&yes, "yes", false, "confirm the write, but never accept trust")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		return l.setOrUnset(ctx, cc, args, yes, true)
	})
	return c
}

func (l *launcher) setOrUnset(ctx context.Context, cc *clicore.Context, args []string, yes, unset bool) error {
	var key, value string
	if len(args) > 0 {
		key = args[0]
	}
	if len(args) > 1 {
		value = args[1]
	}
	if key != "" {
		if _, ok := config.LookupSetting(key); !ok {
			return refuseKey(key)
		}
	}
	file, err := openConfigFile(cc)
	if err != nil {
		return err
	}
	cfg := file.clone()
	var rec *ui.Recorder
	if key == "" || (!unset && len(args) < 2) {
		if !canPrompt(cc) {
			usage := "run: ccshelf config set <key> <value>"
			if unset {
				usage = "run: ccshelf config unset <key>"
			}
			return ui.Usage(withHint(errors.New("missing argument"), "%s  (keys: %s)", usage, strings.Join(config.SettingKeys(), ", ")))
		}
		if key == "" {
			if key, err = pickSetting(ctx, cc, cfg); err != nil {
				return err
			}
		}
		if !unset && len(args) < 2 {
			var clear bool
			if value, clear, err = askSettingValue(ctx, cc, cfg, key); err != nil {
				return err
			}
			unset = clear
		}
		rec = settingRecorder(key, value, unset)
		rec.Bool("--yes")
	}
	if unset {
		err = cfg.UnsetSetting(key)
	} else {
		err = cfg.SetSetting(key, value)
	}
	if err != nil {
		return ui.Usage(err)
	}
	return l.commitConfig(ctx, cc, &writePlan{file: file, next: cfg, yes: yes, rec: rec})
}

func settingRecorder(key, value string, unset bool) *ui.Recorder {
	if unset {
		return ui.NewRecorder("config", "unset", key)
	}
	return ui.NewRecorder("config", "set", key, value)
}

// ---- edit -----------------------------------------------------------------

func (l *launcher) configEditCmd() *cobra.Command {
	var printPath, yes bool
	c := &cobra.Command{
		Use:   "edit",
		Short: "Edit config.toml in $VISUAL or $EDITOR, checked before it replaces the file",
		Long: `Open a copy of config.toml (mode 0600, in the same folder) in the editor named by
$VISUAL or $EDITOR (split on spaces, quotes group words, never run through a
shell). When the editor exits, the command checks the copy like the real file.
The copy replaces config.toml only if it is valid. Comments are kept, because
the command writes the text as you saved it. It keeps the previous file as
config.toml.bak. If the copy is invalid, the command prints the errors with
line numbers, does not touch config.toml and keeps the copy so you can fix it.
The command needs a terminal and an existing file (ccshelf init creates it).

--path prints the file name instead, for scripts and for editors you start
yourself.`,
		Args: cobra.NoArgs,
	}
	c.Flags().BoolVar(&printPath, "path", false, "print the configuration file path instead of opening it")
	c.Flags().BoolVar(&yes, "yes", false, "confirm the write after the check, but never accept trust")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, _ []string) error {
		return l.editConfig(ctx, cc, printPath, yes, nil)
	})
	return c
}

// editConfig is "config edit". The file is checked as a missing-file case
// first so that --path and the editor agree.
func (l *launcher) editConfig(ctx context.Context, cc *clicore.Context, printPath, yes bool, rec *ui.Recorder) error {
	path, err := configPath(cc)
	if err != nil {
		return err
	}
	if _, serr := os.Lstat(path); serr != nil && errors.Is(serr, fs.ErrNotExist) {
		return ui.Failure(withHint(fmt.Errorf("there is no configuration file at %s", ui.SanitizeLine(path)), "create it first with: ccshelf init"))
	}
	if printPath {
		fmt.Fprintln(cc.Streams.Out, path)
		return nil
	}
	if !canPrompt(cc) {
		return ui.Usage(withHint(errors.New("edit opens an editor and needs a terminal, but none is available"),
			"use: ccshelf config edit --path, and open the printed file yourself"))
	}
	// The original may be invalid: this is also how it is repaired.
	fi, err := os.Lstat(path)
	switch {
	case err != nil:
		return ui.Failure(err)
	case fi.Mode()&fs.ModeSymlink != 0:
		return ui.Failure(withHint(fmt.Errorf("%s is a symbolic link", ui.SanitizeLine(path)), "ccshelf never replaces a link. Point --config at the real file"))
	case !fi.Mode().IsRegular():
		return ui.Failure(fmt.Errorf("%s is not a regular file", ui.SanitizeLine(path)))
	}
	raw, err := config.ReadFile(path)
	if err != nil {
		return ui.Failure(err)
	}
	file := &cfgFile{path: path, raw: raw}
	if cfg, err := config.ParseUnexpanded(raw, path); err == nil {
		file.cfg = cfg
	} else {
		warnf(cc, "the current file is invalid: %v", err)
		file.cfg = config.Default()
	}
	copyPath, err := config.CreateExclusive(path, raw)
	if err != nil {
		return ui.Failure(err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(copyPath)
		}
	}()
	if err := l.runEditor(ctx, cc, copyPath, "ccshelf config edit --path"); err != nil {
		// A usage error (no editor) leaves nothing worth keeping.
		if ui.CodeOf(err) != ui.ExitUsage {
			keep = true
			warnf(cc, "your edit is kept in %s", ui.SanitizeLine(copyPath))
		}
		return err
	}
	edited, err := config.ReadFile(copyPath)
	if err != nil {
		keep = true
		return ui.Failure(err)
	}
	if _, err := config.ParseUnexpanded(edited, copyPath); err != nil {
		keep = true
		return ui.Failure(withHint(fmt.Errorf("the edited configuration is invalid, so %s was not changed:\n%s", ui.SanitizeLine(path), ui.Sanitize(err.Error())),
			"your edit is kept in %s. A new ccshelf config edit starts from the current file, so copy your fix over from it", ui.SanitizeLine(copyPath)))
	}
	err = l.commitConfig(ctx, cc, &writePlan{file: file, nextRaw: edited, yes: yes, rec: rec})
	if err != nil {
		keep = true
		warnf(cc, "your edit is kept in %s", ui.SanitizeLine(copyPath))
	}
	return err
}

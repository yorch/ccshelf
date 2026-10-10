package launcher

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cache"
	"github.com/yorch/ccshelf/internal/claude"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/policy"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/settings"
	"github.com/yorch/ccshelf/internal/trust"
	"github.com/yorch/ccshelf/internal/ui"
)

// Names used for the instructions directory of a launch.
const (
	instructionsPrefix = "instructions"
	instructionsFile   = "CLAUDE.md"
	// additionalDirsClaudeMDEnv makes Claude Code load CLAUDE.md files from
	// the directories given with --add-dir.
	additionalDirsClaudeMDEnv = "CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD"
)

// launch is everything needed to start claude, produced by the run pipeline.
type launch struct {
	Bin  string
	Args []string
	// EnvAdd are the environment variables added or replaced for the child.
	EnvAdd map[string]string
	// Env is the complete child environment.
	Env []string

	// PassFrom is the index in Args where the arguments the user passed
	// through to claude start.
	PassFrom int

	Settings, MCPConfig, PromptFile string
	// InstructionsDir is the cache directory that holds the CLAUDE.md built
	// from the profile's instructions, or "" when the profile has none.
	InstructionsDir string
	Account         string
	Warnings        []string
	Resolved        *profile.Resolved
}

func (l *launcher) runCmd() *cobra.Command {
	var yes, refresh bool
	c := &cobra.Command{
		Use:   "run [profile] [-- claude args]",
		Short: "Start claude with a profile",
		Long: `Start claude with a profile: the plugins, skills and MCP servers it names.

ccshelf passes the arguments after the profile name (and after -- when no
profile is given) to claude unchanged. Thus flags of ccshelf itself must come
before the profile name: ccshelf run --account work sre --resume.

Without a profile name, a terminal gets a picker. Anything else exits with
code 2. You must trust a profile from a shared source first (exit code 4
otherwise). --yes never accepts trust. Only an interactive confirmation of the
printed closure or "ccshelf trust <profile> --accept <closure-hash>" accepts
it.

A git source that tracks a branch runs the commit you trusted, with no network.
At most once per trust.branch_check_interval (default 24h), ccshelf asks the
remote for the head of the branch. If the head moved, a terminal shows what
changed and asks. Without a terminal, or with --yes, ccshelf keeps the trusted
commit and prints the command that reviews the update. --refresh checks now.
If another profile of the same source trusted a later commit of the branch,
ccshelf offers that commit to this profile at once, with no network call and
whatever the interval says. It offers only a commit that it can prove is
later, and it runs only a commit that this profile trusts.`,
		Example: `  ccshelf run sre
  ccshelf run sre -- -p "summarize this repo"
  ccshelf run --account personal sre --resume`,
		DisableFlagsInUseLine: true,
	}
	c.Flags().SetInterspersed(false)
	c.Flags().BoolVar(&yes, "yes", false, "answer yes to confirmations other than trust (trust is never auto-accepted)")
	c.Flags().BoolVar(&refresh, "refresh", false, "check tracked branches for a new commit now, not only once per trust.branch_check_interval")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, cmd *cobra.Command, args []string) error {
		name, pass, err := splitRunArgs(cmd, args)
		if err != nil {
			return err
		}
		return l.runProfile(ctx, cc, name, pass, yes, false, refresh)
	})
	return c
}

func (l *launcher) dryRunCmd() *cobra.Command {
	var refresh bool
	c := &cobra.Command{
		Use:   "dry-run [profile] [-- claude args]",
		Short: "Print the exact claude command a run would execute",
		Long: `Run the whole pipeline of "run" (including the trust check) and print the exact
claude command instead of starting it. The command writes the generated
settings, MCP config, prompt and instructions files to the private cache, so the printed
command is valid. It never prints environment values from profiles.`,
		DisableFlagsInUseLine: true,
	}
	c.Flags().SetInterspersed(false)
	c.Flags().BoolVar(&refresh, "refresh", false, "check tracked branches for a new commit now, not only once per trust.branch_check_interval")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, cmd *cobra.Command, args []string) error {
		name, pass, err := splitRunArgs(cmd, args)
		if err != nil {
			return err
		}
		return l.runProfile(ctx, cc, name, pass, false, true, refresh)
	})
	return c
}

// runProfile is the run and dry-run command body.
func (l *launcher) runProfile(ctx context.Context, cc *clicore.Context, name string, pass []string, yes, dry, refresh bool) error {
	command := "run"
	if dry {
		command = "dry-run"
	}
	s, err := l.openWith(ctx, cc, openOpts{prepare: true, needClaude: true, refreshBranches: refresh})
	if err != nil {
		return err
	}
	name, picked, err := pickProfile(ctx, cc, name, command, func() ([]ui.Option, error) { return s.profileOptions() })
	if err != nil {
		return err
	}
	if !refresh {
		// An explicit refresh already checked the heads and goes through the
		// ordinary trust check below (on_change = "fail" included).
		s = l.alignBranchCommits(ctx, cc, s, name)
		s = l.checkBranches(ctx, cc, s, name, yes)
	}
	ln, err := s.buildLaunch(ctx, name, pass, yes)
	if err != nil {
		return err
	}
	if picked {
		// Built by hand: global flags must come before the profile name, and
		// the Recorder only supports flags after positional arguments.
		eq := []string{command}
		if cc.G.ConfigPath != "" {
			eq = append(eq, "--config", cc.G.ConfigPath)
		}
		if cc.G.ClaudePath != "" {
			eq = append(eq, "--claude", cc.G.ClaudePath)
		}
		if cc.G.Account != "" {
			eq = append(eq, "--account", cc.G.Account)
		}
		if refresh {
			eq = append(eq, "--refresh")
		}
		eq = append(eq, name)
		if len(pass) > 0 {
			eq = append(eq, "--")
			eq = append(eq, redactPass(pass)...)
		}
		if err := ui.Equivalent(cc.Streams.Err, cc.GOOS, eq); err != nil {
			warnf(cc, "cannot print the equivalent command: %v", err)
		}
	}
	if dry {
		return printDryRun(cc, ln)
	}
	if err := ctx.Err(); err != nil {
		// Ctrl+C while the pipeline ran: do not start claude.
		return fmt.Errorf("interrupted before starting claude: %w", err)
	}
	// Give claude the signal dispositions plain claude would have had.
	releaseSignals()
	code, err := l.opt.Start(ln.Bin, ln.Args, ln.Env)
	if err != nil {
		return ui.Failure(fmt.Errorf("starting claude: %w", err))
	}
	if code != 0 {
		// The child's own exit code is propagated; there is nothing to report.
		return &ui.ExitError{Code: code}
	}
	return nil
}

// profileOptions lists the profiles for the picker, leaving out invalid and
// conflicting ones.
func (s *session) profileOptions() ([]ui.Option, error) {
	list, err := profile.List(s.sources)
	if err != nil {
		return nil, ui.Failure(err)
	}
	var opts []ui.Option
	for _, p := range list {
		if p.Err != "" || len(p.Conflict) > 0 {
			continue
		}
		detail := ui.Sanitize(p.Description)
		if p.Status == profile.StatusDeprecated {
			detail = "(deprecated) " + detail
		}
		opts = append(opts, ui.Option{Label: ui.Sanitize(p.Name), Detail: detail, Value: p.Name})
	}
	return opts, nil
}

// buildLaunch runs steps 2 to 10 of the pipeline (see the package comment)
// after the sources are prepared, and returns what to execute. It prints
// warnings to the error stream.
func (s *session) buildLaunch(ctx context.Context, name string, pass []string, yes bool) (*launch, error) {
	cc := s.cc

	// Resolve the profile closure.
	r, err := s.resolve(name)
	if err != nil {
		return nil, err
	}
	m := r.Merged.WithDefaults()

	// The profile's own account applies only now that it is known. A plugin
	// source was prepared under the earlier choice, so it must not change.
	choice, extra, err := accountEnv(cc, s.cfg, m.Account)
	if err != nil {
		return nil, err
	}
	if s.hasPluginSource && (choice.Name != s.choice.Name || choice.SetEnv != s.choice.SetEnv) {
		return nil, ui.Usage(withHint(
			fmt.Errorf("profile %q selects account %q, but a plugin profile source was already read under another account", r.Name, choice.Name),
			"pass --account %s explicitly", choice.Name))
	}
	s.choice = choice
	s.env = claude.Env(cc.Environ(), extra)
	printAccountNotes(cc, choice)

	// What was raised while the sources were prepared (an unavailable source,
	// a cached checkout used offline) is part of the structured warnings; it was
	// already printed once.
	warnings := append([]string{}, s.warnings...)
	warn := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		for _, w := range warnings {
			if w == msg {
				return
			}
		}
		warnings = append(warnings, msg)
		warnf(cc, "%s", msg)
	}
	for _, w := range warningsForGOOS(cc.GOOS, r.Warnings) {
		warn("%s", w)
	}
	if m.Status == profile.StatusDeprecated {
		msg := fmt.Sprintf("profile %s is deprecated", r.Name)
		if m.SupersededBy != "" {
			msg += ": use " + m.SupersededBy
		}
		warn("%s", msg)
	}

	// Trust check: fail closed.
	if err := s.settleTrust(ctx, r); err != nil {
		return nil, err
	}

	// Installed plugins, listed in the working directory.
	bin, err := s.locate()
	if err != nil {
		return nil, ui.Failure(err)
	}
	cdir, err := cache.Dir()
	if err != nil {
		return nil, ui.Failure(fmt.Errorf("cache directory: %w", err))
	}
	ic := &claude.InstalledCache{Dir: cdir, Now: cc.Now}
	installed, _, err := ic.List(ctx, bin, s.cwd, s.env)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, fmt.Errorf("listing installed plugins: %w", cerr)
		}
		return nil, ui.Failure(withHint(fmt.Errorf("listing installed plugins: %w", err),
			"claude plugin list --json must work in this directory (and under the chosen account)"))
	}

	// Policy capability matrix.
	pol, err := policy.Detect(ctx, s.l.opt.Policy)
	if err != nil {
		return nil, fmt.Errorf("reading managed policy: %w", err)
	}
	matrix := policy.Evaluate(pol, installed)
	strict := m.MCP.Strict != nil && *m.MCP.Strict
	hide := m.MCP.ClaudeAIConnectors == profile.ConnectorsNone

	// mcp.strict: --strict-mcp-config removes every server but the profile's,
	// protected ones included, and managed policy can block the flag. Where the
	// flag cannot be used, deniedMcpServers names the known plugin servers
	// instead (protected ones excluded), and the profile degrades with a note.
	strictFlag, denyRoute, strictWhy := strict, false, ""
	if strict {
		if gone := missingFrom(s.protectedMCP, r.MCP); gone != "" {
			strictFlag, denyRoute = false, true
			strictWhy = fmt.Sprintf("it would also remove the protected MCP server %s", ui.SanitizeLine(gone))
		} else if f := matrix.Features[policy.StrictMCPConfig]; f.State == policy.Blocked && m.Policy.OnBlocked != profile.OnBlockedFail {
			strictFlag, denyRoute = false, true
			strictWhy = "managed policy blocks it: " + f.Reason
		}
	}
	needs := policy.Needs{
		ExtraMCPServers:        strictFlag || len(r.MCP) > 0,
		StrictMCPConfig:        strictFlag,
		DropUserSettingSources: !m.InheritsUserSettings(),
		AppendSystemPromptFile: len(r.Prompt) > 0,
		HideConnectors:         hide,
		DenyMCPServers:         denyRoute,
	}
	applied, err := matrix.Plan(needs, m.Policy.OnBlocked)
	if err != nil {
		var be *policy.BlockedError
		if errors.As(err, &be) {
			return nil, ui.Policy(withHint(err, "the profile has policy.on_blocked = \"fail\". Set it to \"warn\" to drop the blocked part instead"))
		}
		return nil, ui.Failure(err)
	}
	for _, w := range matrix.Warnings {
		warn("policy: %s", w)
	}
	// The resolver already warned that inherit_user_settings = false drops the
	// user layer; applied.Warnings adds only what policy changes about that.
	for _, w := range applied.Warnings {
		warn("%s", w)
	}
	if denyRoute {
		warn("mcp.strict: --strict-mcp-config is not used (%s). ccshelf denies the known plugin MCP servers outside the profile instead. MCP servers from your own settings or project files stay available", strictWhy)
	}

	// Settings: build, validate, write, re-read, validate again.
	spec := settings.Spec{
		Installed:      installed,
		Mode:           m.Plugins.Mode,
		Include:        m.Plugins.Include,
		Exclude:        m.Plugins.Exclude,
		Protected:      s.protectedPlugins,
		PolicyLocked:   matrix.LockedPlugins(),
		ProtectedMCP:   s.protectedMCP,
		OffSkills:      m.Skills.Off,
		NameOnlySkills: m.Skills.NameOnly,
		HideConnectors: hide && applied.HideConnectors,
		Model:          m.Session.Model,
		OutputStyle:    m.Session.OutputStyle,
		Env:            m.Session.Env,
		Profile:        r.Name,

		UserLayerDropped: applied.DropUserSettingSources,
	}
	if denyRoute && applied.DenyMCPServers {
		spec.DenyMCP = deniableMCP(installed, m.Plugins.Include, s.protectedPlugins, matrix.LockedPlugins(), s.protectedMCP)
	}
	res, err := settings.Build(spec)
	if err != nil {
		return nil, ui.Failure(fmt.Errorf("building settings: %w", err))
	}
	notInstalled := map[string]bool{}
	for _, id := range res.Missing {
		notInstalled[fmt.Sprintf("plugin %s is not installed and was not written", id)] = true
	}
	for _, w := range res.Warnings {
		if !notInstalled[w] { // reported once, with the install hint, below
			warn("%s", w)
		}
	}
	if len(res.Missing) > 0 {
		for _, id := range res.Missing {
			if validPluginID(id) {
				warn("plugin %s is in the profile but not installed. Install it inside Claude Code with: /plugin install %s", id, id)
			} else {
				warn("plugin %q is in the profile but not installed (and is not a valid plugin id)", ui.SanitizeLine(id))
			}
		}
		if canPrompt(cc) && !yes {
			ok, err := cc.Prompt.Confirm(ctx, "Some plugins of the profile are not installed. Start anyway?", true)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, ui.Failure(errors.New("not started: plugins of the profile are not installed"))
			}
		}
	}
	for _, id := range res.Protected {
		warn("plugin %s is protected and stays enabled", id)
	}
	if len(res.Locked) > 0 {
		warn("plugins required by policy stay enabled and cannot be masked: %s", strings.Join(res.Locked, ", "))
	}
	raw, err := res.JSON()
	if err != nil {
		return nil, ui.Failure(fmt.Errorf("settings JSON: %w", err))
	}
	if err := s.l.validateSettings(raw); err != nil {
		return nil, ui.Failure(fmt.Errorf("generated settings are invalid: %w", err))
	}
	settingsPath, err := cache.Write(cdir, "settings", "json", raw)
	if err != nil {
		return nil, ui.Failure(fmt.Errorf("writing settings: %w", err))
	}
	if s.l.opt.afterSettingsWrite != nil {
		s.l.opt.afterSettingsWrite(settingsPath)
	}
	back, err := cache.ReadFile(cdir, filepath.Base(settingsPath))
	if err != nil {
		return nil, ui.Failure(fmt.Errorf("re-reading settings: %w", err))
	}
	if string(back) != string(raw) {
		return nil, ui.Failure(fmt.Errorf("settings file %s changed after it was written", safeBase(settingsPath)))
	}
	if err := s.l.validateSettings(back); err != nil {
		return nil, ui.Failure(fmt.Errorf("settings file on disk is invalid: %w", err))
	}

	ln := &launch{Bin: bin, Settings: settingsPath, Resolved: r, Account: s.choice.Name, EnvAdd: extra}
	ln.Args = []string{"--settings", settingsPath}

	if applied.ExtraMCPServers {
		cfgJSON, err := profile.MCPConfigJSON(r.MCP, cc.GOOS)
		if err != nil {
			return nil, ui.Failure(fmt.Errorf("MCP config: %w", err))
		}
		p, err := cache.Write(cdir, "mcp", "json", cfgJSON)
		if err != nil {
			return nil, ui.Failure(fmt.Errorf("writing MCP config: %w", err))
		}
		ln.MCPConfig = p
		ln.Args = append(ln.Args, "--mcp-config", p)
	}
	if applied.StrictMCPConfig {
		ln.Args = append(ln.Args, "--strict-mcp-config")
	}
	if applied.DropUserSettingSources {
		ln.Args = append(ln.Args, "--setting-sources", "project,local")
	}
	if applied.AppendSystemPromptFile {
		p, err := cache.Write(cdir, "prompt", "md", r.Prompt)
		if err != nil {
			return nil, ui.Failure(fmt.Errorf("writing prompt file: %w", err))
		}
		ln.PromptFile = p
		ln.Args = append(ln.Args, "--append-system-prompt-file", p)
	}
	if len(r.InstructionsText) > 0 {
		// --add-dir is not a sideload flag, so no policy check applies. Claude
		// Code can write to the directory, so WriteDir checks it on every
		// launch and rebuilds it when it differs from the expected content.
		d, modesIgnored, err := cache.WriteDirChecked(cdir, instructionsPrefix, instructionsFile, r.InstructionsText)
		if err != nil {
			return nil, ui.Failure(fmt.Errorf("writing instructions: %w", err))
		}
		if modesIgnored {
			warn("the cache file system ignores file modes, so the instructions directory is not read-only. ccshelf still checks its content at every start")
		}
		ln.InstructionsDir = d
		ln.Args = append(ln.Args, "--add-dir", d)
		envAdd := map[string]string{}
		for k, v := range ln.EnvAdd {
			envAdd[k] = v
		}
		envAdd[additionalDirsClaudeMDEnv] = "1"
		ln.EnvAdd = envAdd
	}
	if m.Session.Effort != "" {
		ln.Args = append(ln.Args, "--effort", m.Session.Effort)
	}
	for _, a := range pass {
		// "--flag=value" is the same flag as "--flag value".
		name, _, _ := strings.Cut(a, "=")
		switch name {
		case "--settings", "--setting-sources", "--mcp-config", "--strict-mcp-config", "--append-system-prompt-file":
			// Whether the later flag wins or is merged has not been verified
			// against a real claude here {U}, so only the risk is stated.
			warn("the argument %s after the profile name can override what the profile generated", name)
		case "--add-dir":
			if len(r.InstructionsText) > 0 {
				warn("the argument --add-dir after the profile name adds a directory whose CLAUDE.md files Claude Code also loads, because the profile sets instructions")
			}
		case "--resume", "-r", "--continue", "-c":
			warn("%s resumes a session that another profile may have started. The session reuses its recorded prompt and skill list", name)
		}
	}
	ln.PassFrom = len(ln.Args)
	ln.Args = append(ln.Args, pass...)
	ln.Env = s.env
	if v, ok := ln.EnvAdd[additionalDirsClaudeMDEnv]; ok {
		ln.Env = claude.Env(s.env, map[string]string{additionalDirsClaudeMDEnv: v})
	}
	ln.Warnings = warnings
	return ln, nil
}

// settleTrust enforces the trust model (SR2). A closure that is not trusted
// is accepted only by an interactive confirmation of the printed closure; a
// non-interactive run fails with exit code 4. --yes is never consulted.
func (s *session) settleTrust(ctx context.Context, r *profile.Resolved) error {
	cc := s.cc
	lock, err := config.LockfilePath()
	if err != nil {
		return ui.Failure(fmt.Errorf("trust lockfile location: %w", err))
	}
	store, err := trust.Open(lock)
	if err != nil {
		return ui.Failure(withHint(fmt.Errorf("trust lockfile: %w", err), "fix or remove %s. Nothing runs on an unreadable trust record", lock))
	}
	v := store.CheckWithProject(r, s.proj.Allowed)
	if v.State == trust.Trusted {
		// The closure is trusted by content. If a source now has another pin
		// at the same commit (a tag that became a branch), record that.
		_, _ = store.Rekey(r)
		return nil
	}
	needs := &trust.NeedsTrustError{Profile: r.Name, Verdict: v}
	review := fmt.Sprintf("review it with: ccshelf trust %s. Accept it with: ccshelf trust %s --accept %s", r.Name, r.Name, v.Hash)
	switch {
	case v.Problem != "":
		// An inconsistent closure is a bug or tampering, not something a
		// person can review and accept: exit 1, not "needs trust" (exit 4).
		return ui.Failure(withHint(fmt.Errorf("profile %s: %w: %s", ui.SanitizeLine(r.Name), trust.ErrInconsistentClosure, ui.SanitizeLine(v.Problem)), "the closure cannot be trusted. This is a bug or a tampered file"))
	case v.State == trust.ProjectUntrusted:
		return ui.TrustRequired(withHint(needs, "review the .ccshelf folder, then run: ccshelf trust --project"))
	}
	interactive := canPrompt(cc) && (v.State == trust.New || s.cfg.Trust.OnChange == config.OnChangePrompt)
	if !interactive {
		return ui.TrustRequired(withHint(needs, "%s", review))
	}
	fmt.Fprintf(cc.Streams.Err, "Profile %s needs trust (closure %s):\n", ui.Sanitize(r.Name), v.Hash)
	v.Describe(cc.Streams.Err)
	ok, err := ui.ConfirmRisky(ctx, cc.Prompt, fmt.Sprintf("Trust profile %s as shown?", ui.Sanitize(r.Name)))
	if err != nil {
		return err
	}
	if !ok {
		return ui.TrustRequired(withHint(needs, "%s", review))
	}
	// Accept what was shown (v.Hash), not whatever the closure is by now.
	if err := store.Accept(r, v.Hash); err != nil {
		return ui.Failure(fmt.Errorf("recording trust: %w", err))
	}
	rec := ui.NewRecorder("trust", r.Name)
	rec.Flag("--accept", v.Hash)
	printEquivalent(cc, rec)
	return nil
}

// printDryRun prints the command a run would execute.
func printDryRun(cc *clicore.Context, ln *launch) error {
	if cc.Mode.JSON {
		env := map[string]string{}
		for k, v := range ln.EnvAdd {
			env[k] = v
		}
		type out struct {
			Profile    string            `json:"profile"`
			Account    string            `json:"account,omitempty"`
			Command    []string          `json:"command"`
			Env        map[string]string `json:"env,omitempty"`
			Settings   string            `json:"settings"`
			MCPConfig  string            `json:"mcp_config,omitempty"`
			PromptFile string            `json:"prompt_file,omitempty"`
			// InstructionsDir is the --add-dir directory of the instructions.
			InstructionsDir string   `json:"instructions_dir,omitempty"`
			Warnings        []string `json:"warnings"`
		}
		w := ln.Warnings
		if w == nil {
			w = []string{}
		}
		return ui.WriteJSON(cc.Streams.Out, "dry-run", out{
			Profile: ln.Resolved.Name, Account: ln.Account,
			Command:  append([]string{ln.Bin}, ln.redactedArgs()...),
			Env:      emptyToNil(env),
			Settings: ln.Settings, MCPConfig: ln.MCPConfig, PromptFile: ln.PromptFile, InstructionsDir: ln.InstructionsDir, Warnings: w,
		})
	}
	sh := shell(cc)
	words := append([]string{ln.Bin}, ln.redactedArgs()...)
	joined, err := ui.Join(sh, words)
	if err != nil {
		return ui.Failure(fmt.Errorf("printing the command: %w", err))
	}
	var prefix string
	names := make([]string, 0, len(ln.EnvAdd))
	for k := range ln.EnvAdd {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		q, err := ui.Quote(sh, ln.EnvAdd[k])
		if err != nil {
			return ui.Failure(fmt.Errorf("printing %s: %w", k, err))
		}
		if sh == ui.ShellPowerShell {
			prefix += fmt.Sprintf("$env:%s = %s; ", k, q)
		} else {
			prefix += fmt.Sprintf("%s=%s ", k, q)
		}
	}
	if sh == ui.ShellPowerShell {
		joined = "& " + joined
	}
	_, err = fmt.Fprintln(cc.Streams.Out, prefix+joined)
	return err
}

func emptyToNil(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// redactedArgs returns Args for display: the generated part as it is and the
// passthrough part through [redactPass].
func (l *launch) redactedArgs() []string {
	i := l.PassFrom
	if i < 0 || i > len(l.Args) {
		i = 0
	}
	return append(append([]string(nil), l.Args[:i]...), redactPass(l.Args[i:])...)
}

// redactPass redacts the arguments passed through to claude for display. A
// literal "--" does not end the redaction: every segment between "--"
// separators is redacted on its own, so "-- --api-key sk-abc" cannot leak.
func redactPass(pass []string) []string {
	out := make([]string, 0, len(pass))
	start := 0
	for i := 0; i <= len(pass); i++ {
		if i < len(pass) && pass[i] != "--" {
			continue
		}
		out = append(out, ui.RedactArgs(pass[start:i])...)
		if i < len(pass) {
			out = append(out, "--")
		}
		start = i + 1
	}
	return out
}

// missingFrom returns the first label of protected that is not a server the
// profile itself provides, or "" when every protected server is one of them.
func missingFrom(protected []string, servers map[string]profile.MCPServer) string {
	for _, l := range protected {
		if _, ok := servers[l]; !ok {
			return l
		}
	}
	return ""
}

// deniableMCP lists the full labels (plugin:<plugin>:<server>) of the MCP
// servers of installed plugins that the profile does not include, leaving out
// protected plugins, plugins that policy keeps on and protected servers.
func deniableMCP(installed []claude.Plugin, include, protectedPlugins, locked, protectedMCP []string) []string {
	skip := map[string]bool{}
	for _, l := range [][]string{include, protectedPlugins, locked} {
		for _, id := range l {
			skip[id] = true
		}
	}
	keep := map[string]bool{}
	for _, l := range protectedMCP {
		keep[l] = true
	}
	// Labels carry no marketplace, so a label owned by a plugin that is kept
	// (included, protected, locked or required by org) may also belong to a
	// same-named plugin of another marketplace: never deny it.
	for _, p := range installed {
		if p.Name != "" && (skip[p.ID] || p.RequiredByOrg) {
			for server := range p.MCPServers {
				keep["plugin:"+p.Name+":"+server] = true
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range installed {
		if p.Name == "" || skip[p.ID] || p.RequiredByOrg {
			continue
		}
		for server := range p.MCPServers {
			label := "plugin:" + p.Name + ":" + server
			if !keep[label] && !seen[label] {
				seen[label] = true
				out = append(out, label)
			}
		}
	}
	sort.Strings(out)
	return out
}

// warningsForGOOS drops the profile warnings about a per-OS override of an MCP
// server ("... on windows") when goos is another system: they describe a
// command that does not run here.
func warningsForGOOS(goos string, ws []string) []string {
	name := goos
	if goos == "darwin" {
		name = "macos"
	}
	var out []string
	for _, w := range ws {
		if strings.HasPrefix(w, "MCP server ") {
			skip := false
			for _, o := range []string{"windows", "macos", "linux"} {
				if strings.HasSuffix(w, " on "+o) && o != name {
					skip = true
				}
			}
			if skip {
				continue
			}
		}
		out = append(out, w)
	}
	return out
}

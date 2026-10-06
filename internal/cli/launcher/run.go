package launcher

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/cache"
	"github.com/ccshelf/ccshelf/internal/claude"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/config"
	"github.com/ccshelf/ccshelf/internal/policy"
	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/settings"
	"github.com/ccshelf/ccshelf/internal/trust"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// launch is everything needed to start claude, produced by the run pipeline.
type launch struct {
	Bin  string
	Args []string
	// EnvAdd are the environment variables added or replaced for the child.
	EnvAdd map[string]string
	// Env is the complete child environment.
	Env []string

	Settings, MCPConfig, PromptFile string
	Account                         string
	Warnings                        []string
	Resolved                        *profile.Resolved
}

func (l *launcher) runCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "run [profile] [-- claude args]",
		Short: "Start claude with a profile",
		Long: `Start claude with a profile: the plugins, skills and MCP servers it names.

Arguments after the profile name (and after -- when no profile is given) are
passed to claude unchanged, so flags of ccshelf itself must come before the
profile name: ccshelf run --account work sre --resume.

Without a profile name, a terminal gets a picker; anything else exits with
code 2. A profile from a shared source must be trusted first (exit code 4
otherwise); --yes never accepts trust, only an interactive confirmation of the
printed closure or "ccshelf trust <profile> --accept <closure-hash>" does.`,
		Example: `  ccshelf run sre
  ccshelf run sre -- -p "summarize this repo"
  ccshelf run --account personal sre --resume`,
		DisableFlagsInUseLine: true,
	}
	c.Flags().SetInterspersed(false)
	c.Flags().BoolVar(&yes, "yes", false, "answer yes to confirmations other than trust (trust is never auto-accepted)")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, cmd *cobra.Command, args []string) error {
		name, pass, err := splitRunArgs(cmd, args)
		if err != nil {
			return err
		}
		return l.runProfile(ctx, cc, name, pass, yes, false)
	})
	return c
}

func (l *launcher) dryRunCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "dry-run [profile] [-- claude args]",
		Short: "Print the exact claude command a run would execute",
		Long: `Run the whole pipeline of "run" (including the trust check) and print the exact
claude command instead of starting it. The generated settings, MCP config and
prompt files are written to the private cache so the printed command is valid.
Environment values from profiles are never printed.`,
		DisableFlagsInUseLine: true,
	}
	c.Flags().SetInterspersed(false)
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, cmd *cobra.Command, args []string) error {
		name, pass, err := splitRunArgs(cmd, args)
		if err != nil {
			return err
		}
		return l.runProfile(ctx, cc, name, pass, false, true)
	})
	return c
}

// runProfile is the run and dry-run command body.
func (l *launcher) runProfile(ctx context.Context, cc *clicore.Context, name string, pass []string, yes, dry bool) error {
	command := "run"
	if dry {
		command = "dry-run"
	}
	s, err := l.open(ctx, cc, true, true)
	if err != nil {
		return err
	}
	name, picked, err := pickProfile(ctx, cc, name, command, func() ([]ui.Option, error) { return s.profileOptions() })
	if err != nil {
		return err
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
		eq = append(eq, name)
		if len(pass) > 0 {
			eq = append(eq, "--")
			eq = append(eq, ui.RedactArgs(pass)...)
		}
		if err := ui.Equivalent(cc.Streams.Err, cc.GOOS, eq); err != nil {
			warnf(cc, "cannot print the equivalent command: %v", err)
		}
	}
	if dry {
		return printDryRun(cc, ln)
	}
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

	warnings := []string{}
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
	for _, w := range r.Warnings {
		warn("%s", w)
	}
	if m.Status == profile.StatusDeprecated {
		msg := fmt.Sprintf("profile %s is deprecated", r.Name)
		if m.SupersededBy != "" {
			msg += "; use " + m.SupersededBy
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
	if strict && len(s.protectedMCP) > 0 {
		return nil, ui.Failure(withHint(
			fmt.Errorf("profile %q sets mcp.strict, which would also remove the protected MCP servers: %s", r.Name, strings.Join(s.protectedMCP, ", ")),
			"the org protects these servers (SR3); remove mcp.strict from the profile"))
	}
	needs := policy.Needs{
		ExtraMCPServers:        strict || len(r.MCP) > 0,
		StrictMCPConfig:        strict,
		DropUserSettingSources: !m.InheritsUserSettings(),
		AppendSystemPromptFile: len(r.Prompt) > 0,
		HideConnectors:         hide,
	}
	applied, err := matrix.Plan(needs, m.Policy.OnBlocked)
	if err != nil {
		var be *policy.BlockedError
		if errors.As(err, &be) {
			return nil, ui.Policy(withHint(err, "the profile has policy.on_blocked = \"fail\"; set it to \"warn\" to drop the blocked part instead"))
		}
		return nil, ui.Failure(err)
	}
	for _, w := range matrix.Warnings {
		warn("policy: %s", w)
	}
	for _, w := range applied.Warnings {
		warn("%s", w)
	}
	if !m.InheritsUserSettings() && applied.DropUserSettingSources {
		warn("inherit_user_settings = false: your user settings (permissions, hooks, MCP servers, model) are not loaded in this session")
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
		Env:            m.Session.Env,
		Profile:        r.Name,
	}
	res, err := settings.Build(spec)
	if err != nil {
		return nil, ui.Failure(fmt.Errorf("building settings: %w", err))
	}
	for _, w := range res.Warnings {
		warn("%s", w)
	}
	if len(res.Missing) > 0 {
		for _, id := range res.Missing {
			warn("plugin %s is in the profile but not installed; install it inside Claude Code with: /plugin install %s", id, id)
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
	if err := settings.Validate(raw); err != nil {
		return nil, ui.Failure(fmt.Errorf("generated settings are invalid: %w", err))
	}
	settingsPath, err := cache.Write(cdir, "settings", "json", raw)
	if err != nil {
		return nil, ui.Failure(fmt.Errorf("writing settings: %w", err))
	}
	back, err := cache.ReadFile(cdir, filepath.Base(settingsPath))
	if err != nil {
		return nil, ui.Failure(fmt.Errorf("re-reading settings: %w", err))
	}
	if string(back) != string(raw) {
		return nil, ui.Failure(fmt.Errorf("settings file %s changed after it was written", safeBase(settingsPath)))
	}
	if err := settings.Validate(back); err != nil {
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
	if m.Session.Effort != "" {
		ln.Args = append(ln.Args, "--effort", m.Session.Effort)
	}
	for _, a := range pass {
		switch a {
		case "--settings", "--setting-sources", "--mcp-config", "--strict-mcp-config", "--append-system-prompt-file":
			warn("the argument %s after the profile name can override what the profile generated", a)
		}
	}
	ln.Args = append(ln.Args, pass...)
	ln.Env = s.env
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
		return ui.Failure(withHint(fmt.Errorf("trust lockfile: %w", err), "fix or remove %s; nothing runs on an unreadable trust record", lock))
	}
	v := store.CheckWithProject(r, s.proj.Allowed)
	if v.State == trust.Trusted {
		return nil
	}
	needs := &trust.NeedsTrustError{Profile: r.Name, Verdict: v}
	review := fmt.Sprintf("review it with: ccshelf trust %s; accept it with: ccshelf trust %s --accept %s", r.Name, r.Name, v.Hash)
	switch {
	case v.Problem != "":
		// An inconsistent closure is a bug or tampering, not something a
		// person can review and accept: exit 1, not "needs trust" (exit 4).
		return ui.Failure(withHint(fmt.Errorf("profile %s: %w: %s", ui.SanitizeLine(r.Name), trust.ErrInconsistentClosure, ui.SanitizeLine(v.Problem)), "the closure cannot be trusted; this is a bug or a tampered file"))
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
			Warnings   []string          `json:"warnings"`
		}
		w := ln.Warnings
		if w == nil {
			w = []string{}
		}
		return ui.WriteJSON(cc.Streams.Out, "dry-run", out{
			Profile: ln.Resolved.Name, Account: ln.Account,
			Command:  append([]string{ln.Bin}, ui.RedactArgs(ln.Args)...),
			Env:      emptyToNil(env),
			Settings: ln.Settings, MCPConfig: ln.MCPConfig, PromptFile: ln.PromptFile, Warnings: w,
		})
	}
	sh := shell(cc)
	words := append([]string{ln.Bin}, ui.RedactArgs(ln.Args)...)
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

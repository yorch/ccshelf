package launcher

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/trust"
	"github.com/yorch/ccshelf/internal/ui"
)

func (l *launcher) showCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "show [profile]",
		Short: "Show a resolved profile: parents merged, MCP servers, closure",
		Long: `Show a profile as it would run: the extends chain, plugins, skills, MCP servers,
session defaults, warnings and the closure that a trust decision pins, plus the
trust state. Environment values are never printed. Use --json for a stable,
versioned machine-readable form.`,
		Args: cobra.MaximumNArgs(1),
	}
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		s, err := l.open(ctx, cc, true, false)
		if err != nil {
			return err
		}
		name, picked, err := pickProfile(ctx, cc, first(args), "show", s.profileOptions)
		if err != nil {
			return err
		}
		r, err := s.resolve(name)
		if err != nil {
			return err
		}
		state, err := s.trustState(r)
		if err != nil {
			return err
		}
		if picked {
			printEquivalent(cc, ui.NewRecorder("show", name))
		}
		if cc.Mode.JSON {
			return ui.WriteJSON(cc.Streams.Out, "profile", showData(r, state, s.sourceLabels()))
		}
		fmt.Fprint(cc.Streams.Out, profile.Describe(r))
		for _, f := range r.Chain {
			if tr := sourceTracks(f.Source); tr != "" {
				fmt.Fprintf(cc.Streams.Out, "Source: git:%s tracks %s\n", ui.Sanitize(trimLocator(f.Source.(branchSource).Locator())), ui.Sanitize(tr))
				break
			}
		}
		fmt.Fprintf(cc.Streams.Out, "Trust: %s\n", state)
		return nil
	})
	return c
}

func first(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// trustState reports the trust state of a resolved profile without prompting.
func (s *session) trustState(r *profile.Resolved) (string, error) {
	lock, err := config.LockfilePath()
	if err != nil {
		return "", ui.Failure(fmt.Errorf("trust lockfile location: %w", err))
	}
	store, err := trust.Open(lock)
	if err != nil {
		return "", ui.Failure(fmt.Errorf("trust lockfile: %w", err))
	}
	v := store.CheckWithProject(r, s.proj.Allowed)
	return v.State.String(), nil
}

type showSession struct {
	Model                  string `json:"model,omitempty"`
	Effort                 string `json:"effort,omitempty"`
	AppendSystemPromptFile string `json:"append_system_prompt_file,omitempty"`
	InheritUserSettings    bool   `json:"inherit_user_settings"`
	// EnvNames lists variable names only. Values are never exported.
	EnvNames []string `json:"env_names"`
}

type showMCP struct {
	Servers            []string `json:"servers"`
	ClaudeAIConnectors string   `json:"claudeai_connectors,omitempty"`
	Strict             bool     `json:"strict"`
}

type showItem struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Risky  bool   `json:"risky"`
}

type showDoc struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Owner        string   `json:"owner,omitempty"`
	Status       string   `json:"status"`
	SupersededBy string   `json:"superseded_by,omitempty"`
	Kind         string   `json:"kind"`
	Chain        []string `json:"chain"`
	// Tracks lists, for sources that follow a branch, "branch main @ 1a2b3c4".
	// Sources keeps its format.
	Tracks      []string    `json:"tracks,omitempty"`
	Sources     []string    `json:"sources"`
	Account     string      `json:"account,omitempty"`
	PluginMode  string      `json:"plugin_mode"`
	Include     []string    `json:"plugins_include"`
	Exclude     []string    `json:"plugins_exclude"`
	SkillsOff   []string    `json:"skills_off"`
	SkillsName  []string    `json:"skills_name_only"`
	MCP         showMCP     `json:"mcp"`
	Session     showSession `json:"session"`
	OnBlocked   string      `json:"on_blocked"`
	WhenToUse   []string    `json:"when_to_use"`
	AvoidWhen   []string    `json:"avoid_when"`
	Warnings    []string    `json:"warnings"`
	ClosureHash string      `json:"closure_hash"`
	Closure     []showItem  `json:"closure"`
	Trust       string      `json:"trust"`
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func showData(r *profile.Resolved, state string, labels map[string]string) showDoc {
	m := r.Merged.WithDefaults()
	env := make([]string, 0, len(m.Session.Env))
	for k := range m.Session.Env {
		env = append(env, k)
	}
	sort.Strings(env)
	d := showDoc{
		Name: r.Name, Description: m.Description, Owner: m.Owner, Status: m.Status, SupersededBy: m.SupersededBy,
		Kind: r.Kind.String(), Account: m.Account, PluginMode: m.Plugins.Mode,
		Include: nz(m.Plugins.Include), Exclude: nz(m.Plugins.Exclude),
		SkillsOff: nz(m.Skills.Off), SkillsName: nz(m.Skills.NameOnly),
		MCP: showMCP{Servers: nz(m.MCP.Servers), ClaudeAIConnectors: m.MCP.ClaudeAIConnectors, Strict: m.MCP.Strict != nil && *m.MCP.Strict},
		Session: showSession{
			Model: m.Session.Model, Effort: m.Session.Effort, AppendSystemPromptFile: m.Session.AppendSystemPromptFile,
			InheritUserSettings: m.InheritsUserSettings(), EnvNames: env,
		},
		OnBlocked: m.Policy.OnBlocked, WhenToUse: nz(m.WhenToUse), AvoidWhen: nz(m.AvoidWhen),
		Warnings: make([]string, 0, len(r.Warnings)), ClosureHash: r.Closure.Hash, Trust: state,
	}
	for _, f := range r.Chain {
		d.Chain = append(d.Chain, f.Name)
		id := profile.PortableSourceID(f.Source)
		if l, ok := labels[f.Source.ID()]; ok {
			id = l
		}
		d.Sources = append(d.Sources, id)
		if tr := sourceTracks(f.Source); tr != "" {
			d.Tracks = append(d.Tracks, tr)
		}
	}
	for _, w := range r.Warnings {
		d.Warnings = append(d.Warnings, ui.Sanitize(w))
	}
	for _, it := range r.Closure.Items {
		d.Closure = append(d.Closure, showItem{Kind: it.Kind, Name: ui.Sanitize(it.Name), Digest: it.Digest, Risky: it.Risky})
	}
	d.Description = ui.Sanitize(d.Description)
	d.Owner = ui.Sanitize(d.Owner)
	d.Sources = uniqKeep(d.Sources)
	d.Tracks = uniqKeep(d.Tracks)
	return d
}

func uniqKeep(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range in {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// join renders a list for one-line output.
func join(list []string) string {
	if len(list) == 0 {
		return "-"
	}
	return strings.Join(list, ", ")
}

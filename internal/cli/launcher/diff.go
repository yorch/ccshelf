package launcher

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
)

// listDiff is the difference of one list between two profiles.
type listDiff struct {
	Field   string   `json:"field"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// scalarDiff is one scalar that differs.
type scalarDiff struct {
	Field string `json:"field"`
	A     string `json:"a"`
	B     string `json:"b"`
}

type diffDoc struct {
	A         string       `json:"a"`
	B         string       `json:"b"`
	Identical bool         `json:"identical"`
	Lists     []listDiff   `json:"lists"`
	Scalars   []scalarDiff `json:"scalars"`
}

func (l *launcher) diffCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "diff [profile-a] [profile-b]",
		Short: "Compare two resolved profiles",
		Long: `Compare two profiles after their parents are merged: plugins, skills, MCP
servers, environment variable names and session defaults. "+" marks what b adds
to a and "-" what b lacks. Environment values, prompt text and instructions text are never printed.
The exit code is 0 whether or not they differ. --json has an "identical" field.`,
		Args: cobra.MaximumNArgs(2),
	}
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		s, err := l.open(ctx, cc, true)
		if err != nil {
			return err
		}
		var a, b string
		if len(args) > 0 {
			a = args[0]
		}
		if len(args) > 1 {
			b = args[1]
		}
		picked := false
		for _, p := range []*string{&a, &b} {
			n, pk, err := pickProfile(ctx, cc, *p, "diff", s.profileOptions)
			if err != nil {
				return err
			}
			*p = n
			picked = picked || pk
		}
		ra, err := s.resolve(a)
		if err != nil {
			return err
		}
		rb, err := s.resolve(b)
		if err != nil {
			return err
		}
		if picked {
			printEquivalent(cc, ui.NewRecorder("diff", a, b))
		}
		d := diffResolved(ra, rb)
		if cc.Mode.JSON {
			return ui.WriteJSON(cc.Streams.Out, "diff", d)
		}
		if d.Identical {
			fmt.Fprintf(cc.Streams.Out, "%s and %s resolve to the same settings\n", ui.Sanitize(a), ui.Sanitize(b))
			return nil
		}
		fmt.Fprintf(cc.Streams.Out, "--- %s\n+++ %s\n", ui.Sanitize(a), ui.Sanitize(b))
		for _, x := range d.Lists {
			fmt.Fprintf(cc.Streams.Out, "%s:\n", x.Field)
			for _, v := range x.Removed {
				fmt.Fprintf(cc.Streams.Out, "  - %s\n", ui.Sanitize(v))
			}
			for _, v := range x.Added {
				fmt.Fprintf(cc.Streams.Out, "  + %s\n", ui.Sanitize(v))
			}
		}
		for _, x := range d.Scalars {
			fmt.Fprintf(cc.Streams.Out, "%s: %s -> %s\n", x.Field, show(x.A), show(x.B))
		}
		return nil
	})
	return c
}

func show(s string) string {
	if s == "" {
		return "(unset)"
	}
	return ui.Sanitize(s)
}

func boolStr(p *bool) string {
	if p == nil {
		return ""
	}
	if *p {
		return "true"
	}
	return "false"
}

func diffResolved(a, b *profile.Resolved) diffDoc {
	ma, mb := a.Merged.WithDefaults(), b.Merged.WithDefaults()
	d := diffDoc{A: a.Name, B: b.Name, Lists: []listDiff{}, Scalars: []scalarDiff{}}
	lists := []struct {
		name string
		a, b []string
	}{
		{"plugins.include", ma.Plugins.Include, mb.Plugins.Include},
		{"plugins.exclude", ma.Plugins.Exclude, mb.Plugins.Exclude},
		{"skills.off", ma.Skills.Off, mb.Skills.Off},
		{"skills.name_only", ma.Skills.NameOnly, mb.Skills.NameOnly},
		{"mcp.servers", ma.MCP.Servers, mb.MCP.Servers},
		{"session.env (names)", envNames(ma.Session.Env), envNames(mb.Session.Env)},
		{"instructions.files", instructionLabels(a), instructionLabels(b)},
	}
	for _, l := range lists {
		added, removed := setDiff(l.a, l.b)
		if len(added) > 0 || len(removed) > 0 {
			d.Lists = append(d.Lists, listDiff{Field: l.name, Added: nz(added), Removed: nz(removed)})
		}
	}
	scalars := []struct{ name, a, b string }{
		{"plugins.mode", ma.Plugins.Mode, mb.Plugins.Mode},
		{"mcp.claudeai_connectors", ma.MCP.ClaudeAIConnectors, mb.MCP.ClaudeAIConnectors},
		{"mcp.strict", boolStr(ma.MCP.Strict), boolStr(mb.MCP.Strict)},
		{"session.model", ma.Session.Model, mb.Session.Model},
		{"session.effort", ma.Session.Effort, mb.Session.Effort},
		{"session.output_style", ma.Session.OutputStyle, mb.Session.OutputStyle},
		{"session.append_system_prompt_file", ma.Session.AppendSystemPromptFile, mb.Session.AppendSystemPromptFile},
		{"session.prompt (sha256)", itemDigest(a, profile.ItemPrompt), itemDigest(b, profile.ItemPrompt)},
		{"instructions.order", orderIfSameSet(instructionLabels(a), instructionLabels(b)), orderIfSameSet(instructionLabels(b), instructionLabels(a))},
		{"instructions.inherit", inheritLabel(a), inheritLabel(b)},
		{"instructions (sha256)", textDigest(a.InstructionsText), textDigest(b.InstructionsText)},
		{"session.inherit_user_settings", boolStr(ma.Session.InheritUserSettings), boolStr(mb.Session.InheritUserSettings)},
		{"policy.on_blocked", ma.Policy.OnBlocked, mb.Policy.OnBlocked},
		{"account", ma.Account, mb.Account},
	}
	for _, s := range scalars {
		if s.a != s.b {
			d.Scalars = append(d.Scalars, scalarDiff{Field: s.name, A: s.a, B: s.b})
		}
	}
	d.Identical = len(d.Lists) == 0 && len(d.Scalars) == 0
	return d
}

// instructionLabels lists the effective instructions files as "path [source]",
// so that one path from two sources is not shown as the same file.
func instructionLabels(r *profile.Resolved) []string {
	out := make([]string, 0, len(r.Instructions))
	for _, f := range r.Instructions {
		out = append(out, f.Path+" ["+f.Source+"]")
	}
	return out
}

// orderIfSameSet returns the order of a when a and b hold the same files, so
// that a pure reorder shows up and an added or removed file shows only once,
// in the list.
func orderIfSameSet(a, b []string) string {
	if len(a) != len(b) || len(a) < 2 {
		return ""
	}
	added, removed := setDiff(a, b)
	if len(added) > 0 || len(removed) > 0 {
		return ""
	}
	return strings.Join(a, ", ")
}

// inheritLabel describes whether inherit = false dropped instructions files.
func inheritLabel(r *profile.Resolved) string {
	if r.InstructionsCutBy == "" {
		return ""
	}
	return "false (set in " + r.InstructionsCutBy + ")"
}

func textDigest(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return profile.DigestBytes(b)[:12]
}

func itemDigest(r *profile.Resolved, kind string) string {
	for _, it := range r.Closure.Items {
		if it.Kind == kind {
			if len(it.Digest) > 12 {
				return it.Digest[:12]
			}
			return it.Digest
		}
	}
	return ""
}

func envNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// setDiff returns what b has that a lacks, and what a has that b lacks, each
// sorted.
func setDiff(a, b []string) (added, removed []string) {
	in := func(list []string) map[string]bool {
		m := map[string]bool{}
		for _, x := range list {
			m[x] = true
		}
		return m
	}
	ia, ib := in(a), in(b)
	for x := range ib {
		if !ia[x] {
			added = append(added, x)
		}
	}
	for x := range ia {
		if !ib[x] {
			removed = append(removed, x)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

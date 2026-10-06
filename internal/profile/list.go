package profile

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Summary is one row of `ccshelf ls`.
type Summary struct {
	Name        string
	Description string
	Owner       string
	Status      string
	Kind        Kind
	// Source is the id of the source the profile comes from.
	Source string
	// Shadows lists the ids of sources whose profile of the same name this
	// personal profile shadows.
	Shadows []string
	// Conflict lists the ids of every source holding the name when the
	// collision rules make it an error (Resolve would fail).
	Conflict []string
	// Err is set when the profile file is invalid; other fields are partial.
	Err string
}

// List summarizes every profile in sources, sorted by name. Invalid profiles
// are listed with Err set rather than failing the listing. Callers decide which
// sources to pass: project sources should be left out unless trusted.
func List(sources []Source) ([]Summary, error) {
	type entry struct {
		src  Source
		file *File
		err  error
	}
	if err := checkKinds(sources); err != nil {
		return nil, err
	}
	by := map[string][]entry{}
	seen := map[string]bool{}
	for _, s := range sources {
		if seen[s.ID()] {
			continue
		}
		seen[s.ID()] = true
		names, err := s.Names()
		if err != nil {
			return nil, fmt.Errorf("listing profiles in %s: %w", s.ID(), err)
		}
		for _, n := range names {
			f, err := s.Open(n)
			by[n] = append(by[n], entry{s, f, err})
		}
	}
	var out []Summary
	for _, n := range sortedKeys(by) {
		es := by[n]
		pick := es[0]
		var shadows, conflict []string
		if len(es) > 1 {
			var personal []entry
			project := false
			for _, e := range es {
				if e.src.Kind() == KindPersonal {
					personal = append(personal, e)
				}
				if e.src.Kind() == KindProject {
					project = true
				}
			}
			if !project && len(personal) == 1 {
				pick = personal[0]
				for _, e := range es {
					if e.src != pick.src {
						shadows = append(shadows, e.src.ID())
					}
				}
			} else {
				for _, e := range es {
					conflict = append(conflict, e.src.ID())
				}
			}
		}
		s := Summary{Name: n, Kind: pick.src.Kind(), Source: pick.src.ID(), Shadows: shadows, Conflict: conflict}
		if pick.err != nil {
			s.Err = pick.err.Error()
		} else {
			m := pick.file.Manifest.WithDefaults()
			s.Description, s.Owner, s.Status = m.Description, m.Owner, m.Status
		}
		out = append(out, s)
	}
	return out, nil
}

// redactURL returns scheme://host/path of raw with userinfo, query and
// fragment removed, so that nothing secret reaches the screen or a log.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<invalid url>"
	}
	return u.Scheme + "://" + u.Host + u.EscapedPath()
}

// Describe renders a resolved profile as stable, human-readable text for
// `ccshelf show`. It contains no absolute paths and no timestamps, so it is
// safe to compare in golden files. It never prints secrets: environment values
// are shown as <redacted>, MCP URLs lose their query and fragment, and a
// prompt is shown as its size and a short digest, never its text (B6).
func Describe(r *Resolved) string {
	var b strings.Builder
	m := r.Merged
	fmt.Fprintf(&b, "Profile: %s (%s)\n", r.Name, r.Kind)
	if m.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", m.Description)
	}
	if m.Owner != "" {
		fmt.Fprintf(&b, "Owner: %s\n", m.Owner)
	}
	fmt.Fprintf(&b, "Status: %s", m.Status)
	if m.SupersededBy != "" {
		fmt.Fprintf(&b, " (superseded by %s)", m.SupersededBy)
	}
	b.WriteString("\n")
	if m.Account != "" {
		fmt.Fprintf(&b, "Account: %s\n", m.Account)
	}
	var chain []string
	for _, f := range r.Chain {
		chain = append(chain, fmt.Sprintf("%s [%s]", f.Name, PortableSourceID(f.Source)))
	}
	fmt.Fprintf(&b, "Chain: %s\n", strings.Join(chain, " -> "))
	list := func(label string, items []string) {
		if len(items) > 0 {
			fmt.Fprintf(&b, "  %s: %s\n", label, strings.Join(items, ", "))
		}
	}
	b.WriteString("Plugins:\n")
	fmt.Fprintf(&b, "  mode: %s\n", m.Plugins.Mode)
	list("include", m.Plugins.Include)
	list("exclude", m.Plugins.Exclude)
	if len(m.Skills.Off)+len(m.Skills.NameOnly) > 0 {
		b.WriteString("Skills:\n")
		list("off", m.Skills.Off)
		list("name_only", m.Skills.NameOnly)
	}
	b.WriteString("MCP:\n")
	for _, n := range m.MCP.Servers {
		s := r.MCP[n]
		target := s.Command
		if s.Type != MCPStdio {
			target = redactURL(s.URL)
		}
		fmt.Fprintf(&b, "  server %s: %s %s\n", n, s.Type, target)
	}
	if m.MCP.ClaudeAIConnectors != "" {
		fmt.Fprintf(&b, "  claudeai_connectors: %s\n", m.MCP.ClaudeAIConnectors)
	}
	if m.MCP.Strict != nil {
		fmt.Fprintf(&b, "  strict: %t\n", *m.MCP.Strict)
	}
	b.WriteString("Session:\n")
	if m.Session.Model != "" {
		fmt.Fprintf(&b, "  model: %s\n", m.Session.Model)
	}
	if m.Session.Effort != "" {
		fmt.Fprintf(&b, "  effort: %s\n", m.Session.Effort)
	}
	if m.Session.AppendSystemPromptFile != "" {
		fmt.Fprintf(&b, "  prompt: %s (%d bytes, sha256:%s)\n", m.Session.AppendSystemPromptFile, len(r.Prompt), digest(r.Prompt)[:12])
	}
	fmt.Fprintf(&b, "  inherit_user_settings: %t\n", m.InheritsUserSettings())
	envNames := sortedKeys(m.Session.Env)
	for _, k := range envNames {
		fmt.Fprintf(&b, "  env %s = <redacted>\n", k)
	}
	fmt.Fprintf(&b, "Policy:\n  on_blocked: %s\n", m.Policy.OnBlocked)
	if len(m.WhenToUse) > 0 {
		fmt.Fprintf(&b, "When to use: %s\n", strings.Join(m.WhenToUse, "; "))
	}
	if len(m.AvoidWhen) > 0 {
		fmt.Fprintf(&b, "Avoid when: %s\n", strings.Join(m.AvoidWhen, "; "))
	}
	ws := append([]string(nil), r.Warnings...)
	sort.Strings(ws)
	for _, w := range ws {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}
	fmt.Fprintf(&b, "Closure: sha256:%s\n", r.Closure.Hash)
	for _, it := range r.Closure.Items {
		risk := ""
		if it.Risky {
			risk = " risky"
		}
		fmt.Fprintf(&b, "  %s %s %s%s\n", it.Kind, it.Name, it.Digest[:12], risk)
	}
	return b.String()
}

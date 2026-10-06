package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// MaxExtendsDepth is the deepest extends chain Resolve follows.
const MaxExtendsDepth = 16

// MaxPromptSize is the largest append_system_prompt_file accepted.
const MaxPromptSize = 64 << 10

// registryRel is the registry location below a source root.
const registryRel = "mcp/registry.toml"

// ResolveOptions controls Resolve.
type ResolveOptions struct {
	// AllowProject lets project-kind sources take part. The caller sets it
	// only after explicit per-repo trust (SR2); the default is false.
	AllowProject bool
}

// Resolved is a profile with its parents merged. See the package comment for
// the merge rules.
type Resolved struct {
	Name     string
	Kind     Kind
	Chain    []*File // root parent first, the requested profile last
	Merged   Manifest
	MCP      map[string]MCPServer
	Prompt   []byte // CRLF normalized to LF
	Warnings []string
	Closure  Closure
}

type resolver struct {
	sources  []Source
	opt      ResolveOptions
	cache    map[string]*File
	done     map[string]bool
	chain    []*File
	warnings []string
	regs     map[string]map[string]MCPServer
}

// Resolve finds the named profile, follows extends and merges the chain.
func Resolve(name string, sources []Source, opt ResolveOptions) (*Resolved, error) {
	r := &resolver{sources: sources, opt: opt, cache: map[string]*File{}, done: map[string]bool{}, regs: map[string]map[string]MCPServer{}}
	req, err := r.lookup(name)
	if err != nil {
		return nil, err
	}
	if err := r.visit(req, nil); err != nil {
		return nil, err
	}
	if err := r.checkKinds(req); err != nil {
		return nil, err
	}
	res := &Resolved{Name: name, Kind: req.Source.Kind(), Chain: r.chain}
	if err := r.merge(res, req); err != nil {
		return nil, err
	}
	res.Warnings = append(r.warnings, res.Warnings...)
	closure, err := buildClosure(res)
	if err != nil {
		return nil, err
	}
	res.Closure = closure
	return res, nil
}

func (r *resolver) warn(format string, a ...any) {
	w := fmt.Sprintf(format, a...)
	for _, x := range r.warnings {
		if x == w {
			return
		}
	}
	r.warnings = append(r.warnings, w)
}

func (r *resolver) lookup(name string) (*File, error) {
	if f, ok := r.cache[name]; ok {
		return f, nil
	}
	var hits []Source
	var skipped Source
	seen := map[string]bool{}
	for _, s := range r.sources {
		if seen[s.ID()] {
			continue
		}
		seen[s.ID()] = true
		names, err := s.Names()
		if err != nil {
			return nil, fmt.Errorf("listing profiles in %s: %w", s.ID(), err)
		}
		if !contains(names, name) {
			continue
		}
		if s.Kind() == KindProject && !r.opt.AllowProject {
			skipped = s
			continue
		}
		hits = append(hits, s)
	}
	var winner Source
	switch len(hits) {
	case 0:
		if skipped != nil {
			return nil, fmt.Errorf("%w: %q exists only in %s, which has not been trusted for this run", ErrProjectNotTrusted, name, skipped.ID())
		}
		return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	case 1:
		winner = hits[0]
	default:
		var personal []Source
		project := false
		for _, s := range hits {
			switch s.Kind() {
			case KindPersonal:
				personal = append(personal, s)
			case KindProject:
				project = true
			}
		}
		if project || len(personal) != 1 {
			return nil, fmt.Errorf("%w: %q is in %s", ErrCollision, name, joinIDs(hits))
		}
		winner = personal[0]
		var others []Source
		for _, s := range hits {
			if s != winner {
				others = append(others, s)
			}
		}
		r.warn("personal profile %s shadows %s", name, joinIDs(others))
	}
	f, err := winner.Open(name)
	if err != nil {
		return nil, err
	}
	r.cache[name] = f
	return f, nil
}

func joinIDs(ss []Source) string {
	ids := make([]string, len(ss))
	for i, s := range ss {
		ids[i] = s.ID()
	}
	return strings.Join(ids, " and ")
}

func (r *resolver) visit(f *File, stack []string) error {
	if len(stack) >= MaxExtendsDepth {
		return fmt.Errorf("%w: more than %d levels at %s", ErrDepth, MaxExtendsDepth, strings.Join(append(stack, f.Name), " -> "))
	}
	stack = append(stack, f.Name)
	for _, p := range f.Manifest.Extends {
		for i, s := range stack {
			if s == p {
				return fmt.Errorf("%w: %s", ErrCycle, strings.Join(append(append([]string(nil), stack[i:]...), p), " -> "))
			}
		}
		if r.done[p] {
			continue
		}
		pf, err := r.lookup(p)
		if err != nil {
			return fmt.Errorf("profile %q extends %q: %w", f.Name, p, err)
		}
		if err := r.visit(pf, stack); err != nil {
			return err
		}
	}
	if !r.done[f.Name] {
		r.done[f.Name] = true
		r.chain = append(r.chain, f)
	}
	return nil
}

// checkKinds enforces the SR2 and SR3 rules per origin.
func (r *resolver) checkKinds(req *File) error {
	for _, f := range r.chain {
		m := f.Manifest
		switch f.Source.Kind() {
		case KindProject:
			var bad []string
			if len(m.MCP.Servers) > 0 {
				bad = append(bad, "mcp.servers")
			}
			if len(m.Session.Env) > 0 {
				bad = append(bad, "session.env")
			}
			if m.Session.AppendSystemPromptFile != "" {
				bad = append(bad, "session.append_system_prompt_file")
			}
			if m.Account != "" {
				bad = append(bad, "account")
			}
			if m.Session.InheritUserSettings != nil && !*m.Session.InheritUserSettings {
				bad = append(bad, "session.inherit_user_settings = false")
			}
			if len(bad) > 0 {
				return fmt.Errorf("%w: %q sets %s (SR2)", ErrProjectForbidden, f.Name, strings.Join(bad, ", "))
			}
			if err := r.checkProjectParents(f, map[string]bool{}); err != nil {
				return err
			}
		case KindOrg:
			if m.Session.InheritUserSettings != nil && !*m.Session.InheritUserSettings {
				return fmt.Errorf("%w: profile %q", ErrSharedDropsUserLayer, f.Name)
			}
		}
	}
	if req.Source.Kind() != KindPersonal {
		for _, f := range r.chain {
			if f.Manifest.Session.InheritUserSettings != nil && !*f.Manifest.Session.InheritUserSettings {
				return fmt.Errorf("%w: profile %q pulls in %q", ErrSharedDropsUserLayer, req.Name, f.Name)
			}
		}
	}
	return nil
}

// checkProjectParents rejects a project profile whose ancestors from other
// kinds carry MCP servers, env, prompts or an account.
func (r *resolver) checkProjectParents(f *File, seen map[string]bool) error {
	for _, p := range f.Manifest.Extends {
		if seen[p] {
			continue
		}
		seen[p] = true
		pf := r.cache[p]
		if pf == nil {
			continue
		}
		if pf.Source.Kind() != KindProject {
			m := pf.Manifest
			if len(m.MCP.Servers) > 0 || len(m.Session.Env) > 0 || m.Session.AppendSystemPromptFile != "" || m.Account != "" {
				return fmt.Errorf("%w: project profile %q extends %q (%s), which sets MCP servers, env, a prompt or an account (SR2)", ErrProjectForbidden, f.Name, p, pf.Source.Kind())
			}
		}
		if err := r.checkProjectParents(pf, seen); err != nil {
			return err
		}
	}
	return nil
}

func (r *resolver) registry(s Source) (map[string]MCPServer, error) {
	if reg, ok := r.regs[s.ID()]; ok {
		return reg, nil
	}
	reg := map[string]MCPServer{}
	if root := s.Root(); root != "" {
		b, resolved, err := readConfined(root, registryRel, MaxRegistrySize)
		switch {
		case err == nil:
			reg, err = ParseRegistry(b, filepath.Base(resolved))
			if err != nil {
				return nil, fmt.Errorf("MCP registry of %s: %w", s.ID(), err)
			}
		case errors.Is(err, fs.ErrNotExist):
		default:
			return nil, fmt.Errorf("MCP registry of %s: %w", s.ID(), err)
		}
	}
	r.regs[s.ID()] = reg
	return reg, nil
}

func (r *resolver) merge(res *Resolved, req *File) error {
	rm := req.Manifest
	var m Manifest
	m.Name, m.Description, m.Owner = rm.Name, rm.Description, rm.Owner
	m.Status, m.SupersededBy = rm.Status, rm.SupersededBy
	m.Extends, m.WhenToUse, m.AvoidWhen = rm.Extends, rm.WhenToUse, rm.AvoidWhen

	var inc, exc, off, nameOnly, servers []string
	excAt := map[string]string{}
	offAt := map[string]string{}
	listers := map[string][]*File{}
	env := map[string]string{}
	var promptFrom *File

	for _, f := range res.Chain {
		c := f.Manifest
		if c.Status == StatusDeprecated {
			r.warn("profile %s is deprecated; use %s", f.Name, c.SupersededBy)
		}
		for _, id := range c.Plugins.Include {
			if by, ok := excAt[id]; ok {
				r.warn("profile %s includes %s but %s excludes it; exclusion wins", f.Name, id, by)
			}
			inc = appendUnique(inc, id)
		}
		for _, id := range c.Plugins.Exclude {
			exc = appendUnique(exc, id)
			if _, ok := excAt[id]; !ok {
				excAt[id] = f.Name
			}
		}
		for _, s := range c.Skills.NameOnly {
			if by, ok := offAt[s]; ok {
				r.warn("profile %s sets skill %s to name-only but %s turns it off; off wins", f.Name, s, by)
			}
			nameOnly = appendUnique(nameOnly, s)
		}
		for _, s := range c.Skills.Off {
			off = appendUnique(off, s)
			if _, ok := offAt[s]; !ok {
				offAt[s] = f.Name
			}
		}
		for _, s := range c.MCP.Servers {
			servers = appendUnique(servers, s)
			listers[s] = append(listers[s], f)
		}
		if c.Plugins.Mode != "" {
			m.Plugins.Mode = c.Plugins.Mode
		}
		if c.MCP.ClaudeAIConnectors != "" {
			m.MCP.ClaudeAIConnectors = c.MCP.ClaudeAIConnectors
		}
		if c.MCP.Strict != nil {
			v := *c.MCP.Strict
			m.MCP.Strict = &v
		}
		if c.Session.Model != "" {
			m.Session.Model = c.Session.Model
		}
		if c.Session.Effort != "" {
			m.Session.Effort = c.Session.Effort
		}
		if c.Session.AppendSystemPromptFile != "" {
			m.Session.AppendSystemPromptFile = c.Session.AppendSystemPromptFile
			promptFrom = f
		}
		if c.Session.InheritUserSettings != nil {
			v := *c.Session.InheritUserSettings
			m.Session.InheritUserSettings = &v
		}
		for k, v := range c.Session.Env {
			env[k] = v
		}
		if c.Policy.OnBlocked != "" {
			m.Policy.OnBlocked = c.Policy.OnBlocked
		}
		if c.Account != "" {
			m.Account = c.Account
		}
	}
	m.Plugins.Include = subtract(inc, exc)
	m.Plugins.Exclude = exc
	m.Skills.Off = off
	m.Skills.NameOnly = subtract(nameOnly, off)
	m.MCP.Servers = servers
	if len(env) > 0 {
		m.Session.Env = env
	}
	m = m.WithDefaults()
	res.Merged = m

	if req.Source.Kind() != KindPersonal && !m.InheritsUserSettings() {
		return fmt.Errorf("%w: profile %q", ErrSharedDropsUserLayer, req.Name)
	}

	res.MCP = map[string]MCPServer{}
	for _, name := range servers {
		var found []MCPServer
		for _, f := range listers[name] {
			reg, err := r.registry(f.Source)
			if err != nil {
				return err
			}
			if s, ok := reg[name]; ok {
				found = append(found, s)
			}
		}
		if len(found) == 0 {
			return fmt.Errorf("%w: %q is not defined in the registry (%s) of the source of %s", ErrUnknownMCPServer, name, registryRel, listerNames(listers[name]))
		}
		first, _ := found[0].canonicalJSON()
		for _, s := range found[1:] {
			other, _ := s.canonicalJSON()
			if !bytes.Equal(first, other) {
				return fmt.Errorf("%w: MCP server %q is defined differently in more than one registry (no shadowing, SR3)", ErrCollision, name)
			}
		}
		res.MCP[name] = found[0]
	}

	if promptFrom != nil {
		root := promptFrom.Source.Root()
		if root == "" {
			return fmt.Errorf("%w: source %s has no root for %s", ErrPath, promptFrom.Source.ID(), m.Session.AppendSystemPromptFile)
		}
		b, _, err := readConfined(root, m.Session.AppendSystemPromptFile, MaxPromptSize)
		if err != nil {
			return fmt.Errorf("session.append_system_prompt_file of %s: %w", promptFrom.Name, err)
		}
		res.Prompt = normalizeNewlines(b)
	}
	return nil
}

func listerNames(files []*File) string {
	n := make([]string, len(files))
	for i, f := range files {
		n[i] = f.Name
	}
	return strings.Join(n, ", ")
}

func normalizeNewlines(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

func appendUnique(list []string, s string) []string {
	if contains(list, s) {
		return list
	}
	return append(list, s)
}

func subtract(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	k := make([]string, 0, len(m))
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

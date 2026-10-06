package trust

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ccshelf/ccshelf/internal/profile"
)

// ExitNeedsTrust is the process exit code for "needs trust" (cli.md).
const ExitNeedsTrust = 4

// State is the outcome of a trust check.
type State int

// States of a Verdict.
const (
	// Trusted means the closure matches an accepted entry, or needs none.
	Trusted State = iota
	// New means no entry exists for the profile and source.
	New
	// Changed means an entry exists but the closure differs from it.
	Changed
	// TagMoved means the same source and ref now resolve to another commit.
	TagMoved
	// ProjectUntrusted means the closure includes a project profile whose
	// repository has not been trusted.
	ProjectUntrusted
)

// String returns a lower-case name for the state.
func (s State) String() string {
	switch s {
	case Trusted:
		return "trusted"
	case New:
		return "new"
	case Changed:
		return "changed"
	case TagMoved:
		return "tag-moved"
	case ProjectUntrusted:
		return "project-untrusted"
	}
	return fmt.Sprintf("state(%d)", int(s))
}

// ChangeKind says how a closure item differs from the accepted one.
type ChangeKind string

// Change kinds.
const (
	Added   ChangeKind = "added"
	Removed ChangeKind = "removed"
	Altered ChangeKind = "changed"
)

// Change is one difference between the accepted closure and the current one.
type Change struct {
	Kind ChangeKind
	// Item is the new item, or the old one for a removal.
	Item profile.ClosureItem
	// Old and New are the item digests (empty on the missing side).
	Old, New string
	// Risky is true for registry entries, prompts, plugin includes and
	// profiles that set environment names.
	Risky bool
	// Detail is a short human description of a registry entry (what it runs
	// or connects to). It never contains environment values.
	Detail string
}

// Verdict is the result of Check.
type Verdict struct {
	State State
	// Profile is the requested profile name.
	Profile string
	// Changes lists every difference, sorted risky first.
	Changes []Change
	// Risky is true when any change is risky, or when nothing was accepted
	// before and the closure contains risky items.
	Risky bool
	// Ref, OldCommit and NewCommit are set for TagMoved.
	Ref, OldCommit, NewCommit string
	// Problem explains an inconsistent closure (never trusted).
	Problem string
}

type identity struct {
	needsEntry bool
	project    bool
	source     string
	ref        string
	commit     string
}

// locator is implemented by sources whose identity has a part that does not
// change with the commit (gitsource, pluginsource).
type locator interface {
	Locator() string
	Ref() string
}

// identify decides whether r needs a lockfile entry and under which key. Any
// non-personal source in the chain needs one (a personal profile that extends
// a shared one inherits its code), keyed by the most specific such source.
func identify(r *profile.Resolved) identity {
	var id identity
	for i := len(r.Chain) - 1; i >= 0; i-- {
		src := r.Chain[i].Source
		if src.Kind() == profile.KindPersonal {
			continue
		}
		if src.Kind() == profile.KindProject {
			id.project = true
		}
		if id.needsEntry {
			continue
		}
		id.needsEntry = true
		id.commit = src.Commit()
		if l, ok := src.(locator); ok {
			id.source, id.ref = l.Locator(), l.Ref()
		} else {
			id.source = profile.PortableSourceID(src)
		}
	}
	return id
}

// closureHash returns the hash of r's items, failing when r.Closure.Hash does
// not match them.
func closureHash(r *profile.Resolved) (string, error) {
	h := profile.HashItems(r.Closure.Items)
	if h != r.Closure.Hash {
		return "", ErrInconsistentClosure
	}
	return h, nil
}

// Check compares the closure of r with the lockfile. It hashes r.Closure as
// given and never re-reads files: callers must generate the settings and
// prompts they run from the same Resolved value they checked (SR2), and must
// not modify it between Check and use. Personal-only closures are always
// Trusted. A closure that includes a project profile is ProjectUntrusted
// here; use CheckWithProject once the repository has been trusted.
func (s *Store) Check(r *profile.Resolved) Verdict { return s.CheckWithProject(r, false) }

// CheckWithProject is Check for callers that know whether the project folder
// is trusted (see ProjectStore and ProjectAllowed). Project trust never
// replaces the lockfile: a trusted project profile still needs an entry.
func (s *Store) CheckWithProject(r *profile.Resolved, projectTrusted bool) Verdict {
	if r == nil {
		return Verdict{State: New, Problem: "no resolved profile"}
	}
	v := Verdict{Profile: r.Name}
	id := identify(r)
	if !id.needsEntry {
		v.State = Trusted
		return v
	}
	if id.project && !projectTrusted {
		v.State = ProjectUntrusted
		v.Changes = diff(nil, r)
		v.Risky = anyRisky(v.Changes)
		return v
	}
	hash, err := closureHash(r)
	if err != nil {
		return Verdict{State: Changed, Profile: r.Name, Risky: true, Problem: err.Error()}
	}
	s.mu.Lock()
	var old *Entry
	if e := s.find(r.Name, id.source); e != nil {
		c := *e
		old = &c
	}
	s.mu.Unlock()
	if old == nil {
		v.State = New
		v.Changes = diff(nil, r)
		v.Risky = anyRisky(v.Changes)
		return v
	}
	v.Changes = diff(old.Items, r)
	v.Risky = anyRisky(v.Changes)
	switch {
	case old.Ref != "" && old.Ref == id.ref && old.Commit != "" && id.commit != "" && old.Commit != id.commit:
		v.State = TagMoved
		v.Ref, v.OldCommit, v.NewCommit = id.ref, old.Commit, id.commit
		v.Risky = true
	case old.ClosureHash == hash:
		v.State = Trusted
		v.Changes = nil
		v.Risky = false
	default:
		v.State = Changed
	}
	return v
}

func anyRisky(c []Change) bool {
	for _, x := range c {
		if x.Risky {
			return true
		}
	}
	return false
}

type itemKey struct{ kind, name string }

// diff compares stored items with the current closure of r.
func diff(old []profile.ClosureItem, r *profile.Resolved) []Change {
	oldM := map[itemKey]profile.ClosureItem{}
	for _, it := range old {
		oldM[itemKey{it.Kind, it.Name}] = it
	}
	var out []Change
	seen := map[itemKey]bool{}
	for _, it := range r.Closure.Items {
		k := itemKey{it.Kind, it.Name}
		seen[k] = true
		detail := ""
		if it.Kind == profile.ItemRegistry {
			if m, ok := r.MCP[it.Name]; ok {
				detail = describeServer(m)
			}
		}
		o, had := oldM[k]
		switch {
		case !had:
			out = append(out, Change{Kind: Added, Item: it, New: it.Digest, Risky: it.Risky, Detail: detail})
		case o.Digest != it.Digest || o.Risky != it.Risky:
			out = append(out, Change{Kind: Altered, Item: it, Old: o.Digest, New: it.Digest, Risky: it.Risky || o.Risky, Detail: detail})
		}
	}
	for _, it := range old {
		if !seen[itemKey{it.Kind, it.Name}] {
			out = append(out, Change{Kind: Removed, Item: it, Old: it.Digest, Risky: it.Risky})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Risky != out[j].Risky {
			return out[i].Risky
		}
		a, b := out[i].Item, out[j].Item
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	return out
}

// secretLike reports whether a name suggests that its value is a secret.
func secretLike(s string) bool {
	l := strings.ToLower(s)
	for _, w := range []string{"token", "secret", "password", "passwd", "apikey", "api-key", "api_key", "auth", "bearer", "credential"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// redactArgs masks arguments that look like they carry a secret: the value of
// KEY=value pairs and of options whose name suggests a secret.
func redactArgs(args []string) []string {
	out := make([]string, len(args))
	mask := false
	for i, a := range args {
		switch {
		case mask:
			out[i] = "<redacted>"
			mask = false
		case strings.HasPrefix(a, "-") && secretLike(a) && !strings.Contains(a, "="):
			out[i] = a
			mask = true
		case strings.Contains(a, "=") && secretLike(a[:strings.Index(a, "=")]):
			out[i] = a[:strings.Index(a, "=")+1] + "<redacted>"
		default:
			out[i] = a
		}
	}
	return out
}

// describeServer says what a registry entry runs or connects to. Environment
// variable names are listed; values are never known here.
func describeServer(m profile.MCPServer) string {
	var b strings.Builder
	if m.Type == profile.MCPStdio || m.Type == "" || m.Command != "" {
		b.WriteString("runs: ")
		b.WriteString(strings.Join(append([]string{m.Command}, redactArgs(m.Args)...), " "))
	} else {
		b.WriteString("connects to: ")
		b.WriteString(redactURL(m.URL))
	}
	for _, o := range []struct {
		os string
		ov *profile.MCPOverride
	}{{"windows", m.Windows}, {"macos", m.MacOS}, {"linux", m.Linux}} {
		if o.ov != nil {
			b.WriteString("; on " + o.os + " runs: " + strings.Join(append([]string{o.ov.Command}, redactArgs(o.ov.Args)...), " "))
		}
	}
	if len(m.EnvRefs) > 0 {
		b.WriteString("; reads environment variable names: " + strings.Join(m.EnvRefs, ", "))
	}
	return b.String()
}

// redactURL drops credentials and the query string from a URL.
func redactURL(u string) string {
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i] + "?<redacted>"
	}
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 && !strings.Contains(rest[:at], "/") {
			u = u[:i+3] + "<redacted>@" + rest[at+1:]
		}
	}
	return u
}

// clean makes s safe to print: control characters (including escape
// sequences) become "?" and very long text is shortened.
func clean(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= 240 {
			b.WriteString("...")
			break
		}
		if unicode.IsControl(r) || r == utf8.RuneError {
			r = '?'
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

func (c Change) describe() string {
	name := clean(c.Item.Name)
	switch c.Item.Kind {
	case profile.ItemRegistry:
		switch c.Kind {
		case Added:
			return fmt.Sprintf("new MCP server %s %s", name, clean(c.Detail))
		case Altered:
			return fmt.Sprintf("MCP server %s changed and now %s", name, clean(c.Detail))
		default:
			return fmt.Sprintf("MCP server %s removed", name)
		}
	case profile.ItemPrompt:
		switch c.Kind {
		case Added:
			return fmt.Sprintf("system prompt file %s added", name)
		case Altered:
			return fmt.Sprintf("prompt text changed (%s)", name)
		default:
			return fmt.Sprintf("system prompt file %s removed", name)
		}
	case profile.ItemPlugin:
		return fmt.Sprintf("plugin %s %s", name, c.Kind)
	case profile.ItemProfile:
		s := fmt.Sprintf("profile %s %s", name, c.Kind)
		if c.Risky {
			s += " (sets environment variables)"
		}
		return s
	case profile.ItemSource:
		return fmt.Sprintf("source %s %s", name, c.Kind)
	}
	return fmt.Sprintf("%s %s %s", clean(c.Item.Kind), name, c.Kind)
}

// Describe writes a plain-words summary for a person: risky changes first,
// then the others. It prints names and commands, never secret values.
func (v Verdict) Describe(w io.Writer) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format+"\n", a...) }
	name := clean(v.Profile)
	switch v.State {
	case Trusted:
		p("Profile %q is trusted.", name)
		return
	case New:
		p("Profile %q has not been trusted yet.", name)
	case Changed:
		p("Profile %q changed since you last trusted it.", name)
	case TagMoved:
		p("Profile %q: the ref %q now points to commit %s (you trusted %s). Treat this as an untrusted update.", name, clean(v.Ref), short(v.NewCommit), short(v.OldCommit))
	case ProjectUntrusted:
		p("Profile %q comes from a project folder that has not been trusted.", name)
	}
	if v.Problem != "" {
		p("Problem: %s", clean(v.Problem))
	}
	var risky, other []Change
	for _, c := range v.Changes {
		if c.Risky {
			risky = append(risky, c)
		} else {
			other = append(other, c)
		}
	}
	if len(risky) > 0 {
		p("Needs your review (these run code or change what the model is told):")
		for _, c := range risky {
			p("  - %s", c.describe())
		}
	}
	if len(other) > 0 {
		p("Other changes:")
		for _, c := range other {
			p("  - %s", c.describe())
		}
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// NeedsTrustError reports that a profile cannot run until it is trusted. A
// command maps it to exit code 4 (ExitNeedsTrust).
type NeedsTrustError struct {
	Profile string
	Verdict Verdict
}

// Error describes what to do next.
func (e *NeedsTrustError) Error() string {
	switch e.Verdict.State {
	case ProjectUntrusted:
		return fmt.Sprintf("profile %q comes from an untrusted project folder; review it, then trust the project", clean(e.Profile))
	case TagMoved:
		return fmt.Sprintf("profile %q needs trust: ref %q now points to a different commit; review it with: ccshelf trust %s", clean(e.Profile), clean(e.Verdict.Ref), clean(e.Profile))
	case Changed:
		return fmt.Sprintf("profile %q needs trust: it changed since you accepted it; review it with: ccshelf trust %s", clean(e.Profile), clean(e.Profile))
	}
	return fmt.Sprintf("profile %q needs trust: review it with: ccshelf trust %s", clean(e.Profile), clean(e.Profile))
}

// ExitCode returns ExitNeedsTrust.
func (e *NeedsTrustError) ExitCode() int { return ExitNeedsTrust }

// Require returns nil only when the closure of r is Trusted, otherwise a
// *NeedsTrustError. It is what non-interactive callers use: they fail closed.
func (s *Store) Require(r *profile.Resolved) error { return s.RequireWithProject(r, false) }

// RequireWithProject is Require for callers that know the project folder is
// trusted.
func (s *Store) RequireWithProject(r *profile.Resolved, projectTrusted bool) error {
	v := s.CheckWithProject(r, projectTrusted)
	if v.State == Trusted {
		return nil
	}
	name := v.Profile
	if r != nil {
		name = r.Name
	}
	return &NeedsTrustError{Profile: name, Verdict: v}
}

// IsNeedsTrust reports whether err is or wraps a *NeedsTrustError.
func IsNeedsTrust(err error) bool {
	var nt *NeedsTrustError
	return errors.As(err, &nt)
}

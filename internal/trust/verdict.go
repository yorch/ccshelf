package trust

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
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
	// Detail describes a registry entry: what it runs or connects to, shown
	// verbatim (registry arguments are code, not secrets: secrets reach a
	// server only through env_refs, which list names).
	Detail string
	// Fields lists, for a profile-controls item, each field that differs
	// (environment variables added or removed, plugin includes and excludes,
	// inherit_user_settings, account, ...), one plain-words line each.
	Fields []string
	// SetsEnv is true when the profile controls set environment variables.
	SetsEnv bool
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
	// Hash is the closure hash this verdict describes: what a person reviews
	// is what Accept must be given. It is empty only when the closure is
	// inconsistent (Problem is set) or r was nil.
	Hash string
	// Ref, OldCommit and NewCommit are set for TagMoved; MovedSource names
	// the source whose ref moved.
	Ref, OldCommit, NewCommit, MovedSource string
	// Problem explains an inconsistent closure (never trusted).
	Problem string
}

type identity struct {
	needsEntry bool
	project    bool
	// source, ref and commit are those of the most specific shared source.
	source  string
	ref     string
	commit  string
	sources []SourceRecord // every shared source, most specific first
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
// The ref and commit of every shared source are recorded, not only of the
// key source, so a tag that moved in an inherited source is noticed as well.
func identify(r *profile.Resolved) identity {
	var id identity
	seen := map[string]bool{}
	for i := len(r.Chain) - 1; i >= 0; i-- {
		src := r.Chain[i].Source
		if src.Kind() == profile.KindPersonal {
			continue
		}
		if src.Kind() == profile.KindProject {
			id.project = true
		}
		rec := SourceRecord{Commit: src.Commit()}
		if l, ok := src.(locator); ok {
			rec.Source, rec.Ref = l.Locator(), l.Ref()
		} else {
			rec.Source = profile.PortableSourceID(src)
		}
		if seen[rec.Source] {
			continue
		}
		seen[rec.Source] = true
		if !id.needsEntry {
			id.needsEntry = true
			id.source, id.ref, id.commit = rec.Source, rec.Ref, rec.Commit
		}
		id.sources = append(id.sources, rec)
	}
	return id
}

// movedSource returns the first source (most specific first) that kept its
// ref but now resolves to another commit.
func movedSource(old *Entry, now []SourceRecord) (was, is SourceRecord, ok bool) {
	recorded := old.Sources
	if len(recorded) == 0 {
		recorded = []SourceRecord{{Source: old.Source, Ref: old.Ref, Commit: old.Commit}}
	}
	for _, cur := range now {
		for _, prev := range recorded {
			if prev.Source == cur.Source && prev.Ref != "" && prev.Ref == cur.Ref &&
				prev.Commit != "" && cur.Commit != "" && prev.Commit != cur.Commit {
				return prev, cur, true
			}
		}
	}
	return SourceRecord{}, SourceRecord{}, false
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
	hash, err := closureHash(r)
	if err != nil {
		return Verdict{State: Changed, Profile: r.Name, Risky: true, Problem: err.Error()}
	}
	v.Hash = hash
	id := identify(r)
	if !id.needsEntry {
		v.State = Trusted
		return v
	}
	if id.project && !projectTrusted {
		v.State = ProjectUntrusted
		v.Changes = diff(nil, nil, r)
		v.Risky = anyRisky(v.Changes)
		return v
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
		v.Changes = diff(nil, nil, r)
		v.Risky = anyRisky(v.Changes)
		return v
	}
	v.Changes = diff(old.Items, old.Controls, r)
	v.Risky = anyRisky(v.Changes)
	if was, is, moved := movedSource(old, id.sources); moved {
		v.State = TagMoved
		v.MovedSource, v.Ref, v.OldCommit, v.NewCommit = is.Source, is.Ref, was.Commit, is.Commit
		v.Risky = true
		return v
	}
	if old.ClosureHash == hash {
		v.State = Trusted
		v.Changes = nil
		v.Risky = false
		return v
	}
	v.State = Changed
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

// diff compares stored items (and the stored controls texts) with the
// closure of r.
func diff(old []profile.ClosureItem, oldControls map[string]string, r *profile.Resolved) []Change {
	oldM := map[itemKey]profile.ClosureItem{}
	for _, it := range old {
		oldM[itemKey{it.Kind, it.Name}] = it
	}
	newControls := map[string]string{}
	for _, f := range r.Chain {
		if f != nil && f.Manifest != nil {
			if b, err := profile.ControlsJSON(f.Manifest); err == nil {
				newControls[f.Name] = string(b)
			}
		}
	}
	var out []Change
	seen := map[itemKey]bool{}
	for _, it := range r.Closure.Items {
		k := itemKey{it.Kind, it.Name}
		seen[k] = true
		o, had := oldM[k]
		if had && o.Digest == it.Digest && o.Risky == it.Risky {
			continue
		}
		c := Change{Kind: Altered, Item: it, Old: o.Digest, New: it.Digest, Risky: it.Risky || o.Risky}
		if !had {
			c.Kind = Added
			c.Risky = it.Risky
		}
		switch it.Kind {
		case profile.ItemRegistry:
			if m, ok := r.MCP[it.Name]; ok {
				c.Detail = describeServer(m)
			}
		case profile.ItemProfileControls:
			oldText := ""
			if had {
				oldText = oldControls[it.Name]
			}
			c.Fields = controlLines(had, oldText, newControls[it.Name])
			c.SetsEnv = setsEnv(newControls[it.Name])
		}
		out = append(out, c)
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

func decodeControls(text string) map[string]any {
	m := map[string]any{}
	if text == "" {
		return m
	}
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		return map[string]any{}
	}
	return m
}

func setsEnv(text string) bool {
	env, _ := decodeControls(text)["session.env"].(map[string]any)
	return len(env) > 0
}

// isEmptyValue reports whether a decoded controls value says "nothing set".
func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// valueText formats one decoded controls value for a person.
func valueText(v any) string {
	switch x := v.(type) {
	case nil:
		return "unset"
	case string:
		if x == "" {
			return "unset"
		}
		return x
	case bool:
		return fmt.Sprint(x)
	case []any:
		if len(x) == 0 {
			return "none"
		}
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = valueText(e)
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprint(v)
}

func stringSet(v any) (set map[string]bool, order []string) {
	set = map[string]bool{}
	list, _ := v.([]any)
	for _, e := range list {
		str := valueText(e)
		if !set[str] {
			order = append(order, str)
		}
		set[str] = true
	}
	return set, order
}

// controlLines explains how a profile's controls differ: one line per field.
// hadOld says whether an earlier version exists; when it does but its text was
// not recorded (an older lockfile) the line says so instead of guessing.
func controlLines(hadOld bool, oldText, newText string) []string {
	var lines []string
	if hadOld && oldText == "" {
		lines = append(lines, "what changed is unknown: the earlier version of these settings was not recorded; they are now:")
		hadOld = false
	}
	oldC, newC := decodeControls(oldText), decodeControls(newText)
	keys := map[string]bool{}
	for k := range oldC {
		keys[k] = true
	}
	for k := range newC {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		ov, nv := oldC[k], newC[k]
		if isEmptyValue(ov) && isEmptyValue(nv) || reflect.DeepEqual(ov, nv) {
			continue
		}
		if nvm, ok := nv.(map[string]any); ok {
			lines = append(lines, envLines(ov, nvm)...)
			continue
		}
		if ovm, ok := ov.(map[string]any); ok {
			lines = append(lines, envLines(ovm, map[string]any{})...)
			continue
		}
		_, isList := nv.([]any)
		if _, oldList := ov.([]any); isList || oldList {
			lines = append(lines, listLines(k, ov, nv)...)
			continue
		}
		if !hadOld {
			lines = append(lines, fmt.Sprintf("%s: %s", k, valueText(nv)))
		} else {
			lines = append(lines, fmt.Sprintf("%s: %s -> %s", k, valueText(ov), valueText(nv)))
		}
	}
	return lines
}

func envLines(ov any, nv map[string]any) []string {
	oldEnv, _ := ov.(map[string]any)
	var names []string
	seen := map[string]bool{}
	for k := range nv {
		names = append(names, k)
		seen[k] = true
	}
	for k := range oldEnv {
		if !seen[k] {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	var lines []string
	for _, n := range names {
		o, had := oldEnv[n]
		nw, has := nv[n]
		switch {
		case has && !had:
			lines = append(lines, fmt.Sprintf("environment variable %s added", envShown(n, nw)))
		case had && !has:
			lines = append(lines, fmt.Sprintf("environment variable %s removed", n))
		case !reflect.DeepEqual(o, nw):
			if isRefName(n) || secretLike(n) {
				lines = append(lines, fmt.Sprintf("environment variable %s changed (the value changed; it is not shown)", n))
			} else {
				lines = append(lines, fmt.Sprintf("environment variable %s changed (was %s, now %s)", n, valueText(o), valueText(nw)))
			}
		}
	}
	return lines
}

// envShown formats a new environment variable: the name alone for a *_REF
// (a reference to a secret store), NAME=<redacted> for a secret-looking name,
// otherwise NAME=value.
func envShown(name string, v any) string {
	switch {
	case isRefName(name):
		return name
	case secretLike(name):
		return name + "=<redacted>"
	}
	return name + "=" + valueText(v)
}

func listLines(key string, ov, nv any) []string {
	oldSet, oldOrder := stringSet(ov)
	newSet, newOrder := stringSet(nv)
	var lines []string
	for _, e := range newOrder {
		if !oldSet[e] {
			lines = append(lines, fmt.Sprintf("%s: %s added", key, e))
		}
	}
	for _, e := range oldOrder {
		if !newSet[e] {
			lines = append(lines, fmt.Sprintf("%s: %s removed", key, e))
		}
	}
	if len(lines) == 0 && !reflect.DeepEqual(ov, nv) {
		lines = append(lines, fmt.Sprintf("%s: order changed (now %s)", key, valueText(nv)))
	}
	return lines
}

// describeServer says what a registry entry runs or connects to, verbatim:
// arguments and URLs are what a person must review, and secrets never appear
// in a registry (they reach a server through env_refs, which list names).
// Environment variable values are never known here.
func describeServer(m profile.MCPServer) string {
	var b strings.Builder
	if m.Type == profile.MCPStdio || m.Type == "" || m.Command != "" {
		b.WriteString("runs: ")
		b.WriteString(strings.Join(append([]string{m.Command}, m.Args...), " "))
	} else {
		b.WriteString("connects to: ")
		b.WriteString(redactURL(m.URL))
	}
	for _, o := range []struct {
		os string
		ov *profile.MCPOverride
	}{{"windows", m.Windows}, {"macos", m.MacOS}, {"linux", m.Linux}} {
		if o.ov != nil {
			b.WriteString("; on " + o.os + " runs: " + strings.Join(append([]string{o.ov.Command}, o.ov.Args...), " "))
		}
	}
	if len(m.EnvRefs) > 0 {
		b.WriteString("; reads environment variable names: " + strings.Join(m.EnvRefs, ", "))
	}
	return b.String()
}

// redactURL keeps the scheme, host and path of a URL and masks credentials,
// the query string and the fragment. Registry validation already rejects
// those parts; this is defense in depth for what is printed.
func redactURL(u string) string {
	tail := ""
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u, tail = u[:i], "?<redacted>"
	}
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		host := rest
		if sl := strings.IndexByte(rest, '/'); sl >= 0 {
			host = rest[:sl]
		}
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			u = u[:i+3] + "<redacted>@" + rest[at+1:]
		}
	}
	return u + tail
}

// secretLike reports whether an environment variable name suggests that its
// value is a secret.
func secretLike(name string) bool {
	l := strings.ToLower(name)
	for _, w := range []string{"token", "secret", "password", "passwd", "key", "credential", "auth", "bearer"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// isRefName reports whether an environment variable name is a *_REF: its
// value points into a secret store and is never shown.
func isRefName(name string) bool { return strings.HasSuffix(strings.ToUpper(name), "_REF") }

// clean makes s safe to print on one line: control characters (including
// ESC, CR, backspace and the C1 range), invisible and bidirectional formatting
// characters, line separators and invalid UTF-8 become U+FFFD. It never
// shortens the text: a long value is wrapped by Describe, not cut, because
// what is hidden cannot be reviewed.
func clean(s string) string { return ui.SanitizeLine(s) }

// wrapWidth is the column Describe wraps long lines at.
const wrapWidth = 100

// wrapText splits s into lines of at most width runes, preferring spaces and
// breaking inside a word only when it is longer than a line. No character is
// dropped except the single space a line is broken at.
func wrapText(s string, width int) []string {
	var lines []string
	for utf8.RuneCountInString(s) > width {
		cut := 0
		n := 0
		for i, r := range s {
			if n == width {
				break
			}
			if r == ' ' && n > 0 {
				cut = i
			}
			n++
		}
		if cut == 0 {
			// no space in the first width runes: break inside the word
			n = 0
			for i := range s {
				if n == width {
					cut = i
					break
				}
				n++
			}
			lines = append(lines, s[:cut])
			s = s[cut:]
			continue
		}
		lines = append(lines, s[:cut])
		s = s[cut+1:]
	}
	return append(lines, s)
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
		return fmt.Sprintf("profile %s %s", name, c.Kind)
	case profile.ItemProfileControls:
		s := fmt.Sprintf("profile %s controls %s", name, c.Kind)
		if c.SetsEnv {
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
		p("Profile %q: the ref %q of %s now points to commit %s (you trusted %s). Treat this as an untrusted update.", name, clean(v.Ref), clean(v.MovedSource), clean(short(v.NewCommit)), clean(short(v.OldCommit)))
	case ProjectUntrusted:
		p("Profile %q comes from a project folder that has not been trusted.", name)
	}
	if v.Problem != "" {
		item(w, "Problem: ", clean(v.Problem))
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
			describeChange(w, c)
		}
	}
	if len(other) > 0 {
		p("Other changes:")
		for _, c := range other {
			describeChange(w, c)
		}
	}
}

// item writes text wrapped to wrapWidth, the first line after first and the
// others after the same number of spaces.
func item(w io.Writer, first, text string) {
	pad := strings.Repeat(" ", utf8.RuneCountInString(first))
	for i, line := range wrapText(text, wrapWidth) {
		lead := pad
		if i == 0 {
			lead = first
		}
		_, _ = fmt.Fprintf(w, "%s%s\n", lead, line)
	}
}

// describeChange writes one change, then the fields of a controls change.
func describeChange(w io.Writer, c Change) {
	item(w, "  - ", c.describe())
	for _, f := range c.Fields {
		item(w, "      * ", clean(f))
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
	if r == nil {
		return &NeedsTrustError{Verdict: s.CheckWithProject(nil, projectTrusted)}
	}
	v := s.CheckWithProject(r, projectTrusted)
	if v.State == Trusted {
		return nil
	}
	if v.Problem != "" {
		// A programming error or a tampered value, not something a person
		// can review and accept: it is not a "needs trust" outcome.
		return fmt.Errorf("profile %q: %w", clean(r.Name), ErrInconsistentClosure)
	}
	return &NeedsTrustError{Profile: r.Name, Verdict: v}
}

// IsNeedsTrust reports whether err is or wraps a *NeedsTrustError.
func IsNeedsTrust(err error) bool {
	var nt *NeedsTrustError
	return errors.As(err, &nt)
}

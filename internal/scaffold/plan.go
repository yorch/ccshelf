package scaffold

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"text/template"

	"github.com/yorch/ccshelf/internal/catalog/codeowners"
	"github.com/yorch/ccshelf/internal/catalog/lint"
	"github.com/yorch/ccshelf/internal/marketplace"
	"github.com/yorch/ccshelf/internal/orgconfig"
	"github.com/yorch/ccshelf/internal/ui"
)

//go:embed templates/*
var templates embed.FS

// Action is what a plan does with one file.
type Action string

// Actions.
const (
	// ActionCreate writes a file that does not exist.
	ActionCreate Action = "create"
	// ActionSkip leaves an existing file alone (Reason says whether it is
	// already up to date).
	ActionSkip Action = "skip-exists"
	// ActionMerge leaves an existing file alone and carries a Suggestion of
	// what to add to it.
	ActionMerge Action = "needs-merge"
	// ActionOverwrite replaces an existing file after saving <file>.bak; it is
	// planned only with Params.Force.
	ActionOverwrite Action = "overwrite"
)

// Entry is one file of the plan.
type Entry struct {
	// Path is the slash-separated path below the target directory.
	Path   string
	Group  Group
	Action Action
	// Reason explains skip, merge and overwrite entries.
	Reason string
	// Content is what create and overwrite write.
	Content []byte
	// Suggestion is the text for a needs-merge file, written to
	// Path+SuggestionSuffix on request.
	Suggestion []byte
	// Old holds the bytes an overwrite saves as Path+BackupSuffix.
	Old     []byte
	OldMode fs.FileMode
}

// Plan is the result of Build.
type Plan struct {
	Mode    Mode
	Entries []Entry
	// Notes are facts about the target the user should know (for example
	// plugin directories that the marketplace does not list).
	Notes []string
	// Todos are the placeholders and pins the generated files still need.
	Todos []string
	// HasGit reports whether the target already has a .git entry.
	HasGit bool
	// NeedsPin is true when a workflow that this plan writes still has the
	// placeholder ccshelf action reference.
	NeedsPin bool
	// PinFiles are those workflows, sorted.
	PinFiles []string
}

// MarkerFiles returns the files this plan creates or replaces that contain
// the TODO(ccshelf) placeholder, sorted. The README is not counted: it quotes
// the marker to explain it.
func (p *Plan) MarkerFiles() []string {
	var out []string
	for _, e := range p.Entries {
		if (e.Action == ActionCreate || e.Action == ActionOverwrite) && e.Path != pathReadme && bytes.Contains(e.Content, []byte(Placeholder)) {
			out = append(out, e.Path)
		}
	}
	sort.Strings(out)
	return out
}

// Count returns the number of entries with action a.
func (p *Plan) Count(a Action) int {
	n := 0
	for _, e := range p.Entries {
		if e.Action == a {
			n++
		}
	}
	return n
}

// Changes reports whether Apply would write anything: a create or an
// overwrite (suggestions are written only on request and are not counted).
func (p *Plan) Changes() bool { return p.Count(ActionCreate)+p.Count(ActionOverwrite) > 0 }

// target is a plugin that gets a sidecar stub.
type target struct {
	Name, Dir, Description, Author string
}

type builder struct {
	fs   FS
	p    *Params
	plan *Plan

	missing map[string]string
	order   []string

	mkt     *marketplace.Marketplace
	mktSeen bool // the marketplace file exists (parsed or not)
	cfg     *orgconfig.Config
	cfgSeen bool
	co      *codeowners.File
	coPath  string
	found   []foundPlugin
	targets []target
	// listed is true when targets come from an existing marketplace file.
	listed bool
	name   string
}

// Build reads the target through fsys and plans the files. It writes nothing.
// The errors are: *FieldError (an invalid value or an invalid combination),
// *MissingError (values needed for the plan are missing) and plain errors for
// conflicts such as a directory where a file belongs.
func Build(fsys FS, p Params) (*Plan, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	b := &builder{fs: fsys, p: &p, plan: &Plan{}, missing: map[string]string{}}
	if err := b.run(); err != nil {
		return nil, err
	}
	if len(b.order) > 0 {
		me := &MissingError{}
		for _, f := range b.order {
			me.Flags = append(me.Flags, f)
			me.Hints = append(me.Hints, b.missing[f])
		}
		return nil, me
	}
	sort.SliceStable(b.plan.Entries, func(i, j int) bool { return b.plan.Entries[i].Path < b.plan.Entries[j].Path })
	return b.plan, nil
}

func (b *builder) need(flag, hint string) {
	if _, ok := b.missing[flag]; !ok {
		b.missing[flag] = hint
		b.order = append(b.order, flag)
	}
}

func (b *builder) note(format string, a ...any) {
	b.plan.Notes = append(b.plan.Notes, fmt.Sprintf(format, a...))
}

func (b *builder) run() error {
	if err := b.detectMode(); err != nil {
		return err
	}
	if err := b.load(); err != nil {
		return err
	}
	b.computeTargets()
	b.addConfig()
	if err := b.addMarketplace(); err != nil {
		return err
	}
	b.addSidecars()
	b.addCodeowners()
	for _, w := range []string{"validate", "catalog", "release"} {
		b.addWorkflow(w)
	}
	if b.p.BranchSource != "" && b.p.Enabled(GroupWorkflows) {
		b.note("the default branch %s of the workflows was read from %s of the existing repository (read only); pass --default-branch to use another", b.p.branch(), b.p.BranchSource)
	}
	if len(b.plan.PinFiles) > 0 {
		sort.Strings(b.plan.PinFiles)
		b.addTodoOnce("pin the ccshelf action in " + strings.Join(b.plan.PinFiles, " and ") + ": a full 40-character commit SHA and the matching version (or re-run with --ccshelf-ref <sha> --ccshelf-version <tag>), then delete the guard job and its needs line; until then the guard job fails with a clear message and the other jobs are skipped")
	}
	b.addReadme()
	b.addLines(pathAttributes, GroupGitattributes, "gitattributes.tmpl")
	b.addLines(pathIgnore, GroupGitignore, "gitignore.tmpl")
	b.addExampleProfile()
	if err := b.firstConflict(); err != nil {
		return err
	}
	return b.writeCollision()
}

// writeCollision stops the plan, before anything is written, when two files it
// writes would be one file on a case-insensitive file system (Windows and the
// default macOS one): the second create would fail halfway through the run.
func (b *builder) writeCollision() error {
	var paths []string
	for _, e := range b.plan.Entries {
		if e.Action == ActionCreate || e.Action == ActionOverwrite {
			paths = append(paths, e.Path)
		}
	}
	if pair := pathCollision(paths); pair != "" {
		return fmt.Errorf("%s differ only in letter case, so they would be one file on Windows and on the default macOS file system; rename one of the plugins (nothing was written)", pair)
	}
	return nil
}

// actionConflict marks an entry that cannot be planned (a directory where a
// file belongs, an existing backup). file() records it and Build reports the
// first one after the whole plan is built, so that every missing flag is still
// listed first. It never reaches a returned Plan.
const actionConflict Action = "conflict"

func (b *builder) firstConflict() error {
	for _, e := range b.plan.Entries {
		if e.Action == actionConflict {
			return fmt.Errorf("%s: %s", e.Path, e.Reason)
		}
	}
	return nil
}

// detectMode decides new or adopt from the root of the target.
func (b *builder) detectMode() error {
	ents, err := b.fs.ReadDir(".")
	empty := true
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("reading the target directory: %w", err)
	default:
		for _, e := range ents {
			if e.Name() == ".git" {
				b.plan.HasGit = true
				continue
			}
			empty = false
		}
	}
	switch {
	case b.p.Mode == ModeNew && !empty:
		return &FieldError{"--mode", "the directory is not empty; use --mode adopt to add the missing files without changing existing ones"}
	case b.p.Mode != "":
		b.plan.Mode = b.p.Mode
	case empty:
		b.plan.Mode = ModeNew
	default:
		b.plan.Mode = ModeAdopt
	}
	return nil
}

// readExisting reads a regular file that may or may not exist. It returns
// seen=false for a missing file; a file that is not a regular file or cannot
// be read is reported with a note and seen=true, data=nil.
func (b *builder) readExisting(path string) (data []byte, seen bool) {
	data, err := b.fs.ReadFile(path, maxReadSize)
	switch {
	case err == nil:
		return data, true
	case errors.Is(err, fs.ErrNotExist):
		return nil, false
	default:
		b.note("%s exists but was not read: %s", path, ui.SanitizeLine(err.Error()))
		return nil, true
	}
}

// load reads the existing marketplace, org config and CODEOWNERS.
func (b *builder) load() error {
	if data, seen := b.readExisting(pathMarketplace); seen {
		b.mktSeen = true
		if data != nil {
			if m, err := marketplace.Parse(data); err != nil {
				b.note("%s exists but is not a valid marketplace file (%s); it is left alone and no sidecars are generated from it", pathMarketplace, ui.SanitizeLine(err.Error()))
			} else if _, err := marketplace.ExactKeys(data); err != nil {
				b.note("%s exists but is ambiguous (%s); it is left alone and no sidecars are generated from it", pathMarketplace, ui.SanitizeLine(err.Error()))
			} else {
				b.mkt = m
			}
		}
	}
	b.name = b.p.MarketplaceName
	if b.mkt != nil {
		switch {
		case ValidMarketplaceName(b.mkt.Name):
			if b.p.MarketplaceName != "" && b.p.MarketplaceName != b.mkt.Name {
				return &FieldError{"--marketplace-name", fmt.Sprintf("%s already names the marketplace %q; leave the flag out or pass that name", pathMarketplace, b.mkt.Name)}
			}
			b.name = b.mkt.Name
		default:
			// The name is untrusted text that would end up in generated files:
			// it is never used. The file itself is left alone.
			b.note("%s names the marketplace %q, which is not a valid marketplace name (%s); it is not used: pass --marketplace-name for the generated files (the file itself is not changed)", pathMarketplace, ui.SanitizeLine(b.mkt.Name), MarketplaceNameRe)
		}
	}
	b.cfg = orgconfig.Default()
	if data, seen := b.readExisting(pathConfig); seen {
		b.cfgSeen = true
		if data != nil {
			if c, err := orgconfig.Parse(data); err != nil {
				b.note("%s exists but is not valid (%s); it is left alone and the default lint.require is assumed for the sidecars", pathConfig, ui.SanitizeLine(firstLine(err.Error())))
			} else {
				b.cfg = c
			}
		}
	}
	b.coPath = pathCodeowners
	for _, loc := range codeowners.Locations() {
		data, seen := b.readExisting(loc)
		if !seen {
			continue
		}
		b.coPath = loc
		if data != nil {
			if f, err := codeowners.Parse(data); err == nil {
				b.co = f
			}
		}
		break
	}
	return nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// platform returns the platform owners: the flag, else the ones of an
// existing ccshelf.toml.
func (b *builder) platform() []string {
	if len(b.p.PlatformOwners) > 0 {
		return b.p.PlatformOwners
	}
	if b.cfgSeen {
		var out []string
		for _, o := range b.cfg.Lint.PlatformOwners {
			if ValidOwner(o) {
				out = append(out, o)
			}
		}
		return out
	}
	return nil
}

// willWrite reports whether the file of group g at path is created or replaced.
func (b *builder) willWrite(path string, g Group) bool {
	if !b.p.Enabled(g) {
		return false
	}
	_, err := b.fs.Lstat(path)
	return err != nil || b.p.Force
}

// requirePlatform records --platform-owners as missing when there are none.
func (b *builder) requirePlatform(what string) ([]string, bool) {
	if pl := b.platform(); len(pl) > 0 {
		return pl, true
	}
	b.need("--platform-owners", "who owns "+what+": @user, @org/team or an email address")
	return nil, false
}

// computeTargets decides which plugins get sidecar stubs.
func (b *builder) computeTargets() {
	if !b.p.Enabled(GroupSidecars) && !b.p.Enabled(GroupMarketplace) && !b.p.Enabled(GroupCodeowners) {
		return
	}
	found, notes := discoverPlugins(b.fs)
	b.found = found
	b.plan.Notes = append(b.plan.Notes, notes...)
	if b.mkt == nil {
		for _, f := range found {
			b.targets = append(b.targets, target(f))
		}
		return
	}
	b.listed = true
	listedDirs := map[string]bool{}
	listedNames := map[string]bool{}
	seenTargets := map[string]string{}
	for _, p := range b.mkt.Plugins {
		listedNames[p.Name] = true
		dir := p.Source.LocalPath()
		if dir != "" {
			listedDirs[dir] = true
		}
		if strings.HasPrefix(p.Name, bundlePrefix) && strings.HasPrefix(dir, bundlesDir+"/") {
			continue // generated bundles have no sidecar
		}
		if ok, why := portableName(p.Name); !ok {
			b.note("the marketplace entry %q was skipped: %s, so it cannot be a sidecar file name", ui.SanitizeLine(p.Name), why)
			continue
		}
		if dir != "" && !safeSourceDir(dir) {
			b.note("the source %q of the marketplace entry %q is not a plain directory below the repository (letters, digits, . _ - and / only, no \"..\"): its sidecar is generated, but no CODEOWNERS rule is", ui.SanitizeLine(dir), p.Name)
			dir = ""
		}
		if prev, dup := seenTargets[p.Name]; dup {
			b.note("the marketplace entry %q is listed twice (%s and %s); only the first gets a sidecar", p.Name, prev, ui.SanitizeLine(p.Source.Summary()))
			continue
		}
		seenTargets[p.Name] = ui.SanitizeLine(p.Source.Summary())
		b.targets = append(b.targets, target{Name: p.Name, Dir: dir, Description: p.Description})
	}
	var unlisted []string
	for _, f := range found {
		if !listedDirs[f.Dir] && !listedNames[f.Name] {
			unlisted = append(unlisted, f.Dir)
		}
	}
	if len(unlisted) > 0 {
		b.note("%s hold plugins that %s does not list: add entries, then run catalog init again to get their sidecars", strings.Join(unlisted, ", "), pathMarketplace)
	}
}

// view is the data of the text templates. Every value is validated or
// encoded: nothing here can carry markup of the target format.
type view struct {
	Pin             pin
	Comment         string
	Runner          string // validated label, safe in a YAML single-quoted scalar
	Branch          string // validated branch name, safe in a YAML double-quoted scalar
	Org             string // Markdown-escaped
	MarketplaceCode string // Markdown code span
	BranchCode      string // Markdown code span
	PlatformOwners  string // Markdown code spans
	PluginRef       string // TOML array holding one string
}

func render(name string, v view) ([]byte, error) {
	t, err := template.New(name).Delims("[[", "]]").Option("missingkey=error").ParseFS(templates, "templates/"+name)
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, v); err != nil {
		return nil, fmt.Errorf("template %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

func (b *builder) comment(pn pin) string {
	switch {
	case pn.Pinned:
		return pn.Version
	case pn.NeedSHA:
		return placeholder + " replace with the full commit SHA of a released ccshelf commit"
	default:
		return placeholder + " set version to the release tag of this commit"
	}
}

// gen produces the content of a file. strict is true when the file will be
// written, so missing values must be reported as needed; when false the
// content is only wanted for comparison, and a missing value gives ok=false.
type gen func(strict bool) (data []byte, ok bool)

// kind selects what happens to an existing file that differs.
type kind int

const (
	kindPlain kind = iota // left alone
	kindLines             // needs-merge with the missing lines
	kindRules             // needs-merge with the missing CODEOWNERS rules
	kindNever             // never replaced, not even with --force
)

// file plans one file.
func (b *builder) file(path string, g Group, k kind, content gen, rules func() []byte) {
	if !b.p.Enabled(g) {
		return
	}
	add := func(e Entry) {
		e.Path, e.Group = path, g
		b.plan.Entries = append(b.plan.Entries, e)
	}
	fi, err := b.fs.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if data, ok := content(true); ok {
			add(Entry{Action: ActionCreate, Content: data})
		}
		return
	case errors.Is(err, ErrSymlink):
		add(Entry{Action: ActionSkip, Reason: "a path component is a symbolic link; left alone"})
		return
	case err != nil:
		add(Entry{Action: actionConflict, Reason: "cannot be inspected: " + ui.SanitizeLine(err.Error())})
		return
	case fi.Mode()&fs.ModeSymlink != 0:
		if b.p.Force {
			add(Entry{Action: actionConflict, Reason: "is a symbolic link; --force does not replace links"})
			return
		}
		add(Entry{Action: ActionSkip, Reason: "is a symbolic link; left alone"})
		return
	case !fi.Mode().IsRegular():
		add(Entry{Action: actionConflict, Reason: "exists and is not a regular file"})
		return
	}
	old, err := b.fs.ReadFile(path, maxReadSize)
	if err != nil {
		add(Entry{Action: ActionSkip, Reason: "exists; not compared (" + ui.SanitizeLine(err.Error()) + ")"})
		return
	}
	want, ok := content(b.p.Force && k != kindNever)
	if ok && bytes.Equal(old, want) {
		add(Entry{Action: ActionSkip, Reason: "up to date"})
		return
	}
	if k == kindNever {
		add(Entry{Action: ActionSkip, Reason: "exists; never rewritten (edit it by hand)"})
		return
	}
	if b.p.Force && ok {
		if _, err := b.fs.Lstat(path + BackupSuffix); err == nil {
			add(Entry{Action: actionConflict, Reason: "the backup " + path + BackupSuffix + " already exists; move it away or remove it first"})
			return
		}
		add(Entry{
			Action: ActionOverwrite, Content: want, Old: old, OldMode: fi.Mode().Perm(),
			Reason: "replaces the existing file; the old one is saved as " + path + BackupSuffix,
		})
		return
	}
	if !ok {
		add(Entry{Action: ActionSkip, Reason: "exists; left alone"})
		return
	}
	switch k {
	case kindLines:
		if s := missingLines(old, want); len(s) > 0 {
			add(Entry{Action: ActionMerge, Suggestion: s, Reason: "exists; the lines to add are in the suggestion"})
			b.noteBlockedSuggestion(path)
			return
		}
		add(Entry{Action: ActionSkip, Reason: "exists and already has every generated line"})
	case kindRules:
		if rules == nil {
			add(Entry{Action: ActionSkip, Reason: "exists; left alone"})
			return
		}
		if s := rules(); len(s) > 0 {
			add(Entry{Action: ActionMerge, Suggestion: s, Reason: "exists; the rules to add are in the suggestion"})
			b.noteBlockedSuggestion(path)
			return
		}
		add(Entry{Action: ActionSkip, Reason: "exists and already covers every generated rule"})
	default:
		add(Entry{Action: ActionSkip, Reason: "exists; left alone"})
	}
}

// noteBlockedSuggestion notes a <file>.ccshelf-suggested that exists and was
// not written by this tool: --write-suggestions would not replace it.
func (b *builder) noteBlockedSuggestion(path string) {
	sp := path + SuggestionSuffix
	data, err := b.fs.ReadFile(sp, maxReadSize)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil || !isOurSuggestion(data):
		b.note("%s exists and was not written by ccshelf: --write-suggestions does not replace it (move it away first)", sp)
	}
}

// missingLines returns a suggestion made of the generated lines (comments and
// blanks excluded from the comparison) that the existing file lacks, or nil.
func missingLines(existing, want []byte) []byte {
	have := map[string]bool{}
	for _, l := range strings.Split(string(existing), "\n") {
		if t := strings.Join(strings.Fields(l), " "); t != "" && !strings.HasPrefix(t, "#") {
			have[t] = true
		}
	}
	var out []string
	for _, l := range strings.Split(string(want), "\n") {
		t := strings.Join(strings.Fields(l), " ")
		if t == "" || strings.HasPrefix(t, "#") || have[t] {
			continue
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		return nil
	}
	return []byte(suggestionHeads[0] + " adding to the existing file.\n" + strings.Join(out, "\n") + "\n")
}

func (b *builder) addLines(path string, g Group, tmpl string) {
	b.file(path, g, kindLines, func(bool) ([]byte, bool) {
		data, err := templates.ReadFile("templates/" + tmpl)
		return data, err == nil
	}, nil)
}

func (b *builder) addReadme() {
	b.file(pathReadme, GroupReadme, kindPlain, func(strict bool) ([]byte, bool) {
		if b.name == "" {
			if strict {
				b.need("--marketplace-name", "the name of the marketplace, lower case letters, digits and hyphens")
			}
			return nil, false
		}
		pl, ok := b.platformForText(strict)
		if !ok {
			return nil, false
		}
		codes := make([]string, len(pl))
		for i, o := range pl {
			codes[i] = mdCode(o)
		}
		data, err := render("README.md.tmpl", view{Org: mdEscape(b.p.org(b.name)), MarketplaceCode: mdCode(b.name), BranchCode: mdCode(b.p.branch()), PlatformOwners: strings.Join(codes, ", ")})
		return data, err == nil
	}, nil)
}

// platformForText returns the platform owners for a generated text file.
func (b *builder) platformForText(strict bool) ([]string, bool) {
	if strict {
		return b.requirePlatform("what runs code on developer machines or shapes the catalog")
	}
	pl := b.platform()
	return pl, len(pl) > 0
}

func (b *builder) addWorkflow(name string) {
	b.file(".github/workflows/"+name+".yml", GroupWorkflows, kindPlain, func(strict bool) ([]byte, bool) {
		pn := b.p.pin()
		if strict && !pn.Pinned && name != "release" {
			b.plan.NeedsPin = true
			b.plan.PinFiles = append(b.plan.PinFiles, ".github/workflows/"+name+".yml")
		}
		data, err := render(name+".yml.tmpl", view{Pin: pn, Comment: b.comment(pn), Runner: b.p.runner(), Branch: b.p.branch()})
		return data, err == nil
	}, nil)
}

func (b *builder) addTodoOnce(s string) {
	for _, t := range b.plan.Todos {
		if t == s {
			return
		}
	}
	b.plan.Todos = append(b.plan.Todos, s)
}

func (b *builder) addExampleProfile() {
	b.file("profiles/example.toml.sample", GroupExampleProfile, kindPlain, func(bool) ([]byte, bool) {
		m := b.name
		if m == "" {
			m = "your-marketplace"
		}
		data, err := render("example-profile.toml.sample.tmpl", view{PluginRef: tomlArray([]string{"my-plugin@" + m})})
		return data, err == nil
	}, nil)
}

// Placeholder is the text written into values a person must fill in; lint
// reports it as CAT048.
const Placeholder = lint.PlaceholderMarker

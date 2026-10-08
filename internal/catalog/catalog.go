package catalog

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yorch/ccshelf/internal/catalog/gitdata"
	"github.com/yorch/ccshelf/internal/catalog/lint"
	"github.com/yorch/ccshelf/internal/orgconfig"
)

// Version is the catalog.json format version.
const Version = 1

// Limits applied by Build to text from the repo.
const (
	maxName     = 128
	maxShort    = 200
	maxDesc     = 1000
	maxListLen  = 50
	maxURL      = 2048
	defaultName = "Plugin catalog"
)

// GitInfo is optional history data for a plugin directory.
type GitInfo struct {
	// LastCommit is YYYY-MM-DD (UTC).
	LastCommit string `json:"last_commit,omitempty"`
	// Authors is the number of distinct commit authors.
	Authors int `json:"authors,omitempty"`
}

// Entry is one plugin in the catalog.
type Entry struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name,omitempty"`
	Description string   `json:"description"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags"`
	Version     string   `json:"version,omitempty"`
	Author      string   `json:"author,omitempty"`
	// Marketplace is the name of the marketplace that lists the plugin.
	Marketplace string `json:"marketplace"`
	// Source is a short description of where the plugin comes from, for
	// example "plugins/design-kit" or "github:acme/figma-bridge".
	Source   string `json:"source,omitempty"`
	External bool   `json:"external,omitempty"`
	// Homepage, Repository and Docs are kept only when they are http or https URLs.
	Homepage   string `json:"homepage,omitempty"`
	Repository string `json:"repository,omitempty"`
	License    string `json:"license,omitempty"`

	Owner        string   `json:"owner,omitempty"`
	Status       string   `json:"status,omitempty"`
	WhenToUse    []string `json:"when_to_use"`
	AvoidWhen    []string `json:"avoid_when"`
	OverlapsWith []string `json:"overlaps_with"`
	SupersededBy string   `json:"superseded_by,omitempty"`
	ReviewBy     string   `json:"review_by,omitempty"`
	Support      string   `json:"support,omitempty"`
	Docs         string   `json:"docs,omitempty"`

	HasHooks bool `json:"has_hooks,omitempty"`
	HasMCP   bool `json:"has_mcp,omitempty"`
	// NeedsPlatformReview is true when the plugin ships hooks or MCP servers.
	NeedsPlatformReview bool `json:"needs_platform_review,omitempty"`
	Skills              int  `json:"skills,omitempty"`
	Agents              int  `json:"agents,omitempty"`
	Commands            int  `json:"commands,omitempty"`

	Git *GitInfo `json:"git,omitempty"`
	// ChangedSinceTag is true when the plugin directory changed since
	// Catalog.LatestTag (git data only).
	ChangedSinceTag bool `json:"changed_since_tag,omitempty"`
}

// ProfileInfo describes a profile for the catalog. The profiles package parses
// profile manifests, and the caller fills this in.
type ProfileInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Status      string   `json:"status,omitempty"`
	WhenToUse   []string `json:"when_to_use"`
}

// Catalog is the whole catalog, the content of catalog.json.
type Catalog struct {
	Version int    `json:"version"`
	Title   string `json:"title"`
	// GeneratedAt is RFC 3339 UTC, present only when Options.Now was set.
	GeneratedAt  string   `json:"generated_at,omitempty"`
	Marketplaces []string `json:"marketplaces"`
	// LatestTag is the newest git tag, present with git data.
	LatestTag  string        `json:"latest_tag,omitempty"`
	Plugins    []Entry       `json:"plugins"`
	Profiles   []ProfileInfo `json:"profiles"`
	Categories []string      `json:"categories"`
	Tags       []string      `json:"tags"`
}

// Options tune Build.
type Options struct {
	// Now, when set, stamps GeneratedAt and drives the lint's review dates.
	// Without it, the catalog has no timestamp and the lint uses the wall clock.
	Now func() time.Time
	// GitData adds last-change, author-count and changed-since-tag data.
	// catalog.git_data in ccshelf.toml also enables it.
	GitData bool
	// Profiles are the profiles that the catalog shows. The caller supplies them.
	Profiles []ProfileInfo
}

// Build loads the repo at root and builds the catalog. Build also returns the
// lint report: a repo with lint errors still produces a catalog, so a preview
// can show the findings next to it. The error is only for failures that stop
// the build (unusable root, git data requested but failing).
func Build(root string, cfg *orgconfig.Config, opt Options) (*Catalog, *lint.Report, error) {
	return BuildContext(context.Background(), root, cfg, opt)
}

// BuildContext is Build with a context for the git commands.
func BuildContext(ctx context.Context, root string, cfg *orgconfig.Config, opt Options) (*Catalog, *lint.Report, error) {
	if cfg == nil {
		cfg = orgconfig.Default()
	}
	data, err := lint.LoadData(root, cfg)
	if err != nil {
		return nil, nil, err
	}
	report := lint.Check(data, cfg, lint.Options{Now: opt.Now})

	c := &Catalog{
		Version:      Version,
		Title:        Text(cfg.Catalog.Title, maxShort),
		Marketplaces: append([]string{}, cfg.Catalog.Marketplaces...),
		Plugins:      []Entry{},
		Profiles:     []ProfileInfo{},
	}
	if c.Title == "" {
		c.Title = defaultName
	}
	if opt.Now != nil {
		c.GeneratedAt = opt.Now().UTC().Format(time.RFC3339)
	}

	dirs := map[string]string{} // plugin name -> directory
	for _, ref := range data.Plugins {
		if ref.Dup || ref.IsBundle() {
			continue
		}
		e := entryFor(ref, data)
		c.Plugins = append(c.Plugins, e)
		if d := ref.PluginDir(); d != "" && d != "." && ref.InfoErr == nil {
			dirs[e.Name] = d
		}
	}
	sort.SliceStable(c.Plugins, func(i, j int) bool {
		if c.Plugins[i].Name != c.Plugins[j].Name {
			return c.Plugins[i].Name < c.Plugins[j].Name
		}
		return c.Plugins[i].Marketplace < c.Plugins[j].Marketplace
	})

	if opt.GitData || cfg.Catalog.GitData {
		if err := addGitData(ctx, root, c, dirs, cfg.Catalog.ReleaseTagPattern); err != nil {
			return nil, nil, fmt.Errorf("git data: %w", err)
		}
	}

	cats, tags := map[string]bool{}, map[string]bool{}
	for _, e := range c.Plugins {
		if e.Category != "" {
			cats[e.Category] = true
		}
		for _, t := range e.Tags {
			tags[t] = true
		}
	}
	c.Categories, c.Tags = sortedKeys(cats), sortedKeys(tags)

	for _, p := range opt.Profiles {
		c.Profiles = append(c.Profiles, ProfileInfo{
			Name: Text(p.Name, maxName), Description: Text(p.Description, maxDesc), Owner: Text(p.Owner, maxShort),
			Status: Text(p.Status, 32), WhenToUse: textList(p.WhenToUse, maxShort),
		})
	}
	sort.SliceStable(c.Profiles, func(i, j int) bool { return c.Profiles[i].Name < c.Profiles[j].Name })
	return c, report, nil
}

func entryFor(ref lint.PluginRef, d *lint.Data) Entry {
	p := ref.Plugin
	e := Entry{
		Name:        Text(p.Name, maxName),
		DisplayName: Text(p.DisplayName, maxName),
		Description: Text(p.Description, maxDesc),
		Category:    Text(p.Category, 64),
		Tags:        textList(p.Tags, 64),
		Version:     Text(p.Version, 64),
		Author:      Text(p.Author.String(), maxName),
		Marketplace: Text(ref.MarketplaceName, maxName),
		Source:      Text(p.Source.Summary(), maxShort),
		External:    !p.Source.IsLocal(),
		Homepage:    Link(p.Homepage),
		Repository:  Link(p.Repository),
		License:     Text(p.License, 64),
		WhenToUse:   []string{}, AvoidWhen: []string{}, OverlapsWith: []string{},
	}
	if info := ref.Info; info != nil && !info.External {
		e.HasHooks, e.HasMCP = info.HasHooks, info.HasMCP
		e.NeedsPlatformReview = info.HasHooks || info.HasMCP
		e.Skills, e.Agents, e.Commands = info.Skills, info.Agents, info.Commands
		if e.Description == "" {
			e.Description = Text(info.Description, maxDesc)
		}
		if e.Version == "" {
			e.Version = Text(info.Version, 64)
		}
	}
	if sc := d.Sidecars[p.Name]; sc != nil {
		e.Owner = Text(sc.Owner, maxShort)
		e.Status = Text(sc.Status, 32)
		e.WhenToUse = textList(sc.WhenToUse, maxShort)
		e.AvoidWhen = textList(sc.AvoidWhen, maxShort)
		e.OverlapsWith = textList(sc.OverlapsWith, maxName)
		e.SupersededBy = Text(sc.SupersededBy, maxName)
		e.ReviewBy = Text(sc.ReviewBy, 16)
		e.Support = Text(sc.Support, maxShort)
		e.Docs = Link(sc.Docs)
	}
	return e
}

func addGitData(ctx context.Context, root string, c *Catalog, dirs map[string]string, tagPattern string) error {
	if !gitdata.IsRepo(ctx, root) {
		return errors.New("the repository root is not inside a git work tree")
	}
	if shallow, err := gitdata.IsShallow(ctx, root); err != nil {
		return err
	} else if shallow {
		return fmt.Errorf("%w: fetch the full history (in GitHub Actions: actions/checkout with fetch-depth: 0, with fetch-tags if tags are needed) or turn catalog.git_data off", gitdata.ErrShallow)
	}
	var list []string
	seen := map[string]bool{}
	for _, d := range dirs {
		if !seen[d] {
			seen[d] = true
			list = append(list, d)
		}
	}
	sort.Strings(list)
	info, err := gitdata.Collect(ctx, root, list)
	if err != nil {
		return err
	}
	tag, err := gitdata.LatestReleaseTag(ctx, root, tagPattern)
	if err != nil {
		return err
	}
	changed := map[string]bool{}
	if tag != "" {
		ch, err := gitdata.ChangedSince(ctx, root, tag, list)
		if err != nil {
			return err
		}
		for _, d := range ch {
			changed[d] = true
		}
		c.LatestTag = Text(tag, 200)
	}
	for i := range c.Plugins {
		d, ok := dirs[c.Plugins[i].Name]
		if !ok {
			continue
		}
		gi := info[d]
		c.Plugins[i].Git = &GitInfo{LastCommit: gi.LastCommit, Authors: gi.Authors}
		c.Plugins[i].ChangedSinceTag = changed[d]
	}
	return nil
}

// Text makes untrusted text safe to store and show: control characters,
// DEL, C1 controls, bidirectional overrides and line breaks become spaces,
// runs of spaces collapse, the result is trimmed and cut to max runes (with an
// ellipsis).
func Text(s string, max int) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	space := true
	n := 0
	truncated := false
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) ||
			r == 0x200e || r == 0x200f || r == 0xfeff || r == 0x2028 || r == 0x2029 || unicode.IsSpace(r) {
			r = ' '
		}
		if r == ' ' {
			if space {
				continue
			}
			space = true
		} else {
			space = false
		}
		if n >= max {
			truncated = true
			break
		}
		b.WriteRune(r)
		n++
	}
	out := strings.TrimRight(b.String(), " ")
	if truncated {
		out += "…"
	}
	return out
}

func textList(in []string, max int) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := Text(s, max); t != "" {
			out = append(out, t)
		}
		if len(out) >= maxListLen {
			break
		}
	}
	return out
}

// Link returns s when it is an absolute http or https URL that is safe to
// render as a link, and "" otherwise.
func Link(s string) string {
	if len(s) > maxURL || !lint.IsHTTPURL(s) {
		return ""
	}
	return s
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

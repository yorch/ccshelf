package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// normalized returns a copy whose slices are never nil, so JSON has [] and
// never null.
func (c *Catalog) normalized() *Catalog {
	n := *c
	n.Marketplaces = nonNil(n.Marketplaces)
	n.Categories = nonNil(n.Categories)
	n.Tags = nonNil(n.Tags)
	n.Plugins = append([]Entry{}, n.Plugins...)
	for i := range n.Plugins {
		e := &n.Plugins[i]
		e.Tags, e.WhenToUse, e.AvoidWhen, e.OverlapsWith = nonNil(e.Tags), nonNil(e.WhenToUse), nonNil(e.AvoidWhen), nonNil(e.OverlapsWith)
	}
	n.Profiles = append([]ProfileInfo{}, n.Profiles...)
	for i := range n.Profiles {
		n.Profiles[i].WhenToUse = nonNil(n.Profiles[i].WhenToUse)
	}
	return &n
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// JSON renders catalog.json: stable key order, two-space indent, trailing
// newline. The encoder escapes <, > and & as <, > and &, so the
// bytes are also safe inside an HTML <script type="application/json"> block.
// schema/catalog.schema.json documents the format.
func JSON(c *Catalog) ([]byte, error) {
	out, err := json.MarshalIndent(c.normalized(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode catalog: %w", err)
	}
	return append(out, '\n'), nil
}

// mdEscaper backslash-escapes the characters Markdown (including GFM) treats
// specially inline, and turns HTML metacharacters into entities so no raw HTML
// can come out of plain text. Line breaks become spaces.
var mdEscaper = strings.NewReplacer(
	"\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)",
	"#", "\\#", "|", "\\|", "~", "\\~", "!", "\\!", "<", "&lt;", ">", "&gt;", "&", "&amp;",
	"\r\n", " ", "\n", " ", "\r", " ", "\t", " ",
	"@", "&#64;",
)

var wwwRe = regexp.MustCompile(`(?i)(www)\.`)

// MarkdownText escapes untrusted plain text for Markdown prose and tables.
// "@", the colon of "://" and the dot after "www" become numeric entities so a
// mention or an autolink cannot form.
func MarkdownText(s string) string {
	s = strings.ReplaceAll(mdEscaper.Replace(s), "://", "&#58;//")
	// GFM autolinks text that starts with "www." without any scheme.
	s = wwwRe.ReplaceAllString(s, "${1}&#46;")
	if strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "=") {
		s = "\\" + s
	}
	return s
}

// markdownCode renders s as an inline code span that cannot be closed early.
func markdownCode(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	if strings.ContainsAny(s, "`<>&") {
		// Code spans cannot carry entities; plain escaped text can.
		return MarkdownText(s)
	}
	s = strings.ReplaceAll(s, "|", "\\|")
	fence := "`"
	return fence + s + fence
}

// markdownLink renders [docs](url) for a URL already validated as http(s).
func markdownLink(raw string) string {
	if Link(raw) == "" {
		return ""
	}
	u := strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E", "|", "%7C", "\\", "%5C", "`", "%60", "[", "%5B", "]", "%5D").Replace(raw)
	if _, err := url.Parse(u); err != nil {
		return ""
	}
	return "[docs](" + u + ")"
}

func joinText(list []string, sep string) string {
	esc := make([]string, len(list))
	for i, s := range list {
		esc[i] = MarkdownText(s)
	}
	return strings.Join(esc, sep)
}

// Markdown renders CATALOG.md. It escapes all text. The result contains no
// raw HTML and the only links are http or https URLs.
func Markdown(c *Catalog) []byte {
	var b bytes.Buffer
	title := c.Title
	if title == "" {
		title = defaultName
	}
	fmt.Fprintf(&b, "# %s\n\n", MarkdownText(title))
	b.WriteString("ccshelf generates this file from the marketplace, the sidecar files and the plugin directories. Do not edit it by hand.\n\n")
	if c.GeneratedAt != "" {
		fmt.Fprintf(&b, "Generated at %s.\n\n", MarkdownText(c.GeneratedAt))
	}

	counts := map[string]int{}
	review := 0
	for _, e := range c.Plugins {
		counts[e.Status]++
		if e.NeedsPlatformReview {
			review++
		}
	}
	fmt.Fprintf(&b, "**%d plugins**: %d active, %d experimental, %d deprecated", len(c.Plugins), counts["active"], counts["experimental"], counts["deprecated"])
	if other := len(c.Plugins) - counts["active"] - counts["experimental"] - counts["deprecated"]; other > 0 {
		fmt.Fprintf(&b, ", %d without a valid status", other)
	}
	fmt.Fprintf(&b, ". %d need platform review (hooks or MCP servers).\n\n", review)

	// One table per category, current plugins only.
	byCat := map[string][]Entry{}
	for _, e := range c.Plugins {
		if e.Status == "deprecated" {
			continue
		}
		cat := e.Category
		if cat == "" {
			cat = "Uncategorized"
		}
		byCat[cat] = append(byCat[cat], e)
	}
	cats := make([]string, 0, len(byCat))
	for k := range byCat {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	for _, cat := range cats {
		fmt.Fprintf(&b, "## %s\n\n", MarkdownText(cat))
		b.WriteString("| Plugin | Description | Owner | Status | When to use | Docs |\n|---|---|---|---|---|---|\n")
		for _, e := range byCat[cat] {
			name := markdownCode(e.Name)
			if e.NeedsPlatformReview {
				name += " (needs platform review)"
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", name, MarkdownText(e.Description), markdownCode(e.Owner),
				MarkdownText(e.Status), joinText(e.WhenToUse, "; "), markdownLink(e.Docs))
		}
		b.WriteString("\n")
	}

	var deprecated []Entry
	for _, e := range c.Plugins {
		if e.Status == "deprecated" {
			deprecated = append(deprecated, e)
		}
	}
	if len(deprecated) > 0 {
		b.WriteString("## Deprecated\n\n| Plugin | Use instead | Description |\n|---|---|---|\n")
		for _, e := range deprecated {
			repl := markdownCode(e.SupersededBy)
			if repl == "" {
				repl = "none named"
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n", markdownCode(e.Name), repl, MarkdownText(e.Description))
		}
		b.WriteString("\n")
	}

	var overlaps []Entry
	for _, e := range c.Plugins {
		if len(e.OverlapsWith) > 0 {
			overlaps = append(overlaps, e)
		}
	}
	if len(overlaps) > 0 {
		b.WriteString("## Overlapping plugins\n\nPlugins a reader might confuse. Check the \"when to use\" and \"avoid when\" notes before picking one.\n\n")
		for _, e := range overlaps {
			codes := make([]string, len(e.OverlapsWith))
			for i, o := range e.OverlapsWith {
				codes[i] = markdownCode(o)
			}
			fmt.Fprintf(&b, "- %s overlaps with %s", markdownCode(e.Name), strings.Join(codes, ", "))
			if len(e.AvoidWhen) > 0 {
				fmt.Fprintf(&b, ". Avoid when: %s", joinText(e.AvoidWhen, "; "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	var needs []Entry
	for _, e := range c.Plugins {
		if e.NeedsPlatformReview {
			needs = append(needs, e)
		}
	}
	if len(needs) > 0 {
		b.WriteString("## Needs platform review\n\nThese plugins ship code that runs on developers' machines.\n\n")
		for _, e := range needs {
			var what []string
			if e.HasHooks {
				what = append(what, "hooks")
			}
			if e.HasMCP {
				what = append(what, "MCP servers")
			}
			fmt.Fprintf(&b, "- %s: %s\n", markdownCode(e.Name), strings.Join(what, " and "))
		}
		b.WriteString("\n")
	}

	if len(c.Profiles) > 0 {
		b.WriteString("## Profiles\n\n| Profile | Description | Owner | Status | When to use |\n|---|---|---|---|---|\n")
		for _, p := range c.Profiles {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", markdownCode(p.Name), MarkdownText(p.Description), markdownCode(p.Owner), MarkdownText(p.Status), joinText(p.WhenToUse, "; "))
		}
		b.WriteString("\n")
	}

	if c.LatestTag != "" {
		var changed []Entry
		for _, e := range c.Plugins {
			if e.ChangedSinceTag {
				changed = append(changed, e)
			}
		}
		fmt.Fprintf(&b, "## New or changed since %s\n\n", markdownCode(c.LatestTag))
		if len(changed) == 0 {
			b.WriteString("No plugin changed.\n\n")
		}
		for _, e := range changed {
			fmt.Fprintf(&b, "- %s", markdownCode(e.Name))
			if e.Git != nil && e.Git.LastCommit != "" {
				fmt.Fprintf(&b, " (last change %s, %d author(s))", MarkdownText(e.Git.LastCommit), e.Git.Authors)
			}
			b.WriteString("\n")
		}
		if len(changed) > 0 {
			b.WriteString("\n")
		}
	}
	return append(bytes.TrimRight(b.Bytes(), "\n"), '\n')
}

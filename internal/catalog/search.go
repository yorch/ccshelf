package catalog

import (
	"sort"
	"strings"
)

// Match is one search result.
type Match struct {
	Entry Entry
	// Score is higher for better matches.
	Score int
	// Fields lists where the query matched, in a fixed order.
	Fields []string
}

// Field weights for Search.
const (
	wNameExact  = 100
	wNamePrefix = 60
	wNameSub    = 40
	wDisplay    = 30
	wTag        = 20
	wCategory   = 15
	wWhenToUse  = 12
	wDesc       = 10
	wOwner      = 8
	deprecated  = -5
)

// Search scores the catalog entries against a query and returns the best
// limit matches (all of them when limit <= 0). The query is split into words;
// every word must match somewhere (case-insensitive substring) or the entry is
// left out. A word scores per field it matches: name (exact, prefix or
// substring), display name, tags, category, when_to_use, description and
// owner. Deprecated plugins score a little lower. Ties sort by name, so the
// order is deterministic. An empty query returns every entry by name.
func Search(c *Catalog, query string, limit int) []Match {
	words := strings.Fields(strings.ToLower(query))
	var out []Match
	for _, e := range c.Plugins {
		score, fields, ok := scoreEntry(e, words)
		if !ok {
			continue
		}
		out = append(out, Match{Entry: e, Score: score, Fields: fields})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Entry.Name < out[j].Entry.Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func scoreEntry(e Entry, words []string) (int, []string, bool) {
	total := 0
	hit := map[string]bool{}
	for _, w := range words {
		best := 0
		add := func(field string, weight int) {
			hit[field] = true
			total += weight
			best++
		}
		name := strings.ToLower(e.Name)
		switch {
		case name == w:
			add("name", wNameExact)
		case strings.HasPrefix(name, w):
			add("name", wNamePrefix)
		case strings.Contains(name, w):
			add("name", wNameSub)
		}
		if strings.Contains(strings.ToLower(e.DisplayName), w) {
			add("display_name", wDisplay)
		}
		for _, t := range e.Tags {
			if strings.EqualFold(t, w) || strings.Contains(strings.ToLower(t), w) {
				add("tags", wTag)
				break
			}
		}
		if strings.Contains(strings.ToLower(e.Category), w) {
			add("category", wCategory)
		}
		for _, s := range e.WhenToUse {
			if strings.Contains(strings.ToLower(s), w) {
				add("when_to_use", wWhenToUse)
				break
			}
		}
		if strings.Contains(strings.ToLower(e.Description), w) {
			add("description", wDesc)
		}
		if strings.Contains(strings.ToLower(e.Owner), w) {
			add("owner", wOwner)
		}
		if best == 0 {
			return 0, nil, false
		}
	}
	if len(words) > 0 && e.Status == "deprecated" {
		total += deprecated
	}
	order := []string{"name", "display_name", "tags", "category", "when_to_use", "description", "owner"}
	var fields []string
	for _, f := range order {
		if hit[f] {
			fields = append(fields, f)
		}
	}
	return total, fields, true
}

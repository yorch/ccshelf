package analytics

import (
	"sort"
	"strings"
)

// Counts are the usage numbers of one plugin over the window.
type Counts struct {
	// Installs is the number of installs (API: distinct installing users;
	// OTel: plugin_installed events).
	Installs int64 `json:"installs"`
	// Invocations is the number of uses (API: invocation_count; OTel:
	// skill_activated events).
	Invocations int64 `json:"invocations"`
	// Loads is the number of plugin_loaded events (OTel only).
	Loads int64 `json:"loads,omitempty"`
}

func (c *Counts) add(o Counts) {
	c.Installs += o.Installs
	c.Invocations += o.Invocations
	c.Loads += o.Loads
}

// Used reports whether the plugin was invoked at least once.
func (c Counts) Used() bool { return c.Invocations > 0 }

// Usage is plugin usage over a window.
type Usage struct {
	// PerPlugin is keyed by plugin id (name@marketplace) when known, else by
	// plugin name.
	PerPlugin map[string]Counts `json:"per_plugin"`
	// From and To are YYYY-MM-DD; To is inclusive for OTel data and
	// exclusive for API data (it echoes the request). Either may be empty.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Source is "api" or "otel".
	Source string `json:"source"`
	// Redacted counts events whose plugin could not be identified because
	// names were redacted (OTel only).
	Redacted int64 `json:"redacted,omitempty"`
	// Skipped counts input lines that could not be used (OTel only).
	Skipped int `json:"skipped,omitempty"`
}

func newUsage(source string) *Usage {
	return &Usage{PerPlugin: map[string]Counts{}, Source: source}
}

func (u *Usage) add(key string, c Counts) {
	cur := u.PerPlugin[key]
	cur.add(c)
	u.PerPlugin[key] = cur
}

// Lookup finds the counts of a plugin given as name@marketplace or as a bare
// name. An exact key wins; otherwise the counts of every key with the same
// name part are summed, so usage reported by name matches an id and the other
// way round. found is false when no key matches.
func (u *Usage) Lookup(id string) (c Counts, found bool) {
	if u == nil {
		return Counts{}, false
	}
	if v, ok := u.PerPlugin[id]; ok {
		return v, true
	}
	name := nameOf(id)
	for _, k := range u.Keys() {
		if nameOf(k) == name && (!strings.Contains(id, "@") || !strings.Contains(k, "@")) {
			c.add(u.PerPlugin[k])
			found = true
		}
	}
	return c, found
}

// Keys returns the plugin keys in sorted order.
func (u *Usage) Keys() []string {
	keys := make([]string, 0, len(u.PerPlugin))
	for k := range u.PerPlugin {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func nameOf(id string) string {
	if i := strings.LastIndex(id, "@"); i >= 0 {
		return id[:i]
	}
	return id
}

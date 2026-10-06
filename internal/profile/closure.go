package profile

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
)

// Closure item kinds.
const (
	ItemProfile  = "profile"
	ItemRegistry = "registry"
	ItemPrompt   = "prompt"
	ItemPlugin   = "plugin"
	ItemSource   = "source"
)

// ClosureItem is one thing the trust decision covers.
type ClosureItem struct {
	// Kind is one of the Item* constants.
	Kind string
	// Name identifies the item: profile name, MCP server name, prompt path,
	// plugin id or portable source id.
	Name string
	// Digest is a hex SHA-256 of the item's canonical content.
	Digest string
	// Risky marks items whose change needs review: registry entries, prompts
	// and plugin includes, and profiles that set session.env.
	Risky bool
}

// Closure is the resolved set a trust decision pins (SR2). Hash is identical
// on every OS for the same inputs: it never contains machine paths, line
// endings are normalized and items are sorted.
type Closure struct {
	Hash  string
	Items []ClosureItem
}

func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// PortableSourceID returns a machine-independent label for a source: directory
// sources become "dir:<kind>"; other ids are used as given.
func PortableSourceID(s Source) string {
	id := s.ID()
	if len(id) >= 4 && id[:4] == "dir:" {
		return "dir:" + s.Kind().String()
	}
	return id
}

func buildClosure(res *Resolved) (Closure, error) {
	var items []ClosureItem
	seenSrc := map[string]bool{}
	for _, f := range res.Chain {
		items = append(items, ClosureItem{Kind: ItemProfile, Name: f.Name, Digest: digest(normalizeNewlines(f.Raw)), Risky: len(f.Manifest.Session.Env) > 0})
		if !seenSrc[f.Source.ID()] {
			seenSrc[f.Source.ID()] = true
			p := PortableSourceID(f.Source)
			items = append(items, ClosureItem{Kind: ItemSource, Name: p, Digest: digest([]byte(p + "\x00" + f.Source.Commit()))})
		}
	}
	for _, n := range sortedKeys(res.MCP) {
		b, err := res.MCP[n].canonicalJSON()
		if err != nil {
			return Closure{}, err
		}
		items = append(items, ClosureItem{Kind: ItemRegistry, Name: n, Digest: digest(b), Risky: true})
	}
	if res.Merged.Session.AppendSystemPromptFile != "" {
		items = append(items, ClosureItem{Kind: ItemPrompt, Name: res.Merged.Session.AppendSystemPromptFile, Digest: digest(res.Prompt), Risky: true})
	}
	for _, id := range res.Merged.Plugins.Include {
		items = append(items, ClosureItem{Kind: ItemPlugin, Name: id, Digest: digest([]byte(id)), Risky: true})
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Digest < b.Digest
	})
	return Closure{Hash: hashItems(items), Items: items}, nil
}

func hashItems(items []ClosureItem) string {
	h := sha256.New()
	put := func(s string) {
		var n [binary.MaxVarintLen64]byte
		h.Write(n[:binary.PutUvarint(n[:], uint64(len(s)))])
		h.Write([]byte(s))
	}
	put("ccshelf-closure-v1")
	put(fmt.Sprint(len(items)))
	for _, it := range items {
		put(it.Kind)
		put(it.Name)
		put(it.Digest)
		if it.Risky {
			put("1")
		} else {
			put("0")
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

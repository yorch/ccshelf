package profile

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Closure item kinds.
const (
	// ItemProfile covers a profile's identity: name, description, owner,
	// status, when_to_use, avoid_when, model and effort.
	ItemProfile = "profile"
	// ItemProfileControls covers everything in a profile that changes what a
	// session can do. It is always Risky.
	ItemProfileControls = "profile-controls"
	ItemRegistry        = "registry"
	ItemPrompt          = "prompt"
	// ItemInstruction is one effective instructions file. Its Name is the
	// 1-based position and the path (for example "01 prompts/base.md"), so
	// that a change of the join order changes the closure hash.
	ItemInstruction = "instruction"
	ItemPlugin      = "plugin"
	ItemSource      = "source"
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
	// Risky marks items whose change needs review: profile controls, registry
	// entries, prompts and plugin includes.
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
// sources become "dir:<kind>". It uses other ids as given.
func PortableSourceID(s Source) string {
	id := s.ID()
	if len(id) >= 4 && id[:4] == "dir:" {
		return "dir:" + s.Kind().String()
	}
	return id
}

// profileIdentity is the canonical form of the descriptive part of a manifest.
type profileIdentity struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Owner        string   `json:"owner"`
	Status       string   `json:"status"`
	SupersededBy string   `json:"superseded_by"`
	WhenToUse    []string `json:"when_to_use"`
	AvoidWhen    []string `json:"avoid_when"`
	Model        string   `json:"session.model"`
	Effort       string   `json:"session.effort"`
}

// profileControls is the canonical form of everything in a manifest that
// changes what a session can do. Set-like lists are sorted so that reordering
// a list does not change the digest; extends keeps its order because it
// decides the merge order.
type profileControls struct {
	Account                string            `json:"account"`
	Extends                []string          `json:"extends"`
	PluginMode             string            `json:"plugins.mode"`
	PluginExclude          []string          `json:"plugins.exclude"`
	PluginInclude          []string          `json:"plugins.include"`
	SkillsOff              []string          `json:"skills.off"`
	SkillsNameOnly         []string          `json:"skills.name_only"`
	MCPServers             []string          `json:"mcp.servers"`
	MCPStrict              *bool             `json:"mcp.strict"`
	MCPClaudeAIConnectors  string            `json:"mcp.claudeai_connectors"`
	InheritUserSettings    *bool             `json:"session.inherit_user_settings"`
	Env                    map[string]string `json:"session.env"`
	AppendSystemPromptFile string            `json:"session.append_system_prompt_file"`
	OnBlocked              string            `json:"policy.on_blocked"`
	// The instructions members use omitempty so that the digest of a profile
	// without [instructions] stays what it was before the table existed.
	// Files keep their order: it decides the join order.
	InstructionsFiles   []string `json:"instructions.files,omitempty"`
	InstructionsInherit *bool    `json:"instructions.inherit,omitempty"`
	// OutputStyle uses omitempty so that the digest of a profile without
	// output_style stays what it was before the key existed. It is a control
	// because a custom style can replace the coding instructions of the
	// system prompt.
	OutputStyle string `json:"session.output_style,omitempty"`
}

func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

// profileDigests returns the digests of the identity and controls items of m.
func profileDigests(m *Manifest) (identity, controls string, err error) {
	id, err := json.Marshal(profileIdentity{
		Name: m.Name, Description: m.Description, Owner: m.Owner, Status: m.Status, SupersededBy: m.SupersededBy,
		WhenToUse: append([]string{}, m.WhenToUse...), AvoidWhen: append([]string{}, m.AvoidWhen...),
		Model: m.Session.Model, Effort: m.Session.Effort,
	})
	if err != nil {
		return "", "", fmt.Errorf("encoding profile %q: %w", m.Name, err)
	}
	env := m.Session.Env
	if env == nil {
		env = map[string]string{}
	}
	ctl, err := json.Marshal(profileControls{
		Account: m.Account, Extends: append([]string{}, m.Extends...),
		PluginMode: m.Plugins.Mode, PluginExclude: sortedCopy(m.Plugins.Exclude), PluginInclude: sortedCopy(m.Plugins.Include),
		SkillsOff: sortedCopy(m.Skills.Off), SkillsNameOnly: sortedCopy(m.Skills.NameOnly),
		MCPServers: sortedCopy(m.MCP.Servers), MCPStrict: m.MCP.Strict, MCPClaudeAIConnectors: m.MCP.ClaudeAIConnectors,
		InheritUserSettings: m.Session.InheritUserSettings, Env: env,
		AppendSystemPromptFile: m.Session.AppendSystemPromptFile, OnBlocked: m.Policy.OnBlocked,
		InstructionsFiles: append([]string(nil), m.Instructions.Files...), InstructionsInherit: m.Instructions.Inherit,
		OutputStyle: m.Session.OutputStyle,
	})
	if err != nil {
		return "", "", fmt.Errorf("encoding profile %q: %w", m.Name, err)
	}
	return digest(id), digest(ctl), nil
}

func buildClosure(res *Resolved) (Closure, error) {
	var items []ClosureItem
	seenSrc := map[string]bool{}
	for _, f := range res.Chain {
		id, ctl, err := profileDigests(f.Manifest)
		if err != nil {
			return Closure{}, err
		}
		items = append(items,
			ClosureItem{Kind: ItemProfile, Name: f.Name, Digest: id},
			ClosureItem{Kind: ItemProfileControls, Name: f.Name, Digest: ctl, Risky: true})
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
	if len(res.InstructionsText) > 0 {
		// The generated header is part of what Claude Code reads. It comes
		// from the profile name (already in the closure) and a fixed
		// template, so the template is pinned here: a new template needs
		// trust again.
		items = append(items, ClosureItem{Kind: ItemInstruction, Name: "00 header", Digest: digest([]byte(InstructionsHeader(res.Name))), Risky: true})
	}
	for i, f := range res.Instructions {
		items = append(items, ClosureItem{Kind: ItemInstruction, Name: fmt.Sprintf("%02d %s", i+1, f.Path), Digest: f.Digest, Risky: true})
	}
	for _, id := range res.Merged.Plugins.Include {
		items = append(items, ClosureItem{Kind: ItemPlugin, Name: id, Digest: digest([]byte(id)), Risky: true})
	}
	sortItems(items)
	return Closure{Hash: hashItems(items), Items: items}, nil
}

// sortItems puts closure items in canonical order: by kind, then name, then
// digest, so that items sharing kind and name (two sources with the same
// portable id and different commits) still have one fixed order.
func sortItems(items []ClosureItem) {
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
}

// hashItems returns a hex SHA-256 over the sorted items. Each field is
// length-prefixed, so two different item lists never hash the same input.
func hashItems(items []ClosureItem) string {
	h := sha256.New()
	put := func(s string) {
		var n [binary.MaxVarintLen64]byte
		h.Write(n[:binary.PutUvarint(n[:], uint64(len(s)))])
		h.Write([]byte(s))
	}
	put("ccshelf-closure-v2")
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

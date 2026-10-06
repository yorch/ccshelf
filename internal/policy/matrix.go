package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ccshelf/ccshelf/internal/claude"
)

// FeatureID names a launcher capability.
type FeatureID string

// Launcher capabilities, in display order (see AllFeatures).
const (
	// SettingsMasking is the generated --settings file (enabledPlugins,
	// skillOverrides, env). Always available.
	SettingsMasking FeatureID = "settings-masking"
	// HideConnectors is disableClaudeAiConnectors in the generated settings.
	HideConnectors FeatureID = "hide-connectors"
	// DenyMCPServers is deniedMcpServers in the generated settings.
	DenyMCPServers FeatureID = "deny-mcp-servers"
	// AddMCPConfig is the --mcp-config flag.
	AddMCPConfig FeatureID = "add-mcp-config"
	// StrictMCPConfig is the --strict-mcp-config flag. Claude Code exits at
	// startup when it is used while a managed-mcp.json is deployed, and it
	// counts as a sideload flag.
	StrictMCPConfig FeatureID = "strict-mcp-config"
	// PluginDir is --plugin-dir and CLAUDE_CODE_PLUGIN_DIRS.
	PluginDir FeatureID = "plugin-dir"
	// Agents is the --agents flag.
	Agents FeatureID = "agents"
	// SettingSources is the --setting-sources flag.
	SettingSources FeatureID = "setting-sources"
	// AppendSystemPromptFile is --append-system-prompt-file.
	AppendSystemPromptFile FeatureID = "append-system-prompt-file"
	// ForcedPlugins lists plugins that cannot be masked. Its state is
	// Blocked when there are any (masking them is impossible).
	ForcedPlugins FeatureID = "forced-plugins"
)

// AllFeatures is the stable display and JSON order.
var AllFeatures = []FeatureID{
	SettingsMasking, HideConnectors, DenyMCPServers, AddMCPConfig, StrictMCPConfig, PluginDir,
	Agents, SettingSources, AppendSystemPromptFile, ForcedPlugins,
}

// State is the availability of a feature.
type State string

// Feature states. Unknown is a valid state: server-managed settings cannot
// be read locally, and an unreadable local source hides what it enforces.
const (
	Available State = "available"
	Blocked   State = "blocked"
	Unknown   State = "unknown"
)

// Feature is the evaluated state of one capability.
type Feature struct {
	ID     FeatureID `json:"id"`
	State  State     `json:"state"`
	Reason string    `json:"reason"`
	// Source names the managed key or file behind a non-available state.
	Source string `json:"source,omitempty"`
	// Items lists plugin ids for ForcedPlugins.
	Items []string `json:"items,omitempty"`
}

// Matrix is the capability matrix for one machine.
type Matrix struct {
	Features map[FeatureID]Feature
	// Sources, Unknown and Warnings are copied from the Policy.
	Sources  []Source
	Unknown  []string
	Warnings []string
	// PartialVisibility and PartialReasons are copied from the Policy.
	PartialVisibility bool
	PartialReasons    []string
	// mcpFilters names the managed keys that filter MCP servers a profile
	// adds (allowedMcpServers, allowManagedMcpServersOnly, deniedMcpServers).
	mcpFilters []string
	// forced and forcedOff are sorted plugin ids.
	forced, forcedOff []string
}

// Evaluate computes the capability matrix. It is pure: no I/O, and it never
// runs claude. A nil policy means "no managed policy". installed supplies
// the "required by your org" marker of plugins.
func Evaluate(p *Policy, installed []claude.Plugin) *Matrix {
	if p == nil {
		p = &Policy{}
	}
	m := &Matrix{
		Features: map[FeatureID]Feature{},
		Sources:  p.Sources, Unknown: p.Unknown, Warnings: append([]string(nil), p.Warnings...),

		PartialVisibility: p.PartialVisibility, PartialReasons: p.PartialReasons,
	}
	if p.AllowedMcpServers != nil {
		m.mcpFilters = append(m.mcpFilters, "allowedMcpServers")
	}
	if p.AllowManagedMcpServersOnly != nil && *p.AllowManagedMcpServersOnly {
		m.mcpFilters = append(m.mcpFilters, "allowManagedMcpServersOnly")
	}
	if len(p.DeniedMcpServers) > 0 {
		m.mcpFilters = append(m.mcpFilters, "deniedMcpServers")
	}
	if p.ManagedMCPFile {
		m.Warnings = append(m.Warnings, "managed-mcp.json is deployed: --mcp-config and --strict-mcp-config make Claude Code exit at startup, so a profile cannot add MCP servers with them")
	}
	set := func(f Feature) { m.Features[f.ID] = f }

	set(Feature{
		ID: SettingsMasking, State: Available,
		Reason: "the generated --settings file is not a sideload flag and cannot be disabled by policy; keys that managed settings lock (force-enabled plugins) still win",
	})
	set(Feature{
		ID: HideConnectors, State: Available,
		Reason: "disableClaudeAiConnectors is valid in any settings file",
	})
	set(Feature{
		ID: DenyMCPServers, State: Available,
		Reason: "deniedMcpServers is valid in any settings file; entries need full server names such as plugin:context7:context7",
	})
	set(Feature{
		ID: AppendSystemPromptFile, State: Available,
		Reason: "not a sideload flag",
	})

	// Features that depend on sideload flags or an unreadable source.
	sideload := func(id FeatureID, flag string) Feature {
		switch {
		case p.DisableSideloadFlags != nil && *p.DisableSideloadFlags:
			return Feature{
				ID: id, State: Blocked, Source: "disableSideloadFlags",
				Reason: "managed settings set disableSideloadFlags, which rejects " + flag,
			}
		case p.Unreadable:
			return Feature{
				ID: id, State: Unknown,
				Reason: "a managed source exists but could not be read, so disableSideloadFlags is not known",
			}
		case p.PartialVisibility:
			return Feature{
				ID: id, State: Unknown,
				Reason: "managed policy is only partially visible from here (" + strings.Join(p.PartialReasons, "; ") + "), so disableSideloadFlags is not known",
			}
		}
		return Feature{ID: id, State: Available, Reason: "no readable managed policy sets disableSideloadFlags"}
	}
	set(sideload(PluginDir, "--plugin-dir and CLAUDE_CODE_PLUGIN_DIRS"))
	set(sideload(Agents, "--agents"))

	mcp := sideload(AddMCPConfig, "--mcp-config")
	strict := sideload(StrictMCPConfig, "--strict-mcp-config")
	if mcp.State != Blocked && p.ManagedMCPFile {
		mcp = Feature{
			ID: AddMCPConfig, State: Blocked, Source: "managed-mcp.json",
			Reason: "a managed-mcp.json is deployed: MCP is under exclusive control and Claude Code exits at startup when --mcp-config is used",
		}
	}
	if strict.State != Blocked && p.ManagedMCPFile {
		strict = Feature{
			ID: StrictMCPConfig, State: Blocked, Source: "managed-mcp.json",
			Reason: "a managed-mcp.json is deployed: Claude Code exits at startup when --strict-mcp-config, or any --mcp-config even an empty one, is used",
		}
	}
	if len(m.mcpFilters) > 0 && mcp.State != Blocked {
		mcp.Reason += "; managed " + strings.Join(m.mcpFilters, ", ") + " still filter the servers it adds"
	}
	set(mcp)
	set(strict)

	ss := Feature{ID: SettingSources, State: Available, Reason: "not restricted by any readable managed key"}
	switch {
	case p.Unreadable:
		ss = Feature{ID: SettingSources, State: Unknown, Reason: "a managed source exists but could not be read"}
	case p.PartialVisibility:
		ss = Feature{ID: SettingSources, State: Unknown, Reason: "managed policy is only partially visible from here (" + strings.Join(p.PartialReasons, "; ") + ")"}
	}
	set(ss)

	forced := map[string]bool{}
	off := map[string]bool{}
	for id, on := range p.EnabledPlugins {
		if on {
			forced[id] = true
		} else {
			off[id] = true
		}
	}
	for _, pl := range installed {
		if pl.RequiredByOrg {
			forced[pl.ID] = true
		}
	}
	m.forced, m.forcedOff = sortedKeys(forced), sortedKeys(off)
	fp := Feature{ID: ForcedPlugins, State: Available, Reason: "no plugin is force-enabled by policy"}
	if len(m.forced) > 0 {
		fp = Feature{
			ID: ForcedPlugins, State: Blocked, Source: "enabledPlugins / required by your org", Items: m.forced,
			Reason: "these plugins are force-enabled by policy and cannot be masked; they stay on in every profile",
		}
	}
	if fp.State == Available && p.PartialVisibility {
		fp = Feature{
			ID: ForcedPlugins, State: Unknown,
			Reason: "managed policy is only partially visible from here (" + strings.Join(p.PartialReasons, "; ") + "), so plugins it force-enables may not be listed",
		}
	}
	set(fp)
	return m
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// LockedPlugins returns the plugin ids that cannot be masked because policy
// force-enables them, sorted.
func (m *Matrix) LockedPlugins() []string { return append([]string(nil), m.forced...) }

// BlockedPlugins returns the plugin ids policy force-disables, sorted.
func (m *Matrix) BlockedPlugins() []string { return append([]string(nil), m.forcedOff...) }

// Needs says what a profile wants from the launcher.
type Needs struct {
	ExtraMCPServers        bool // --mcp-config
	StrictMCPConfig        bool // --strict-mcp-config
	PluginDir              bool // --plugin-dir
	Agents                 bool // --agents
	DropUserSettingSources bool // --setting-sources
	AppendSystemPromptFile bool
	HideConnectors         bool
	DenyMCPServers         bool
}

// Applied is what the launcher may do. A field is true only when the profile
// needs the feature and the matrix does not block it.
type Applied struct {
	ExtraMCPServers        bool
	StrictMCPConfig        bool
	PluginDir              bool
	Agents                 bool
	DropUserSettingSources bool
	AppendSystemPromptFile bool
	HideConnectors         bool
	DenyMCPServers         bool
	// Dropped lists blocked features the profile needed (on_blocked = warn).
	Dropped []FeatureID
	// Warnings are user-visible messages: dropped features, and features
	// whose state is unknown and are attempted anyway.
	Warnings []string
	// Locked lists plugins that cannot be masked.
	Locked []string
}

// BlockedError is returned by Plan when on_blocked is "fail" and a needed
// feature is blocked. It maps to exit code 3.
type BlockedError struct {
	Feature FeatureID
	Reason  string
}

// Error implements error.
func (e *BlockedError) Error() string {
	return fmt.Sprintf("feature %s is blocked by managed policy: %s", e.Feature, e.Reason)
}

// ExitCode is the process exit code for this error.
func (e *BlockedError) ExitCode() int { return 3 }

// ErrOnBlocked is wrapped by Plan for an invalid on_blocked value.
var ErrOnBlocked = errors.New("on_blocked must be \"warn\" or \"fail\"")

// Plan decides what the launcher may do for a profile's needs. onBlocked is
// "warn" (default when empty: drop the feature and record a warning) or
// "fail" (return *BlockedError). A feature in the Unknown state is attempted
// with a warning, never failed on: blocked state is only claimed from
// evidence, and claude is never run to find out.
func (m *Matrix) Plan(need Needs, onBlocked string) (*Applied, error) {
	switch onBlocked {
	case "", "warn", "fail":
	default:
		return nil, fmt.Errorf("%w (got %q)", ErrOnBlocked, onBlocked)
	}
	a := &Applied{Locked: m.LockedPlugins()}
	type req struct {
		id   FeatureID
		need bool
		dst  *bool
	}
	for _, r := range []req{
		{SettingsMasking, true, new(bool)},
		{HideConnectors, need.HideConnectors, &a.HideConnectors},
		{DenyMCPServers, need.DenyMCPServers, &a.DenyMCPServers},
		{AddMCPConfig, need.ExtraMCPServers, &a.ExtraMCPServers},
		{StrictMCPConfig, need.StrictMCPConfig, &a.StrictMCPConfig},
		{PluginDir, need.PluginDir, &a.PluginDir},
		{Agents, need.Agents, &a.Agents},
		{SettingSources, need.DropUserSettingSources, &a.DropUserSettingSources},
		{AppendSystemPromptFile, need.AppendSystemPromptFile, &a.AppendSystemPromptFile},
	} {
		if !r.need {
			continue
		}
		f, ok := m.Features[r.id]
		if !ok {
			f = Feature{ID: r.id, State: Unknown, Reason: "feature not evaluated"}
		}
		switch f.State {
		case Blocked:
			if onBlocked == "fail" {
				return nil, &BlockedError{Feature: r.id, Reason: f.Reason}
			}
			a.Dropped = append(a.Dropped, r.id)
			a.Warnings = append(a.Warnings, fmt.Sprintf("%s is blocked by managed policy and was dropped: %s", r.id, f.Reason))
		case Unknown:
			*r.dst = true
			a.Warnings = append(a.Warnings, fmt.Sprintf("%s may be blocked by managed policy (state unknown): %s", r.id, f.Reason))
		default:
			*r.dst = true
		}
	}
	if a.ExtraMCPServers && len(m.mcpFilters) > 0 {
		a.Warnings = append(a.Warnings, "managed "+strings.Join(m.mcpFilters, ", ")+" will filter the MCP servers this profile adds; servers not admitted are not used")
	}
	if len(a.Locked) > 0 {
		a.Warnings = append(a.Warnings, "always on by policy, cannot be masked: "+strings.Join(a.Locked, ", "))
	}
	return a, nil
}

// jsonDoc is the stable machine-readable form (version 1).
type jsonDoc struct {
	Version       int       `json:"version"`
	Features      []Feature `json:"features"`
	LockedPlugins []string  `json:"locked_plugins"`
	Unknown       []string  `json:"unknown"`
	Warnings      []string  `json:"warnings"`
	// PartialVisibility is true when a machine-specific blind spot (such as
	// WSL without a readable Windows policy) hides sources that could hold
	// lock keys; PartialReasons says why.
	PartialVisibility bool     `json:"partial_visibility"`
	PartialReasons    []string `json:"partial_reasons"`
	// ServerManagedReadable is always false: server-managed settings
	// (claude.ai admin console, Claude apps gateway) cannot be read locally
	// and rank above every local source.
	ServerManagedReadable bool `json:"server_managed_readable"`
}

// JSON renders the matrix as {"version":1,"features":[...],...}, indented
// with two spaces and ending in a newline. Only key names and states appear,
// never policy values.
func (m *Matrix) JSON() ([]byte, error) {
	d := jsonDoc{
		Version: 1, LockedPlugins: nonNil(m.forced), Unknown: nonNil(m.Unknown), Warnings: nonNil(m.Warnings),
		PartialVisibility: m.PartialVisibility, PartialReasons: nonNil(m.PartialReasons),
	}
	for _, id := range AllFeatures {
		d.Features = append(d.Features, m.Features[id])
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode capability matrix: %w", err)
	}
	return append(b, '\n'), nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Text renders the matrix for `doctor --policy`.
func (m *Matrix) Text() string {
	var b strings.Builder
	b.WriteString("Managed policy sources (highest rank first)\n")
	if len(m.Sources) == 0 {
		b.WriteString("  none checked\n")
	}
	for _, s := range m.Sources {
		state := "absent"
		if s.Present {
			state = "present"
			if s.Used {
				state += ", used"
			}
			if len(s.Keys) > 0 {
				state += ", keys: " + strings.Join(s.Keys, " ")
			}
		}
		fmt.Fprintf(&b, "  [%s] %s: %s\n", s.Kind, s.Location, state)
	}
	if len(m.Unknown) > 0 {
		b.WriteString("\nUnknown\n")
		for _, u := range m.Unknown {
			fmt.Fprintf(&b, "  - %s\n", u)
		}
	}
	if len(m.Warnings) > 0 {
		b.WriteString("\nWarnings\n")
		for _, w := range m.Warnings {
			fmt.Fprintf(&b, "  - %s\n", w)
		}
	}
	if m.PartialVisibility {
		b.WriteString("\nPartial visibility (features that depend on a lock key are reported unknown)\n")
		for _, r := range m.PartialReasons {
			fmt.Fprintf(&b, "  - %s\n", r)
		}
	}
	b.WriteString("\nCapabilities\n")
	for _, id := range AllFeatures {
		f := m.Features[id]
		fmt.Fprintf(&b, "  %-26s %-9s %s\n", f.ID, f.State, f.Reason)
		if len(f.Items) > 0 {
			fmt.Fprintf(&b, "  %-26s %-9s %s\n", "", "", strings.Join(f.Items, ", "))
		}
	}
	if len(m.forcedOff) > 0 {
		fmt.Fprintf(&b, "\nForce-disabled by policy: %s\n", strings.Join(m.forcedOff, ", "))
	}
	b.WriteString("\nServer-managed settings (claude.ai admin console, Claude apps gateway) cannot be read locally and rank above every source listed here.\n")
	return b.String()
}

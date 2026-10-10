package profile

// Enumerations. Functions return fresh slices so callers cannot change them.
const (
	StatusActive       = "active"
	StatusExperimental = "experimental"
	StatusDeprecated   = "deprecated"

	ModeAllowOnly = "allow-only"
	ModeAdditive  = "additive"

	ConnectorsNone = "none"
	ConnectorsKeep = "keep"

	OnBlockedWarn = "warn"
	OnBlockedFail = "fail"
)

// Statuses returns the accepted values of status.
func Statuses() []string { return []string{StatusActive, StatusExperimental, StatusDeprecated} }

// PluginModes returns the accepted values of plugins.mode.
func PluginModes() []string { return []string{ModeAllowOnly, ModeAdditive} }

// OutputStyleBuiltins returns the names of the built-in Claude Code output
// styles. Claude Code compares an output style name with case.
func OutputStyleBuiltins() []string {
	return []string{"Default", "Proactive", "Concise", "Explanatory", "Learning"}
}

// Efforts returns the accepted values of session.effort.
func Efforts() []string { return []string{"low", "medium", "high", "xhigh", "max"} }

// ConnectorModes returns the accepted values of mcp.claudeai_connectors.
func ConnectorModes() []string { return []string{ConnectorsNone, ConnectorsKeep} }

// OnBlockedModes returns the accepted values of policy.on_blocked.
func OnBlockedModes() []string { return []string{OnBlockedWarn, OnBlockedFail} }

// Manifest is one profile file (profiles/<name>.toml). The schema is closed
// (SR1): a profile cannot carry permissions, hooks, auth or MCP definitions.
// Unset scalars are "" (or nil for the pointer fields) so that merging can tell
// "not set" from a value. WithDefaults fills the documented defaults.
type Manifest struct {
	Name         string       `toml:"name"`
	Description  string       `toml:"description,omitempty"`
	Owner        string       `toml:"owner,omitempty"`
	Status       string       `toml:"status,omitempty"`
	SupersededBy string       `toml:"superseded_by,omitempty"`
	Account      string       `toml:"account,omitempty"`
	Extends      []string     `toml:"extends,omitempty"`
	WhenToUse    []string     `toml:"when_to_use,omitempty"`
	AvoidWhen    []string     `toml:"avoid_when,omitempty"`
	Plugins      Plugins      `toml:"plugins"`
	Skills       Skills       `toml:"skills"`
	MCP          MCP          `toml:"mcp"`
	Session      Session      `toml:"session"`
	Instructions Instructions `toml:"instructions"`
	Policy       Policy       `toml:"policy"`
}

// Instructions is the [instructions] table. Files are prompt paths
// (prompts/<file> below the declaring source). The launcher joins the
// effective files into one CLAUDE.md. Inherit unset means true: the files
// come after the files of the parents. Inherit false drops the files of all
// earlier profiles in the extends chain.
type Instructions struct {
	Files   []string `toml:"files,omitempty"`
	Inherit *bool    `toml:"inherit,omitempty"`
}

// Set reports whether the table sets anything.
func (i Instructions) Set() bool { return len(i.Files) > 0 || i.Inherit != nil }

// Inherits reports the effective inherit value.
func (i Instructions) Inherits() bool { return i.Inherit == nil || *i.Inherit }

// Plugins is the [plugins] table. Ids are name@marketplace.
type Plugins struct {
	Mode    string   `toml:"mode,omitempty"`
	Include []string `toml:"include,omitempty"`
	Exclude []string `toml:"exclude,omitempty"`
}

// Skills is the [skills] table. It concerns standalone skills only.
type Skills struct {
	Off      []string `toml:"off,omitempty"`
	NameOnly []string `toml:"name_only,omitempty"`
}

// MCP is the [mcp] table. Servers names entries of the MCP registry. The
// definitions never live in a profile.
type MCP struct {
	Servers            []string `toml:"servers,omitempty"`
	ClaudeAIConnectors string   `toml:"claudeai_connectors,omitempty"`
	Strict             *bool    `toml:"strict,omitempty"`
}

// Session is the [session] table.
type Session struct {
	Model                  string            `toml:"model,omitempty"`
	Effort                 string            `toml:"effort,omitempty"`
	OutputStyle            string            `toml:"output_style,omitempty"`
	AppendSystemPromptFile string            `toml:"append_system_prompt_file,omitempty"`
	InheritUserSettings    *bool             `toml:"inherit_user_settings,omitempty"`
	Env                    map[string]string `toml:"env,omitempty"`
}

// Policy is the [policy] table.
type Policy struct {
	OnBlocked string `toml:"on_blocked,omitempty"`
}

// WithDefaults returns a copy with the documented defaults applied:
// status active, plugins.mode allow-only, policy.on_blocked warn,
// session.inherit_user_settings true. mcp.claudeai_connectors and mcp.strict
// stay unset (unset means "keep" and "not strict" to consumers).
func (m Manifest) WithDefaults() Manifest {
	if m.Status == "" {
		m.Status = StatusActive
	}
	if m.Plugins.Mode == "" {
		m.Plugins.Mode = ModeAllowOnly
	}
	if m.Policy.OnBlocked == "" {
		m.Policy.OnBlocked = OnBlockedWarn
	}
	if m.Session.InheritUserSettings == nil {
		t := true
		m.Session.InheritUserSettings = &t
	}
	return m
}

// InheritsUserSettings reports the effective inherit_user_settings value.
func (m Manifest) InheritsUserSettings() bool {
	return m.Session.InheritUserSettings == nil || *m.Session.InheritUserSettings
}

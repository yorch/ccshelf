package profile

import "testing"

func TestEffectiveMCPValues(t *testing.T) {
	var m Manifest
	if m.EffectiveConnectors() != ConnectorsKeep || m.IsStrict() {
		t.Error("unset values must be keep and not strict")
	}
	if got := m.UnsetDefaults(); len(got) != 2 || got[0] != "claudeai_connectors" || got[1] != "strict" {
		t.Errorf("UnsetDefaults = %v", got)
	}
	f, tr := false, true
	m.MCP.ClaudeAIConnectors, m.MCP.Strict = ConnectorsKeep, &f
	if got := m.UnsetDefaults(); got == nil || len(got) != 0 {
		t.Errorf("UnsetDefaults = %#v, want empty and not nil", got)
	}
	m.MCP.ClaudeAIConnectors, m.MCP.Strict = ConnectorsNone, &tr
	if m.EffectiveConnectors() != ConnectorsNone || !m.IsStrict() {
		t.Error("explicit values must win")
	}
	// WithDefaults must keep the raw values: the trust hash reads them.
	var u Manifest
	if w := u.WithDefaults(); w.MCP.ClaudeAIConnectors != "" || w.MCP.Strict != nil {
		t.Error("WithDefaults changed the MCP scalars")
	}
}

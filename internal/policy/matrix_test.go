package policy

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ccshelf/ccshelf/internal/claude"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	p := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func fixturePolicy() *Policy {
	return &Policy{
		Sources: []Source{
			{Kind: KindMDM, Location: "com.anthropic.claudecode (/Library/Managed Preferences/com.anthropic.claudecode.plist)"},
			{Kind: KindFile, Location: "/etc/claude-code/managed-settings.json", Present: true, Used: true, Keys: []string{"disableSideloadFlags", "enabledPlugins"}},
			{Kind: KindDropIn, Location: "/etc/claude-code/managed-settings.d/10-x.json", Present: true, Used: true, Keys: []string{"deniedMcpServers"}},
		},
		DisableSideloadFlags: bp(true),
		EnabledPlugins:       map[string]bool{"audit-logger@acme": true, "legacy@acme": false},
		Unknown:              []string{"server-managed settings cannot be read locally"},
		Warnings:             []string{"managed key disableAllHooks should be a boolean; ignoring the value"},
	}
}

func TestGolden(t *testing.T) {
	installed := []claude.Plugin{{ID: "sre-kit@acme", RequiredByOrg: true}, {ID: "design-kit@acme"}}
	for name, tc := range map[string]struct {
		p     *Policy
		insts []claude.Plugin
	}{
		"strict":  {fixturePolicy(), installed},
		"none":    {&Policy{}, nil},
		"unknown": {&Policy{Unreadable: true, Unknown: []string{"managed settings file /etc/claude-code/managed-settings.json: malformed: not valid JSON"}}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			m := Evaluate(tc.p, tc.insts)
			golden(t, name+".txt.golden", m.Text())
			b, err := m.JSON()
			if err != nil {
				t.Fatal(err)
			}
			golden(t, name+".json.golden", string(b))
		})
	}
}

func TestJSONShape(t *testing.T) {
	b, err := Evaluate(nil, nil).JSON()
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasPrefix(s, "{\n  \"version\": 1,\n  \"features\": [") || !strings.HasSuffix(s, "}\n") {
		t.Fatalf("%s", s)
	}
	if strings.Contains(s, "null") {
		t.Fatalf("no nulls in the stable format: %s", s)
	}
}

func TestEvaluateRequiredByOrg(t *testing.T) {
	p := &Policy{EnabledPlugins: map[string]bool{"a@m": true}}
	m := Evaluate(p, []claude.Plugin{{ID: "b@m", RequiredByOrg: true}, {ID: "a@m", RequiredByOrg: true}, {ID: "c@m"}})
	if !reflect.DeepEqual(m.LockedPlugins(), []string{"a@m", "b@m"}) {
		t.Fatalf("%v", m.LockedPlugins())
	}
	got := m.LockedPlugins()
	got[0] = "mutated"
	if m.LockedPlugins()[0] != "a@m" {
		t.Fatal("LockedPlugins must return a copy")
	}
	if Evaluate(nil, nil).LockedPlugins() != nil && len(Evaluate(nil, nil).LockedPlugins()) != 0 {
		t.Fatal("none")
	}
}

func TestPlan(t *testing.T) {
	all := Needs{ExtraMCPServers: true, PluginDir: true, Agents: true, DropUserSettingSources: true, AppendSystemPromptFile: true, HideConnectors: true, DenyMCPServers: true}
	blocked := Evaluate(&Policy{DisableSideloadFlags: bp(true), EnabledPlugins: map[string]bool{"a@m": true}}, nil)
	open := Evaluate(nil, nil)
	unknown := Evaluate(&Policy{Unreadable: true}, nil)

	t.Run("open", func(t *testing.T) {
		a, err := open.Plan(all, "warn")
		if err != nil || !a.ExtraMCPServers || !a.PluginDir || !a.Agents || !a.DropUserSettingSources || !a.AppendSystemPromptFile || !a.HideConnectors || !a.DenyMCPServers || len(a.Dropped) != 0 || len(a.Warnings) != 0 {
			t.Fatalf("%+v %v", a, err)
		}
	})
	t.Run("not needed is not applied", func(t *testing.T) {
		a, err := open.Plan(Needs{}, "")
		if err != nil || a.PluginDir || a.ExtraMCPServers {
			t.Fatalf("%+v %v", a, err)
		}
	})
	t.Run("warn drops", func(t *testing.T) {
		for _, ob := range []string{"warn", ""} {
			a, err := blocked.Plan(all, ob)
			if err != nil {
				t.Fatal(err)
			}
			if a.ExtraMCPServers || a.PluginDir || a.Agents || !a.HideConnectors || !a.DenyMCPServers || !a.DropUserSettingSources {
				t.Fatalf("%+v", a)
			}
			want := []FeatureID{AddMCPConfig, PluginDir, Agents}
			if !reflect.DeepEqual(a.Dropped, want) {
				t.Fatalf("dropped %v", a.Dropped)
			}
			joined := strings.Join(a.Warnings, "\n")
			if !strings.Contains(joined, "plugin-dir is blocked") || !strings.Contains(joined, "a@m") {
				t.Fatalf("warnings: %s", joined)
			}
		}
	})
	t.Run("fail", func(t *testing.T) {
		_, err := blocked.Plan(all, "fail")
		var be *BlockedError
		if !errors.As(err, &be) || be.Feature != AddMCPConfig || be.ExitCode() != 3 || !strings.Contains(err.Error(), "disableSideloadFlags") {
			t.Fatalf("%v", err)
		}
		if _, err := blocked.Plan(Needs{HideConnectors: true}, "fail"); err != nil {
			t.Fatalf("needing only available features must not fail: %v", err)
		}
	})
	t.Run("unknown is attempted with a warning, never failed", func(t *testing.T) {
		a, err := unknown.Plan(all, "fail")
		if err != nil || !a.PluginDir || len(a.Warnings) == 0 || !strings.Contains(strings.Join(a.Warnings, ""), "state unknown") {
			t.Fatalf("%+v %v", a, err)
		}
	})
	t.Run("invalid on_blocked", func(t *testing.T) {
		if _, err := open.Plan(all, "ignore"); !errors.Is(err, ErrOnBlocked) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("missing feature", func(t *testing.T) {
		m := &Matrix{Features: map[FeatureID]Feature{}}
		a, err := m.Plan(Needs{PluginDir: true}, "fail")
		if err != nil || !a.PluginDir {
			t.Fatal(err)
		}
	})
}

func TestTextEmptyMatrix(t *testing.T) {
	m := &Matrix{Features: map[FeatureID]Feature{}}
	if s := m.Text(); !strings.Contains(s, "none checked") {
		t.Fatal(s)
	}
}

// TestPolicyCombinations runs a table over many combinations, end to end from
// files to Plan.
func TestPolicyCombinations(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		state   map[FeatureID]State
		locked  int
		failsOn FeatureID
	}{
		{"empty object", `{}`, map[FeatureID]State{PluginDir: Available, AddMCPConfig: Available}, 0, ""},
		{"only marketplaces", `{"strictKnownMarketplaces":[]}`, map[FeatureID]State{PluginDir: Available}, 0, ""},
		{"sideload false", `{"disableSideloadFlags":false}`, map[FeatureID]State{PluginDir: Available}, 0, ""},
		{"sideload string true", `{"disableSideloadFlags":"true"}`, map[FeatureID]State{PluginDir: Blocked}, 0, AddMCPConfig},
		{"sideload plus forced", `{"disableSideloadFlags":true,"enabledPlugins":{"a@m":true,"b@m":true}}`, map[FeatureID]State{Agents: Blocked, ForcedPlugins: Blocked}, 2, AddMCPConfig},
		{"hooks and permissions locks", `{"allowManagedHooksOnly":true,"allowManagedPermissionRulesOnly":true,"disableAllHooks":true}`, map[FeatureID]State{SettingsMasking: Available, SettingSources: Available}, 0, ""},
		{"mcp lock", `{"allowManagedMcpServersOnly":true,"allowedMcpServers":[{"serverName":"x"}]}`, map[FeatureID]State{AddMCPConfig: Available, DenyMCPServers: Available}, 0, ""},
		{"connectors off", `{"disableClaudeAiConnectors":true}`, map[FeatureID]State{HideConnectors: Available}, 0, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, "managed-settings.json"), tc.doc)
			m := Evaluate(detect(t, linuxOpt(dir)), nil)
			for id, st := range tc.state {
				if m.Features[id].State != st {
					t.Errorf("%s = %s, want %s", id, m.Features[id].State, st)
				}
			}
			if len(m.LockedPlugins()) != tc.locked {
				t.Errorf("locked = %v", m.LockedPlugins())
			}
			_, err := m.Plan(Needs{ExtraMCPServers: true, PluginDir: true}, "fail")
			var be *BlockedError
			if tc.failsOn == "" {
				if err != nil {
					t.Errorf("unexpected %v", err)
				}
			} else if !errors.As(err, &be) || be.Feature != tc.failsOn {
				t.Errorf("err = %v, want blocked %s", err, tc.failsOn)
			}
		})
	}
}

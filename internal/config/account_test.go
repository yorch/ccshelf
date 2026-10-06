package config

import (
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolveAccount(t *testing.T) {
	cfg := Default()
	cfg.DefaultAccount = "work"
	cfg.Accounts = map[string]Account{"work": {ConfigDir: "/w"}, "personal": {ConfigDir: "/p"}}
	setEnv := envOf(map[string]string{"CLAUDE_CONFIG_DIR": "/e"})
	none := envOf(nil)
	tests := []struct {
		name          string
		flag, profile string
		env           func(string) string
		wantName      string
		wantSource    string
		fromEnv       bool
		setEnv        bool
		overrides     bool
		notes         int
		wantErr       string
	}{
		{"flag wins", "personal", "work", none, "personal", "flag", false, true, false, 0, ""},
		{"profile beats default", "", "personal", none, "personal", "profile", false, true, false, 0, ""},
		{"default", "", "", none, "work", "default", false, true, false, 0, ""},
		{"env honored over default", "", "", setEnv, "", "env", true, false, false, 1, ""},
		{"env honored over profile", "", "personal", setEnv, "", "env", true, false, false, 1, ""},
		{"flag overrides env, flagged", "personal", "", setEnv, "personal", "flag", false, true, true, 1, ""},
		{"unknown flag", "nope", "", none, "", "", false, false, false, 0, "known: personal, work"},
		{"unknown profile account", "", "nope", none, "", "", false, false, false, 0, "unknown account \"nope\" from profile"},
		{"nil env uses os", "", "", nil, "work", "default", false, true, false, 0, ""},
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAccount(tt.flag, tt.profile, cfg, tt.env)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != tt.wantName || got.Source != tt.wantSource || got.FromEnv != tt.fromEnv || got.SetEnv != tt.setEnv || got.OverridesEnv != tt.overrides || len(got.Notes) != tt.notes {
				t.Errorf("got %+v", got)
			}
			if tt.wantName != "" && got.Account.ConfigDir == "" {
				t.Error("account not populated")
			}
		})
	}
}

func TestResolveAccountNone(t *testing.T) {
	got, err := ResolveAccount("", "", nil, envOf(nil))
	if err != nil || got.Source != "none" || got.SetEnv || got.FromEnv {
		t.Errorf("got %+v, %v", got, err)
	}
	_, err = ResolveAccount("x", "", nil, envOf(nil))
	if err == nil || !strings.Contains(err.Error(), "none configured") {
		t.Errorf("err = %v", err)
	}
}

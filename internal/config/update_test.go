package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpdateDefaults(t *testing.T) {
	var u Update
	if u.Present() {
		t.Error("the zero Update must not be Present")
	}
	if got := u.EffectiveMode(); got != UpdateOff {
		t.Errorf("default mode = %q, want off", got)
	}
	if got := u.EffectiveInterval(); got != 24*time.Hour {
		t.Errorf("default interval = %s", got)
	}
	if got := Default().Update.EffectiveMode(); got != UpdateOff {
		t.Errorf("Default() mode = %q", got)
	}
	if !(Update{Mode: "notify"}).Present() || !(Update{BaseURL: "x"}).Present() || !(Update{Interval: "2h"}).Present() {
		t.Error("any set field must make the section Present")
	}
}

func TestEffectiveInterval(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"", 24 * time.Hour},
		{"1h", time.Hour},
		{"36h", 36 * time.Hour},
		{"90m", 90 * time.Minute},
		{"59m", 24 * time.Hour},      // below the minimum: default (Validate reports it)
		{"9000h", 24 * time.Hour},    // above the maximum
		{"nonsense", 24 * time.Hour}, // invalid
	} {
		if got := (Update{Interval: tc.in}).EffectiveInterval(); got != tc.want {
			t.Errorf("EffectiveInterval(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func loadString(t *testing.T, body string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

func TestLoadUpdateSection(t *testing.T) {
	cfg, err := loadString(t, "[update]\nmode = \"install\"\ninterval = \"48h\"\nbase_url = \"https://ghe.example.com/tools/ccshelf\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Update.Mode != "install" || cfg.Update.EffectiveInterval() != 48*time.Hour || cfg.Update.BaseURL != "https://ghe.example.com/tools/ccshelf" {
		t.Errorf("got %+v", cfg.Update)
	}
	cfg, err = loadString(t, "")
	if err != nil || cfg.Update.Present() {
		t.Errorf("an empty file must leave [update] absent: %+v %v", cfg.Update, err)
	}
}

func TestLoadUpdateRejects(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"bad mode":        {"[update]\nmode = \"auto\"\n", "update.mode"},
		"mode case":       {"[update]\nmode = \"Notify\"\n", "update.mode"},
		"channel":         {"[update]\nchannel = \"beta\"\n", "channel"},
		"interval text":   {"[update]\ninterval = \"daily\"\n", "update.interval"},
		"interval low":    {"[update]\ninterval = \"30m\"\n", "minimum"},
		"interval high":   {"[update]\ninterval = \"9000h\"\n", "maximum"},
		"key case":        {"[update]\nMode = \"notify\"\n", "must be spelled"},
		"http":            {"[update]\nbase_url = \"http://ghe.example.com\"\n", "https"},
		"http loopback":   {"[update]\nbase_url = \"http://127.0.0.1:8080\"\n", "https"},
		"userinfo":        {"[update]\nbase_url = \"https://user:pw@ghe.example.com\"\n", "user information"},
		"token":           {"[update]\nbase_url = \"https://ghe.example.com/ghp_abc/x\"\n", "credential"},
		"query":           {"[update]\nbase_url = \"https://ghe.example.com/?a=b\"\n", "query"},
		"fragment":        {"[update]\nbase_url = \"https://ghe.example.com/#x\"\n", "query"},
		"deep path":       {"[update]\nbase_url = \"https://ghe.example.com/a/b/c\"\n", "/owner/repo"},
		"file scheme":     {"[update]\nbase_url = \"file://h/tmp\"\n", "https"},
		"space":           {"[update]\nbase_url = \"https://ghe.example.com /x\"\n", "whitespace"},
		"empty base":      {"[update]\nbase_url = \"\"\nmode = \"off\"\n", ""},
		"interval number": {"[update]\ninterval = 24\n", "interval"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadString(t, tc.body)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestValidateUpdateBaseURL(t *testing.T) {
	good := []string{
		"https://github.com", "https://ghe.example.com", "https://ghe.example.com/", "https://ghe.example.com:8443",
		"https://ghe.example.com/tools/ccshelf", "https://ghe.example.com/tools/ccshelf/",
	}
	for _, g := range good {
		if err := ValidateUpdateBaseURL(g, false); err != nil {
			t.Errorf("%q: %v", g, err)
		}
	}
	for _, loop := range []string{"http://127.0.0.1:9", "http://localhost:80", "http://[::1]:5"} {
		if err := ValidateUpdateBaseURL(loop, false); err == nil {
			t.Errorf("%q must be rejected without the loopback allowance", loop)
		}
		if err := ValidateUpdateBaseURL(loop, true); err != nil {
			t.Errorf("%q must be accepted with the allowance: %v", loop, err)
		}
	}
	if err := ValidateUpdateBaseURL("http://ghe.example.com", true); err == nil {
		t.Error("a non-loopback http URL must be rejected even with the allowance")
	}
	if err := ValidateUpdateBaseURL("http://127.0.0.1.evil.example.com", true); err == nil {
		t.Error("a lookalike host must not count as loopback")
	}
	if err := ValidateUpdateBaseURL("", false); err == nil {
		t.Error("empty must be rejected")
	}
}

func TestLoopbackAllowanceIsOffInThisBuild(t *testing.T) {
	// The unit tests are never built with -tags e2eloopback; if this fails the
	// production guard has been broken.
	if LoopbackHTTPAllowed {
		t.Fatal("LoopbackHTTPAllowed must be false unless built with -tags e2eloopback")
	}
}

func TestSaveOmitsAbsentUpdateSection(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := Save(p, Default()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), "update") {
		t.Errorf("a default config must not write [update]:\n%s", b)
	}
	cfg := Default()
	cfg.Update = Update{Mode: UpdateNotify}
	if err := Save(p, cfg); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	if !strings.Contains(string(b), "[update]") || !strings.Contains(string(b), `mode = 'notify'`) && !strings.Contains(string(b), `mode = "notify"`) {
		t.Errorf("[update] mode not written:\n%s", b)
	}
}

package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The binary reads the machine's real managed-settings location, which a test
// cannot redirect (a seam for that would be a policy bypass). So these tests
// cover what is observable end to end: plugins the fake reports as required
// by the org, and a fake claude that enforces disableSideloadFlags. Policy
// detection itself (exit 3) is covered by the unit tests of internal/policy
// and internal/cli/launcher.

func hostHasManagedPolicy() bool {
	for _, p := range []string{
		"/Library/Application Support/ClaudeCode/managed-settings.json",
		"/etc/claude-code/managed-settings.json",
		`C:\Program Files\ClaudeCode\managed-settings.json`,
	} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

func initPluginNames(t *testing.T, stdout string) []string {
	t.Helper()
	var names []string
	for _, line := range strings.Split(stdout, "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) != nil || m["subtype"] != "init" {
			continue
		}
		for _, p := range m["plugins"].([]any) {
			pm := p.(map[string]any)
			if pm["source"] != "harness" {
				names = append(names, pm["name"].(string))
			}
		}
		return names
	}
	t.Fatalf("no init event in:\n%s", stdout)
	return nil
}

var initArgs = []string{"--", "-p", "hi", "--output-format", "stream-json", "--verbose"}

func TestPolicyNone(t *testing.T) {
	if hostHasManagedPolicy() {
		t.Skip("this machine has a managed Claude Code policy; the 'none' case cannot be observed here")
	}
	s := newSandbox(t)
	s.writeProfile("mine", personalMine)
	r := s.mustRun(append([]string{"run", "mine"}, initArgs...)...)
	if got := strings.Join(initPluginNames(t, r.Stdout), ","); got != "design-kit" {
		t.Errorf("plugins = %s, want design-kit", got)
	}
	r = s.mustRun("doctor", "--policy", "--root", exampleOrg(t))
	contains(t, "doctor --policy", r.Stdout, "capability matrix", "settings-masking", "available")
	if strings.Contains(r.Stdout, "blocked") {
		t.Errorf("nothing may be blocked without a policy:\n%s", r.Stdout)
	}
}

func TestPolicyPartialForcedPlugin(t *testing.T) {
	if hostHasManagedPolicy() {
		t.Skip("managed policy present on this machine")
	}
	s := newSandbox(t)
	s.writeProfile("mine", personalMine)
	// The org forces sre-kit on; the profile does not list it.
	s.Setenv("FAKE_CLAUDE_MANAGED", `{"enabledPlugins":{"sre-kit@acme":true}}`)
	r := s.mustRun(append([]string{"run", "mine"}, initArgs...)...)
	got := strings.Join(initPluginNames(t, r.Stdout), ",")
	if got != "design-kit,sre-kit" {
		t.Errorf("plugins = %s, want the profile's design-kit plus the forced sre-kit", got)
	}
	ep := enabledPlugins(t, settingsOf(t, s.launches()[0]))
	if v, ok := ep["sre-kit@acme"]; ok && !v {
		t.Errorf("a plugin forced by policy must never be masked: %v", ep)
	}
	// doctor reads it from the installed list and says so.
	r = s.mustRun("doctor", "--installed", "--root", exampleOrg(t))
	contains(t, "doctor --installed", r.Stdout, "DOC009")
}

func TestPolicyStrictSideloadBlocked(t *testing.T) {
	if hostHasManagedPolicy() {
		t.Skip("managed policy present on this machine")
	}
	s := newSandbox(t)
	// A profile that needs no sideload flag (settings only) still works: the
	// generated --settings file is not a sideload flag.
	s.writeProfile("mine", personalMine)
	s.Setenv("FAKE_CLAUDE_MANAGED", `{"disableSideloadFlags":true,"enabledPlugins":{"sre-kit@acme":true}}`)
	r := s.mustRun(append([]string{"run", "mine"}, initArgs...)...)
	if got := strings.Join(initPluginNames(t, r.Stdout), ","); got != "design-kit,sre-kit" {
		t.Errorf("plugins = %s", got)
	}

	// A profile with an MCP server needs --mcp-config; the (fake) claude
	// refuses it under the policy. ccshelf must pass the failure through, not
	// retry without the flag or edit anything to get around it.
	org := exampleOrg(t)
	s.addDirSource(org + string(os.PathSeparator) + "profiles")
	s.mustRun("trust", "sre", "--accept", s.closureHash("sre"))
	before := len(s.anyStart())
	r = s.run("run", "sre")
	if r.Code == 0 {
		t.Fatalf("exit 0; a blocked sideload flag must fail the run\n%s", r.Stderr)
	}
	contains(t, "stderr", r.Stderr, "managed policy")
	after := s.anyStart()
	if len(after)-before != 1 {
		t.Errorf("claude was started %d times for one run; it must not be retried with a different command line", len(after)-before)
	}
}

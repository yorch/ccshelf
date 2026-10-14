package launcher

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/testutil"
	"github.com/yorch/ccshelf/internal/ui"
)

const ghostProfile = `name = "mine"
description = "My profile"

[plugins]
include = ["ghost@acme"]
`

// installedWith writes a fake plugin list that holds the given ids and points
// FAKE_CLAUDE_PLUGINS at it. It returns a function that adds one more id, to
// emulate "claude plugin install".
func installedWith(t *testing.T, ids ...string) func(id string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugins.json")
	var list []map[string]any
	add := func(id string) {
		list = append(list, map[string]any{
			"id": id, "version": "1.0.0", "scope": "user", "enabled": true, "installPath": "/fake/" + id,
			"installedAt": "2026-01-01T00:00:00.000Z", "lastUpdated": "2026-01-01T00:00:00.000Z", "projectEnabled": false,
		})
		b, err := json.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}
		testutil.WriteFile(t, path, string(b))
	}
	for _, id := range ids {
		add(id)
	}
	t.Setenv("FAKE_CLAUDE_PLUGINS", path)
	return add
}

func TestInstallMissingInstallsAfterConfirm(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", ghostProfile)
	add := installedWith(t, "design-kit@acme")
	h.spawnHook = func(args []string) {
		if len(args) >= 4 && args[0] == "plugin" && args[1] == "install" {
			add(args[len(args)-1])
		}
	}
	h.prompt = ui.NewScripted(true)
	h.mustRun("run", "--install-missing", "mine")
	if len(h.spawned) != 1 {
		t.Fatalf("spawned %v", h.spawned)
	}
	got := strings.Join(h.spawned[0][1:], " ")
	if got != "plugin install --scope user ghost@acme" {
		t.Errorf("command %q", got)
	}
	if strings.Contains(got, "-y") || strings.Contains(got, "--marketplace") {
		t.Errorf("must not pass -y or --marketplace: %q", got)
	}
	if h.started != 1 {
		t.Errorf("started %d", h.started)
	}
	if strings.Contains(h.errb.String(), "is not installed") {
		t.Errorf("plugin reported missing after install: %s", h.errb)
	}
	if h.settingsOf(h.startArgs) == nil {
		t.Fatal("no settings")
	}
}

func TestInstallMissingDeclinedStops(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", ghostProfile)
	installedWith(t, "design-kit@acme")
	h.prompt = ui.NewScripted(false)
	if code := h.run("run", "--install-missing", "mine"); code != ui.ExitFailure {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if len(h.spawned) != 0 || h.started != 0 {
		t.Errorf("spawned %v started %d", h.spawned, h.started)
	}
}

func TestInstallMissingNeedsYesWithoutTerminal(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", ghostProfile)
	installedWith(t, "design-kit@acme")
	if code := h.run("--no-interactive", "run", "--install-missing", "mine"); code != ui.ExitUsage {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if len(h.spawned) != 0 || h.started != 0 {
		t.Errorf("spawned %v started %d", h.spawned, h.started)
	}
}

func TestInstallMissingYesInstallsWithoutTerminal(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", ghostProfile)
	add := installedWith(t, "design-kit@acme")
	h.spawnHook = func(args []string) { add(args[len(args)-1]) }
	if code := h.run("--no-interactive", "run", "--install-missing", "--yes", "mine"); code != 0 {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if len(h.spawned) != 1 || h.started != 1 {
		t.Errorf("spawned %v started %d", h.spawned, h.started)
	}
}

func TestInstallMissingFailureStops(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", ghostProfile)
	installedWith(t, "design-kit@acme")
	h.spawnCode = 1
	if code := h.run("run", "--install-missing", "--yes", "mine"); code != ui.ExitFailure {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
	if h.started != 0 {
		t.Errorf("started %d", h.started)
	}
	if !strings.Contains(h.errb.String(), "ghost@acme") {
		t.Errorf("stderr: %s", h.errb)
	}
}

func TestInstallMissingNeverAddsMarketplace(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", strings.ReplaceAll(ghostProfile, "ghost@acme", "ghost@unknown"))
	installedWith(t, "design-kit@acme")
	h.mustRun("run", "--install-missing", "--yes", "mine")
	if len(h.spawned) != 0 {
		t.Fatalf("must not install from a marketplace that is not added: %v", h.spawned)
	}
	if !strings.Contains(h.errb.String(), "marketplace unknown is not added") {
		t.Errorf("stderr: %s", h.errb)
	}
	if h.started != 1 {
		t.Errorf("--yes starts with the plugin missing, as without the flag: started %d", h.started)
	}
}

func TestInstallMissingSkipsInstalledPlugins(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", personalMine)
	h.mustRun("run", "--install-missing", "--yes", "mine")
	if len(h.spawned) != 0 {
		t.Errorf("nothing is missing, nothing to install: %v", h.spawned)
	}
}

func TestRunWithoutFlagNeverInstalls(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", ghostProfile)
	h.mustRun("run", "--yes", "mine")
	if len(h.spawned) != 0 {
		t.Errorf("run without --install-missing must not install: %v", h.spawned)
	}
}

func TestInstallMissingAfterProfileNameIsRejected(t *testing.T) {
	h := newHarness(t)
	h.writeProfile("mine", ghostProfile)
	if code := h.run("run", "mine", "--install-missing"); code != ui.ExitUsage {
		t.Fatalf("code %d\n%s", code, h.errb)
	}
}

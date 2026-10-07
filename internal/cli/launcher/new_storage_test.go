package launcher

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/profile/gitsource"
	"github.com/yorch/ccshelf/internal/testutil"
	"github.com/yorch/ccshelf/internal/ui"
)

func emptyNewPlugins(t *testing.T) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "plugins.json")
	testutil.WriteFile(t, p, "[]")
	t.Setenv("FAKE_CLAUDE_PLUGINS", p)
}

func newTarget(t *testing.T, h *harness, scope, name string) string {
	t.Helper()
	dir, err := profile.PersonalDir()
	if scope == "project" {
		dir, err = profile.ProjectProfilesDir(h.cwd)
	}
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, name+".toml")
}

func assertNoNewState(t *testing.T, h *harness, target string) {
	t.Helper()
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected profile at %s: %v", target, err)
	}
	for _, file := range []string{"lock.json", "project-trust.json"} {
		if _, err := os.Stat(filepath.Join(h.configDir(), file)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s: %v", file, err)
		}
	}
	if h.started != 0 || len(h.spawned) != 0 {
		t.Fatalf("started Claude: %d, %v", h.started, h.spawned)
	}
}

func TestNewStorageFlagsAndNoPrompts(t *testing.T) {
	for _, scope := range []string{"default", "user", "project"} {
		t.Run(scope, func(t *testing.T) {
			h := newHarness(t)
			sc := ui.NewScripted()
			h.prompt = sc
			args := []string{"new", "mine", "--description", ""}
			targetScope := scope
			if scope == "default" {
				targetScope = "user"
			} else {
				args = append(args, "--scope", scope)
			}
			target := newTarget(t, h, targetScope, "mine")
			h.mustRun(args...)
			if err := sc.Done(); err != nil {
				t.Fatal(err)
			}
			if len(sc.Asked) != 0 {
				t.Fatalf("flags gained prompts: %v", sc.Asked)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatal(err)
			}
			if h.started != 0 || len(h.spawned) != 0 {
				t.Fatal("creation started Claude")
			}
			for _, file := range []string{"config.toml", "lock.json", "project-trust.json"} {
				if _, err := os.Stat(filepath.Join(h.configDir(), file)); !os.IsNotExist(err) {
					t.Fatalf("creation wrote %s: %v", file, err)
				}
			}
			if scope == "project" {
				for _, hint := range []string{"does not enable or trust", "off by default", "trust.trust_project_profiles", "ccshelf trust --project"} {
					if !strings.Contains(h.errb.String(), hint) {
						t.Fatalf("missing hint %q: %s", hint, h.errb)
					}
				}
				if code := h.run("run", "mine", "--yes"); code == 0 || h.started != 0 {
					t.Fatalf("--yes activated project: %d, %d", code, h.started)
				}
			}
		})
	}
	for _, mode := range []string{"non-tty", "no-interactive", "CI"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			args := []string{"new", "quiet", "--scope", "project"}
			if mode != "non-tty" {
				h.prompt = ui.NewScripted()
			}
			if mode == "no-interactive" {
				args = append(args, "--no-interactive")
			}
			if mode == "CI" {
				t.Setenv("CI", "false")
			}
			h.mustRun(args...)
			if mode != "non-tty" && len(h.prompt.(*ui.Scripted).Asked) != 0 {
				t.Fatal("unexpected prompt")
			}
		})
	}
}

func TestNewStoragePersonalDirectoryPolicy(t *testing.T) {
	h := newHarness(t)
	// PersonalDir uses the existing native config policy, not the command's GOOS
	// rendering seam. Native Windows exercises APPDATA; Unix exercises XDG.
	base := filepath.Join(t.TempDir(), "custom-config")
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", base)
	} else {
		t.Setenv("XDG_CONFIG_HOME", base)
	}
	h.mustRun("new", "custom", "--no-interactive")
	if _, err := os.Stat(filepath.Join(base, "ccshelf", "profiles", "custom.toml")); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", "")
	} else {
		t.Setenv("XDG_CONFIG_HOME", "")
	}
	h.mustRun("new", "fallback", "--no-interactive")
	home := h.dirs["HOME"]
	want := filepath.Join(home, ".config", "ccshelf", "profiles", "fallback.toml")
	if runtime.GOOS == "windows" {
		want = filepath.Join(home, "AppData", "Roaming", "ccshelf", "profiles", "fallback.toml")
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
}

func TestNewStorageProjectRoots(t *testing.T) {
	for _, marker := range []string{"directory", "worktree-file", "outside-git"} {
		t.Run(marker, func(t *testing.T) {
			h := newHarness(t)
			root := h.cwd
			nested := filepath.Join(root, "src", "nested")
			if err := os.MkdirAll(nested, 0o700); err != nil {
				t.Fatal(err)
			}
			if marker == "directory" {
				if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if marker == "worktree-file" {
				testutil.WriteFile(t, filepath.Join(root, ".git"), "gitdir: ../main/.git/worktrees/task\n")
			}
			h.cwd = nested
			h.mustRun("new", "local", "--scope", "project")
			wantRoot := root
			if marker == "outside-git" {
				wantRoot = nested
			}
			if _, err := os.Stat(filepath.Join(wantRoot, ".ccshelf", "profiles", "local.toml")); err != nil {
				t.Fatal(err)
			}
			if wantRoot != nested {
				if _, err := os.Stat(filepath.Join(nested, ".ccshelf")); !os.IsNotExist(err) {
					t.Fatal("created nested project instead of Git root")
				}
			}
		})
	}
}

// newWizardSpy checks the location default and the preview before confirmation.
type newWizardSpy struct {
	*ui.Scripted
	questions       []ui.Question
	confirmDefaults []bool
	beforeConfirm   func()
}

func (p *newWizardSpy) Select(ctx context.Context, q ui.Question) (int, error) {
	p.questions = append(p.questions, q)
	return p.Scripted.Select(ctx, q)
}

func (p *newWizardSpy) Confirm(ctx context.Context, title string, def bool) (bool, error) {
	p.confirmDefaults = append(p.confirmDefaults, def)
	if p.beforeConfirm != nil {
		p.beforeConfirm()
	}
	return p.Scripted.Confirm(ctx, title, def)
}

func TestNewStorageWizardScopeAndReplay(t *testing.T) {
	for _, scope := range []string{"default", "picked-project", "user", "project"} {
		t.Run(scope, func(t *testing.T) {
			h := newHarness(t)
			emptyNewPlugins(t)
			args := []string{"new", "wizard"}
			actualScope := scope
			answers := []any{}
			switch scope {
			case "default":
				actualScope = "user"
				answers = append(answers, 0)
			case "picked-project":
				actualScope = "project"
				answers = append(answers, 1)
			default:
				args = append(args, "--scope", scope)
			}
			answers = append(answers, "Wizard", "", true)
			spy := &newWizardSpy{Scripted: ui.NewScripted(answers...)}
			target := newTarget(t, h, actualScope, "wizard")
			spy.beforeConfirm = func() {
				assertNoNewState(t, h, target)
				if !strings.Contains(h.errb.String(), "Destination: "+target) || !strings.Contains(h.errb.String(), "Profile summary:") || !strings.Contains(h.errb.String(), "Wizard") {
					t.Fatalf("no preview before confirmation: %s", h.errb)
				}
			}
			h.prompt = spy
			h.mustRun(args...)
			if err := spy.Done(); err != nil {
				t.Fatal(err)
			}
			if scope == "default" || scope == "picked-project" {
				if len(spy.questions) != 1 || !spy.questions[0].HasDefault || spy.questions[0].Default != 0 || spy.questions[0].Options[0].Value != "user" {
					t.Fatalf("unsafe location default: %+v", spy.questions)
				}
			} else if len(spy.questions) != 0 {
				t.Fatalf("explicit scope still asked location: %+v", spy.questions)
			}
			if len(spy.confirmDefaults) != 1 || spy.confirmDefaults[0] {
				t.Fatalf("unsafe confirmation default: %v", spy.confirmDefaults)
			}
			first, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			eq := ""
			for _, line := range strings.Split(h.errb.String(), "\n") {
				if strings.HasPrefix(line, "Equivalent: ccshelf ") {
					eq = strings.TrimPrefix(line, "Equivalent: ccshelf ")
				}
			}
			for _, want := range []string{"--scope " + actualScope, "--yes", "--no-interactive"} {
				if !strings.Contains(eq, want) {
					t.Fatalf("missing replay flag %q: %s", want, eq)
				}
			}
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
			h.prompt = ui.NewScripted()
			h.mustRun(strings.Fields(eq)...)
			second, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(first, second) {
				t.Fatalf("replay mismatch: %v\n%s\n%s", err, first, second)
			}
			if len(h.prompt.(*ui.Scripted).Asked) != 0 {
				t.Fatal("replay prompted")
			}
		})
	}
}

func TestNewStorageWizardCancellationAndYes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		confirmation any
		wantCode     int
	}{
		{"declined", false, ui.ExitFailure},
		{"aborted", ui.ErrAborted, ui.ExitInterrupted},
		{"canceled", context.Canceled, ui.ExitInterrupted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			emptyNewPlugins(t)
			h.prompt = ui.NewScripted("Description", "", tc.confirmation)
			if code := h.run("new", "cancel", "--scope", "project"); code != tc.wantCode {
				t.Fatalf("cancel exit %d: %s", code, h.errb)
			}
			assertNoNewState(t, h, newTarget(t, h, "project", "cancel"))
			if _, err := os.Stat(filepath.Join(h.cwd, ".ccshelf")); !os.IsNotExist(err) {
				t.Fatal("cancel created project directory")
			}
		})
	}
	h := newHarness(t)
	emptyNewPlugins(t)
	sc := ui.NewScripted("Description", "")
	h.prompt = sc
	h.mustRun("new", "yes", "--scope", "project", "--yes")
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.errb.String(), "Destination:") {
		t.Fatal("--yes omitted preview")
	}
	for _, title := range sc.Asked {
		if strings.Contains(title, "Create this profile") {
			t.Fatal("--yes did not bypass creation confirmation")
		}
	}
}

func TestNewStorageCollisions(t *testing.T) {
	for _, namespace := range []string{"personal", "disabled-project", "shared-dir", "cached-git"} {
		for _, scope := range []string{"user", "project"} {
			t.Run(namespace+"/"+scope, func(t *testing.T) {
				h := newHarness(t)
				switch namespace {
				case "personal":
					h.writeProfile("collision", "name = 'collision'\n")
				case "disabled-project":
					testutil.WriteFile(t, filepath.Join(h.cwd, ".ccshelf", "profiles", "collision.toml"), "name = 'collision'\n")
				case "shared-dir":
					org := h.fixtureOrg()
					testutil.WriteFile(t, filepath.Join(org, "profiles", "collision.toml"), "name = 'collision'\n")
					h.useOrg(org)
				case "cached-git":
					rem := newRemote(h)
					rem.cachedList = []string{fakeSHA}
					testutil.WriteFile(t, filepath.Join(rem.org, "profiles", "collision.toml"), "name = 'collision'\n")
					h.useFakeGit(rem)
					t.Cleanup(func() {
						for _, call := range rem.calls {
							if call == "prepare" {
								t.Error("collision check fetched git")
							}
						}
					})
				}
				if code := h.run("new", "collision", "--scope", scope); code != ui.ExitFailure {
					t.Fatalf("collision exit %d: %s", code, h.errb)
				}
				if !strings.Contains(h.errb.String(), "already exists") {
					t.Fatalf("missing collision explanation: %s", h.errb)
				}
			})
		}
	}
}

func TestNewStorageUnavailableNamespaces(t *testing.T) {
	for _, namespace := range []string{"git", "plugin", "local-dir"} {
		t.Run(namespace, func(t *testing.T) {
			h := newHarness(t)
			var rem *fakeRemote
			switch namespace {
			case "git":
				rem = newRemote(h)
				rem.cachedErr = gitsource.ErrNotCached
				rem.cachedList = []string{fakeSHA}
				h.useFakeGit(rem)
			case "plugin":
				h.writeConfig("[[sources]]\ntype = 'plugin'\nplugin = 'profiles@acme'\n")
			case "local-dir":
				p := filepath.Join(t.TempDir(), "not-a-directory")
				testutil.WriteFile(t, p, "file")
				h.writeConfig("[[sources]]\ntype = 'dir'\npath = " + tomlString(p) + "\n")
			}
			h.mustRun("new", "personal", "--description", "local only")
			if !strings.Contains(h.errb.String(), "unavailable") {
				t.Fatal("missing outage warning")
			}
			if code := h.run("new", "project", "--scope", "project"); code != ui.ExitFailure {
				t.Fatalf("unavailable project namespace: %d, %s", code, h.errb)
			}
			for _, want := range []string{"cannot prove", "unavailable namespace", "ccshelf ls", "No source was fetched or trusted"} {
				if !strings.Contains(h.errb.String(), want) {
					t.Fatalf("missing preparation hint %q: %s", want, h.errb)
				}
			}
			assertNoNewState(t, h, newTarget(t, h, "project", "project"))
			if rem != nil {
				for _, call := range rem.calls {
					if call == "prepare" {
						t.Fatal("collision check fetched")
					}
				}
			}
		})
	}
}

func TestNewStorageValidationBeforeWrite(t *testing.T) {
	for _, scope := range []string{"user", "project"} {
		for _, invalid := range []string{"parent", "mcp", "plugin", "effort", "restricted-parent"} {
			t.Run(scope+"/"+invalid, func(t *testing.T) {
				h := newHarness(t)
				args := []string{"new", "invalid", "--scope", scope, "--yes"}
				switch invalid {
				case "parent":
					args = append(args, "--from", "missing")
				case "mcp":
					args = append(args, "--mcp", "missing")
				case "plugin":
					args = append(args, "--plugin", "bad-id")
				case "effort":
					args = append(args, "--effort", "unknown")
				case "restricted-parent":
					if scope == "user" {
						t.Skip("personal parents may set env")
					}
					h.writeProfile("parent", "name = 'parent'\n[session.env]\nCCSHELF_VAR_TEST = 'value'\n")
					args = append(args, "--from", "parent")
				}
				if code := h.run(args...); code == 0 {
					t.Fatalf("invalid accepted: %v", args)
				}
				assertNoNewState(t, h, newTarget(t, h, scope, "invalid"))
				dir := filepath.Dir(newTarget(t, h, scope, "invalid"))
				if invalid != "restricted-parent" {
					if _, err := os.Stat(dir); !os.IsNotExist(err) {
						t.Fatalf("validation created directory: %v", err)
					}
				}
			})
		}
	}
	h := newHarness(t)
	h.writeProfile("parent", "name = 'parent'\n[plugins]\ninclude = ['safe@acme']\n")
	h.mustRun("new", "child", "--scope", "project", "--from", "parent")
	if _, err := os.Stat(newTarget(t, h, "project", "child")); err != nil {
		t.Fatal(err)
	}
	if code := h.run("new", "bad-scope", "--scope", "team"); code != ui.ExitUsage {
		t.Fatalf("bad scope exit %d", code)
	}
}

func TestNewStorageSymlinkRefusal(t *testing.T) {
	for _, component := range []string{".ccshelf", "profiles"} {
		t.Run(component, func(t *testing.T) {
			h := newHarness(t)
			outside := t.TempDir()
			link := filepath.Join(h.cwd, ".ccshelf")
			if component == "profiles" {
				if err := os.Mkdir(link, 0o700); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(link, "profiles")
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if code := h.run("new", "escape", "--scope", "project"); code == 0 {
				t.Fatal("symlink destination accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("escaped writes: %v, %v", entries, err)
			}
		})
	}
}

func TestNewStorageUserScopeSymlinkedConfigRoot(t *testing.T) {
	h := newHarness(t)
	emptyNewPlugins(t)
	personal, err := profile.PersonalDir()
	if err != nil {
		t.Fatal(err)
	}
	configRoot := filepath.Dir(personal)
	if err := os.MkdirAll(filepath.Dir(configRoot), 0o700); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(configRoot); err == nil && len(entries) > 0 {
		t.Skipf("config root already populated: %v", entries)
	}
	if err := os.Remove(configRoot); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	real := t.TempDir()
	if err := os.Symlink(real, configRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	h.mustRun("new", "linked", "--description", "through a symlinked config root")
	if _, err := os.Stat(filepath.Join(real, "profiles", "linked.toml")); err != nil {
		t.Fatalf("profile not written through the symlinked config root: %v", err)
	}
	// The reader must see exactly what the writer created.
	h.mustRun("show", "linked")
}

func TestNewStoragePersonalProfilesSymlinkRefused(t *testing.T) {
	h := newHarness(t)
	emptyNewPlugins(t)
	personal, err := profile.PersonalDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(personal), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, personal); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if code := h.run("new", "escape", "--description", "x"); code == 0 {
		t.Fatal("symlinked profiles directory accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("escaped writes: %v, %v", entries, err)
	}
}

func TestNewStorageUserScopeUninspectableNamespaces(t *testing.T) {
	for _, marker := range []string{".ccshelf", ".git"} {
		t.Run(marker, func(t *testing.T) {
			h := newHarness(t)
			emptyNewPlugins(t)
			if err := os.Symlink(t.TempDir(), filepath.Join(h.cwd, marker)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			h.mustRun("new", "unblocked", "--description", "user scope")
			if _, err := os.Stat(newTarget(t, h, "user", "unblocked")); err != nil {
				t.Fatalf("user-scope creation failed: %v", err)
			}
			if !strings.Contains(h.errb.String(), "cannot be inspected") {
				t.Fatalf("missing namespace warning: %s", h.errb)
			}
		})
	}
}

func TestNewStorageProjectScopeRefusesUninspectableNamespace(t *testing.T) {
	h := newHarness(t)
	emptyNewPlugins(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(h.cwd, ".ccshelf")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if code := h.run("new", "blocked", "--scope", "project"); code == 0 {
		t.Fatal("project creation accepted an uninspectable namespace")
	}
	assertNoNewState(t, h, newTarget(t, h, "project", "blocked"))
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("escaped writes: %v, %v", entries, err)
	}
}

func TestNewStorageParentPreservesPreparation(t *testing.T) {
	h := newHarness(t)
	rem := newRemote(h)
	h.useFakeGit(rem)
	h.mustRun("new", "child", "--from", "base")
	if len(rem.calls) != 1 || rem.calls[0] != "prepare" {
		t.Fatalf("parent preparation changed: %v", rem.calls)
	}
}

func TestNewStorageWizardAbortAtLocation(t *testing.T) {
	h := newHarness(t)
	h.prompt = ui.NewScripted(ui.ErrAborted)
	if code := h.run("new", "early"); code != ui.ExitInterrupted {
		t.Fatalf("abort exit %d", code)
	}
	assertNoNewState(t, h, newTarget(t, h, "user", "early"))
}

func TestNewStorageMCPOriginAndExistingState(t *testing.T) {
	h := newHarness(t)
	configRaw := "[trust]\ntrust_project_profiles = true\n"
	h.writeConfig(configRaw)
	// An existing empty lock/trust store must remain byte-for-byte untouched.
	stores := map[string]string{"lock.json": "{\"version\":1,\"entries\":[]}\n", "project-trust.json": "{\"version\":1,\"projects\":[]}\n"}
	for file, raw := range stores {
		testutil.WriteFile(t, filepath.Join(h.configDir(), file), raw)
	}
	testutil.WriteFile(t, filepath.Join(h.configDir(), "mcp", "registry.toml"), "[servers.local]\ntype = 'stdio'\ncommand = 'echo'\n")
	h.mustRun("new", "personal-mcp", "--mcp", "local")
	if code := h.run("new", "project-mcp", "--scope", "project", "--mcp", "local", "--yes"); code == 0 || !strings.Contains(h.errb.String(), "mcp.servers") {
		t.Fatalf("project MCP origin restriction missing: %d, %s", code, h.errb)
	}
	h.mustRun("new", "project-safe", "--scope", "project", "--yes")
	for file, want := range stores {
		got, err := os.ReadFile(filepath.Join(h.configDir(), file))
		if err != nil || string(got) != want {
			t.Fatalf("creation changed %s: %q, %v", file, got, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(h.configDir(), "config.toml"))
	if err != nil || string(got) != configRaw {
		t.Fatalf("creation changed config: %q, %v", got, err)
	}
	if code := h.run("run", "project-safe", "--yes"); code == 0 || h.started != 0 {
		t.Fatalf("creation/--yes granted trust: %d, %s", code, h.errb)
	}
}

func TestNewStorageWizardRejectsControlText(t *testing.T) {
	h := newHarness(t)
	emptyNewPlugins(t)
	sc := ui.NewScripted("Hostile \x1b[31m\x07", "")
	h.prompt = sc
	if code := h.run("new", "preview", "--scope", "user", "--plain"); code != ui.ExitUsage {
		t.Fatalf("exit %d, %s", code, h.errb)
	}
	if strings.ContainsAny(h.errb.String(), "\x1b\x07") {
		t.Fatalf("terminal controls in diagnostic: %q", h.errb.String())
	}
	if err := sc.Done(); err != nil {
		t.Fatal(err)
	}
	assertNoNewState(t, h, newTarget(t, h, "user", "preview"))
}

func TestNewStorageHomeProjectHint(t *testing.T) {
	h := newHarness(t)
	h.cwd = h.dirs["HOME"]
	h.mustRun("new", "home-local", "--scope", "project")
	if !strings.Contains(h.errb.String(), "does not discover the home directory") {
		t.Fatalf("unusable trust hint: %s", h.errb)
	}
	if _, err := os.Stat(newTarget(t, h, "project", "home-local")); err != nil {
		t.Fatal(err)
	}
	if code := h.run("run", "home-local", "--yes"); code == 0 || h.started != 0 {
		t.Fatalf("home project activated: %d", code)
	}
}

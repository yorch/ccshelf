package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/cache"
	"github.com/ccshelf/ccshelf/internal/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.Cleanup()
	os.Exit(code)
}

func touchFile(t *testing.T, path string) string {
	t.Helper()
	testutil.WriteFile(t, path, "x")
	return path
}

func fakeLocator(goos string, path []string, home string) locator {
	return locator{
		goos:   goos,
		getenv: func(k string) string { return "" },
		lookPath: func(n string) (string, error) {
			for _, p := range path {
				if b := filepath.Base(p); b == n || (goos == "windows" && strings.TrimSuffix(b, filepath.Ext(b)) == n) {
					return p, nil
				}
			}
			return "", exec.ErrNotFound
		},
		home: func() (string, error) { return home, nil },
	}
}

func TestLocate(t *testing.T) {
	dir := t.TempDir()
	unixBin := touchFile(t, filepath.Join(dir, "bin", "claude"))
	exe := touchFile(t, filepath.Join(dir, "win", "claude.exe"))
	cmdShim := touchFile(t, filepath.Join(dir, "npm", "claude.cmd"))
	home := filepath.Join(dir, "home")
	homeBin := touchFile(t, filepath.Join(home, ".local", "bin", "claude"))
	homeExe := touchFile(t, filepath.Join(home, ".local", "bin", "claude.exe"))

	t.Run("override wins", func(t *testing.T) {
		l := fakeLocator("linux", []string{unixBin}, home)
		l.getenv = func(string) string { return homeBin }
		got, err := l.locate(unixBin)
		if err != nil || got != unixBin {
			t.Fatalf("%q %v", got, err)
		}
	})
	t.Run("env beats PATH", func(t *testing.T) {
		l := fakeLocator("linux", []string{unixBin}, home)
		l.getenv = func(k string) string {
			if k == EnvBinary {
				return homeBin
			}
			return ""
		}
		if got, err := l.locate(""); err != nil || got != homeBin {
			t.Fatalf("%q %v", got, err)
		}
	})
	t.Run("path then home", func(t *testing.T) {
		if got, err := fakeLocator("linux", []string{unixBin}, home).locate(""); err != nil || got != unixBin {
			t.Fatalf("%q %v", got, err)
		}
		if got, err := fakeLocator("linux", nil, home).locate(""); err != nil || got != homeBin {
			t.Fatalf("%q %v", got, err)
		}
	})
	t.Run("not found", func(t *testing.T) {
		_, err := fakeLocator("linux", nil, filepath.Join(dir, "empty")).locate("")
		if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), EnvBinary) {
			t.Fatalf("%v", err)
		}
	})
	t.Run("bad explicit", func(t *testing.T) {
		l := fakeLocator("linux", nil, home)
		for _, p := range []string{filepath.Join(dir, "missing"), dir, "nosuchcommand"} {
			if _, err := l.locate(p); !errors.Is(err, ErrNotFound) {
				t.Errorf("%q: %v", p, err)
			}
		}
		l.getenv = func(string) string { return filepath.Join(dir, "missing") }
		if _, err := l.locate(""); !errors.Is(err, ErrNotFound) {
			t.Errorf("env: %v", err)
		}
	})
	t.Run("bare name via PATH", func(t *testing.T) {
		l := fakeLocator("linux", []string{unixBin}, home)
		if got, err := l.locate("claude"); err != nil || got != unixBin {
			t.Fatalf("%q %v", got, err)
		}
	})
	t.Run("windows prefers exe", func(t *testing.T) {
		l := fakeLocator("windows", []string{cmdShim, exe}, home)
		if got, err := l.locate(""); err != nil || got != exe {
			t.Fatalf("%q %v", got, err)
		}
	})
	t.Run("windows home exe beats shim", func(t *testing.T) {
		l := fakeLocator("windows", []string{cmdShim}, home)
		if got, err := l.locate(""); err != nil || got != homeExe {
			t.Fatalf("%q %v", got, err)
		}
	})
	t.Run("windows shim only", func(t *testing.T) {
		l := fakeLocator("windows", []string{cmdShim}, filepath.Join(dir, "empty"))
		_, err := l.locate("")
		var se *ShimOnlyError
		if !errors.Is(err, ErrShimOnly) || !errors.As(err, &se) || se.Path != cmdShim || !strings.Contains(err.Error(), EnvBinary) || !strings.Contains(err.Error(), "claude.exe") {
			t.Fatalf("%v", err)
		}
		if _, err := l.locate(cmdShim); !errors.Is(err, ErrShimOnly) {
			t.Fatalf("explicit shim: %v", err)
		}
	})
	t.Run("public Locate with override", func(t *testing.T) {
		if got, err := Locate(unixBin); err != nil || got != unixBin {
			t.Fatalf("%q %v", got, err)
		}
	})
}

func TestVersion(t *testing.T) {
	for in, want := range map[string]string{"2.1.291 (Claude Code)\n": "2.1.291", "1.0": "1.0", "claude 3.4.5-beta.1 x": "3.4.5-beta.1"} {
		if got, err := ParseVersion(in); err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseVersion("garbage"); err == nil {
		t.Error("expected error")
	}
	cmp := []struct {
		a, b string
		want int
	}{
		{"2.1.291", "2.1.291", 0},
		{"2.1.291", "2.1.290", 1},
		{"2.1.9", "2.1.10", -1},
		{"2.1", "2.1.0", 0},
		{"2.2", "2.1.999", 1},
		{"v3.0.0", "2.9.9", 1},
		{"2.1.291-beta", "2.1.291", 0},
		{"2.1.291 (Claude Code)", "2.1.291", 0},
		{"", "0", 0},
		{"1.0.0", "", 1},
	}
	for _, c := range cmp {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if !AtLeast("2.1.291", "2.1.290") || AtLeast("2.1.289", "2.1.290") || AtLeast("", "0.0.1") || !AtLeast("2.1.290", "2.1.290") {
		t.Error("AtLeast")
	}
}

func TestVersionCommand(t *testing.T) {
	testutil.IsolatedEnv(t)
	bin := testutil.BuildFakeClaude(t)
	v, err := Version(context.Background(), bin)
	if err != nil || v != "2.1.291" {
		t.Fatalf("%q %v", v, err)
	}
	if _, err := Version(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected start error")
	}
	// Unparsable output.
	if _, err := Version(context.Background(), bin); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_VERSION", "weird")
	if _, err := Version(context.Background(), bin); err == nil {
		t.Error("expected parse error")
	}
}

func TestPluginJSON(t *testing.T) {
	b, err := os.ReadFile("testdata/plugin-list.json")
	if err != nil {
		t.Fatal(err)
	}
	list, err := parseInstalled(b)
	if err != nil || len(list) != 3 {
		t.Fatalf("%v %d", err, len(list))
	}
	p := list[0]
	if p.ID != "chrome-devtools-mcp@claude-plugins-official" || p.Name != "chrome-devtools-mcp" || p.Marketplace != "claude-plugins-official" ||
		p.Version != "1.9.0" || p.Scope != "user" || !p.Enabled || p.ProjectEnabled || p.InstallPath == "" || p.InstalledAt == "" || p.LastUpdated == "" ||
		p.RequiredByOrg || len(p.MCPServers) != 1 || p.Extra != nil {
		t.Fatalf("%+v", p)
	}
	for _, k := range requiredKeys {
		var q Plugin
		in := fmt.Sprintf(`{"id":"a@b","%s":true,"surprise":[1]}`, k)
		if err := json.Unmarshal([]byte(in), &q); err != nil || !q.RequiredByOrg || string(q.Extra["surprise"]) != "[1]" {
			t.Errorf("%s: %v %+v", k, err, q)
		}
		in = fmt.Sprintf(`{"id":"a@b","%s":false}`, k)
		if err := json.Unmarshal([]byte(in), &q); err != nil || q.RequiredByOrg {
			t.Errorf("%s false: %v %+v", k, err, q)
		}
	}
	for _, bad := range []string{`{"id":5}`, `{"scope":"user"}`, `{"id":"a@b","enabled":"yes"}`, `[]`, `{"id":"a@b","mcpServers":[]}`} {
		var q Plugin
		if err := json.Unmarshal([]byte(bad), &q); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	var noMarket Plugin
	_ = json.Unmarshal([]byte(`{"id":"solo"}`), &noMarket)
	if noMarket.Name != "solo" || noMarket.Marketplace != "" {
		t.Errorf("%+v", noMarket)
	}
	if _, err := parseInstalled(nil); err == nil {
		t.Error("empty accepted")
	}
	if _, err := parseInstalled([]byte(`[{"id":1}]`)); err == nil {
		t.Error("bad entry accepted")
	}
	if _, err := parseInstalled([]byte(`oops`)); err == nil {
		t.Error("garbage accepted")
	}
	if l, err := parseInstalled([]byte(`{"installed":[{"id":"a@b"}],"available":[]}`)); err != nil || len(l) != 1 {
		t.Errorf("object form: %v %v", l, err)
	}
	ab, _ := os.ReadFile("testdata/plugin-list-available.json")
	inst, avail, err := parseAvailable(ab)
	if err != nil || len(inst) != 0 || len(avail) != 2 || avail[0].PluginID == "" || avail[0].InstallCount == 0 || len(avail[0].Source) == 0 {
		t.Fatalf("%v %+v", err, avail)
	}
}

func fakeEnv(t *testing.T, extra map[string]string) (bin string, env []string) {
	t.Helper()
	testutil.IsolatedEnv(t)
	bin = testutil.BuildFakeClaude(t)
	return bin, testutil.Environ(extra)
}

func TestListInstalled(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	bin, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_LOG": log})
	work := t.TempDir()
	list, err := ListInstalled(context.Background(), bin, work, env)
	if err != nil || len(list) != 5 {
		t.Fatalf("%v %d", err, len(list))
	}
	if !slices.IsSortedFunc(list, func(a, b Plugin) int { return strings.Compare(a.ID, b.ID) }) {
		t.Error("not sorted")
	}
	inv := testutil.ReadLog(t, log)
	if len(inv) != 1 || !slices.Equal(inv[0].Argv, []string{"plugin", "list", "--json"}) {
		t.Fatalf("%+v", inv)
	}
	want, _ := filepath.EvalSymlinks(work)
	got, _ := filepath.EvalSymlinks(inv[0].Cwd)
	if got != want {
		t.Errorf("cwd %q, want %q", got, want)
	}
}

func TestListInstalledErrors(t *testing.T) {
	bin, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_PLUGIN_LIST_FAIL": "1"})
	_, err := ListInstalled(context.Background(), bin, "", env)
	if err == nil || !strings.Contains(err.Error(), "exited 1") || !strings.Contains(err.Error(), "failed to list plugins") {
		t.Fatalf("%v", err)
	}
	if _, _, err := ListAvailable(context.Background(), bin, "", env); err == nil {
		t.Error("ListAvailable should fail too")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	testutil.WriteFile(t, bad, "not json")
	_, env = fakeEnv(t, map[string]string{"FAKE_CLAUDE_PLUGINS": bad})
	if _, err := ListInstalled(context.Background(), bin, "", env); err == nil {
		t.Error("expected error for unusable fake output")
	}
	if _, err := ListInstalled(context.Background(), filepath.Join(t.TempDir(), "nope"), "", nil); err == nil {
		t.Error("expected start error")
	}
}

func TestListAvailable(t *testing.T) {
	bin, env := fakeEnv(t, nil)
	inst, avail, err := ListAvailable(context.Background(), bin, "", env)
	if err != nil || len(inst) != 5 || len(avail) != 2 || avail[0].PluginID > avail[1].PluginID {
		t.Fatalf("%v %d %d", err, len(inst), len(avail))
	}
}

func TestInstalledCache(t *testing.T) {
	home := t.TempDir()
	log := filepath.Join(t.TempDir(), "log")
	bin, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_LOG": log, "HOME": home, "USERPROFILE": home})
	cdir := filepath.Join(t.TempDir(), "cache")
	if err := cache.Ensure(cdir); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c := &InstalledCache{Dir: cdir, TTL: time.Minute, Now: func() time.Time { return now }}
	work := t.TempDir()
	calls := func() int { return len(testutil.ReadLog(t, log)) }
	list, cached, err := c.List(context.Background(), bin, work, env)
	if err != nil || cached || len(list) != 5 || calls() != 1 {
		t.Fatalf("first: %v %v %d", err, cached, calls())
	}
	list, cached, err = c.List(context.Background(), bin, work, env)
	if err != nil || !cached || len(list) != 5 || calls() != 1 {
		t.Fatalf("second: %v %v %d", err, cached, calls())
	}
	entries, _ := os.ReadDir(cdir)
	if len(entries) != 1 {
		t.Fatalf("cache files: %v", entries)
	}
	// Different cwd is a different key.
	if _, cached, _ := c.List(context.Background(), bin, t.TempDir(), env); cached {
		t.Error("other cwd hit the cache")
	}
	// A changed settings file invalidates.
	testutil.WriteFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), "{}")
	if _, cached, _ := c.List(context.Background(), bin, work, env); cached {
		t.Error("registry change ignored")
	}
	if _, cached, _ := c.List(context.Background(), bin, work, env); !cached {
		t.Error("expected hit after refresh")
	}
	// Project settings change.
	testutil.WriteFile(t, filepath.Join(work, ".claude", "settings.json"), "{}")
	if _, cached, _ := c.List(context.Background(), bin, work, env); cached {
		t.Error("project settings change ignored")
	}
	// ExtraFiles.
	extra := filepath.Join(t.TempDir(), "managed.json")
	c.ExtraFiles = []string{extra}
	if _, cached, _ := c.List(context.Background(), bin, work, env); cached {
		t.Error("new key should miss")
	}
	testutil.WriteFile(t, extra, "{}")
	if _, cached, _ := c.List(context.Background(), bin, work, env); cached {
		t.Error("extra file creation ignored")
	}
	// TTL.
	c.List(context.Background(), bin, work, env)
	now = now.Add(2 * time.Minute)
	if _, cached, _ := c.List(context.Background(), bin, work, env); cached {
		t.Error("expired entry used")
	}
	// A clock that went backwards is doubt.
	now = now.Add(-10 * time.Minute)
	if _, cached, _ := c.List(context.Background(), bin, work, env); cached {
		t.Error("future-dated entry used")
	}
	// CLAUDE_CONFIG_DIR is part of the key.
	env2 := append(slices.Clone(env), "CLAUDE_CONFIG_DIR="+filepath.Join(home, "other"))
	if _, cached, _ := c.List(context.Background(), bin, work, env2); cached {
		t.Error("account switch hit the cache")
	}
}

func TestInstalledCacheCorruption(t *testing.T) {
	bin, env := fakeEnv(t, nil)
	cdir := filepath.Join(t.TempDir(), "cache")
	if err := cache.Ensure(cdir); err != nil {
		t.Fatal(err)
	}
	c := &InstalledCache{Dir: cdir}
	work := t.TempDir()
	if _, _, err := c.List(context.Background(), bin, work, env); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cdir)
	path := filepath.Join(cdir, entries[0].Name())
	orig, _ := os.ReadFile(path)
	for name, mutate := range map[string]func([]byte) []byte{
		"garbage":  func([]byte) []byte { return []byte("{{{") },
		"tampered": func(b []byte) []byte { return bytes.Replace(b, []byte("sre-kit"), []byte("evil-kit"), 1) },
		"wrongkey": func(b []byte) []byte { return bytes.Replace(b, []byte(`"key":"`), []byte(`"key":"0`), 1) },
		"badsum":   func(b []byte) []byte { return bytes.Replace(b, []byte(`"sum":"`), []byte(`"sum":"0`), 1) },
	} {
		if err := os.WriteFile(path, mutate(orig), 0o600); err != nil {
			t.Fatal(err)
		}
		list, cached, err := c.List(context.Background(), bin, work, env)
		if err != nil || cached || len(list) != 5 {
			t.Errorf("%s: %v cached=%v", name, err, cached)
		}
	}
	// No cache dir: always fresh.
	c2 := &InstalledCache{}
	if _, cached, err := c2.List(context.Background(), bin, work, env); err != nil || cached {
		t.Errorf("no dir: %v %v", err, cached)
	}
	// Failures are not cached.
	badBin, badEnv := fakeEnv(t, map[string]string{"FAKE_CLAUDE_PLUGIN_LIST_FAIL": "1"})
	if _, _, err := c.List(context.Background(), badBin, t.TempDir(), badEnv); err == nil {
		t.Error("expected failure")
	}
}

func TestEnvMerge(t *testing.T) {
	got := Env([]string{"A=1", "B=2", "A=3", "bogus", "=x", "C="}, map[string]string{"B": "9", "Z": "z", "D": "d"})
	want := []string{"A=3", "B=9", "C=", "D=d", "Z=z"}
	if !slices.Equal(got, want) {
		t.Fatalf("%v", got)
	}
	if got := Env(nil, nil); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestParseInit(t *testing.T) {
	f, err := os.Open("testdata/stream-init.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	info, err := ParseInit(f)
	if err != nil {
		t.Fatal(err)
	}
	if info.PermissionMode != "bypassPermissions" || info.Model != "claude-haiku" ||
		!slices.Equal(info.Plugins, []string{"feature-dev", "plain-name"}) ||
		!slices.Equal(info.Skills, []string{"pdf", "feature-dev:main"}) ||
		len(info.SlashCommands) != 2 || len(info.Agents) != 1 || len(info.Tools) != 2 ||
		len(info.MCPServers) != 2 || info.MCPServers[1] != (MCPStatus{"claude.ai Slack", "needs-auth"}) {
		t.Fatalf("%+v", info)
	}
	for _, in := range []string{"", `{"type":"system","subtype":"other"}`, "garbage\n", `{"type":"result"}`} {
		if _, err := ParseInit(strings.NewReader(in)); !errors.Is(err, ErrNoInit) {
			t.Errorf("%q: %v", in, err)
		}
	}
	// Last line without newline, snake-case permission key.
	info, err = ParseInit(strings.NewReader(`{"type":"system","subtype":"init","permission_mode":"plan"}`))
	if err != nil || info.PermissionMode != "plan" {
		t.Errorf("%v %+v", err, info)
	}
	if _, err := ParseInit(failingReader{}); err == nil || errors.Is(err, ErrNoInit) {
		t.Errorf("read error: %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestSpawn(t *testing.T) {
	bin, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_EXIT": "5"})
	var out, errb bytes.Buffer
	code, err := Spawn(context.Background(), bin, []string{"x"}, env, strings.NewReader("in"), &out, &errb)
	if err != nil || code != 5 || out.String() != "fake claude: ok\n" {
		t.Fatalf("%d %v %q", code, err, out.String())
	}
	if _, err := Spawn(context.Background(), filepath.Join(t.TempDir(), "nope"), nil, nil, nil, nil, nil); err == nil {
		t.Error("expected start error")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, env = fakeEnv(t, map[string]string{"FAKE_CLAUDE_SLEEP": "20000"})
	start := time.Now()
	if _, err := Spawn(ctx, bin, []string{"x"}, env, nil, &out, &errb); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("timeout: %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Error("timeout not honored")
	}
}

func TestSpawnSignalExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal deaths are Unix behavior")
	}
	code, err := Spawn(context.Background(), "/bin/sh", []string{"-c", "kill -TERM $$"}, nil, nil, nil, nil)
	if err != nil || code != 128+15 {
		t.Fatalf("%d %v", code, err)
	}
}

func TestStartHook(t *testing.T) {
	var gotBin string
	var gotArgs, gotEnv []string
	StartHook = func(bin string, args, env []string) (int, error) {
		gotBin, gotArgs, gotEnv = bin, args, env
		return 9, nil
	}
	defer func() { StartHook = nil }()
	code, err := Start("claude", []string{"--resume"}, []string{"A=1"})
	if code != 9 || err != nil || gotBin != "claude" || !slices.Equal(gotArgs, []string{"--resume"}) || !slices.Equal(gotEnv, []string{"A=1"}) {
		t.Fatalf("%d %v", code, err)
	}
}

func TestSpawnForeground(t *testing.T) {
	bin, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_EXIT": "3"})
	code, err := spawnForeground(bin, []string{"x"}, env)
	if err != nil || code != 3 {
		t.Fatalf("%d %v", code, err)
	}
	if _, err := spawnForeground(filepath.Join(t.TempDir(), "nope"), nil, nil); err == nil {
		t.Error("expected start error")
	}
}

// TestStartHelper is re-executed by TestStartReplacesProcess; on Unix Start
// replaces the helper with the fake claude, so the exit code is the fake's.
func TestStartHelper(t *testing.T) {
	bin := os.Getenv("CCSHELF_START_BIN")
	if bin == "" {
		t.Skip("helper for TestStartReplacesProcess")
	}
	code, err := Start(bin, []string{"--resume"}, os.Environ())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(99)
	}
	os.Exit(code)
}

func TestStartReplacesProcess(t *testing.T) {
	bin, _ := fakeEnv(t, nil)
	exe, err := os.Executable()
	if err != nil {
		t.Skip("no executable")
	}
	log := filepath.Join(t.TempDir(), "log")
	cmd := exec.Command(exe, "-test.run=^TestStartHelper$")
	cmd.Env = testutil.Environ(map[string]string{"CCSHELF_START_BIN": bin, "FAKE_CLAUDE_EXIT": "4", "FAKE_CLAUDE_LOG": log})
	var out bytes.Buffer
	cmd.Stdout = &out
	err = cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 4 {
		t.Fatalf("err = %v, out = %q", err, out.String())
	}
	if !strings.Contains(out.String(), "fake claude: ok") {
		t.Errorf("out = %q", out.String())
	}
	if inv := testutil.ReadLog(t, log); len(inv) != 1 || inv[0].Argv[0] != "--resume" {
		t.Errorf("%+v", inv)
	}
}

func TestStartError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered by spawnForeground")
	}
	code, err := Start(filepath.Join(t.TempDir(), "nope"), nil, nil)
	if err == nil || code != -1 {
		t.Fatalf("%d %v", code, err)
	}
}

func TestReferencedPaths(t *testing.T) {
	agents := `[{"id":"a","settings":"/home/u/.cache/ccshelf/settings-0123456789abcdef.json","nested":{"files":["C:\\x\\y.json","relative/path","not a path"]},"n":5}]`
	bin, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_AGENTS_JSON": agents})
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "FAKE_CLAUDE_AGENTS_JSON="); ok {
			t.Setenv("FAKE_CLAUDE_AGENTS_JSON", v)
		}
	}
	got, err := ReferencedPaths(context.Background(), bin)
	want := []string{`/home/u/.cache/ccshelf/settings-0123456789abcdef.json`, `C:\x\y.json`}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("%v %v", got, err)
	}
	t.Setenv("FAKE_CLAUDE_AGENTS_JSON", "not json")
	if got, err := ReferencedPaths(context.Background(), bin); got != nil || err != nil {
		t.Fatalf("%v %v", got, err)
	}
	t.Setenv("FAKE_CLAUDE_AGENTS_JSON", "[]")
	if got, err := ReferencedPaths(context.Background(), bin); got != nil || err != nil {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := ReferencedPaths(context.Background(), filepath.Join(t.TempDir(), "nope")); got != nil || err != nil {
		t.Fatalf("%v %v", got, err)
	}
	t.Setenv("FAKE_CLAUDE_AGENTS_JSON", "")
	t.Setenv("FAKE_CLAUDE_EXIT", "")
}

func TestExcerpt(t *testing.T) {
	if got := excerpt("a\n  b\tc", 100); got != "a b c" {
		t.Error(got)
	}
	if got := excerpt(strings.Repeat("x", 50), 10); got != strings.Repeat("x", 10)+"..." {
		t.Error(got)
	}
}

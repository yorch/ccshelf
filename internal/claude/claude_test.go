package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yorch/ccshelf/internal/cache"
	"github.com/yorch/ccshelf/internal/testutil"
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
	// An unparsable version is never at least anything, not even zero.
	for _, have := range []string{"", "garbage", "v", "-beta"} {
		for _, want := range []string{"0", "0.0.0", "", "0.0.1"} {
			if AtLeast(have, want) {
				t.Errorf("AtLeast(%q, %q) = true", have, want)
			}
		}
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
	if _, err := parseInstalled([]byte(`{"installed":[{"id":"a@b"}],"available":[]}`)); err == nil {
		t.Error("object form accepted by parseInstalled")
	}
	if l, err := parseInstalled([]byte(" []\n")); err != nil || len(l) != 0 {
		t.Errorf("empty array is valid: %v %v", l, err)
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

func TestEnvMergeWindows(t *testing.T) {
	base := []string{`=C:=C:\work`, `=D:=D:\data`, "Path=C:\\bin", "ComSpec=x", "=nonsense", "=:", "noequals"}
	got := mergeEnv("windows", base, map[string]string{"PATH": "C:\\new", "Extra": "1"})
	want := []string{`=C:=C:\work`, `=D:=D:\data`, "Path=C:\\new", "ComSpec=x", "Extra=1"}
	if !slices.Equal(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}
	// Elsewhere a leading "=" has no name and is dropped.
	if got := mergeEnv("linux", []string{`=C:=C:\work`, "A=1"}, nil); !slices.Equal(got, []string{"A=1"}) {
		t.Fatalf("%q", got)
	}
	if k, v, ok := splitEnv("windows", `=C:=C:\w=x`); k != "=C:" || v != `C:\w=x` || !ok {
		t.Fatalf("%q %q %v", k, v, ok)
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

func TestPluginListBadShapesFailClosed(t *testing.T) {
	cases := []struct{ shape, want string }{
		{`null`, "null"},
		{`{}`, "empty object"},
		{`{"installed":null}`, "installed"},
		{`{"plugins":[{"id":"a@b"}]}`, "plugins"},
		{`"oops"`, "a string"},
		{`5`, "a number"},
		{`true`, "a boolean"},
	}
	bin, _ := fakeEnv(t, nil)
	for _, c := range cases {
		f := filepath.Join(t.TempDir(), "p.json")
		testutil.WriteFile(t, f, c.shape)
		env := testutil.Environ(map[string]string{"FAKE_CLAUDE_PLUGINS": f})
		_, err := ListInstalled(context.Background(), bin, "", env)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("ListInstalled(%s) = %v, want error containing %q", c.shape, err, c.want)
		}
		if _, _, err := ListAvailable(context.Background(), bin, "", env); err == nil {
			t.Errorf("ListAvailable(%s) accepted", c.shape)
		}
		cdir := filepath.Join(t.TempDir(), "cache")
		if err := cache.Ensure(cdir); err != nil {
			t.Fatal(err)
		}
		ic := &InstalledCache{Dir: cdir}
		if _, _, err := ic.List(context.Background(), bin, t.TempDir(), env); err == nil {
			t.Errorf("InstalledCache accepted %s", c.shape)
		}
		if entries, _ := os.ReadDir(cdir); len(entries) != 0 {
			t.Errorf("a bad shape was cached: %v", entries)
		}
	}
	// An empty array is a valid answer.
	f := filepath.Join(t.TempDir(), "p.json")
	testutil.WriteFile(t, f, "[]")
	env := testutil.Environ(map[string]string{"FAKE_CLAUDE_PLUGINS": f})
	if l, err := ListInstalled(context.Background(), bin, "", env); err != nil || len(l) != 0 {
		t.Fatalf("%v %v", l, err)
	}
	inst, avail, err := ListAvailable(context.Background(), bin, "", env)
	if err != nil || len(inst) != 0 || len(avail) != 2 {
		t.Fatalf("%v %v %v", inst, avail, err)
	}
}

func TestParseAvailableShapes(t *testing.T) {
	for _, in := range []string{``, `[]`, `null`, `{}`, `{"installed":{}}`, `{"installed":"x"}`, `{"installed":null,"available":[]}`, `{"installed":[],"available":5}`, `{"installed":[{"id":1}]}`} {
		if _, _, err := parseAvailable([]byte(in)); err == nil {
			t.Errorf("parseAvailable(%q) accepted", in)
		}
	}
	inst, avail, err := parseAvailable([]byte(`{"installed":[],"available":null}`))
	if err != nil || len(inst) != 0 || len(avail) != 0 {
		t.Errorf("%v %v %v", inst, avail, err)
	}
	if got := shapeOf([]byte(`{oops`)); got != "malformed JSON" {
		t.Errorf("%q", got)
	}
}

func TestRequiredByOrgFromManagedPolicy(t *testing.T) {
	bin, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_MANAGED": `{"enabledPlugins":{"sre-kit@acme":true,"seo-tools@acme":false}}`})
	list, err := ListInstalled(context.Background(), bin, "", env)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if want := p.ID == "sre-kit@acme"; p.RequiredByOrg != want {
			t.Errorf("%s RequiredByOrg = %v, want %v", p.ID, p.RequiredByOrg, want)
		}
	}
}

func TestListOutputLimit(t *testing.T) {
	bin, env := fakeEnv(t, nil)
	old := maxListOutput
	maxListOutput = 512
	defer func() { maxListOutput = old }()
	_, err := ListInstalled(context.Background(), bin, "", env)
	if err == nil || !strings.Contains(err.Error(), "more than 512 bytes") {
		t.Fatalf("%v", err)
	}
	var b limitedBuffer
	if _, err := b.Write(make([]byte, 400)); err != nil || b.over {
		t.Fatal("under the limit")
	}
	if n, err := b.Write(make([]byte, 400)); err != nil || n != 400 || !b.over || b.Len() != 400 {
		t.Fatalf("over the limit: %d %v %v %d", n, err, b.over, b.Len())
	}
}

func TestCwdDependentList(t *testing.T) {
	work := t.TempDir()
	bin, env := fakeEnv(t, map[string]string{
		"FAKE_CLAUDE_PROJECT_SETTINGS": `{"enabledPlugins":{"design-kit@acme":false}}`,
		"FAKE_CLAUDE_PROJECT_DIR":      work,
	})
	find := func(dir string) Plugin {
		list, err := ListInstalled(context.Background(), bin, dir, env)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range list {
			if p.ID == "design-kit@acme" {
				return p
			}
		}
		t.Fatal("design-kit missing")
		return Plugin{}
	}
	if p := find(t.TempDir()); !p.Enabled {
		t.Errorf("elsewhere: %+v", p)
	}
	if p := find(work); p.Enabled || p.ProjectEnabled {
		t.Errorf("in the project: %+v", p)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o700); err != nil { //nolint:gosec // a test copy of the fake binary must be executable
		t.Fatal(err)
	}
}

// cacheHarness runs List and reports whether it was served from the cache.
type cacheHarness struct {
	t    *testing.T
	c    *InstalledCache
	bin  string
	work string
	env  []string
	home string
}

func newCacheHarness(t *testing.T) *cacheHarness {
	t.Helper()
	home := t.TempDir()
	fake, env := fakeEnv(t, map[string]string{"HOME": home, "USERPROFILE": home})
	bin := filepath.Join(t.TempDir(), filepath.Base(fake))
	copyFile(t, fake, bin)
	cdir := filepath.Join(t.TempDir(), "cache")
	if err := cache.Ensure(cdir); err != nil {
		t.Fatal(err)
	}
	h := &cacheHarness{
		t: t, bin: bin, work: t.TempDir(), env: env, home: home,
		c: &InstalledCache{Dir: cdir, ManagedFiles: []string{}},
	}
	if _, cached, err := h.c.List(context.Background(), bin, h.work, env); err != nil || cached {
		t.Fatalf("prime: %v %v", err, cached)
	}
	if _, cached, _ := h.c.List(context.Background(), bin, h.work, env); !cached {
		t.Fatal("second call should hit")
	}
	return h
}

func (h *cacheHarness) cached() bool {
	h.t.Helper()
	_, cached, err := h.c.List(context.Background(), h.bin, h.work, h.env)
	if err != nil {
		h.t.Fatal(err)
	}
	return cached
}

func TestInstalledCacheFingerprintStamps(t *testing.T) {
	t.Run("user settings.json", func(t *testing.T) {
		h := newCacheHarness(t)
		testutil.WriteFile(t, filepath.Join(h.home, ".claude", "settings.json"), "{}")
		if h.cached() {
			t.Error("user settings creation ignored")
		}
		if !h.cached() {
			t.Error("expected a hit after refresh")
		}
		testutil.WriteFile(t, filepath.Join(h.home, ".claude", "settings.json"), `{"a":1}`)
		if h.cached() {
			t.Error("user settings edit ignored")
		}
	})
	t.Run("settings.local.json", func(t *testing.T) {
		h := newCacheHarness(t)
		testutil.WriteFile(t, filepath.Join(h.work, ".claude", "settings.local.json"), "{}")
		if h.cached() {
			t.Error("settings.local.json creation ignored")
		}
	})
	t.Run("project settings.json", func(t *testing.T) {
		h := newCacheHarness(t)
		testutil.WriteFile(t, filepath.Join(h.work, ".claude", "settings.json"), "{}")
		if h.cached() {
			t.Error("project settings creation ignored")
		}
	})
	t.Run("claude binary", func(t *testing.T) {
		h := newCacheHarness(t)
		later := time.Now().Add(time.Hour)
		if err := os.Chtimes(h.bin, later, later); err != nil {
			t.Fatal(err)
		}
		if h.cached() {
			t.Error("claude binary mtime ignored")
		}
		if !h.cached() {
			t.Error("expected a hit after refresh")
		}
	})
	t.Run("registry", func(t *testing.T) {
		h := newCacheHarness(t)
		testutil.WriteFile(t, filepath.Join(h.home, ".claude", "plugins", "installed_plugins.json"), "{}")
		if h.cached() {
			t.Error("registry ignored")
		}
	})
	t.Run("same size and mtime, different content", func(t *testing.T) {
		h := newCacheHarness(t)
		p := filepath.Join(h.home, ".claude", "settings.json")
		testutil.WriteFile(t, p, `{"a":1}`)
		h.cached() // refresh
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !h.cached() {
			t.Fatal("expected a hit")
		}
		testutil.WriteFile(t, p, `{"a":2}`)
		if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
			t.Fatal(err)
		}
		if h.cached() {
			t.Error("an edit that kept size and mtime was missed")
		}
	})
	t.Run("sub-second mtime", func(t *testing.T) {
		h := newCacheHarness(t)
		p := filepath.Join(h.work, ".claude", "settings.json")
		testutil.WriteFile(t, p, "{}")
		base := time.Unix(1_700_000_000, 0)
		if err := os.Chtimes(p, base, base); err != nil {
			t.Fatal(err)
		}
		h.cached()
		if !h.cached() {
			t.Fatal("expected a hit")
		}
		if err := os.Chtimes(p, base.Add(50*time.Millisecond), base.Add(50*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if h.cached() {
			t.Error("a 50 ms mtime change was missed")
		}
	})
}

func TestInstalledCacheManagedFiles(t *testing.T) {
	h := newCacheHarness(t)
	managed := filepath.Join(t.TempDir(), "managed-settings.json")
	h.c.ManagedFiles = []string{managed}
	if h.cached() {
		t.Fatal("a new key should miss")
	}
	if !h.cached() {
		t.Fatal("expected a hit")
	}
	testutil.WriteFile(t, managed, `{"enabledPlugins":{"a@b":true}}`)
	if h.cached() {
		t.Error("managed settings creation ignored")
	}
	testutil.WriteFile(t, managed, `{"enabledPlugins":{"a@b":false}}`)
	if h.cached() {
		t.Error("managed settings edit ignored")
	}
}

func TestManagedLocations(t *testing.T) {
	cases := []struct {
		goos      string
		file, dir string
	}{
		{"linux", "/etc/claude-code/managed-settings.json", "/etc/claude-code/managed-settings.d"},
		{"linux", "/mnt/c/Program Files/ClaudeCode/managed-settings.json", "/mnt/c/Program Files/ClaudeCode/managed-settings.d"},
		{"darwin", "/Library/Application Support/ClaudeCode/managed-settings.json", "/Library/Application Support/ClaudeCode/managed-settings.d"},
		{"darwin", "/Library/Managed Preferences/com.anthropic.claudecode.plist", ""},
		{"windows", `C:\Program Files\ClaudeCode\managed-settings.json`, `C:\Program Files\ClaudeCode\managed-settings.d`},
	}
	for _, c := range cases {
		files, dirs := ManagedLocations(c.goos)
		if !slices.Contains(files, c.file) || (c.dir != "" && !slices.Contains(dirs, c.dir)) {
			t.Errorf("%s: %v %v", c.goos, files, dirs)
		}
	}
	// The default fingerprint of the running OS covers them (no ManagedFiles).
	var c InstalledCache
	if _, err := c.fingerprint("x", t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledCacheDropIn(t *testing.T) {
	dir := t.TempDir()
	if s := dirStamp(filepath.Join(dir, "missing")); !strings.Contains(s, "absent") {
		t.Fatal(s)
	}
	a := dirStamp(dir)
	testutil.WriteFile(t, filepath.Join(dir, "10-a.json"), "{}")
	b := dirStamp(dir)
	testutil.WriteFile(t, filepath.Join(dir, "notes.txt"), "x")
	if a == b || dirStamp(dir) != b {
		t.Fatalf("%q %q", a, b)
	}
	testutil.WriteFile(t, filepath.Join(dir, "10-a.json"), `{"x":1}`)
	if dirStamp(dir) == b {
		t.Fatal("drop-in edit missed")
	}
}

func TestInstalledCacheStoresOnlyNeededFields(t *testing.T) {
	plugins := filepath.Join(t.TempDir(), "p.json")
	testutil.WriteFile(t, plugins, `[{"id":"ctx@m","version":"1","scope":"user","enabled":true,"installPath":"/p","requiredByOrg":true,
	  "mcpServers":{"srv":{"command":"npx","env":{"TOKEN":"supersecret"}},"other":{}},"hooks":{"x":"secret-hook"},"surprise":42}]`)
	bin, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_PLUGINS": plugins})
	cdir := filepath.Join(t.TempDir(), "cache")
	if err := cache.Ensure(cdir); err != nil {
		t.Fatal(err)
	}
	c := &InstalledCache{Dir: cdir, ManagedFiles: []string{}}
	work := t.TempDir()
	fresh, cached, err := c.List(context.Background(), bin, work, env)
	if err != nil || cached || len(fresh) != 1 || len(fresh[0].MCPServers) != 2 || fresh[0].Extra == nil {
		t.Fatalf("%v %v %+v", err, cached, fresh)
	}
	entries, _ := os.ReadDir(cdir)
	if len(entries) != 1 {
		t.Fatal(entries)
	}
	raw, _ := os.ReadFile(filepath.Join(cdir, entries[0].Name()))
	for _, leak := range []string{"supersecret", "npx", "secret-hook", "surprise"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the cache file contains %q: %s", leak, raw)
		}
	}
	got, cached, err := c.List(context.Background(), bin, work, env)
	if err != nil || !cached || len(got) != 1 {
		t.Fatalf("%v %v", err, cached)
	}
	p := got[0]
	if p.ID != "ctx@m" || p.Name != "ctx" || p.Marketplace != "m" || p.Version != "1" || p.Scope != "user" || !p.Enabled || p.InstallPath != "/p" || !p.RequiredByOrg {
		t.Errorf("%+v", p)
	}
	names := make([]string, 0, len(p.MCPServers))
	for n := range p.MCPServers {
		names = append(names, n)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"other", "srv"}) {
		t.Errorf("mcp server names %v", names)
	}
	// An entry with a bad plugin is doubt, not a hit.
	bad := bytes.Replace(raw, []byte(`"id":"ctx@m"`), []byte(`"id":""`), 1)
	if _, ok := fromCached([]cachedPlugin{{}}); ok {
		t.Error("a cached plugin without an id was accepted")
	}
	if err := os.WriteFile(filepath.Join(cdir, entries[0].Name()), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, cached, _ := c.List(context.Background(), bin, work, env); cached {
		t.Error("a tampered entry was used")
	}
}

func TestInstalledCacheNameIs128Bits(t *testing.T) {
	bin, env := fakeEnv(t, nil)
	cdir := filepath.Join(t.TempDir(), "cache")
	if err := cache.Ensure(cdir); err != nil {
		t.Fatal(err)
	}
	c := &InstalledCache{Dir: cdir, ManagedFiles: []string{}}
	if _, _, err := c.List(context.Background(), bin, t.TempDir(), env); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(cdir)
	if len(entries) != 1 || !regexp.MustCompile(`^installed-[0-9a-f]{32}\.json$`).MatchString(entries[0].Name()) {
		t.Fatalf("%v", entries)
	}
}

// TestForegroundHelper is re-executed by the foreground tests as the child.
func TestForegroundHelper(t *testing.T) {
	mode := os.Getenv("CCSHELF_FG_MODE")
	if mode == "" {
		t.Skip("helper for the spawnForeground tests")
	}
	if mode == "ignore" {
		signal.Ignore(syscall.SIGTERM)
	}
	if ready := os.Getenv("CCSHELF_FG_READY"); ready != "" {
		_ = os.WriteFile(ready, []byte("ok"), 0o600)
	}
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

func runForegroundChild(t *testing.T, mode string, grace time.Duration, send func(chan<- os.Signal, string)) (code int, elapsed time.Duration) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix signal semantics")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skip("no executable")
	}
	ready := filepath.Join(t.TempDir(), "ready")
	env := testutil.Environ(map[string]string{"CCSHELF_FG_MODE": mode, "CCSHELF_FG_READY": ready})
	sigs := make(chan os.Signal, 4)
	type result struct {
		code int
		err  error
	}
	res := make(chan result, 1)
	start := time.Now()
	go func() {
		c, err := runForeground(exe, []string{"-test.run=^TestForegroundHelper$"}, env, sigs, grace)
		res <- result{c, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper child never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	send(sigs, mode)
	select {
	case r := <-res:
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.code, time.Since(start)
	case <-time.After(15 * time.Second):
		t.Fatal("runForeground did not return: the child was never killed")
		return 0, 0
	}
}

func TestForegroundTermIsDeliveredToChild(t *testing.T) {
	// A child that handles SIGTERM by default dies from it, long before the
	// grace period: the termination was delivered, not turned into a kill.
	code, elapsed := runForegroundChild(t, "default", time.Hour, func(sigs chan<- os.Signal, _ string) {
		sigs <- os.Interrupt // ignored by the launcher
		time.Sleep(100 * time.Millisecond)
		sigs <- syscall.SIGTERM
	})
	if code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit code %d, want %d (SIGTERM), so the child did not get SIGTERM", code, 128+int(syscall.SIGTERM))
	}
	if elapsed > 10*time.Second {
		t.Fatalf("took %v", elapsed)
	}
}

func TestForegroundKillsAfterGrace(t *testing.T) {
	grace := 400 * time.Millisecond
	code, elapsed := runForegroundChild(t, "ignore", grace, func(sigs chan<- os.Signal, _ string) {
		sigs <- syscall.SIGTERM
		sigs <- syscall.SIGTERM // a repeated request must not restart the clock
	})
	if code != 128+int(syscall.SIGKILL) {
		t.Fatalf("exit code %d, want %d (SIGKILL)", code, 128+int(syscall.SIGKILL))
	}
	if elapsed < grace {
		t.Fatalf("the child was killed after %v, before the %v grace period", elapsed, grace)
	}
}

func TestForegroundInterruptIsIgnored(t *testing.T) {
	// Ctrl+C reaches the child itself; the launcher must neither die nor
	// kill the child. The child is ended with a TERM afterwards.
	code, _ := runForegroundChild(t, "default", time.Hour, func(sigs chan<- os.Signal, _ string) {
		for i := 0; i < 3; i++ {
			sigs <- os.Interrupt
		}
		time.Sleep(200 * time.Millisecond)
		sigs <- syscall.SIGTERM
	})
	if code != 128+int(syscall.SIGTERM) {
		t.Fatalf("exit code %d", code)
	}
}

func TestParseMarketplaces(t *testing.T) {
	good := `[{"name":"a","source":"github","repo":"o/r","installLocation":"/x"},
	{"name":"b","source":"git","url":"https://h/o/r.git","installLocation":"/y","extra":1},
	{"name":"c","source":"directory","path":"/p","installLocation":"/p"}]`
	list, err := parseMarketplaces([]byte(good))
	if err != nil || len(list) != 3 {
		t.Fatalf("parse: %v %v", list, err)
	}
	for i, want := range []string{"o/r", "https://h/o/r.git", "/p"} {
		got, err := list[i].Origin()
		if err != nil || got != want {
			t.Errorf("origin %d = %q, %v; want %q", i, got, err, want)
		}
	}
	if list[1].Extra["extra"] == nil {
		t.Error("unknown key not kept")
	}
	if l, err := parseMarketplaces([]byte(" [] ")); err != nil || len(l) != 0 {
		t.Errorf("empty array: %v %v", l, err)
	}
	for name, in := range map[string]string{
		"empty": "", "null": "null", "object": `{"a":1}`, "string": `"x"`,
		"no name": `[{"source":"git","url":"u"}]`, "bad type": `[{"name":1}]`,
		"non-object": `[1]`, "truncated": `[{"name":"a"`,
	} {
		if _, err := parseMarketplaces([]byte(in)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	for name, m := range map[string]Marketplace{
		"unknown kind": {Name: "a", Kind: "weird", URL: "u"},
		"no repo":      {Name: "a", Kind: "github"},
		"no url":       {Name: "a", Kind: "git"},
		"no path":      {Name: "a", Kind: "directory"},
	} {
		if _, err := m.Origin(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := MarketplaceOrigin(list, "zz"); err == nil {
		t.Error("unknown marketplace accepted")
	}
	if _, err := MarketplaceOrigin(append(list, list[0]), "a"); err == nil {
		t.Error("duplicate marketplace accepted")
	}
	if got, err := MarketplaceOrigin(list, "b"); err != nil || got != "https://h/o/r.git" {
		t.Errorf("MarketplaceOrigin = %q, %v", got, err)
	}
}

func TestListMarketplaces(t *testing.T) {
	log := filepath.Join(t.TempDir(), "log")
	bin, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_LOG": log})
	list, err := ListMarketplaces(context.Background(), bin, t.TempDir(), env)
	if err != nil || len(list) != 2 || list[0].Name != "acme" || list[1].Name != "claude-plugins-official" {
		t.Fatalf("%v %+v", err, list)
	}
	if o, err := MarketplaceOrigin(list, "acme"); err != nil || o != "acme/plugins" {
		t.Errorf("origin = %q, %v", o, err)
	}
	inv := testutil.ReadLog(t, log)
	if len(inv) != 1 || !slices.Equal(inv[0].Argv, []string{"plugin", "marketplace", "list", "--json"}) {
		t.Fatalf("%+v", inv)
	}

	// A custom listing, and every way the output can be unusable.
	for name, tc := range map[string]struct {
		content string
		wantErr string
	}{
		"git source":   {`[{"name":"x","source":"git","url":"https://h.example/o/r.git","installLocation":"/y"}]`, ""},
		"null":         {`null`, "expected a JSON array"},
		"object":       {`{"marketplaces":[]}`, "expected a JSON array"},
		"missing name": {`[{"source":"git","url":"u"}]`, "no name"},
	} {
		f := filepath.Join(t.TempDir(), "mk.json")
		testutil.WriteFile(t, f, tc.content)
		_, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_MARKETPLACES": f})
		got, err := ListMarketplaces(context.Background(), bin, "", env)
		if tc.wantErr == "" {
			if err != nil || len(got) != 1 {
				t.Errorf("%s: %v %v", name, got, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.wantErr)
		}
	}

	_, env = fakeEnv(t, map[string]string{"FAKE_CLAUDE_MARKETPLACES_FAIL": "1"})
	if _, err := ListMarketplaces(context.Background(), bin, "", env); err == nil || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("failing claude: %v", err)
	}
	if _, err := ListMarketplaces(context.Background(), filepath.Join(t.TempDir(), "nope"), "", nil); err == nil {
		t.Error("expected start error")
	}
}

func TestListMarketplacesOverTheLimitIsAnError(t *testing.T) {
	old := maxListOutput
	maxListOutput = 128
	defer func() { maxListOutput = old }()
	big := filepath.Join(t.TempDir(), "mk.json")
	testutil.WriteFile(t, big, `[{"name":"x","source":"git","url":"https://h.example/o/r.git","installLocation":"/y","pad":"`+strings.Repeat("a", 400)+`"}]`)
	bin, env := fakeEnv(t, map[string]string{"FAKE_CLAUDE_MARKETPLACES": big})
	if _, err := ListMarketplaces(context.Background(), bin, "", env); err == nil || !strings.Contains(err.Error(), "more than 128 bytes") {
		t.Fatalf("err = %v, want the size error", err)
	}
}

func TestMarketplaceIdentity(t *testing.T) {
	parse := func(t *testing.T, j string) Marketplace {
		t.Helper()
		l, err := parseMarketplaces([]byte("[" + j + "]"))
		if err != nil || len(l) != 1 {
			t.Fatalf("%v %v", l, err)
		}
		return l[0]
	}
	exp := func(t *testing.T, raw string) string {
		t.Helper()
		id, err := ExpectedIdentity(raw)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	cases := []struct {
		name     string
		json     string
		expected string // "" = must not equal any expected form
		wantID   string
	}{
		{"github", `{"name":"a","source":"github","repo":"Acme/Plugins"}`, "acme/plugins", "github:acme/plugins"},
		{"github with ref", `{"name":"a","source":"github","repo":"acme/plugins","ref":"dev"}`, "", "github:acme/plugins@dev"},
		{"github with path", `{"name":"a","source":"github","repo":"acme/plugins","path":"sub"}`, "", "github:acme/plugins#sub"},
		{"git https", `{"name":"a","source":"git","url":"https://GHE.Example.com/acme/plugins.git"}`, "https://ghe.example.com/acme/plugins/", "git:ghe.example.com/acme/plugins"},
		{"git ssh equals https", `{"name":"a","source":"git","url":"ssh://git@ghe.example.com:22/acme/plugins.git"}`, "https://ghe.example.com/acme/plugins", "git:ghe.example.com/acme/plugins"},
		{"git scp equals https", `{"name":"a","source":"git","url":"git@ghe.example.com:acme/plugins.git"}`, "https://ghe.example.com/acme/plugins", "git:ghe.example.com/acme/plugins"},
		{"git path case matters", `{"name":"a","source":"git","url":"https://ghe.example.com/Acme/plugins"}`, "", "git:ghe.example.com/Acme/plugins"},
		{"git with ref", `{"name":"a","source":"git","url":"https://ghe.example.com/acme/plugins","ref":"v2"}`, "", "git:ghe.example.com/acme/plugins@v2"},
		{"url kind", `{"name":"a","source":"url","url":"https://github.com/acme/plugins"}`, "", "url:github.com/acme/plugins"},
		{"directory path that looks like a repo", `{"name":"a","source":"directory","path":"acme/plugins"}`, "", ""},
		{"plain http", `{"name":"a","source":"git","url":"http://ghe.example.com/acme/plugins"}`, "", "git:http://ghe.example.com/acme/plugins"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parse(t, tc.json).Identity()
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantID != "" && got != tc.wantID {
				t.Errorf("Identity = %q, want %q", got, tc.wantID)
			}
			if tc.expected != "" {
				if want := exp(t, tc.expected); got != want {
					t.Errorf("Identity = %q, ExpectedIdentity(%q) = %q", got, tc.expected, want)
				}
				return
			}
			for _, e := range []string{"acme/plugins", "https://github.com/acme/plugins", "https://ghe.example.com/acme/plugins"} {
				if got == exp(t, e) {
					t.Errorf("Identity %q must not equal the expected %q", got, e)
				}
			}
		})
	}
	// A directory source is identified by a hash, never the machine path.
	d, err := parse(t, `{"name":"a","source":"directory","path":"/home/me/plugins"}`).Identity()
	if err != nil || strings.Contains(d, "home") || !strings.HasPrefix(d, "directory:sha256-") {
		t.Errorf("directory identity = %q, %v", d, err)
	}
	for name, m := range map[string]Marketplace{
		"bad ref type": parse(t, `{"name":"a","source":"github","repo":"o/r","ref":1}`),
		"bad url":      {Name: "a", Kind: "git", URL: "not a url"},
		"no path":      {Name: "a", Kind: "git", URL: "https://h.example/"},
		"unknown kind": {Name: "a", Kind: "weird", URL: "u"},
	} {
		if _, err := m.Identity(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	for _, bad := range []string{"", "nonsense", "https://"} {
		if _, err := ExpectedIdentity(bad); err == nil {
			t.Errorf("ExpectedIdentity(%q): no error", bad)
		}
	}
	list := []Marketplace{parse(t, `{"name":"a","source":"github","repo":"o/r"}`)}
	if id, err := MarketplaceIdentity(list, "a"); err != nil || id != "github:o/r" {
		t.Errorf("MarketplaceIdentity = %q, %v", id, err)
	}
	if _, err := MarketplaceIdentity(list, "zz"); err == nil {
		t.Error("unknown marketplace accepted")
	}
	if _, err := MarketplaceIdentity(append(list, list[0]), "a"); err == nil {
		t.Error("duplicate marketplace accepted")
	}
}

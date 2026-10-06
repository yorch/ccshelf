package testutil

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	code := m.Run()
	Cleanup()
	os.Exit(code)
}

func TestIsolatedEnv(t *testing.T) {
	d := IsolatedEnv(t)
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "APPDATA", "LOCALAPPDATA"} {
		if got := os.Getenv(k); got == "" || got != d[k] {
			t.Errorf("%s = %q, want %q", k, got, d[k])
		}
	}
	if os.Getenv("GIT_CONFIG_GLOBAL") == "" || os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		t.Error("git or claude config not isolated")
	}
	if fi, err := os.Stat(d["WORK"]); err != nil || !fi.IsDir() {
		t.Error("work dir missing")
	}
}

func TestHelpers(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b.txt")
	WriteFile(t, p, "x")
	if b, _ := os.ReadFile(p); string(b) != "x" {
		t.Fatal("WriteFile")
	}
	pf := PluginsFile(t, "a@m", "b@m")
	b, _ := os.ReadFile(pf)
	if !strings.Contains(string(b), `"id": "a@m"`) || !strings.Contains(string(b), `"id": "b@m"`) {
		t.Fatalf("%s", b)
	}
	if got := ReadLog(t, filepath.Join(t.TempDir(), "none")); got != nil {
		t.Fatal("missing log should be empty")
	}
	env := Environ(map[string]string{"CCSHELF_TEST_X": "1"})
	if env[len(env)-1] != "CCSHELF_TEST_X=1" {
		t.Fatal("Environ")
	}
}

func run(t *testing.T, bin string, extra map[string]string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = Environ(extra)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

func TestBuiltFakeClaude(t *testing.T) {
	IsolatedEnv(t)
	bin := BuildFakeClaude(t)
	if again := BuildFakeClaude(t); again != bin {
		t.Fatal("not built once")
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(bin, ".exe") {
		t.Fatal("missing .exe")
	}
	if code, out, _ := run(t, bin, nil, "--version"); code != 0 || out != "2.1.291 (Claude Code)\n" {
		t.Fatalf("%d %q", code, out)
	}
	log := filepath.Join(t.TempDir(), "log")
	code, out, _ := run(t, bin, map[string]string{"FAKE_CLAUDE_LOG": log, "FAKE_CLAUDE_EXIT": "3", "CCSHELF_PROFILE": "p"}, "--resume")
	if code != 3 || out != "fake claude: ok\n" {
		t.Fatalf("%d %q", code, out)
	}
	inv := ReadLog(t, log)
	if len(inv) != 1 || inv[0].Argv[0] != "--resume" || inv[0].Env["CCSHELF_PROFILE"] != "p" {
		t.Fatalf("%+v", inv)
	}
}

func TestFakeClaudeSleepAndStdin(t *testing.T) {
	bin := BuildFakeClaude(t)
	log := filepath.Join(t.TempDir(), "log")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "x")
	cmd.Env = Environ(map[string]string{"FAKE_CLAUDE_LOG": log, "FAKE_CLAUDE_SLEEP": "50"})
	cmd.Stdin = strings.NewReader("data")
	start := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Error("did not sleep")
	}
	if inv := ReadLog(t, log); len(inv) != 1 || !inv[0].StdinPiped {
		t.Fatalf("%+v", inv)
	}
}

// Package testutil holds helpers shared by ccshelf's tests: a build of the
// fake claude (internal/testutil/fakeclaude), an isolated environment that
// cannot touch the real home directory, and small file helpers.
//
// Tests must never call the real claude binary. Build the fake with
// [BuildFakeClaude], point CCSHELF_CLAUDE or an explicit override at it, and
// drive it with the FAKE_CLAUDE_* variables documented in the fakeclaude
// command. Use [IsolatedEnv] first in every test that spawns processes so
// that HOME, XDG_* and git configuration are temporary.
package testutil

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// origEnviron is the process environment before any test changed it; go
// build must keep seeing the real GOCACHE and GOPATH even after IsolatedEnv.
var origEnviron = os.Environ()

var (
	buildOnce sync.Once
	buildPath string
	buildDir  string
	buildErr  error
)

// BuildFakeClaude builds the fake claude once per test process and returns
// its path. The test is skipped when the go tool is not on PATH. Call
// [Cleanup] from TestMain to remove the build directory.
func BuildFakeClaude(t testing.TB) string {
	t.Helper()
	buildOnce.Do(func() {
		goBin, err := exec.LookPath("go")
		if err != nil {
			buildErr = fmt.Errorf("go tool not on PATH: %w", err)
			return
		}
		buildDir, err = os.MkdirTemp("", "ccshelf-fakeclaude-")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(buildDir, "claude")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		cmd := exec.CommandContext(context.Background(), goBin, "build", "-o", out, "github.com/yorch/ccshelf/internal/testutil/fakeclaude") //nolint:gosec // test helper building a fixed package
		cmd.Env = origEnviron
		if b, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build fakeclaude: %w\n%s", err, b)
			return
		}
		buildPath = out
	})
	if buildErr != nil {
		if strings.Contains(buildErr.Error(), "not on PATH") {
			t.Skip(buildErr)
		}
		t.Fatal(buildErr)
	}
	return buildPath
}

// Cleanup removes the directory made by [BuildFakeClaude]. Call it from
// TestMain after m.Run.
func Cleanup() {
	if buildDir != "" {
		_ = os.RemoveAll(buildDir)
	}
}

// IsolatedEnv points HOME, USERPROFILE, XDG_CONFIG_HOME, XDG_CACHE_HOME,
// APPDATA, LOCALAPPDATA and git's global and system configuration at fresh
// temporary directories for the duration of the test, clears CLAUDE_CONFIG_DIR
// and CCSHELF_CLAUDE, and returns the directories as a map (keys: HOME,
// XDG_CONFIG_HOME, XDG_CACHE_HOME, APPDATA, LOCALAPPDATA, USERPROFILE and
// WORK, an empty working directory). Cleanup is automatic.
func IsolatedEnv(t testing.TB) map[string]string {
	t.Helper()
	root := t.TempDir()
	d := map[string]string{
		"HOME":            filepath.Join(root, "home"),
		"XDG_CONFIG_HOME": filepath.Join(root, "home", ".config"),
		"XDG_CACHE_HOME":  filepath.Join(root, "home", ".cache"),
		// On Windows the config and cache directories come from APPDATA and
		// LOCALAPPDATA; pointing them at the same places as the XDG variables
		// (all below HOME) lets tests locate them the same way on every OS.
		"APPDATA":      filepath.Join(root, "home", ".config"),
		"LOCALAPPDATA": filepath.Join(root, "home", ".cache"),
		"WORK":         filepath.Join(root, "work"),
	}
	for _, p := range d {
		if err := os.MkdirAll(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The launcher makes some cache directories read-only. Give the owner
	// write permission back so that the temporary directory can go away.
	t.Cleanup(func() { restoreWrite(root) })
	gitcfg := filepath.Join(root, "gitconfig")
	if err := os.WriteFile(gitcfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for k, v := range d {
		if k != "WORK" {
			t.Setenv(k, v)
		}
	}
	t.Setenv("USERPROFILE", d["HOME"])
	t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
	t.Setenv("GIT_CONFIG_SYSTEM", gitcfg)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CCSHELF_CLAUDE", "")
	d["USERPROFILE"] = d["HOME"]
	return d
}

// WriteFile writes content to path (creating parent directories, mode 0600)
// and fails the test on error.
func WriteFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// PluginsFile writes a plugin-list JSON file (the format of
// `claude plugin list --json`) with one user-scope enabled plugin per id and
// returns its path, suitable for FAKE_CLAUDE_PLUGINS.
func PluginsFile(t testing.TB, ids ...string) string {
	t.Helper()
	list := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		name, _, _ := strings.Cut(id, "@")
		list = append(list, map[string]any{
			"id": id, "version": "1.0.0", "scope": "user", "enabled": true,
			"installPath": filepath.ToSlash(filepath.Join(os.TempDir(), "fake-plugins", name)),
			"installedAt": "2026-01-01T00:00:00.000Z", "lastUpdated": "2026-01-01T00:00:00.000Z",
			"projectEnabled": false,
		})
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "plugins.json")
	WriteFile(t, path, string(b))
	return path
}

// Invocation is one recorded call of the fake claude.
type Invocation struct {
	Argv       []string          `json:"argv"`
	Cwd        string            `json:"cwd"`
	Env        map[string]string `json:"env"`
	StdinPiped bool              `json:"stdin_piped"`
}

// ReadLog parses a FAKE_CLAUDE_LOG file. A missing file yields no entries.
func ReadLog(t testing.TB, path string) []Invocation {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	defer f.Close()
	var out []Invocation
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var inv Invocation
		if err := json.Unmarshal(sc.Bytes(), &inv); err != nil {
			t.Fatalf("bad log line %q: %v", sc.Text(), err)
		}
		out = append(out, inv)
	}
	return out
}

// Environ returns the current environment with extra entries appended (later
// entries win), for exec.Cmd.Env.
func Environ(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

// restoreWrite gives the owner write permission on every directory and
// regular file below root. It does not follow links and ignores errors.
func restoreWrite(root string) {
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		switch {
		case d.IsDir():
			chmodNoFollow(p, true, 0o700)
		case d.Type().IsRegular():
			chmodNoFollow(p, false, 0o600)
		}
		return nil
	})
}

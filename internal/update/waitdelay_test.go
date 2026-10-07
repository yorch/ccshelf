package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// forkingScript writes an executable that prints out, then leaves a background
// "sleep" holding its stdout and stderr, and records that sleep's pid.
func forkingScript(t *testing.T, out string) (path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	path = filepath.Join(dir, "tool")
	script := "#!/bin/sh\nsleep 25 &\necho $! > " + pidFile + "\necho '" + out + "'\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // a test script
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
		}
	})
	return path
}

func shortWaitDelay(t *testing.T) {
	t.Helper()
	old := execWaitDelay
	execWaitDelay = 300 * time.Millisecond
	t.Cleanup(func() { execWaitDelay = old })
}

// A child that forks a grandchild holding the inherited pipes must not keep
// Wait blocked past the budget (with a plain exec.Cmd it blocks until the
// grandchild exits, here 25s).
func TestRunVersionIsBoundedByAForkedGrandchild(t *testing.T) {
	shortWaitDelay(t)
	tool := forkingScript(t, `{"version":1,"kind":"version","data":{"version":"0.2.0"}}`)
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := RunVersion(ctx, tool, os.Environ())
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("RunVersion took %s with a 1s context: Wait was not bounded", d)
	}
	if err == nil {
		t.Error("a check that left a process holding its pipes must fail closed")
	}
}

func TestVerifyCosignIsBoundedByAForkedGrandchild(t *testing.T) {
	shortWaitDelay(t)
	tool := forkingScript(t, "verified OK")
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := VerifyCosign(ctx, tool, os.Environ(), "b", "c", "id", "iss")
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("VerifyCosign took %s with a 1s context: Wait was not bounded", d)
	}
	if err == nil {
		t.Error("a verification that left a process holding its pipes must not count as verified")
	}
}

func TestExecWaitDelayDefault(t *testing.T) {
	if execWaitDelay != 2*time.Second {
		t.Errorf("execWaitDelay = %s, want 2s", execWaitDelay)
	}
	_ = exec.ErrWaitDelay
}

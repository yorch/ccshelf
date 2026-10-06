package claude

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// DefaultTimeout is applied to listing commands when the context has no
// deadline.
const DefaultTimeout = 30 * time.Second

func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// excerpt returns a short single-line prefix of s for error messages.
func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}

// Spawn runs bin with args and waits for it, on every OS and never through a
// shell. A nil env inherits the current environment. It returns the exit code
// (128+n when a signal killed the process on Unix); a non-zero exit is not an
// error. The error is non-nil when the process cannot start or ctx ended.
func Spawn(ctx context.Context, bin string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	return spawnDir(ctx, "", bin, args, env, stdin, stdout, stderr)
}

func spawnDir(ctx context.Context, dir, bin string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if cerr := ctx.Err(); cerr != nil {
		return -1, fmt.Errorf("run %s: %w", bin, cerr)
	}
	if err == nil {
		return 0, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return exitCode(ee.ProcessState), nil
	}
	return -1, fmt.Errorf("run %s: %w", bin, err)
}

// StartHook replaces [Start] entirely when it is non-nil. It exists only so tests
// can observe a launch without replacing the test process or running claude.
// Production code never sets it.
var StartHook func(bin string, args, env []string) (int, error)

// Start launches the interactive claude. On Unix it replaces the current
// process (syscall.Exec), so it returns only on error; on Windows it spawns
// the child with inherited stdio, ignores Ctrl+C in the launcher, forwards
// termination and returns the child's exit code. A nil env inherits.
func Start(bin string, args, env []string) (int, error) {
	if StartHook != nil {
		return StartHook(bin, args, env)
	}
	return start(bin, args, env)
}

// TermGrace is how long the foreground launcher waits, after delivering a
// termination request to the child, before killing it.
const TermGrace = 5 * time.Second

// spawnForeground runs bin with the process's own stdio, ignoring interrupts
// (the child shares the terminal and receives Ctrl+C itself) and, on a
// termination request, delivering the termination to the child, waiting up to
// [TermGrace] and only then killing it. It is the Windows start path, kept
// portable so tests can exercise it everywhere.
func spawnForeground(bin string, args, env []string) (int, error) {
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	return runForeground(bin, args, env, sigs, TermGrace)
}

// runForeground is spawnForeground with the signal source and the grace
// period injected.
func runForeground(bin string, args, env []string, sigs <-chan os.Signal, grace time.Duration) (int, error) {
	cmd := exec.CommandContext(context.Background(), bin, args...) //nolint:gosec // running the located claude binary is the purpose of this package
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("start %s: %w", bin, err)
	}
	release := adoptChild(cmd.Process)
	defer release()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var killTimer <-chan time.Time
	for {
		select {
		case s := <-sigs:
			if s == syscall.SIGTERM && killTimer == nil {
				// Ask first; a child that ignores the request is killed
				// after the grace period.
				_ = terminateChild(cmd.Process)
				killTimer = time.After(grace)
			}
		case <-killTimer:
			_ = cmd.Process.Kill()
			killTimer = nil
		case err := <-done:
			if err == nil {
				return 0, nil
			}
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return exitCode(ee.ProcessState), nil
			}
			return -1, fmt.Errorf("wait for %s: %w", bin, err)
		}
	}
}

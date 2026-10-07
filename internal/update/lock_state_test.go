package update

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquireLock(t *testing.T) {
	dir := t.TempDir()
	rel, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire = %v, want ErrLocked", err)
	}
	path := filepath.Join(dir, LockName)
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(path)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("lock mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	if b, _ := os.ReadFile(path); !strings.HasPrefix(string(b), strconv.Itoa(os.Getpid())) {
		t.Errorf("lock file = %q, want the holder's pid", b)
	}
	rel()
	rel2, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	rel2()
}

// A release that runs twice (or late) must never free a lock that someone
// else holds by then.
func TestReleaseNeverFreesANewerHoldersLock(t *testing.T) {
	dir := t.TempDir()
	relA, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	relA()
	relB, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer relB()
	relA() // a second, late release by the previous holder
	if _, err := AcquireLock(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("after A's late release the lock is B's: acquire = %v, want ErrLocked", err)
	}
}

// A lock file left behind by a crashed process (any age, with a pid that
// means nothing) is not a lock: only a live holder is.
func TestLeftoverLockFileIsNotHeld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LockName)
	if err := os.WriteFile(path, []byte("123456\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, age := range []time.Duration{time.Second, time.Hour, 30 * 24 * time.Hour} {
		old := time.Now().Add(-age)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		rel, err := AcquireLock(dir)
		if err != nil {
			t.Fatalf("a leftover lock file %s old blocked the update: %v", age, err)
		}
		rel()
	}
}

func TestAcquireLockRefusesALink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating links needs privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, LockName)); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(dir); err == nil || errors.Is(err, ErrLocked) {
		t.Fatalf("a symlinked lock must be refused, got %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Errorf("the link target was written: %q", b)
	}
}

// lockHelper is the body of a child process of TestAcquireLockAcrossProcesses:
// it tries the lock, reports "won" or "locked" on stdout, and a winner holds
// the lock until its stdin is closed.
func lockHelper(dir string) int {
	rel, err := AcquireLock(dir)
	switch {
	case errors.Is(err, ErrLocked):
		fmt.Println("locked")
		return 0
	case err != nil:
		fmt.Println("error", err)
		return 1
	}
	fmt.Println("won")
	_, _ = io.Copy(io.Discard, os.Stdin)
	rel()
	return 0
}

type lockChild struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	line  string
}

func startLockChild(t *testing.T, dir string) *lockChild {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self) //nolint:gosec // the test binary re-executing itself as a helper
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+dir)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &lockChild{cmd: cmd, stdin: in}
	t.Cleanup(func() { _ = in.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("helper said nothing: %v", err)
	}
	c.line = strings.TrimSpace(line)
	return c
}

// Racing processes: exactly one wins, whatever the interleaving; when the
// winner is killed (a crash) the lock is free at once, with no waiting for a
// stale lock to expire, and exactly one of the next racers wins again.
func TestAcquireLockAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	const racers = 6
	children := make([]*lockChild, racers)
	var wg sync.WaitGroup
	for i := range children {
		wg.Add(1)
		go func() {
			defer wg.Done()
			children[i] = startLockChild(t, dir)
		}()
	}
	wg.Wait()
	var winner *lockChild
	wins := 0
	for _, c := range children {
		switch c.line {
		case "won":
			wins++
			winner = c
		case "locked":
		default:
			t.Fatalf("helper said %q", c.line)
		}
	}
	if wins != 1 {
		t.Fatalf("%d of %d processes got the lock, want exactly 1", wins, racers)
	}
	// The holder crashes.
	_ = winner.cmd.Process.Kill()
	_ = winner.cmd.Wait()
	next := startLockChild(t, dir)
	if next.line != "won" {
		t.Fatalf("after the holder was killed a new process got %q, want won", next.line)
	}
	// And the parent sees it held.
	if _, err := AcquireLock(dir); !errors.Is(err, ErrLocked) {
		t.Errorf("parent acquire = %v, want ErrLocked", err)
	}
	_ = next.stdin.Close()
	_ = next.cmd.Wait()
	rel, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("after a clean release: %v", err)
	}
	rel()
}

func TestAcquireLockOnlyOneWinner(t *testing.T) {
	dir := t.TempDir()
	var wins, locked int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	hold := make(chan struct{})
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rel, err := AcquireLock(dir)
			switch {
			case err == nil:
				atomic.AddInt32(&wins, 1)
				<-hold
				rel()
			case errors.Is(err, ErrLocked):
				atomic.AddInt32(&locked, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	time.Sleep(200 * time.Millisecond)
	close(hold)
	wg.Wait()
	if wins != 1 || locked != 15 {
		t.Errorf("wins=%d locked=%d, want 1 and 15", wins, locked)
	}
}

func TestAcquireLockNoDirectory(t *testing.T) {
	if _, err := AcquireLock(filepath.Join(t.TempDir(), "missing")); err == nil || errors.Is(err, ErrLocked) {
		t.Errorf("a missing directory must be an ordinary error, got %v", err)
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	if got := LoadState(dir, now); got != (State{}) {
		t.Errorf("missing state = %+v", got)
	}
	in := State{LastCheck: now.Add(-time.Hour), Latest: "0.2.0", NotifiedAt: now.Add(-2 * time.Hour)}
	if err := SaveState(dir, in); err != nil {
		t.Fatal(err)
	}
	got := LoadState(dir, now)
	if !got.LastCheck.Equal(in.LastCheck) || got.Latest != in.Latest || !got.NotifiedAt.Equal(in.NotifiedAt) {
		t.Errorf("round trip = %+v, want %+v", got, in)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, StateName))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("state file mode = %v %v, want 0600", fi.Mode().Perm(), err)
		}
	}
	// What is stored is times and a version, nothing else.
	raw := readFile(t, filepath.Join(dir, StateName))
	for _, k := range []string{"last_check", "latest", "notified_at"} {
		if !strings.Contains(raw, `"`+k+`"`) {
			t.Errorf("state file lacks %s: %s", k, raw)
		}
	}
	if err := SaveState(dir, State{}); err != nil {
		t.Fatal(err)
	}
	if raw := readFile(t, filepath.Join(dir, StateName)); strings.Contains(raw, "last_check") {
		t.Errorf("a zero time must be omitted: %s", raw)
	}
}

func TestStateIgnoresBadFiles(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	for name, content := range map[string]string{
		"not json":      "{{{",
		"empty":         "",
		"unknown key":   `{"last_check":"2026-10-05T12:00:00Z","latest":"0.2.0","machine_id":"x"}`,
		"bad version":   `{"latest":"banana"}`,
		"bad time":      `{"last_check":"yesterday"}`,
		"future check":  `{"last_check":"2030-01-01T00:00:00Z","latest":"0.2.0"}`,
		"future notice": `{"notified_at":"2030-01-01T00:00:00Z","latest":"0.2.0"}`,
		"trailing":      `{"latest":"0.2.0"} {"latest":"0.3.0"}`,
		"wrong type":    `{"latest":2}`,
		"array":         `[1,2]`,
		"too big":       `{"latest":"0.2.0","x":"` + string(make([]byte, 5000)) + `"}`,
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, StateName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := LoadState(dir, now); got != (State{}) {
			t.Errorf("%s: state = %+v, want zero", name, got)
		}
	}
	good := t.TempDir()
	if err := os.WriteFile(filepath.Join(good, StateName), []byte(`{"last_check":"2026-10-05T12:00:00Z","latest":"v0.2.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LoadState(good, now); got.Latest != "v0.2.0" || got.LastCheck.IsZero() {
		t.Errorf("a good file was ignored: %+v", got)
	}
}

func TestStateIgnoresSymlinkAndDirectory(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, StateName), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := LoadState(dir, now); got != (State{}) {
		t.Errorf("directory: %+v", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	dir2 := t.TempDir()
	target := filepath.Join(dir2, "real.json")
	if err := os.WriteFile(target, []byte(`{"latest":"9.9.9"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir2, StateName)); err != nil {
		t.Fatal(err)
	}
	if got := LoadState(dir2, now); got != (State{}) {
		t.Errorf("a symlinked state file must not be followed: %+v", got)
	}
}

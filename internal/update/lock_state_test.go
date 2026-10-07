package update

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAcquireLock(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	clock := func() time.Time { return now }
	rel, err := AcquireLock(dir, clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(dir, clock); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire = %v, want ErrLocked", err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dir, LockName))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("lock mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	rel()
	if _, err := os.Lstat(filepath.Join(dir, LockName)); !os.IsNotExist(err) {
		t.Error("release must remove the lock")
	}
	rel2, err := AcquireLock(dir, clock)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	rel2()
}

func TestAcquireLockStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, LockName)
	if err := os.WriteFile(path, []byte("123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, tc := range []struct {
		age  time.Duration
		want error
	}{
		{StaleLockAge - time.Minute, ErrLocked},
		{StaleLockAge + time.Minute, nil},
	} {
		old := now.Add(-tc.age)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		rel, err := AcquireLock(dir, func() time.Time { return now })
		if !errors.Is(err, tc.want) && !(tc.want == nil && err == nil) {
			t.Errorf("age %s: err = %v, want %v", tc.age, err, tc.want)
		}
		if err == nil {
			rel()
			if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
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
			rel, err := AcquireLock(dir, time.Now)
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
	if _, err := AcquireLock(filepath.Join(t.TempDir(), "missing"), time.Now); err == nil || errors.Is(err, ErrLocked) {
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

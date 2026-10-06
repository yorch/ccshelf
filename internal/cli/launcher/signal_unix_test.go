//go:build !windows

package launcher

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestIgnoreInterruptSurvivesSIGINT sends the test process a SIGINT while the
// interrupt is being swallowed; without the handler the default action would
// end the test binary.
func TestIgnoreInterruptSurvivesSIGINT(t *testing.T) {
	release := ignoreInterrupt()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	release()
}

// TestReleaseSignalsRestoresInheritedIgnore checks L2 end to end: a process
// started with SIGINT ignored (a background job) that installs a handler like
// signal.NotifyContext does and then calls releaseSignals must hand the
// ignored disposition on to the program it executes.
func TestReleaseSignalsRestoresInheritedIgnore(t *testing.T) {
	survives := func(mode string) bool {
		cmd := exec.Command("/bin/sh", "-c", `trap "" INT; exec "$0" -test.run='^TestReleaseSignalsHelper$'`, os.Args[0])
		cmd.Env = append(os.Environ(), "CCSHELF_TEST_RELEASE="+mode)
		out, _ := cmd.Output()
		return strings.Contains(string(out), "survived")
	}
	if survives("control") {
		t.Skip("cannot observe signal dispositions through exec on this system")
	}
	if !survives("release") {
		t.Error("claude would start with SIGINT at its default and die on Ctrl+C in a background job")
	}
}

// TestReleaseSignalsHelper is the child of the test above, not a test.
func TestReleaseSignalsHelper(t *testing.T) {
	mode := os.Getenv("CCSHELF_TEST_RELEASE")
	if mode == "" {
		t.Skip("helper process")
	}
	notifyAsMain()
	if mode == "release" {
		releaseSignals()
	}
	_ = syscall.Exec("/bin/sh", []string{"sh", "-c", "kill -INT $$; echo survived"}, os.Environ())
	os.Exit(3)
}

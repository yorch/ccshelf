//go:build !windows

package launcher

import (
	"os"
	"os/signal"
	"syscall"
)

// releaseSignals undoes the signal handling of the main program just before
// the process is replaced by claude. signal.NotifyContext replaced an
// inherited "ignore" disposition of SIGINT (a job started with & in a
// non-interactive shell) by a handler, and exec resets handlers to the
// default, so without this a backgrounded "ccshelf run x &" would die on
// Ctrl+C where plain claude would not. signal.Reset puts the original
// disposition back for SIGINT.
func releaseSignals() { signal.Reset(os.Interrupt, syscall.SIGTERM) }

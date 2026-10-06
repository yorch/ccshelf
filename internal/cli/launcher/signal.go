package launcher

import (
	"os"
	"os/signal"
)

// ignoreInterrupt swallows SIGINT (Ctrl+C) until the returned function is
// called. A foreground child that shares the terminal (an editor) receives the
// interrupt itself; the launcher must neither die nor cancel the child. It uses
// a registered, drained channel rather than signal.Ignore so that it composes
// with the signal.NotifyContext of the main program.
func ignoreInterrupt() (release func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	return func() { signal.Stop(ch) }
}

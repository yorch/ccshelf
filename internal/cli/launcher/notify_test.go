package launcher

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// notifyAsMain registers the handlers cmd/ccshelf's main registers.
func notifyAsMain() {
	_, _ = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

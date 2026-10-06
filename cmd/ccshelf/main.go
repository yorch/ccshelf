// Command ccshelf runs Claude Code with a named profile and lints and
// publishes an organization's plugin catalog.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/yorch/ccshelf/internal/cli"
	"github.com/yorch/ccshelf/internal/cli/clicore"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Execute(ctx, clicore.NewEnv(), os.Args[1:])
	stop()
	os.Exit(code)
}

//go:build !windows

package claude

import (
	"fmt"
	"os"
	"syscall"
)

func start(bin string, args, env []string) (int, error) {
	if env == nil {
		env = os.Environ()
	}
	argv := append([]string{bin}, args...)
	err := syscall.Exec(bin, argv, env) //nolint:gosec // exec of the located claude binary is the purpose of this package
	return -1, fmt.Errorf("exec %s: %w", bin, err)
}

// exitCode maps a finished process to a shell-style exit code.
func exitCode(ps *os.ProcessState) int {
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}

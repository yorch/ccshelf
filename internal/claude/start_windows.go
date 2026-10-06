//go:build windows

package claude

import "os"

func start(bin string, args, env []string) (int, error) {
	return spawnForeground(bin, args, env)
}

// exitCode returns the child's exit code (Windows has no signal deaths).
func exitCode(ps *os.ProcessState) int { return ps.ExitCode() }

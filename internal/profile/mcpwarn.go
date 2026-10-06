package profile

import (
	"fmt"
	"path"
	"strings"
)

// shellCommands are command names that run whatever string they are given.
var shellCommands = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true, "ksh": true, "csh": true, "tcsh": true,
	"cmd": true, "powershell": true, "pwsh": true, "env": true,
}

// interpreterCommands run code from their arguments when given an inline flag.
var interpreterCommands = []string{"python", "python3", "node", "deno", "bun", "perl", "ruby", "php"}

// inlineFlags make a command run an argument as code or as a shell command.
var inlineFlags = map[string]bool{
	"-c": true, "/c": true, "/k": true, "-e": true, "--eval": true, "-command": true, "-encodedcommand": true,
}

// commandBase returns the lowercase executable name of command without its
// directory (either separator) or a trailing ".exe".
func commandBase(command string) string {
	c := strings.ReplaceAll(command, `\`, "/")
	b := strings.ToLower(path.Base(c))
	return strings.TrimSuffix(b, ".exe")
}

func isInterpreter(base string) bool {
	for _, p := range interpreterCommands {
		if base == p || strings.HasPrefix(base, p) && isVersionSuffix(base[len(p):]) {
			return true
		}
	}
	return false
}

func isVersionSuffix(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}

// commandWarning describes why a stdio command deserves attention, or "".
func commandWarning(command string, args []string) string {
	base := commandBase(command)
	if shellCommands[base] {
		return fmt.Sprintf("runs %q, a shell or command launcher", command)
	}
	if base == "docker" || base == "podman" {
		return ""
	}
	for _, a := range args {
		if inlineFlags[strings.ToLower(a)] {
			if isInterpreter(base) {
				return fmt.Sprintf("runs inline code through %q %s", command, a)
			}
			return fmt.Sprintf("passes the %s flag to %q, which usually runs an inline command", a, command)
		}
	}
	return ""
}

// MCPWarnings returns warnings about a stdio server whose command (or any
// per-OS override) is a shell interpreter or takes -c, /c or -e style flags.
// The registry is treated as fully trusted code (see the package comment):
// these warnings only make the unusual case visible in `show` and in the trust
// prompt. Servers of other types have none.
func MCPWarnings(s MCPServer) []string {
	if s.Type != MCPStdio {
		return nil
	}
	var out []string
	add := func(where, command string, args []string) {
		if w := commandWarning(command, args); w != "" {
			out = append(out, fmt.Sprintf("MCP server %q %s%s", s.Name, w, where))
		}
	}
	add("", s.Command, s.Args)
	for _, o := range []struct {
		os string
		o  *MCPOverride
	}{{"windows", s.Windows}, {"macos", s.MacOS}, {"linux", s.Linux}} {
		if o.o != nil {
			add(" on "+o.os, o.o.Command, o.o.Args)
		}
	}
	return out
}

package profile

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
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

// packageRunners are the launchers a Windows `cmd /c` wrapper may start: the
// documented form is `cmd /c npx -y pkg@1.2.3`, because npx and its relatives
// are .cmd scripts that Windows cannot run without a shell.
var packageRunners = map[string]bool{"npx": true, "node": true, "uvx": true, "bunx": true}

// pinnedPackageRe is a package argument pinned to a version: name@1.2.3, with an
// optional @scope/ and an optional leading v. A tag such as @latest is not a pin.
var pinnedPackageRe = regexp.MustCompile(`^(@[A-Za-z0-9._~-]+/)?[A-Za-z0-9._~-]+@v?[0-9][0-9A-Za-z.+_-]*$`)

// cmdMetaChars would let an argument of `cmd /c` run a second command.
const cmdMetaChars = "&|<>^%\"\r\n\x00"

// isDocumentedLauncher reports whether command and args are the documented
// Windows launcher pattern: cmd /c, then npx, node, uvx or bunx, with a pinned
// name@version package among the remaining arguments and nothing in them that
// cmd would read as another command.
func isDocumentedLauncher(command string, args []string) bool {
	if commandBase(command) != "cmd" || len(args) < 3 || !strings.EqualFold(args[0], "/c") || !packageRunners[strings.TrimSuffix(commandBase(args[1]), ".cmd")] {
		return false
	}
	pinned := false
	for _, a := range args[1:] {
		if strings.ContainsAny(a, cmdMetaChars) {
			return false
		}
		pinned = pinned || pinnedPackageRe.MatchString(a)
	}
	return pinned
}

// commandWarning describes why a stdio command deserves attention, or "".
func commandWarning(command string, args []string) string {
	if isDocumentedLauncher(command, args) {
		return ""
	}
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

// MCPNotes returns informational notes about a stdio server: a command (or
// per-OS override) that is the documented Windows launcher pattern, cmd /c
// followed by npx, node, uvx or bunx and a pinned name@version package. It is
// the one case MCPWarnings leaves out although the command is "cmd", so that
// lint can still show it as info. Other servers, and servers of other types,
// have none.
func MCPNotes(s MCPServer) []string {
	if s.Type != MCPStdio {
		return nil
	}
	var out []string
	add := func(where, command string, args []string) {
		if isDocumentedLauncher(command, args) {
			line := strings.Join(args[1:], " ")
			if strings.IndexFunc(line, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
				line = strconv.Quote(line)
			}
			out = append(out, fmt.Sprintf("MCP server %q uses the documented Windows launcher pattern (cmd /c %s)%s", s.Name, line, where))
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

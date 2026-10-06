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
// are .cmd scripts that Windows cannot run without a shell. They must be given
// as a bare name (optionally with ".cmd"), never as a path, so cmd resolves
// them from PATH and not from a directory the registry chose. node is not a
// package runner and is not accepted.
var packageRunners = map[string]bool{"npx": true, "uvx": true, "bunx": true}

// pinnedPackageRe is exactly one package pinned to an exact version:
// name@1.2.3 or name@1.2.3-rc.1, with an optional @scope/ and an optional
// leading v. A tag such as @latest, a range or a partial version is not a pin.
var pinnedPackageRe = regexp.MustCompile(`^(@[A-Za-z0-9._~-]+/)?[A-Za-z0-9._~-]+@v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// cmdMetaChars would let an argument of `cmd /c` run a second command, expand
// a variable or quote its way out of the line.
const cmdMetaChars = "&|<>^%()!\"'"

// codeFlags make npx, uvx or bunx run an argument as code or add a second
// package, which would defeat the pin.
var codeFlags = []string{"-c", "-e", "-p", "--call", "--eval", "--package"}

func isCodeFlag(a string) bool {
	l := strings.ToLower(a)
	for _, f := range codeFlags {
		if l == f || strings.HasPrefix(l, f+"=") {
			return true
		}
	}
	return false
}

// isDocumentedLauncher reports whether command and args are exactly the
// documented Windows launcher shape, and nothing looser:
//
//	command = "cmd"
//	args    = ["/c", <runner>, "-y"|"--yes", "<name>@<x.y.z[-pre]>", rest...]   (npx)
//	args    = ["/c", <runner>, "<name>@<x.y.z[-pre]>", rest...]                 (uvx, bunx)
//
// npx takes the -y flag before the package; uvx and bunx have no such flag, so
// the pinned package follows the runner directly. The runner is the bare name
// npx, uvx or bunx (optionally .cmd): no directory, drive or UNC part. No
// argument may contain a cmd metacharacter or a control character, and none of
// the rest may be a code or extra-package flag (-c, -e, -p, --call, --eval,
// --package). Anything else is not documented and keeps its warning.
func isDocumentedLauncher(command string, args []string) bool {
	if command != "cmd" || len(args) < 3 || !strings.EqualFold(args[0], "/c") {
		return false
	}
	runner := strings.ToLower(args[1])
	runner = strings.TrimSuffix(runner, ".cmd")
	if !packageRunners[runner] {
		return false
	}
	pkgAt := 2
	if runner == "npx" {
		if len(args) < 4 || (args[2] != "-y" && args[2] != "--yes") {
			return false
		}
		pkgAt = 3
	}
	if !pinnedPackageRe.MatchString(args[pkgAt]) {
		return false
	}
	for i, a := range args[1:] {
		if strings.ContainsAny(a, cmdMetaChars) || strings.IndexFunc(a, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
			return false
		}
		if i+1 > pkgAt && isCodeFlag(a) {
			return false
		}
	}
	return true
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
// followed by npx, uvx or bunx and a pinned name@version package. It is
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

package profile

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors returned (wrapped) by Resolve. Use errors.Is.
var (
	// ErrCollision means a profile name exists in more than one source and
	// no shadowing rule allows it.
	ErrCollision = errors.New("profile name exists in more than one source")
	// ErrCycle means extends forms a cycle.
	ErrCycle = errors.New("extends cycle")
	// ErrDepth means extends is nested deeper than MaxExtendsDepth.
	ErrDepth = errors.New("extends chain too deep")
	// ErrNotFound means no source has the profile.
	ErrNotFound = errors.New("profile not found")
	// ErrProjectNotTrusted means the profile only exists in a project source
	// and project profiles have not been trusted for this run.
	ErrProjectNotTrusted = errors.New("project profile is not trusted")
	// ErrProjectForbidden means a project profile sets something project
	// profiles may never set (SR2).
	ErrProjectForbidden = errors.New("not allowed in a project profile")
	// ErrSharedDropsUserLayer means a shared profile sets
	// inherit_user_settings = false (SR3).
	ErrSharedDropsUserLayer = errors.New("a shared profile may not set inherit_user_settings = false (SR3)")
	// ErrUnknownMCPServer means a profile names a server missing from the registry.
	ErrUnknownMCPServer = errors.New("unknown MCP server")
	// ErrInvalidKind means a source reports a Kind that is not personal,
	// project or org (for example the zero value).
	ErrInvalidKind = errors.New("source has an invalid kind")
	// ErrPath means a path escapes its root or is not a regular file.
	ErrPath = errors.New("unsafe path")
)

// Problem is one field-level validation problem.
type Problem struct {
	// Field is the dotted key, for example "plugins.include[1]".
	Field string
	// Line is the 1-based line in the file, or 0 when unknown.
	Line int
	// Message says what is wrong.
	Message string
}

// ValidationError lists every problem found in one file.
type ValidationError struct {
	// File is the file name or path the problems refer to (may be empty).
	File     string
	Problems []Problem
}

// Error formats one problem per line.
func (e *ValidationError) Error() string {
	var b strings.Builder
	file := e.File
	if file == "" {
		file = "profile"
	}
	if len(e.Problems) == 1 {
		return "invalid " + file + ": " + e.Problems[0].String()
	}
	fmt.Fprintf(&b, "invalid %s: %d problems", file, len(e.Problems))
	for _, p := range e.Problems {
		b.WriteString("\n  ")
		b.WriteString(p.String())
	}
	return b.String()
}

// String formats the problem as "line N: field: message".
func (p Problem) String() string {
	s := p.Message
	if p.Field != "" {
		s = p.Field + ": " + s
	}
	if p.Line > 0 {
		s = fmt.Sprintf("line %d: %s", p.Line, s)
	}
	return s
}

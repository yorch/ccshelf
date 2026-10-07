package cli

import (
	"errors"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/yorch/ccshelf/internal/ui"
)

// configureHelp uses Cobra's own groups, so help and completion still share
// the real command tree rather than a separately maintained command list.
func configureHelp(root *cobra.Command) {
	root.AddGroup(
		&cobra.Group{ID: "profiles", Title: "Profiles:"},
		&cobra.Group{ID: "discovery", Title: "Discovery and diagnostics:"},
		&cobra.Group{ID: "org", Title: "Org data repo maintenance:"},
		&cobra.Group{ID: "setup", Title: "Setup and utilities:"},
	)
	groups := map[string]string{
		"run": "profiles", "ls": "profiles", "show": "profiles",
		"diff": "profiles", "dry-run": "profiles", "new": "profiles",
		"edit": "profiles", "trust": "profiles",
		"search": "discovery", "recommend": "discovery", "doctor": "discovery",
		"catalog": "org", "compile": "org", "lint": "org",
	}
	for _, cmd := range root.Commands() {
		cmd.GroupID = groups[cmd.Name()]
		if cmd.GroupID == "" {
			cmd.GroupID = "setup"
		}
	}
	root.SetHelpCommandGroupID("setup")
}

type usageHint struct {
	error
	hint string
}

// Unwrap preserves the original error and its exit-code classification.
func (e usageHint) Unwrap() error { return e.error }

// Hint returns the command-specific recovery instruction.
func (e usageHint) Hint() string { return e.hint }

// Preserve more specific recovery instructions and missing-value hints.
// A generic usage error instead points to the command that failed, not a
// wall of global help. CommandPath contains definitions, never user arguments.
func withUsageHint(err error, cmd, root *cobra.Command) error {
	var h interface{ Hint() string }
	var missing *ui.MissingFlagError
	if (errors.As(err, &h) && h.Hint() != "") || errors.As(err, &missing) {
		return err
	}
	if cmd == nil {
		cmd = root
	}
	return usageHint{error: err, hint: "run " + cmd.CommandPath() + " --help for usage and examples"}
}

// requestsJSON is only a presentation fallback for errors before Cobra parsed
// flags. Skip values of known flags and everything after --: a filename or
// child argument spelled "--json" is not a request for JSON diagnostics.
func requestsJSON(args []string, cmd, root *cobra.Command) bool {
	requested := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		name, value, equals := strings.Cut(arg, "=")
		if name == "--json" {
			if !equals {
				requested = true
			} else if b, err := strconv.ParseBool(value); err == nil {
				requested = b
			}
			continue
		}
		if equals || !strings.HasPrefix(name, "-") {
			continue
		}
		lookup := func(flags *pflag.FlagSet) *pflag.Flag {
			if strings.HasPrefix(name, "--") {
				return flags.Lookup(strings.TrimPrefix(name, "--"))
			}
			if len(name) == 2 {
				return flags.ShorthandLookup(name[1:])
			}
			return nil
		}
		flag := lookup(root.PersistentFlags())
		if flag == nil && cmd != nil {
			flag = lookup(cmd.Flags())
		}
		if flag != nil && flag.NoOptDefVal == "" {
			i++
		}
	}
	return requested
}

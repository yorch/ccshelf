package config

import (
	"fmt"
	"os"
)

// EnvConfigDir is the variable Claude Code reads for its configuration directory.
const EnvConfigDir = "CLAUDE_CONFIG_DIR"

// AccountChoice is the outcome of ResolveAccount.
type AccountChoice struct {
	// Name is the account name; empty when none or when FromEnv.
	Name string
	// Account is the configured account (zero when Name is empty).
	Account Account
	// Source is "flag", "env", "profile", "default" or "none".
	Source string
	// FromEnv is true when CLAUDE_CONFIG_DIR was already set and is honored.
	FromEnv bool
	// SetEnv is true when the launcher must set CLAUDE_CONFIG_DIR to
	// Account.ConfigDir for the child. It is never true with FromEnv.
	SetEnv bool
	// OverridesEnv is true when an explicit --account replaces an existing
	// CLAUDE_CONFIG_DIR; callers should warn.
	OverridesEnv bool
	// Notes lists implicit choices that were ignored because of the environment.
	Notes []string
}

// ResolveAccount picks the account for a run. See the package documentation
// for the precedence. env may be nil (os.Getenv). An unknown explicit name is
// an error that lists the known account names.
func ResolveAccount(flag, profileAccount string, cfg *Config, env func(string) string) (AccountChoice, error) {
	if env == nil {
		env = os.Getenv
	}
	if cfg == nil {
		cfg = Default()
	}
	envDir := env(EnvConfigDir)
	lookup := func(name, from string) (AccountChoice, error) {
		a, ok := cfg.Accounts[name]
		if !ok {
			return AccountChoice{}, fmt.Errorf("unknown account %q from %s (known: %s)", name, from, knownList(cfg.Accounts))
		}
		return AccountChoice{Name: name, Account: a, Source: from, SetEnv: true}, nil
	}

	if flag != "" {
		c, err := lookup(flag, "flag")
		if err != nil {
			return c, err
		}
		c.OverridesEnv = envDir != ""
		return c, nil
	}
	implicit, from := profileAccount, "profile"
	if implicit == "" {
		implicit, from = cfg.DefaultAccount, "default"
	}
	if envDir != "" {
		c := AccountChoice{Source: "env", FromEnv: true}
		if implicit != "" {
			c.Notes = append(c.Notes, fmt.Sprintf("%s is set; ignoring account %q from %s", EnvConfigDir, implicit, from))
		}
		return c, nil
	}
	if implicit == "" {
		return AccountChoice{Source: "none"}, nil
	}
	return lookup(implicit, from)
}

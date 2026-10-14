package clicore

import (
	"fmt"
	"strings"

	"github.com/yorch/ccshelf/internal/account"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
)

// ConfigPath returns the user configuration file: --config, or the default
// location.
func (c *Context) ConfigPath() (string, error) {
	if c.G.ConfigPath != "" {
		return c.G.ConfigPath, nil
	}
	p, err := config.Path()
	if err != nil {
		return "", fmt.Errorf("locating the configuration file: %w", err)
	}
	return p, nil
}

// LoadConfig reads the user configuration file. A missing file gives the
// defaults.
func (c *Context) LoadConfig() (*config.Config, string, error) {
	p, err := c.ConfigPath()
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(p)
	if err != nil {
		return nil, "", fmt.Errorf("configuration %s: %w", p, err)
	}
	return cfg, p, nil
}

// AccountEnv resolves the account for a command that runs claude, with
// config.ResolveAccount: --account, then CLAUDE_CONFIG_DIR, then the profile
// account, then default_account. It returns the choice and the environment
// additions for the child process. Pass an empty profileAccount when no
// profile applies.
func (c *Context) AccountEnv(cfg *config.Config, profileAccount string) (config.AccountChoice, map[string]string, error) {
	choice, err := config.ResolveAccount(c.G.Account, profileAccount, cfg, c.Getenv)
	if err != nil {
		return choice, nil, ui.Usage(fmt.Errorf("account: %w", err))
	}
	extra := map[string]string{}
	if choice.SetEnv {
		list, err := account.Env(cfg, choice.Name)
		if err != nil {
			return choice, nil, fmt.Errorf("account %s: %w", choice.Name, err)
		}
		for _, e := range list {
			if k, v, ok := strings.Cut(e, "="); ok {
				extra[k] = v
			}
		}
	}
	return choice, extra, nil
}

// PrintAccountNotes prints, as warnings on the error stream, what an account
// choice ignored or overrode, so the choice is never silent.
func (c *Context) PrintAccountNotes(choice config.AccountChoice) {
	for _, n := range choice.Notes {
		fmt.Fprintln(c.Streams.Err, ui.Status(c.Mode, ui.LevelWarn, ui.Sanitize(n)))
	}
}

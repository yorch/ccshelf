// Package clicore holds the small, dependency-light pieces every ccshelf
// command shares: the global flags, the environment a command may touch, and
// the per-invocation Context that carries the terminal mode and the prompter.
//
// Command packages (launcher commands, org and catalog commands) import this
// package, never each other. internal/cli connects them. Each package exposes
// a constructor of the form
//
//	func Commands(get clicore.Provider) []*cobra.Command
//
// and every command's RunE calls get() once, after cobra has parsed the flags,
// to obtain its Context. Tests build an Env with scripted streams, a fake
// environment, a fixed clock and a scripted prompter and call the root command
// directly. Nothing in a command may read os.Stdin, os.Stdout, os.Getenv or
// time.Now except through Env (rule: hermetic commands).
package clicore

import (
	"context"
	"errors"
	"os"
	"runtime"
	"time"

	"github.com/yorch/ccshelf/internal/catalog"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
)

// Globals are the persistent flags shared by every command. The root command
// binds them. Commands only read them through a Context.
type Globals struct {
	// ConfigPath overrides the user config file location (--config).
	ConfigPath string
	// ClaudePath overrides the claude binary (--claude).
	ClaudePath string
	// Account selects an account for this invocation (--account).
	Account string
	// Root is the org data repo root used by catalog and org commands
	// (--root). An empty value means the current directory.
	Root string
	// NoInteractive disables prompts (--no-interactive).
	NoInteractive bool
	// NoColor disables color (--no-color).
	NoColor bool
	// Plain selects line-oriented output and prompts (--plain).
	Plain bool
	// JSON selects machine-readable output on commands that support it (--json).
	JSON bool
}

// Env is everything a command touches outside its flags. Production code uses
// NewEnv. Tests replace any field.
type Env struct {
	Streams ui.Streams
	// In and Out are the files used only for terminal detection. A nil value
	// means "not a terminal" (what tests want).
	In, Out *os.File
	Getenv  func(string) string
	Environ func() []string
	Getwd   func() (string, error)
	Now     func() time.Time
	// GOOS is the operating system the command behaves as (tests set it to
	// check Windows behavior on any machine).
	GOOS string
	// Prompter, when non-nil, replaces the prompter that the mode selects
	// (tests pass ui.NewScripted).
	Prompter ui.Prompter
}

// NewEnv returns the production environment.
func NewEnv() *Env {
	return &Env{
		Streams: ui.StdStreams(),
		In:      os.Stdin,
		Out:     os.Stdout,
		Getenv:  os.Getenv,
		Environ: os.Environ,
		Getwd:   os.Getwd,
		Now:     time.Now,
		GOOS:    runtime.GOOS,
	}
}

// Context is what a command receives once its flags are parsed.
type Context struct {
	*Env
	G *Globals
	// Mode is the detected terminal mode (rule 2).
	Mode ui.Mode
	// Prompt asks for missing values. It is ui.NonInteractive when the mode
	// is not interactive, so a script gets exit code 2 and the name of the flag.
	Prompt ui.Prompter
	// Config holds the user's ui preferences read from config.toml, filled
	// by the root command.
	ConfigColor, ConfigInteractive string
}

// UIPrefs reads the [ui] section of the configuration file (--config or the
// default location) for the color and interactive preferences. It is read-only
// and tolerant:
//   - A missing file gives the defaults.
//   - An unreadable or invalid file gives empty values. The command that needs
//     the configuration reports the problem itself.
//
// Thus presentation never makes a command fail.
func UIPrefs(g *Globals) (color, interactive string) {
	path := g.ConfigPath
	if path == "" {
		p, err := config.Path()
		if err != nil {
			return "", ""
		}
		path = p
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", ""
	}
	return cfg.UI.Color, cfg.UI.Interactive
}

// Provider returns the Context for the current invocation. RunE calls it after
// flag parsing.
type Provider func() (*Context, error)

// Context builds the Context for the given globals and config preferences.
func (e *Env) Context(g *Globals, configColor, configInteractive string) *Context {
	mode := ui.DetectMode(e.Getenv, e.In, e.Out, ui.ModeFlags{
		NoInteractive:     g.NoInteractive,
		NoColor:           g.NoColor,
		Plain:             g.Plain,
		JSON:              g.JSON,
		ConfigColor:       configColor,
		ConfigInteractive: configInteractive,
	})
	var p ui.Prompter = ui.NonInteractive{}
	switch {
	case e.Prompter != nil:
		p = e.Prompter
	case mode.Interactive:
		p = ui.NewTTY(e.Streams, mode)
	}
	return &Context{Env: e, G: g, Mode: mode, Prompt: p, ConfigColor: configColor, ConfigInteractive: configInteractive}
}

// CatalogData is catalog data of an org data repo that is not the working
// directory: either a local source root or a published catalog.json.
type CatalogData struct {
	// Root is the directory that holds ccshelf.toml, the marketplace files and
	// catalog/plugins/ of the org data repo.
	Root string
	// Catalog is set when the provider retrieved a published catalog.json.
	Catalog *catalog.Catalog
	// Source describes where it came from, without machine paths for git
	// sources, for example "git https://example.com/acme/data.git at 1a2b3c4d5e6f".
	Source string
}

// ErrNoCatalog is the error that a CatalogProvider returns when none of the
// configured sources has usable catalog data.
var ErrNoCatalog = errors.New("no catalog data is available from the configured sources")

// CatalogProvider finds catalog data for commands that run outside an org
// data repo (search, recommend). It may retrieve an explicitly configured
// remote catalog or read an already available profile source. internal/cli
// wires the launcher's implementation into the org commands, which never
// import the launcher.
type CatalogProvider func(ctx context.Context, c *Context) (*CatalogData, error)

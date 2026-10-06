package launcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/claude"
	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/config"
	"github.com/ccshelf/ccshelf/internal/policy"
	"github.com/ccshelf/ccshelf/internal/settings"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// Options holds the seams tests replace so that nothing real is started.
// The zero value is production behavior.
type Options struct {
	// Start launches claude (default claude.Start). On Unix the production
	// Start replaces the process and returns only on error.
	Start func(bin string, args, env []string) (int, error)
	// Spawn runs a child to completion (default claude.Spawn); edit uses it
	// for $EDITOR.
	Spawn func(ctx context.Context, bin string, args, env []string, stdin io.Reader, stdout, stderr io.Writer) (int, error)
	// Executable returns the absolute path of the ccshelf binary (default
	// os.Executable); shell-init embeds it.
	Executable func() (string, error)
	// Policy configures managed-policy detection (default: the real,
	// documented locations of the running OS). Tests point ManagedDir at a
	// temporary directory.
	Policy policy.Options
	// NewGit builds a prepared-on-demand git source (default gitsource.New).
	NewGit GitFactory

	// Test seams (unexported: only tests in this package set them).
	// validateSettings replaces settings.Validate; afterSettingsWrite runs
	// between writing the settings file and reading it back.
	validateSettings   func([]byte) error
	afterSettingsWrite func(path string)
}

type launcher struct {
	get clicore.Provider
	opt Options
}

// Commands returns the launcher commands with production behavior.
func Commands(get clicore.Provider) []*cobra.Command {
	return CommandsWith(get, Options{})
}

// CommandsWith is Commands with explicit seams (used by tests).
func CommandsWith(get clicore.Provider, opt Options) []*cobra.Command {
	if opt.Start == nil {
		opt.Start = claude.Start
	}
	if opt.Spawn == nil {
		opt.Spawn = claude.Spawn
	}
	if opt.Executable == nil {
		opt.Executable = os.Executable
	}
	l := &launcher{get: get, opt: opt}
	return []*cobra.Command{
		l.runCmd(),
		l.dryRunCmd(),
		l.showCmd(),
		l.lsCmd(),
		l.diffCmd(),
		l.newCmd(),
		l.editCmd(),
		l.initCmd(),
		l.trustCmd(),
		l.accountCmd(),
		l.shellInitCmd(),
		l.versionCmd(),
		l.completionCmd(),
	}
}

// validateSettings checks generated settings against the closed schema; the
// run pipeline calls it before and after the file is written.
func (l *launcher) validateSettings(raw []byte) error {
	if l.opt.validateSettings != nil {
		return l.opt.validateSettings(raw)
	}
	return settings.Validate(raw)
}

// do wraps a command body: it obtains the Context once, after flag parsing.
func (l *launcher) do(f func(ctx context.Context, cc *clicore.Context, cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cc, err := l.get()
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		cmd.SilenceUsage = true
		return f(ctx, cc, cmd, args)
	}
}

// hintError is an error that tells the user how to fix it.
type hintError struct {
	err  error
	hint string
}

func (e *hintError) Error() string { return e.err.Error() }
func (e *hintError) Unwrap() error { return e.err }

// Hint returns the advice shown after the error.
func (e *hintError) Hint() string { return e.hint }

func withHint(err error, format string, a ...any) error {
	return &hintError{err: err, hint: fmt.Sprintf(format, a...)}
}

// canPrompt reports whether a flow may ask the user: the prompter is a real one
// (a terminal or a scripted test double), and neither --no-interactive nor
// --json was given.
func canPrompt(cc *clicore.Context) bool {
	if cc.G.NoInteractive || cc.Mode.JSON {
		return false
	}
	_, non := cc.Prompt.(ui.NonInteractive)
	return !non
}

func printStatus(cc *clicore.Context, level ui.Level, format string, a ...any) {
	fmt.Fprintln(cc.Streams.Err, ui.Status(cc.Mode, level, ui.Sanitize(fmt.Sprintf(format, a...))))
}

func warnf(cc *clicore.Context, format string, a ...any) { printStatus(cc, ui.LevelWarn, format, a...) }

func okf(cc *clicore.Context, format string, a ...any) { printStatus(cc, ui.LevelOK, format, a...) }

// printEquivalent prints the equivalent flag command (R6 rule 4). A failure to
// quote (control characters) is reported as a warning, never fatal.
func printEquivalent(cc *clicore.Context, rec *ui.Recorder) {
	if err := rec.Print(cc.Streams.Err, cc.GOOS); err != nil {
		warnf(cc, "cannot print the equivalent command: %v", err)
	}
}

// shell returns the quoting style for the Equivalent and dry-run lines.
func shell(cc *clicore.Context) string { return ui.ShellForGOOS(cc.GOOS) }

// configPath returns the config file location honoring --config.
func configPath(cc *clicore.Context) (string, error) {
	if cc.G.ConfigPath != "" {
		return cc.G.ConfigPath, nil
	}
	p, err := config.Path()
	if err != nil {
		return "", fmt.Errorf("locating the configuration file: %w", err)
	}
	return p, nil
}

// pickProfile returns the profile named in args, or asks for one. Without a
// terminal a missing name is a usage error that names the argument (exit 2).
func pickProfile(ctx context.Context, cc *clicore.Context, name, command string, choices func() ([]ui.Option, error)) (string, bool, error) {
	if name != "" {
		return name, false, nil
	}
	if !canPrompt(cc) {
		return "", false, ui.Usage(withHint(
			errors.New("missing argument <profile>"),
			"run: ccshelf %s <profile>  (ccshelf ls lists the profiles; in a terminal without --no-interactive you are asked)", command))
	}
	opts, err := choices()
	if err != nil {
		return "", false, err
	}
	if len(opts) == 0 {
		return "", false, ui.Failure(withHint(errors.New("no profiles found"), "create one with: ccshelf new <name>"))
	}
	i, err := cc.Prompt.Select(ctx, ui.Question{Title: "Profile", Options: opts, Default: -1, Filterable: true})
	if err != nil {
		return "", false, err
	}
	if i < 0 || i >= len(opts) {
		return "", false, ui.Failure(fmt.Errorf("invalid selection %d", i))
	}
	return opts[i].Value, true, nil
}

// splitRunArgs separates the profile name from the arguments for claude. The
// run command stops flag parsing at the first positional argument, so
// "ccshelf run sre --resume" and "ccshelf run sre -- --resume" are the same.
func splitRunArgs(cmd *cobra.Command, args []string) (name string, pass []string, err error) {
	dash := cmd.ArgsLenAtDash()
	switch {
	case dash == 0:
		return "", args, nil
	case dash > 1:
		return "", nil, ui.Usage(fmt.Errorf("expected one profile name before --, got %d arguments", dash))
	}
	if len(args) == 0 {
		return "", nil, nil
	}
	pass = args[1:]
	if err := checkPassthrough(pass); err != nil {
		return "", nil, err
	}
	// With flag parsing stopped at the profile name, pflag leaves a literal
	// "--" in place; it is the separator, not an argument for claude.
	if len(pass) > 0 && pass[0] == "--" {
		pass = pass[1:]
	}
	return args[0], pass, nil
}

// ownFlags are the flags of ccshelf itself. After the profile name they would
// go to claude, which does not know them.
var ownFlags = map[string]bool{
	"--account": true, "--yes": true, "--claude": true, "--config": true, "--root": true,
	"--no-interactive": true, "--no-color": true, "--plain": true, "--json": true,
}

// checkPassthrough fails (exit code 2) when a flag of ccshelf appears among the
// arguments passed to claude, before any literal "--", because it would not do
// what the user meant.
func checkPassthrough(pass []string) error {
	for _, a := range pass {
		if a == "--" {
			return nil
		}
		name, _, _ := strings.Cut(a, "=")
		if ownFlags[name] {
			return ui.Usage(withHint(fmt.Errorf("%s comes after the profile name, so it would be passed to claude, which does not know it", ui.SanitizeLine(name)),
				"flags of ccshelf go before the profile name: ccshelf run %s <profile> ...", ui.SanitizeLine(name)))
		}
	}
	return nil
}

// pluginIDRe is the plugin id form accepted anywhere ccshelf prints or passes
// it on: name@marketplace, each part starting with a letter or digit so that
// an id can never read as a command-line flag.
var pluginIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9._-]*$`)

// validPluginID reports whether id is a well-formed plugin id.
func validPluginID(id string) bool { return len(id) <= 256 && pluginIDRe.MatchString(id) }

// safeBase returns the base name of path for messages.
func safeBase(p string) string { return filepath.Base(p) }

// parseEditor splits an editor command line into the program and its
// arguments. It is never run through a shell. When the whole value names an
// existing file it is the program as it is (a Windows path such as
// C:\Program Files\Notepad++\notepad++.exe has spaces and backslashes);
// otherwise the value is split on white space, and single or double quotes
// group words (no escape characters: a backslash is an ordinary character, so
// "C:\Program Files\x.exe" -f works on Windows). An unterminated quote is an
// error.
func parseEditor(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if fi, err := os.Stat(s); err == nil && !fi.IsDir() {
		return []string{s}, nil
	}
	var (
		out    []string
		cur    strings.Builder
		quote  rune
		inWord bool
	)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote in the editor command", quote)
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out, nil
}

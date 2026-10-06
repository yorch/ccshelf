package account

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/ccshelf/ccshelf/internal/config"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// Options controls Add.
type Options struct {
	// Persist writes the account to the configuration file (through
	// config.Save) and updates cfg. When false, cfg and the file are untouched.
	Persist bool
	// ConfigPath is the configuration file to write when Persist is set. Empty
	// means config.Path().
	ConfigPath string
	// Marketplaces are marketplace sources to add inside the new account, such
	// as "acme/claude-plugins". They appear as steps of the Plan.
	Marketplaces []string
	// Plugins are plugin ids ("name@marketplace") to install inside the new
	// account. They appear as steps of the Plan.
	Plugins []string
}

// Step is one manual, one-time action after the directory exists. Exactly one
// of Argv and Slash is set.
type Step struct {
	// Description says what the step does.
	Description string
	// Env holds NAME=value pairs to set for Argv.
	Env []string
	// Argv is a shell command (program and arguments).
	Argv []string
	// Slash is a slash command to type inside the Claude Code session.
	Slash string
}

// Plan is the result of Add.
type Plan struct {
	// Name is the account name.
	Name string
	// Dir is the absolute, cleaned configuration directory.
	Dir string
	// Created is true when Add created the directory.
	Created bool
	// Persisted is true when Add wrote the config entry.
	Persisted bool
	// Steps are the one-time steps for the person to run.
	Steps []Step
}

var (
	pluginIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*@[A-Za-z0-9][A-Za-z0-9._-]*$`)
	// ErrDirInUse is returned when the directory is not suitable for a new account.
	ErrDirInUse = errors.New("directory is not available for a new account")
)

// Add prepares the account name in dir. See the package documentation. cfg
// must not be nil.
func Add(ctx context.Context, cfg *config.Config, name, dir string, opts Options) (*Plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, errors.New("adding account: nil config")
	}
	if !config.ValidAccountName(name) {
		return nil, fmt.Errorf("invalid account name %q: use lower-case letters, digits and hyphens (at most 32); an account is a name, never a path", ui.Sanitize(name))
	}
	if _, err := buildSteps(dir, opts); err != nil {
		return nil, err
	}
	abs, err := checkDir(cfg, name, dir)
	if err != nil {
		return nil, err
	}
	// Render the steps with the resolved directory.
	steps, err := buildSteps(abs, opts)
	if err != nil {
		return nil, err
	}

	if existing, ok := cfg.Accounts[name]; ok && !samePath(existing.ConfigDir, abs) {
		return nil, fmt.Errorf("account %q already exists with directory %s; remove it first or pick another name", name, existing.ConfigDir)
	}
	// Validate the resulting configuration before touching the disk.
	updated := withAccount(cfg, name, abs)
	if err := updated.Validate(); err != nil {
		return nil, fmt.Errorf("adding account %q: %w", name, err)
	}

	own := false
	if existing, ok := cfg.Accounts[name]; ok && samePath(existing.ConfigDir, abs) {
		own = true
	}
	top, err := ensureDir(abs, own)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Name: name, Dir: abs, Created: top != "", Steps: steps}

	if opts.Persist {
		path := opts.ConfigPath
		if path == "" {
			if path, err = config.Path(); err != nil {
				return nil, rollback(abs, top, fmt.Errorf("locating the config file: %w", err))
			}
		}
		if err := config.Save(path, updated); err != nil {
			return nil, rollback(abs, top, fmt.Errorf("saving account %q: %w", name, err))
		}
		cfg.Accounts = updated.Accounts
		plan.Persisted = true
	}
	return plan, nil
}

// rollback removes the directories Add just created (dir and any parents it
// made; only while empty) and returns err.
func rollback(dir, top string, err error) error {
	if top != "" {
		_ = removeCreated(dir, top)
	}
	return err
}

func withAccount(cfg *config.Config, name, dir string) *config.Config {
	c := *cfg
	c.Accounts = make(map[string]config.Account, len(cfg.Accounts)+1)
	for k, v := range cfg.Accounts {
		c.Accounts[k] = v
	}
	c.Accounts[name] = config.Account{ConfigDir: dir}
	return &c
}

// samePath reports whether a and b name the same directory on the running OS.
func samePath(a, b string) bool { return samePathFor(runtime.GOOS, a, b) }

// foldsCase reports whether goos has case-insensitive file systems by default
// (macOS and Windows), where ~/.Claude is ~/.claude.
func foldsCase(goos string) bool { return goos == "windows" || goos == "darwin" }

// samePathFor is samePath for an explicit goos so the case rules are testable
// on every host. Two spellings are the same when they are equal after cleaning
// (and case folding on case-insensitive systems), when their existing prefixes
// resolve to the same place, or when both exist and are the same file.
func samePathFor(goos, a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b || sameFile(a, b) {
		return true
	}
	return canon(goos, a) == canon(goos, b)
}

// sameFile reports whether a and b both exist and are the same file.
func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// canon returns p with its existing prefix's symlinks resolved and, on
// case-insensitive systems, folded to lower case.
func canon(goos, p string) string {
	r := resolve(p)
	if foldsCase(goos) {
		r = strings.ToLower(r)
	}
	return r
}

// resolve returns p with symlinks in its existing prefix resolved, so two
// spellings of one directory compare equal. A path whose tail does not exist
// yet keeps its tail.
func resolve(p string) string {
	p = filepath.Clean(p)
	rest := ""
	cur := p
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// within reports whether path is dir or below it. Both must already be
// canonical (see canon).
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// inside reports whether child is parent or below it: by name (resolving
// symlinks and, where the file system folds case, ignoring case) or because an
// existing ancestor of child is the very same file as parent.
func inside(goos, parent, child string) bool {
	if within(canon(goos, parent), canon(goos, child)) {
		return true
	}
	fp, err := os.Stat(parent)
	if err != nil {
		return false
	}
	for p := filepath.Clean(child); ; p = filepath.Dir(p) {
		if fi, err := os.Stat(p); err == nil && os.SameFile(fp, fi) {
			return true
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}

func hasControl(s string) bool { return ui.HasControl(s) }

// checkDir expands and validates dir and returns the cleaned absolute path.
func checkDir(cfg *config.Config, name, dir string) (string, error) {
	if dir == "" {
		return "", errors.New("account directory is empty")
	}
	if hasControl(dir) {
		return "", errors.New("account directory contains a control character")
	}
	expanded, err := config.ExpandPath(dir)
	if err != nil {
		return "", fmt.Errorf("account directory: %w", err)
	}
	// An environment variable can expand to a control character.
	if hasControl(expanded) {
		return "", errors.New("account directory contains a control character after expanding ~ and variables")
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("account directory %q must be an absolute path (use ~ for your home directory)", dir)
	}
	abs := filepath.Clean(expanded)

	def, err := config.DefaultClaudeDir()
	if err != nil {
		return "", err
	}
	switch {
	case inside(runtime.GOOS, def, abs):
		return "", fmt.Errorf("account directory %s is Claude Code's default directory %s or inside it; pick a separate directory such as ~/.claude-%s", abs, def, name)
	case inside(runtime.GOOS, abs, def):
		return "", fmt.Errorf("account directory %s contains Claude Code's default directory %s", abs, def)
	}

	if err := checkNoSymlinks(abs); err != nil {
		return "", err
	}
	for n, a := range cfg.Accounts {
		if n != name && samePath(a.ConfigDir, abs) {
			return "", fmt.Errorf("%w: %s is already the directory of account %q", ErrDirInUse, abs, n)
		}
	}
	return abs, nil
}

// checkNoSymlinks refuses a path where any existing component is a symlink,
// so a link cannot redirect the account directory somewhere else. Two cases
// are allowed because they are the operating system's own layout: a link that
// is the home directory or one of its ancestors, and a link directly below the
// root (such as /tmp or /var).
func checkNoSymlinks(abs string) error {
	home, _ := os.UserHomeDir()
	for p := abs; ; p = filepath.Dir(p) {
		parent := filepath.Dir(p)
		if parent == p {
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			continue // not created yet
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			continue
		}
		if filepath.Dir(parent) == parent || (home != "" && within(filepath.Clean(p), filepath.Clean(home))) {
			continue
		}
		return fmt.Errorf("%w: %s is a symlink; use the real path", ErrDirInUse, p)
	}
}

// ensureDir creates dir (mode 0700) or accepts an existing empty directory or
// one that already is this account's directory (own). It returns the topmost
// directory it created (dir itself or one of its parents), or "" when nothing
// was created, so a failure later can remove exactly what was added.
func ensureDir(dir string, own bool) (string, error) {
	fi, err := os.Lstat(dir)
	switch {
	case err == nil:
		if fi.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: %s is a symlink", ErrDirInUse, dir)
		}
		if !fi.IsDir() {
			return "", fmt.Errorf("%w: %s exists and is not a directory", ErrDirInUse, dir)
		}
		entries, rerr := os.ReadDir(dir)
		if rerr != nil {
			return "", fmt.Errorf("reading %s: %w", dir, rerr)
		}
		if len(entries) > 0 && !own {
			return "", fmt.Errorf("%w: %s exists and is not empty and is not an account directory", ErrDirInUse, dir)
		}
		if len(entries) == 0 {
			_ = os.Chmod(dir, 0o700) //nolint:gosec // a directory needs the execute bit; meaningless on Windows
		}
		return "", nil
	case errors.Is(err, fs.ErrNotExist):
	default:
		return "", fmt.Errorf("inspecting %s: %w", dir, err)
	}
	top := dir
	for p := filepath.Dir(dir); ; p = filepath.Dir(p) {
		if _, serr := os.Lstat(p); serr == nil || filepath.Dir(p) == p {
			break
		}
		top = p
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", fmt.Errorf("creating %s: %w", filepath.Dir(dir), err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		_ = removeCreated(dir, top)
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	_ = os.Chmod(dir, 0o700) //nolint:gosec // undo a permissive umask; a directory needs the execute bit
	return top, nil
}

// removeCreated removes dir and then its parents up to and including top,
// stopping at the first that is not empty. Only empty directories go.
func removeCreated(dir, top string) error {
	for p := dir; ; p = filepath.Dir(p) {
		if err := os.Remove(p); err != nil {
			return err
		}
		if p == top || filepath.Dir(p) == p {
			return nil
		}
	}
}

func buildSteps(dir string, opts Options) ([]Step, error) {
	env := []string{config.EnvConfigDir + "=" + dir}
	steps := []Step{
		{Description: "Start Claude Code in the new account directory", Env: env, Argv: []string{"claude"}},
		{Description: "Inside Claude Code, sign in (ccshelf never copies or reads credentials)", Slash: "/login"},
	}
	for _, m := range opts.Marketplaces {
		if m == "" || strings.HasPrefix(m, "-") || hasControl(m) || strings.ContainsAny(m, " \t") {
			return nil, fmt.Errorf("invalid marketplace source %q", ui.Sanitize(m))
		}
		steps = append(steps, Step{Description: "Add a marketplace", Slash: "/plugin marketplace add " + m})
	}
	for _, p := range opts.Plugins {
		if !pluginIDRe.MatchString(p) {
			return nil, fmt.Errorf("invalid plugin id %q: use name@marketplace", ui.Sanitize(p))
		}
		steps = append(steps, Step{Description: "Install a plugin", Slash: "/plugin install " + p})
	}
	return steps, nil
}

// Lines renders the plan for a person to follow, quoted for shell: "bash",
// "zsh", "sh" or "posix"; "fish"; "pwsh" or "powershell"; "cmd". A value that
// cannot be quoted safely for the shell is an error.
func (p *Plan) Lines(shell string) ([]string, error) {
	lines := []string{fmt.Sprintf("Account %q uses %s", p.Name, p.Dir)}
	for i, s := range p.Steps {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, s.Description))
		if s.Slash != "" {
			lines = append(lines, "   "+s.Slash)
			continue
		}
		cmd, err := shellCommand(shell, s)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		lines = append(lines, "   "+cmd)
	}
	return lines, nil
}

func shellCommand(shell string, s Step) (string, error) {
	style, ok := ui.ShellForName(shell)
	if !ok {
		return "", fmt.Errorf("unknown shell %q", shell)
	}
	argv, err := ui.Join(style, s.Argv)
	if err != nil {
		return "", err
	}
	if len(s.Env) == 0 {
		return argv, nil
	}
	var sets []string
	for _, kv := range s.Env {
		k, v, _ := strings.Cut(kv, "=")
		qv, err := ui.Quote(style, v)
		if err != nil {
			return "", err
		}
		switch {
		case shell == "fish":
			sets = append(sets, k+"="+qv)
		case style == ui.ShellPOSIX:
			sets = append(sets, k+"="+qv)
		case style == ui.ShellPowerShell:
			sets = append(sets, "$env:"+k+" = "+qv)
		default:
			if strings.Contains(v, `"`) {
				return "", fmt.Errorf("%w: a double quote cannot be carried safely in a cmd.exe set command", ui.ErrUnquotable)
			}
			sets = append(sets, `set "`+k+"="+v+`"`)
		}
	}
	switch {
	case shell == "fish":
		return "env " + strings.Join(sets, " ") + " " + argv, nil
	case style == ui.ShellPOSIX:
		return strings.Join(sets, " ") + " " + argv, nil
	case style == ui.ShellPowerShell:
		return strings.Join(sets, "; ") + "; " + argv, nil
	default:
		return strings.Join(sets, " && ") + " && " + argv, nil
	}
}

// Info describes one configured account.
type Info struct {
	// Name is the account name.
	Name string
	// ConfigDir is the configuration directory.
	ConfigDir string
	// IsDefault is true for config's default_account.
	IsDefault bool
	// Exists is true when ConfigDir exists and is a directory (Describe only).
	Exists bool
}

// List returns the configured accounts sorted by name. It does not touch the
// disk.
func List(cfg *config.Config) []Info {
	if cfg == nil {
		return nil
	}
	out := make([]Info, 0, len(cfg.Accounts))
	for n, a := range cfg.Accounts {
		out = append(out, Info{Name: n, ConfigDir: a.ConfigDir, IsDefault: n == cfg.DefaultAccount})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func find(cfg *config.Config, name string) (config.Account, error) {
	if cfg != nil {
		if a, ok := cfg.Accounts[name]; ok {
			return a, nil
		}
	}
	var names []string
	if cfg != nil {
		for n := range cfg.Accounts {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	known := "none configured"
	if len(names) > 0 {
		known = strings.Join(names, ", ")
	}
	return config.Account{}, fmt.Errorf("unknown account %q (known: %s)", ui.Sanitize(name), known)
}

// Describe returns the account and whether its directory exists. It checks
// only that the directory exists; it never lists or reads its contents.
func Describe(cfg *config.Config, name string) (Info, error) {
	a, err := find(cfg, name)
	if err != nil {
		return Info{}, err
	}
	info := Info{Name: name, ConfigDir: a.ConfigDir, IsDefault: name == cfg.DefaultAccount}
	if fi, err := os.Stat(a.ConfigDir); err == nil && fi.IsDir() {
		info.Exists = true
	}
	return info, nil
}

// Env returns the environment additions for running claude as the account:
// CLAUDE_CONFIG_DIR=<dir>.
func Env(cfg *config.Config, name string) ([]string, error) {
	a, err := find(cfg, name)
	if err != nil {
		return nil, err
	}
	return []string{config.EnvConfigDir + "=" + a.ConfigDir}, nil
}

// Removal reports the outcome of Remove.
type Removal struct {
	// Name is the removed account.
	Name string
	// ConfigDir is the directory that was left in place.
	ConfigDir string
	// ClearedDefault is true when the account was the default and default_account
	// was cleared.
	ClearedDefault bool
}

// Message is a sentence for the person, stating that nothing was deleted.
func (r Removal) Message() string {
	msg := fmt.Sprintf("Removed account %q from the configuration. The directory %s was NOT deleted: it still holds the login, plugins and history. Delete it yourself if you no longer need it.", r.Name, r.ConfigDir)
	if r.ClearedDefault {
		msg += " It was the default account, so default_account is now unset."
	}
	return msg
}

// Remove deletes the account's entry from cfg (in memory; the caller saves it
// with config.Save). It never deletes the directory.
func Remove(cfg *config.Config, name string) (Removal, error) {
	a, err := find(cfg, name)
	if err != nil {
		return Removal{}, err
	}
	delete(cfg.Accounts, name)
	r := Removal{Name: name, ConfigDir: a.ConfigDir}
	if cfg.DefaultAccount == name {
		cfg.DefaultAccount = ""
		r.ClearedDefault = true
	}
	return r, nil
}

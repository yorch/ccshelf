package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ccshelf/ccshelf/internal/testutil"
)

// Paths of the binaries built by TestMain.
var (
	ccshelfBin string
	binDir     string // directory with the fake claude, first on PATH
)

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// moduleRoot finds the module directory: CCSHELF_E2E_MODULE (a developer
// knob, to test another checkout) or the nearest go.mod above the package.
func moduleRoot() (string, error) {
	if v := os.Getenv("CCSHELF_E2E_MODULE"); v != "" {
		return v, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above the working directory")
		}
		dir = parent
	}
}

func goBuild(mod, pkg, out string) error {
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", out, pkg)
	cmd.Dir = mod
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build %s: %w\n%s", pkg, err, b)
	}
	return nil
}

func TestMain(m *testing.M) {
	code, err := setupAndRun(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e setup:", err)
		code = 1
	}
	os.Exit(code)
}

func setupAndRun(m *testing.M) (int, error) {
	mod, err := moduleRoot()
	if err != nil {
		return 1, err
	}
	dir, err := os.MkdirTemp("", "ccshelf-e2e-bin-")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(dir)
	binDir = dir
	ccshelfBin = filepath.Join(dir, exe("ccshelf"))
	if err := goBuild(mod, "./cmd/ccshelf", ccshelfBin); err != nil {
		return 1, err
	}
	// The fake is named claude so that PATH lookup finds it.
	if err := goBuild(mod, "./internal/testutil/fakeclaude", filepath.Join(dir, exe("claude"))); err != nil {
		return 1, err
	}
	return m.Run(), nil
}

// result is the outcome of one ccshelf invocation.
type result struct {
	Code   int
	Stdout string
	Stderr string
}

// sandbox is an isolated user environment for one test.
type sandbox struct {
	t      *testing.T
	root   string
	Home   string
	Config string // XDG_CONFIG_HOME
	Cache  string // XDG_CACHE_HOME
	Work   string // working directory
	Log    string // FAKE_CLAUDE_LOG
	extra  map[string]string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	root := t.TempDir()
	// Resolve symlinks (macOS temp dirs) so that paths compare equal.
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	s := &sandbox{
		t: t, root: root,
		Home:   filepath.Join(root, "home"),
		Config: filepath.Join(root, "home", ".config"),
		Cache:  filepath.Join(root, "home", ".cache"),
		Work:   filepath.Join(root, "work"),
		Log:    filepath.Join(root, "claude-log.jsonl"),
		extra:  map[string]string{},
	}
	for _, d := range []string{s.Home, s.Config, s.Cache, s.Work, filepath.Join(root, "appdata"), filepath.Join(root, "localappdata")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// ConfigDir is where ccshelf keeps config.toml, lock.json and profiles/.
func (s *sandbox) ConfigDir() string { return filepath.Join(s.Config, "ccshelf") }

// CacheDir is ccshelf's cache directory.
func (s *sandbox) CacheDir() string { return filepath.Join(s.Cache, "ccshelf") }

// Setenv adds or overrides one variable of the child's environment.
func (s *sandbox) Setenv(k, v string) { s.extra[k] = v }

// env builds the child environment from scratch, so nothing of the caller's
// (CLAUDE_CONFIG_DIR, CI, ANTHROPIC_*) leaks in.
func (s *sandbox) env() []string {
	// An empty file, not os.DevNull: git for Windows on arm64 cannot open NUL.
	gitcfg := filepath.Join(s.root, "empty.gitconfig")
	if err := os.WriteFile(gitcfg, nil, 0o600); err != nil {
		s.t.Fatal(err)
	}
	e := map[string]string{
		"PATH":                binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME":                s.Home,
		"USERPROFILE":         s.Home,
		"XDG_CONFIG_HOME":     s.Config,
		"XDG_CACHE_HOME":      s.Cache,
		"APPDATA":             s.Config, // Windows: the config and cache bases
		"LOCALAPPDATA":        s.Cache,
		"GIT_CONFIG_GLOBAL":   gitcfg,
		"GIT_CONFIG_SYSTEM":   gitcfg,
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
		"FAKE_CLAUDE_LOG":     s.Log,
		"TMPDIR":              s.root,
		"TEMP":                s.root,
		"TMP":                 s.root,
		"NO_COLOR":            "1",
	}
	if runtime.GOOS == "windows" {
		for _, k := range []string{"SystemRoot", "SYSTEMROOT", "PATHEXT", "ComSpec", "windir"} {
			if v := os.Getenv(k); v != "" {
				e[k] = v
			}
		}
	}
	for k, v := range s.extra {
		e[k] = v
	}
	out := make([]string, 0, len(e))
	for k, v := range e {
		if v != "" {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// try runs ccshelf in dir with stdin; it is safe to call from goroutines (it
// never touches the test). A non-zero exit is a result, not an error.
func (s *sandbox) try(ctx context.Context, dir, stdin string, args ...string) (result, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ccshelfBin, args...) //nolint:gosec // the binary this package built
	cmd.Dir = dir
	cmd.Env = s.env()
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return result{}, fmt.Errorf("running ccshelf %v: %w", args, err)
		}
		code = ee.ExitCode()
	}
	return result{Code: code, Stdout: so.String(), Stderr: se.String()}, nil
}

// runIn runs ccshelf in dir with stdin.
func (s *sandbox) runIn(dir string, stdin string, args ...string) result {
	s.t.Helper()
	r, err := s.try(context.Background(), dir, stdin, args...)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// run runs ccshelf in the sandbox's work directory with empty stdin.
func (s *sandbox) run(args ...string) result {
	s.t.Helper()
	return s.runIn(s.Work, "", args...)
}

// mustRun fails the test unless ccshelf exits 0.
func (s *sandbox) mustRun(args ...string) result {
	s.t.Helper()
	r := s.run(args...)
	if r.Code != 0 {
		s.t.Fatalf("ccshelf %v exited %d\nstdout:\n%s\nstderr:\n%s", args, r.Code, r.Stdout, r.Stderr)
	}
	return r
}

// write creates a file (and its parents) under the sandbox.
func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeProfile writes a personal profile.
func (s *sandbox) writeProfile(name, content string) {
	s.t.Helper()
	write(s.t, filepath.Join(s.ConfigDir(), "profiles", name+".toml"), content)
}

// addDirSource configures dir as a shared (org) profile source through the
// init command, the supported way.
func (s *sandbox) addDirSource(dir string) {
	s.t.Helper()
	s.mustRun("--no-interactive", "init", "--force", "--dir", dir)
}

// launches returns the recorded invocations of the fake claude that started a
// session (the ones carrying --settings), not the read-only queries.
func (s *sandbox) launches() []testutil.Invocation {
	s.t.Helper()
	var out []testutil.Invocation
	for _, inv := range testutil.ReadLog(s.t, s.Log) {
		if argAfter(inv.Argv, "--settings") != "" {
			out = append(out, inv)
		}
	}
	return out
}

// anyStart returns every recorded invocation that is not a read-only query
// (plugin list, agents, --version).
func (s *sandbox) anyStart() []testutil.Invocation {
	s.t.Helper()
	var out []testutil.Invocation
	for _, inv := range testutil.ReadLog(s.t, s.Log) {
		if len(inv.Argv) > 0 && (inv.Argv[0] == "plugin" || inv.Argv[0] == "agents" || inv.Argv[0] == "--version") {
			continue
		}
		out = append(out, inv)
	}
	return out
}

// settingsOf parses the --settings file of an invocation.
func settingsOf(t *testing.T, inv testutil.Invocation) map[string]any {
	t.Helper()
	p := argAfter(inv.Argv, "--settings")
	if p == "" {
		t.Fatalf("no --settings in %v", inv.Argv)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading the settings file: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("settings file is not JSON: %v\n%s", err, b)
	}
	return m
}

// enabledPlugins returns the enabledPlugins map of a settings object.
func enabledPlugins(t *testing.T, st map[string]any) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	m, _ := st["enabledPlugins"].(map[string]any)
	for k, v := range m {
		b, ok := v.(bool)
		if !ok {
			t.Fatalf("enabledPlugins[%q] = %v, want a bool", k, v)
		}
		out[k] = b
	}
	return out
}

func argAfter(argv []string, flag string) string {
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func hasArg(argv []string, a string) bool {
	for _, x := range argv {
		if x == a {
			return true
		}
	}
	return false
}

// contains fails the test when s lacks any of the substrings.
func contains(t *testing.T, what, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("%s lacks %q:\n%s", what, sub, s)
		}
	}
}

// exampleOrg copies examples/org-data-repo into a fresh directory.
func exampleOrg(t *testing.T) string {
	t.Helper()
	mod, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(mod, "examples", "org-data-repo")
	dst := filepath.Join(t.TempDir(), "org")
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// showJSON returns the data object of `show --json`.
func (s *sandbox) showJSON(profile string) map[string]any {
	s.t.Helper()
	r := s.mustRun("show", "--json", profile)
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &env); err != nil {
		s.t.Fatalf("show --json: %v\n%s", err, r.Stdout)
	}
	return env.Data
}

// closureHash returns the closure hash of a profile.
func (s *sandbox) closureHash(profile string) string {
	s.t.Helper()
	h, _ := s.showJSON(profile)["closure_hash"].(string)
	if h == "" {
		s.t.Fatalf("no closure hash for %s", profile)
	}
	return h
}

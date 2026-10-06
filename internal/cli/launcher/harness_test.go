package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/policy"
	"github.com/ccshelf/ccshelf/internal/testutil"
	"github.com/ccshelf/ccshelf/internal/ui"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.Cleanup()
	os.Exit(code)
}

// harness drives the launcher commands under a root command with scripted
// streams, an isolated home and the fake claude.
type harness struct {
	t       *testing.T
	dirs    map[string]string
	out     *bytes.Buffer
	errb    *bytes.Buffer
	in      string
	g       clicore.Globals
	prompt  ui.Prompter
	goos    string
	cwd     string
	managed string
	claude  string
	// Start seam results.
	started   int
	startArgs []string
	startEnv  []string
	startBin  string
	startCode int
	startErr  error
	// Spawn seam.
	spawned   [][]string
	spawnCode int
	spawnHook func(args []string)
	newGit    GitFactory
	// spawnCtx is the context the last Spawn received; ctx, when set, is the
	// context commands run under; lastErr is the error of the last run.
	spawnCtx context.Context
	ctx      context.Context
	lastErr  error
	// Test seams of the run pipeline.
	validate   func([]byte) error
	afterWrite func(string)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	d := testutil.IsolatedEnv(t)
	h := &harness{
		t: t, dirs: d, out: &bytes.Buffer{}, errb: &bytes.Buffer{}, goos: "linux", cwd: d["WORK"],
		managed: filepath.Join(t.TempDir(), "managed"),
	}
	if err := os.MkdirAll(h.managed, 0o700); err != nil {
		t.Fatal(err)
	}
	h.claude = testutil.BuildFakeClaude(t)
	h.g.ClaudePath = h.claude
	t.Setenv("FAKE_CLAUDE_PLUGINS", "")
	t.Setenv("FAKE_CLAUDE_MANAGED", "")
	return h
}

// configDir is where config.toml and profiles/ live.
func (h *harness) configDir() string { return filepath.Join(h.dirs["XDG_CONFIG_HOME"], "ccshelf") }

func (h *harness) writeConfig(content string) {
	h.t.Helper()
	testutil.WriteFile(h.t, filepath.Join(h.configDir(), "config.toml"), content)
}

func (h *harness) writeProfile(name, content string) {
	h.t.Helper()
	testutil.WriteFile(h.t, filepath.Join(h.configDir(), "profiles", name+".toml"), content)
}

func (h *harness) writeManaged(content string) {
	h.t.Helper()
	testutil.WriteFile(h.t, filepath.Join(h.managed, "managed-settings.json"), content)
}

func (h *harness) build() *cobra.Command {
	no := false
	env := &clicore.Env{
		Streams:  ui.Streams{In: strings.NewReader(h.in), Out: h.out, Err: h.errb},
		Getenv:   os.Getenv,
		Environ:  os.Environ,
		Getwd:    func() (string, error) { return h.cwd, nil },
		Now:      func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) },
		GOOS:     h.goos,
		Prompter: h.prompt,
	}
	root := &cobra.Command{Use: "ccshelf", SilenceErrors: true, SilenceUsage: true}
	pf := root.PersistentFlags()
	pf.StringVar(&h.g.ConfigPath, "config", h.g.ConfigPath, "")
	pf.StringVar(&h.g.ClaudePath, "claude", h.g.ClaudePath, "")
	pf.StringVar(&h.g.Account, "account", "", "")
	pf.BoolVar(&h.g.NoInteractive, "no-interactive", false, "")
	pf.BoolVar(&h.g.NoColor, "no-color", false, "")
	pf.BoolVar(&h.g.Plain, "plain", false, "")
	pf.BoolVar(&h.g.JSON, "json", false, "")
	get := func() (*clicore.Context, error) { return env.Context(&h.g, "", ""), nil }
	opt := Options{
		Start: func(bin string, args, e []string) (int, error) {
			h.started++
			h.startBin, h.startArgs, h.startEnv = bin, args, e
			return h.startCode, h.startErr
		},
		Spawn: func(ctx context.Context, bin string, args, _ []string, _ io.Reader, _, _ io.Writer) (int, error) {
			h.spawnCtx = ctx
			h.spawned = append(h.spawned, append([]string{bin}, args...))
			if h.spawnHook != nil {
				h.spawnHook(args)
			}
			return h.spawnCode, nil
		},
		Executable: func() (string, error) {
			if h.goos == "windows" {
				return `C:\tools\ccshelf.exe`, nil
			}
			// A POSIX path regardless of the host: the harness GOOS is "linux",
			// for which a drive-letter path is not absolute.
			return "/opt/ccshelf/bin/ccshelf", nil
		},
		Policy: policy.Options{GOOS: "linux", ManagedDir: h.managed, WSL: &no},
		NewGit: h.newGit,

		validateSettings: h.validate, afterSettingsWrite: h.afterWrite,
	}
	root.AddCommand(CommandsWith(get, opt)...)
	root.SetOut(h.out)
	root.SetErr(h.errb)
	return root
}

// run executes the command line and returns the exit code. Errors are
// reported to the error stream like the real main does.
func (h *harness) run(args ...string) int {
	h.t.Helper()
	h.out.Reset()
	h.errb.Reset()
	root := h.build()
	root.SetArgs(args)
	ctx := h.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	err := root.ExecuteContext(ctx)
	h.lastErr = err
	if err != nil {
		var ee *ui.ExitError
		if !(asExit(err, &ee) && ee.Err == nil) {
			ui.Report(h.errb, err, ui.Mode{})
		}
	}
	return ui.CodeOf(err)
}

func asExit(err error, target **ui.ExitError) bool {
	for err != nil {
		if e, ok := err.(*ui.ExitError); ok { //nolint:errorlint // walks the chain by hand on purpose
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (h *harness) mustRun(args ...string) {
	h.t.Helper()
	if code := h.run(args...); code != 0 {
		h.t.Fatalf("ccshelf %v exited %d\nstdout:\n%s\nstderr:\n%s", args, code, h.out, h.errb)
	}
}

// exampleOrg copies the starter template into a temp dir and returns it. It
// has profiles base, frontend, seo and sre, a registry and prompts.
func (h *harness) exampleOrg() string {
	h.t.Helper()
	dst := h.copyTree(filepath.Join("..", "..", "..", "examples", "org-data-repo"))
	// The starter's ccshelf.toml carries lint keys the launcher does not read;
	// keep the test org config minimal and valid.
	if err := os.WriteFile(filepath.Join(dst, "ccshelf.toml"), []byte("[protect]\nplugins = [\"audit-logger@acme\"]\n"), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return dst
}

func (h *harness) copyTree(src string) string {
	h.t.Helper()
	dst := filepath.Join(h.t.TempDir(), "org")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return dst
}

// fixtureOrg returns a copy of testdata/org, a small stable org used by the
// golden tests (the starter template under examples/ may evolve).
func (h *harness) fixtureOrg() string {
	h.t.Helper()
	return h.copyTree("testdata/org")
}

// useOrg writes a config with the org directory as a dir source.
func (h *harness) useOrg(org string) {
	h.t.Helper()
	h.writeConfig("[[sources]]\ntype = \"dir\"\npath = " + tomlString(filepath.Join(org, "profiles")) + "\n")
}

func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// settingsOf reads and decodes the --settings file of the last start.
func (h *harness) settingsOf(args []string) map[string]any {
	h.t.Helper()
	p := argAfter(args, "--settings")
	if p == "" {
		h.t.Fatalf("no --settings in %v", args)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		h.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		h.t.Fatalf("settings: %v\n%s", err, b)
	}
	return m
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasArg(args []string, a string) bool {
	for _, x := range args {
		if x == a {
			return true
		}
	}
	return false
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from golden\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

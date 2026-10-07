package updatecmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/ui"
	"github.com/yorch/ccshelf/internal/update"
	"github.com/yorch/ccshelf/internal/update/updatetest"
)

// fakeBinary is the content of a fake ccshelf of the given version.
func fakeBinary(v string) []byte { return []byte("fake-ccshelf-" + v + "\n") }

// runVersionFromContent is the RunVersion seam: it reads the version out of a
// fakeBinary instead of executing it.
func runVersionFromContent(_ context.Context, path string, _ []string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(b)
	if !strings.HasPrefix(s, "fake-ccshelf-") {
		return "", update.ErrNotCcshelf
	}
	return strings.TrimSpace(strings.TrimPrefix(s, "fake-ccshelf-")), nil
}

// loopbackOnly is an HTTP transport that refuses every host but 127.0.0.1, so
// that no test can reach the real network whatever the configuration says.
type loopbackOnly struct{ inner http.RoundTripper }

func (l loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() != "127.0.0.1" {
		return nil, fmt.Errorf("test network guard: refusing %s", r.URL.Host)
	}
	return l.inner.RoundTrip(r)
}

type harness struct {
	t       *testing.T
	srv     *updatetest.Server
	out     *bytes.Buffer
	errb    *bytes.Buffer
	g       clicore.Globals
	prompt  ui.Prompter
	goos    string
	opt     Options
	exe     string
	binDir  string
	cfgPath string
	state   string
	now     time.Time
	env     map[string]string
	cur     string
	lastErr error
}

func newHarness(t *testing.T, cur string) *harness {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	h := &harness{
		t: t, srv: updatetest.NewServer(t), out: &bytes.Buffer{}, errb: &bytes.Buffer{}, goos: "linux", cur: cur,
		binDir: filepath.Join(root, "bin"), state: filepath.Join(root, "cache"), cfgPath: filepath.Join(root, "config.toml"),
		now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC), env: map[string]string{},
	}
	for _, d := range []string{h.binDir, h.state} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	h.exe = filepath.Join(h.binDir, "ccshelf")
	if runtime.GOOS == "windows" {
		h.exe += ".exe"
	}
	if err := os.WriteFile(h.exe, fakeBinary(cur), 0o755); err != nil {
		t.Fatal(err)
	}
	inner := h.srv.Client().Transport
	h.opt = Options{
		Executable:     func() (string, error) { return h.exe, nil },
		Client:         &http.Client{Transport: loopbackOnly{inner}, Timeout: 20 * time.Second, CheckRedirect: update.RedirectPolicy(update.Source{})},
		StateDir:       h.state,
		CurrentVersion: func() string { return h.cur },
		RunVersion:     runVersionFromContent,
		InContainer:    func(*clicore.Context) bool { return false },
	}
	h.g.ConfigPath = h.cfgPath
	h.writeConfig("")
	return h
}

// writeConfig writes config.toml with base_url pointing at the fake server and
// extra [update] lines.
func (h *harness) writeConfig(updateLines string) {
	h.t.Helper()
	body := "[update]\nbase_url = \"" + h.srv.URL() + "\"\n" + updateLines
	if err := os.WriteFile(h.cfgPath, []byte(body), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// publish publishes tag for the running platform with a fake binary.
func (h *harness) publish(tag string) *updatetest.Release {
	h.t.Helper()
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		h.t.Skip("no release archive for this OS")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		h.t.Skip("no release archive for this architecture")
	}
	return h.srv.Publish(tag, fakeBinary(strings.TrimPrefix(tag, "v")), runtime.GOOS, runtime.GOARCH, false)
}

func (h *harness) build() *cobra.Command {
	env := &clicore.Env{
		Streams:  ui.Streams{In: strings.NewReader(""), Out: h.out, Err: h.errb},
		Getenv:   func(k string) string { return h.env[k] },
		Environ:  func() []string { return nil },
		Getwd:    func() (string, error) { return h.binDir, nil },
		Now:      func() time.Time { return h.now },
		GOOS:     h.goos,
		Prompter: h.prompt,
	}
	get := func() (*clicore.Context, error) { return env.Context(&h.g, "", ""), nil }
	root := &cobra.Command{Use: "ccshelf", SilenceErrors: true, SilenceUsage: true}
	pf := root.PersistentFlags()
	pf.StringVar(&h.g.ConfigPath, "config", h.cfgPath, "")
	pf.BoolVar(&h.g.NoInteractive, "no-interactive", false, "")
	pf.BoolVar(&h.g.NoColor, "no-color", false, "")
	pf.BoolVar(&h.g.Plain, "plain", false, "")
	pf.BoolVar(&h.g.JSON, "json", false, "")
	root.SetOut(h.out)
	root.SetErr(h.errb)
	root.AddCommand(Commands(get, h.opt)...)
	// Stand-ins for the commands the hooks must (or must not) act around.
	for _, name := range []string{"ls", "run", "dry-run", "version", "completion", "shell-init", "show", "fail"} {
		root.AddCommand(&cobra.Command{Use: name, Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintln(h.out, "ran "+cmd.Name())
			if cmd.Name() == "fail" {
				return errors.New("boom")
			}
			return nil
		}})
	}
	Hook(root, get, h.opt)
	return root
}

// run executes a command line and returns the exit code.
func (h *harness) run(args ...string) int {
	h.t.Helper()
	h.out.Reset()
	h.errb.Reset()
	root := h.build()
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	h.lastErr = err
	code := ui.CodeOf(err)
	if err != nil {
		mode := ui.Mode{JSON: h.g.JSON}
		ui.Report(h.errb, err, mode)
	}
	return code
}

func (h *harness) mustRun(args ...string) {
	h.t.Helper()
	if code := h.run(args...); code != 0 {
		h.t.Fatalf("ccshelf %v exited %d\nstdout:\n%s\nstderr:\n%s", args, code, h.out, h.errb)
	}
}

func (h *harness) exeContent() string {
	h.t.Helper()
	b, err := os.ReadFile(h.exe)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

func (h *harness) hits() int { return len(h.srv.Hits()) }

// envelope decodes the JSON envelope on stdout.
func (h *harness) envelope() (kind string, data map[string]any) {
	h.t.Helper()
	var e struct {
		Version int            `json:"version"`
		Kind    string         `json:"kind"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &e); err != nil {
		h.t.Fatalf("stdout is not a JSON envelope: %v\n%s", err, h.out)
	}
	if e.Version != 1 {
		h.t.Errorf("envelope version = %d", e.Version)
	}
	return e.Kind, e.Data
}

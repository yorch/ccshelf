package orgcmd

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/ccshelf/ccshelf/internal/cli/clicore"
	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/ui"
)

// fixedNow is the clock of every test.
var fixedNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// copyExample copies examples/org-data-repo into a new temp dir.
func copyExample(t *testing.T) string {
	t.Helper()
	src := filepath.Join("..", "..", "..", "examples", "org-data-repo")
	dst := filepath.Join(t.TempDir(), "org")
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	// The starter's ccshelf.toml once had platform_owners above [lint];
	// accept both layouts so these tests do not depend on that fix.
	cfg := read(t, dst, "ccshelf.toml")
	const stray = "platform_owners = [\"@acme/platform\"]\n"
	if i := strings.Index(cfg, stray); i >= 0 && i < strings.Index(cfg, "[lint]") {
		cfg = strings.Replace(cfg, stray, "", 1)
		cfg = strings.Replace(cfg, "[lint]\n", "[lint]\n"+stray, 1)
		write(t, dst, "ccshelf.toml", cfg)
	}
	// The starter's MCP registry once carried a description key the closed
	// registry schema rejects; drop it so these tests do not depend on that.
	reg := filepath.Join(dst, "mcp", "registry.toml")
	if _, err := profile.LoadRegistry(reg); err != nil {
		var keep []string
		for _, l := range strings.Split(read(t, dst, "mcp/registry.toml"), "\n") {
			if !strings.HasPrefix(l, "description =") {
				keep = append(keep, l)
			}
		}
		write(t, dst, "mcp/registry.toml", strings.Join(keep, "\n"))
	}
	return dst
}

// result is one command run.
type result struct {
	out, err string
	code     int
}

// harness runs commands against a root with scripted streams.
type harness struct {
	t   *testing.T
	env *clicore.Env
	g   *clicore.Globals
	cwd string
	so  *bytes.Buffer
	se  *bytes.Buffer
}

func newHarness(t *testing.T, root string) *harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("APPDATA", filepath.Join(home, "appdata"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "local"))
	h := &harness{t: t, g: &clicore.Globals{Root: root}, cwd: t.TempDir(), so: &bytes.Buffer{}, se: &bytes.Buffer{}}
	h.env = &clicore.Env{
		Streams: ui.Streams{In: bytes.NewReader(nil), Out: h.so, Err: h.se},
		Getenv:  func(string) string { return "" },
		Environ: func() []string { return nil },
		Getwd:   func() (string, error) { return h.cwd, nil },
		Now:     func() time.Time { return fixedNow },
		GOOS:    "linux",
	}
	return h
}

// run executes args and returns the output and the exit code.
func (h *harness) run(args ...string) result {
	h.t.Helper()
	h.so.Reset()
	h.se.Reset()
	root := &cobra.Command{Use: "ccshelf", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&h.g.Root, "root", h.g.Root, "")
	root.PersistentFlags().BoolVar(&h.g.JSON, "json", false, "")
	root.SetOut(h.so)
	root.SetErr(h.se)
	root.AddCommand(Commands(func() (*clicore.Context, error) { return h.env.Context(h.g, "", ""), nil })...)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	ui.Report(h.se, err, ui.Mode{})
	h.g.JSON = false
	return result{out: h.so.String(), err: h.se.String(), code: ui.CodeOf(err)}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

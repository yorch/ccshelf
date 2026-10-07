package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/yorch/ccshelf/internal/update/updatetest"
)

// The update tests need two ccshelf binaries that report different versions
// and accept a plain-http release server on 127.0.0.1. They are built here with
//
//	go build -tags e2eloopback -ldflags "-X .../version.Version=<v>"
//
// The tag compiles config.LoopbackHTTPAllowed = true into THESE binaries only
// (internal/config/loopback_e2e.go). It is a compile-time constant, not a flag,
// an environment variable or a file the binary reads, so a release build (which
// never sets the tag) has no such bypass, and the binary the other e2e tests
// use (built without the tag) rejects an http base URL, which
// TestUpdateRejectsPlainHTTPInProduction proves.

var (
	stampedMu   sync.Mutex
	stampedDir  string
	stampedBins = map[string]string{}
)

// stampedBinary builds (once per version) a ccshelf that reports version and
// returns its path.
func stampedBinary(t *testing.T, version string) string {
	t.Helper()
	stampedMu.Lock()
	defer stampedMu.Unlock()
	if p, ok := stampedBins[version]; ok {
		return p
	}
	mod, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	if stampedDir == "" {
		if stampedDir, err = os.MkdirTemp("", "ccshelf-e2e-stamped-"); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(stampedDir, "ccshelf-"+version+exe(""))
	cmd := exec.CommandContext(context.Background(), "go", "build", "-tags", "e2eloopback",
		"-ldflags", "-X github.com/yorch/ccshelf/internal/version.Version="+version,
		"-o", out, "./cmd/ccshelf")
	cmd.Dir = mod
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ccshelf %s: %v\n%s", version, err, b)
	}
	stampedBins[version] = out
	return out
}

func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

// updateEnv is a sandbox whose installed ccshelf is a copy of the 0.0.1 build
// and whose release server publishes the 0.0.2 build.
type updateEnv struct {
	*sandbox
	srv *updatetest.Server
	bin string
}

func newUpdateEnv(t *testing.T) *updateEnv {
	t.Helper()
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("no release archive for this OS")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("no release archive for this architecture")
	}
	old := stampedBinary(t, "0.0.1")
	newer := stampedBinary(t, "0.0.2")
	s := newSandbox(t)
	// cosign must never be found: it may be installed on a developer's machine
	// and the fake release has no real signature. The fake claude is the only
	// thing on PATH.
	s.Setenv("PATH", binDir)
	srv := updatetest.NewPlainServer(t)
	body, err := os.ReadFile(newer)
	if err != nil {
		t.Fatal(err)
	}
	srv.Publish("v0.0.2", body, runtime.GOOS, runtime.GOARCH, false)
	installDir := filepath.Join(s.root, "install")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(installDir, exe("ccshelf"))
	copyExecutable(t, old, bin)
	write(t, filepath.Join(s.ConfigDir(), "config.toml"), "[update]\nbase_url = \""+srv.URL()+"\"\n")
	return &updateEnv{sandbox: s, srv: srv, bin: bin}
}

func (u *updateEnv) ccshelf(args ...string) result {
	u.t.Helper()
	r, err := u.tryBin(context.Background(), u.bin, u.Work, "", args...)
	if err != nil {
		u.t.Fatal(err)
	}
	return r
}

func (u *updateEnv) version() string {
	u.t.Helper()
	r := u.ccshelf("version")
	if r.Code != 0 {
		u.t.Fatalf("version exited %d: %s", r.Code, r.Stderr)
	}
	f := strings.Fields(r.Stdout)
	if len(f) < 2 {
		u.t.Fatalf("version output = %q", r.Stdout)
	}
	return strings.TrimPrefix(f[1], "v")
}

func (u *updateEnv) mustCcshelf(args ...string) result {
	u.t.Helper()
	r := u.ccshelf(args...)
	if r.Code != 0 {
		u.t.Fatalf("ccshelf %v exited %d\nstdout:\n%s\nstderr:\n%s", args, r.Code, r.Stdout, r.Stderr)
	}
	return r
}

func TestUpdateEndToEnd(t *testing.T) {
	u := newUpdateEnv(t)
	if v := u.version(); v != "0.0.1" {
		t.Fatalf("installed version = %q, want 0.0.1", v)
	}

	// --check: exit 0, nothing downloaded.
	r := u.mustCcshelf("update", "--check")
	contains(t, "update --check", r.Stdout, "current: 0.0.1", "latest:  0.0.2", "an update is available")
	for _, p := range u.srv.Hits() {
		if strings.Contains(p, "/releases/download/") {
			t.Errorf("--check downloaded %s", p)
		}
	}

	// No terminal and no --yes: exit 2 naming the flag; nothing changes.
	r = u.ccshelf("update")
	if r.Code != 2 || !strings.Contains(r.Stderr, "--yes") {
		t.Fatalf("update without --yes: exit %d\n%s", r.Code, r.Stderr)
	}
	if v := u.version(); v != "0.0.1" {
		t.Fatalf("version = %s after a refused update", v)
	}

	// The real thing.
	r = u.mustCcshelf("update", "--yes")
	contains(t, "update --yes", r.Stderr, "updated ccshelf 0.0.1 -> 0.0.2", filepath.Base(u.bin)+".old")
	if v := u.version(); v != "0.0.2" {
		t.Fatalf("version after the update = %q, want 0.0.2", v)
	}
	backup := u.bin + ".old"
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("no backup: %v", err)
	}
	if out, err := exec.CommandContext(context.Background(), backup, "version").Output(); err != nil || !strings.Contains(string(out), "0.0.1") { //nolint:gosec // the backup this test just made
		t.Errorf("the backup should still be 0.0.1: %q %v", out, err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(u.bin)); len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("install directory = %v, want exactly the binary and its .old", names)
	}

	// Already current.
	r = u.mustCcshelf("update", "--yes")
	contains(t, "second update", r.Stderr, "up to date")

	// Rollback restores 0.0.1 (and the replaced version becomes the backup).
	r = u.mustCcshelf("update", "--rollback", "--yes")
	contains(t, "rollback", r.Stderr, "restored 0.0.1")
	if v := u.version(); v != "0.0.1" {
		t.Fatalf("version after the rollback = %q, want 0.0.1", v)
	}
	if out, err := exec.CommandContext(context.Background(), backup, "version").Output(); err != nil || !strings.Contains(string(out), "0.0.2") { //nolint:gosec // the backup this test just made
		t.Errorf("the replaced version should now be the backup: %q %v", out, err)
	}
}

func TestUpdateRefusesATamperedRelease(t *testing.T) {
	u := newUpdateEnv(t)
	// A newer release whose checksums.txt does not match its archive.
	u.srv.Add(&updatetest.Release{Tag: "v0.0.3", Assets: map[string][]byte{
		updatetest.ArchiveName("v0.0.3", runtime.GOOS, runtime.GOARCH): []byte("not the archive that was hashed"),
		"checksums.txt": []byte(strings.Repeat("0", 64) + "  " + updatetest.ArchiveName("v0.0.3", runtime.GOOS, runtime.GOARCH) + "\n"),
	}})
	r := u.ccshelf("update", "--yes")
	if r.Code != 1 || !strings.Contains(r.Stderr, "checksum mismatch") {
		t.Fatalf("exit %d\n%s", r.Code, r.Stderr)
	}
	if v := u.version(); v != "0.0.1" {
		t.Errorf("version = %s: a failed verification must change nothing", v)
	}
	if _, err := os.Stat(u.bin + ".old"); err == nil {
		t.Error("a backup exists although nothing was replaced")
	}
}

// The e2e binary runs with stdin, stdout and stderr redirected, so it is
// never "somebody looking": even mode = "install" must not contact the server
// or swap the binary (scripts, cron jobs and pipelines use "ccshelf update
// --yes"). The positive case needs a terminal and is covered by the unit
// tests of internal/cli/updatecmd with a faked terminal.
func TestUpdateAutoInstallNeedsATerminalAndHonorsCIAndKillSwitch(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no terminal": nil,
		"CI":          {"CI": "1"},
		"kill switch": {"CCSHELF_NO_UPDATE_CHECK": "1"},
	} {
		t.Run(name, func(t *testing.T) {
			u := newUpdateEnv(t)
			write(t, filepath.Join(u.ConfigDir(), "config.toml"), "[update]\nmode = \"install\"\nbase_url = \""+u.srv.URL()+"\"\n")
			for k, v := range env {
				u.Setenv(k, v)
			}
			r := u.mustCcshelf("ls")
			if got := u.version(); got != "0.0.1" {
				t.Errorf("version = %s, want 0.0.1 (nothing may be installed)\nstderr: %s", got, r.Stderr)
			}
			if len(u.srv.Hits()) != 0 {
				t.Errorf("the update check contacted the server: %v", u.srv.Hits())
			}
			if strings.Contains(r.Stderr, "updated") {
				t.Errorf("stderr mentions an update: %s", r.Stderr)
			}
			if _, err := os.Stat(u.bin + ".old"); err == nil {
				t.Error("a backup exists: the binary was replaced")
			}
			if _, err := os.Stat(filepath.Join(u.CacheDir(), "update-state.json")); err == nil {
				t.Error("a state file was written although nothing was checked")
			}
		})
	}
}

func TestUpdateDefaultsToNoNetwork(t *testing.T) {
	u := newUpdateEnv(t)
	// The config has [update] with a base_url but no mode, and the default mode
	// is off. Ordinary commands never contact the server.
	for _, args := range [][]string{{"ls"}, {"version"}, {"doctor", "--help"}} {
		u.mustCcshelf(args...)
	}
	if hits := u.srv.Hits(); len(hits) != 0 {
		t.Errorf("requests with mode off: %v", hits)
	}
	if _, err := os.Stat(filepath.Join(u.CacheDir(), "update-state.json")); err == nil {
		t.Error("a state file was written with mode off")
	}
}

// TestUpdateRejectsPlainHTTPInProduction runs the binary the other e2e tests
// use, which is built WITHOUT the e2eloopback tag like a release build: an
// http base URL must be refused by the configuration check.
func TestUpdateRejectsPlainHTTPInProduction(t *testing.T) {
	s := newSandbox(t)
	write(t, filepath.Join(s.ConfigDir(), "config.toml"), "[update]\nbase_url = \"http://127.0.0.1:9\"\n")
	r := s.run("ls")
	if r.Code == 0 || !strings.Contains(r.Stderr, "update.base_url") || !strings.Contains(r.Stderr, "https") {
		t.Errorf("exit %d\nstderr: %s", r.Code, r.Stderr)
	}
	for _, bad := range []string{"https://user:pw@ghe.example.com", "file:///tmp", "ftp://x.example.com"} {
		write(t, filepath.Join(s.ConfigDir(), "config.toml"), fmt.Sprintf("[update]\nbase_url = %q\n", bad))
		if r := s.run("ls"); r.Code == 0 {
			t.Errorf("%s accepted", bad)
		}
	}
}

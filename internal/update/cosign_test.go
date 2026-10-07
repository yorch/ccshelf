package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLookPath(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	mk := func(dir, name string, mode os.FileMode) string {
		return writeExe(t, dir, name, "x", mode)
	}
	if runtime.GOOS != "windows" {
		first := mk(a, "cosign", 0o755)
		mk(b, "cosign", 0o755)
		if got, ok := LookPath(a+string(os.PathListSeparator)+b, "", "linux", "cosign"); !ok || got != first {
			t.Errorf("first match = %q %v, want %q", got, ok, first)
		}
		if _, ok := LookPath(b+string(os.PathListSeparator)+a, "", "linux", "nothing"); ok {
			t.Error("found a tool that does not exist")
		}
		// Not executable: skipped.
		c := t.TempDir()
		mk(c, "cosign", 0o644)
		if _, ok := LookPath(c, "", "linux", "cosign"); ok {
			t.Error("a non-executable file must not be found")
		}
		if got, ok := LookPath(c+string(os.PathListSeparator)+b, "", "linux", "cosign"); !ok || filepath.Dir(got) != b {
			t.Errorf("skipping a non-executable = %q %v", got, ok)
		}
		// A directory called cosign is not a tool.
		d := t.TempDir()
		if err := os.Mkdir(filepath.Join(d, "cosign"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, ok := LookPath(d, "", "linux", "cosign"); ok {
			t.Error("a directory must not be found")
		}
	}
	// Relative and empty entries never count, even when a file is there.
	cwd, _ := os.Getwd()
	rel := t.TempDir()
	mk(rel, "cosign", 0o755)
	t.Chdir(rel)
	for _, p := range []string{"", ".", ":", "." + string(os.PathListSeparator), "bin", "../" + filepath.Base(rel)} {
		if got, ok := LookPath(p, "", "linux", "cosign"); ok {
			t.Errorf("PATH %q found %q in the working directory", p, got)
		}
	}
	_ = cwd
	// Windows: only .exe and .com, from PATHEXT.
	w := t.TempDir()
	mk(w, "cosign.cmd", 0o755)
	mk(w, "cosign.exe", 0o755)
	if got, ok := LookPath(w, ".COM;.EXE;.BAT;.CMD", "windows", "cosign"); !ok || filepath.Base(got) != "cosign.exe" {
		t.Errorf("windows lookup = %q %v", got, ok)
	}
	w2 := t.TempDir()
	mk(w2, "cosign.cmd", 0o755)
	mk(w2, "cosign.bat", 0o755)
	if got, ok := LookPath(w2, ".BAT;.CMD", "windows", "cosign"); ok {
		t.Errorf("a batch file must not stand in for cosign: %q", got)
	}
	if _, ok := LookPath(w, "", "windows", "cosign"); !ok {
		t.Error("an empty PATHEXT defaults to .exe")
	}
}

func TestScrubEnv(t *testing.T) {
	in := []string{
		"PATH=/bin", "HOME=/h", "HTTPS_PROXY=http://p", "SSL_CERT_FILE=/c", "TUF_ROOT=/t",
		"GITHUB_TOKEN=ghp_secret", "ANTHROPIC_API_KEY=sk-x", "CLAUDE_CONFIG_DIR=/c", "AWS_SECRET_ACCESS_KEY=x",
		"SIGSTORE_ID_TOKEN=x", "malformed",
	}
	got := strings.Join(scrubEnv(in), " ")
	for _, want := range []string{"PATH=/bin", "HOME=/h", "HTTPS_PROXY=http://p", "SSL_CERT_FILE=/c", "TUF_ROOT=/t"} {
		if !strings.Contains(got, want) {
			t.Errorf("scrubbed environment lost %s: %s", want, got)
		}
	}
	for _, bad := range []string{"GITHUB_TOKEN", "ANTHROPIC", "CLAUDE_", "AWS_", "SIGSTORE", "malformed"} {
		if strings.Contains(got, bad) {
			t.Errorf("scrubbed environment still has %s: %s", bad, got)
		}
	}
}

func TestVerifyCosign(t *testing.T) {
	dir := t.TempDir()
	sums := filepath.Join(dir, "checksums.txt")
	bundle := filepath.Join(dir, "checksums.txt.sigstore.json")
	writeExe(t, dir, "checksums.txt", "the sums", 0o600)
	writeExe(t, dir, "checksums.txt.sigstore.json", "{}", 0o600)
	id := "https://github.com/yorch/ccshelf/.github/workflows/release.yml@refs/tags/v0.2.0"
	good := fakeCosignConfig{Identity: id, Issuer: OIDCIssuer, SumsSHA256: sha256Hex([]byte("the sums"))}
	environ := []string{"PATH=" + os.Getenv("PATH"), "GITHUB_TOKEN=ghp_secret", "HOME=/h"}

	run := func(cfg fakeCosignConfig, identity, issuer string) (string, error) {
		cd := setFakeCosign(t, cfg)
		err := VerifyCosign(context.Background(), cosignPath(cd), environ, bundle, sums, identity, issuer)
		args, _ := os.ReadFile(filepath.Join(cd, "cosign.args"))
		return string(args), err
	}

	args, err := run(good, id, OIDCIssuer)
	if err != nil {
		t.Fatalf("good signature: %v", err)
	}
	for _, want := range []string{"verify-blob", "--bundle\n" + bundle, "--certificate-identity\n" + id, "--certificate-oidc-issuer\n" + OIDCIssuer, "\n" + sums + "\nENV:"} {
		if !strings.Contains(args, want) {
			t.Errorf("cosign was not called with %q:\n%s", want, args)
		}
	}
	if strings.Contains(args, "certificate-identity-regexp") {
		t.Error("the identity must be pinned exactly, never by regular expression")
	}
	if strings.Contains(args, "GITHUB_TOKEN") || strings.Contains(args, "ghp_secret") {
		t.Error("cosign must run with a scrubbed environment")
	}

	if _, err := run(good, id+"x", OIDCIssuer); !errors.Is(err, ErrSignature) || !strings.Contains(err.Error(), "identit") {
		t.Errorf("wrong identity: %v", err)
	}
	if _, err := run(good, id, "https://evil.example.com"); !errors.Is(err, ErrSignature) {
		t.Errorf("wrong issuer: %v", err)
	}
	tampered := good
	tampered.SumsSHA256 = sha256Hex([]byte("other sums"))
	if _, err := run(tampered, id, OIDCIssuer); !errors.Is(err, ErrSignature) || !strings.Contains(err.Error(), "signature") {
		t.Errorf("tampered checksums: %v", err)
	}
	failing := good
	failing.Fail = true
	if _, err := run(failing, id, OIDCIssuer); !errors.Is(err, ErrSignature) {
		t.Errorf("failing cosign: %v", err)
	}
	if err := VerifyCosign(context.Background(), filepath.Join(dir, "no-such-cosign"), environ, bundle, sums, id, OIDCIssuer); !errors.Is(err, ErrSignature) {
		t.Errorf("an unrunnable cosign must fail closed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cd := setFakeCosign(t, good)
	if err := VerifyCosign(ctx, cosignPath(cd), environ, bundle, sums, id, OIDCIssuer); err == nil {
		t.Error("a canceled context must fail the verification")
	}
}

func TestCapWriter(t *testing.T) {
	var w capWriter
	big := strings.Repeat("x", 10000)
	n, err := w.Write([]byte(big))
	if n != len(big) || err != nil {
		t.Errorf("Write = %d %v: it must accept everything so a child never blocks", n, err)
	}
	if len(w.String()) != capWriterMax {
		t.Errorf("kept %d bytes, want %d", len(w.String()), capWriterMax)
	}
	_, _ = w.Write([]byte("more"))
	if len(w.String()) != capWriterMax {
		t.Error("the cap must hold across writes")
	}
}

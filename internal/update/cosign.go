package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CosignTimeout bounds one cosign run.
const CosignTimeout = 90 * time.Second

// ErrSignature means the keyless signature of checksums.txt did not verify.
var ErrSignature = errors.New("signature verification failed")

// LookPath finds name in the directories of pathEnv, the value of PATH. Only
// absolute directories are searched (an empty or relative entry such as "."
// would let a file in the working directory stand in for a tool), the match
// must be a regular file, and on Unix it must be executable. On Windows the
// extensions of pathext (default ".exe") are tried.
func LookPath(pathEnv, pathext, goos, name string) (string, bool) {
	exts := []string{""}
	if goos == "windows" {
		exts = nil
		for _, e := range strings.Split(strings.ToLower(pathext), ";") {
			if e == ".exe" || e == ".com" {
				exts = append(exts, e)
			}
		}
		if len(exts) == 0 {
			exts = []string{".exe"}
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		for _, e := range exts {
			p := filepath.Join(dir, name+e)
			fi, err := os.Stat(p)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			if goos != "windows" && fi.Mode().Perm()&0o111 == 0 {
				continue
			}
			return p, true
		}
	}
	return "", false
}

// scrubEnv keeps only the variables cosign needs to reach the network and find
// its cache: the user's own PATH, home and temp locations, proxy and CA
// settings. Everything else (tokens, ANTHROPIC_*, CLAUDE_*) is not passed on.
func scrubEnv(environ []string) []string {
	keep := map[string]bool{
		"PATH": true, "HOME": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true,
		"XDG_CACHE_HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
		"TMPDIR": true, "TEMP": true, "TMP": true, "SYSTEMROOT": true, "SystemRoot": true, "WINDIR": true,
		"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "http_proxy": true, "https_proxy": true, "no_proxy": true,
		"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "TUF_ROOT": true, "LANG": true,
	}
	var out []string
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if ok && keep[k] {
			out = append(out, kv)
		}
	}
	return out
}

// VerifyCosign runs "cosign verify-blob" for the keyless bundle of
// checksumsPath. cosign is the binary at cosignPath (found on PATH by the
// caller). The certificate identity and issuer are pinned exactly; there is no
// regular expression and no way to turn the check off from here.
func VerifyCosign(ctx context.Context, cosignPath string, environ []string, bundlePath, checksumsPath, identity, issuer string) error {
	ctx, cancel := context.WithTimeout(ctx, CosignTimeout)
	defer cancel()
	args := []string{
		"verify-blob",
		"--bundle", bundlePath,
		"--certificate-identity", identity,
		"--certificate-oidc-issuer", issuer,
		checksumsPath,
	}
	cmd := exec.CommandContext(ctx, cosignPath, args...) //nolint:gosec // cosignPath is the PATH lookup of "cosign"; args are fixed and never go through a shell
	cmd.Env = scrubEnv(environ)
	var out capWriter
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		msg := strings.Join(strings.Fields(out.String()), " ")
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%w: cosign: %s", ErrSignature, msg)
	}
	return nil
}

// capWriter keeps the first 4 KiB written to it and silently drops the rest,
// so a chatty tool can neither block on a full pipe nor fill memory.
type capWriter struct{ b []byte }

const capWriterMax = 4096

func (w *capWriter) Write(p []byte) (int, error) {
	if room := capWriterMax - len(w.b); room > 0 {
		w.b = append(w.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (w *capWriter) String() string { return string(w.b) }

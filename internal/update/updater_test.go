package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// release publishes a standard release and makes it the latest.
func (fx *fixture) release(tag string, signed bool) *relSpec {
	fx.t.Helper()
	r := fx.srv.add(standardRelease(fx.t, tag, signed))
	fx.srv.latest = tag
	return r
}

func (fx *fixture) apply(req Request) (*Result, error) {
	fx.t.Helper()
	p, err := fx.u.Discover(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return fx.u.Apply(context.Background(), p, req, nil)
}

func (fx *fixture) useCosign(cfg fakeCosignConfig) {
	fx.t.Helper()
	dir := setFakeCosign(fx.t, cfg)
	fx.cosign = dir
	fx.u.LookCosign = func() (string, bool) { return cosignPath(dir), true }
	fx.u.VerifyCosign = nil
}

func (fx *fixture) noLeftovers() {
	fx.t.Helper()
	entries, _ := os.ReadDir(fx.binDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) {
			fx.t.Errorf("leftover temporary file %s next to the binary", e.Name())
		}
	}
	entries, _ = os.ReadDir(fx.state)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "update-") && e.IsDir() {
			fx.t.Errorf("leftover download directory %s", e.Name())
		}
		if e.Name() == LockName {
			fx.t.Error("the lock was not released")
		}
	}
}

func mustKind(t *testing.T, err error, want Kind) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Kind != want {
		t.Fatalf("error = %v (kind %v), want kind %v", err, KindOf(err), want)
	}
	return e
}

func TestApplySuccess(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	var steps []string
	p, err := fx.u.Discover(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	if p.UpToDate || p.Downgrade || p.Dev || p.Target.Tag != "v0.2.0" || p.Current.String() != "0.1.0" || p.Exe != fx.exe || !p.Method.SelfUpdatable() {
		t.Fatalf("plan = %+v", p)
	}
	res, err := fx.u.Apply(context.Background(), p, Request{}, func(s string) { steps = append(steps, s) })
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "0.1.0" || res.To != "0.2.0" || res.Exe != fx.exe || res.Backup != fx.exe+".old" || res.Signature != "none" {
		t.Errorf("result = %+v", res)
	}
	if readFile(t, fx.exe) != string(fakeBinary("0.2.0")) {
		t.Errorf("exe = %q", readFile(t, fx.exe))
	}
	if readFile(t, fx.exe+".old") != string(fakeBinary("0.1.0")) {
		t.Errorf("backup = %q", readFile(t, fx.exe+".old"))
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(fx.exe); fi.Mode().Perm() != 0o755 {
			t.Errorf("mode = %v", fi.Mode().Perm())
		}
	}
	if len(steps) < 4 {
		t.Errorf("progress = %v", steps)
	}
	fx.noLeftovers()
	// Every request carried the User-Agent and nothing else identifying.
	for _, h := range fx.srv.headers {
		if h.Get("User-Agent") != "ccshelf/0.1.0" || h.Get("Authorization") != "" || h.Get("Cookie") != "" {
			t.Errorf("headers = %v", h)
		}
	}
}

func TestApplyUpToDate(t *testing.T) {
	fx := newFixture(t, "0.2.0")
	fx.release("v0.2.0", false)
	p, err := fx.u.Discover(context.Background(), Request{})
	if err != nil || !p.UpToDate {
		t.Fatalf("plan = %+v %v", p, err)
	}
	// An older newest release (this build is a pre-release ahead of it) is
	// "up to date" for the plain command, but installing it is a downgrade.
	fx2 := newFixture(t, "0.3.0-rc.1")
	fx2.release("v0.2.0", false)
	if p, err := fx2.u.Discover(context.Background(), Request{}); err != nil || !p.UpToDate || !p.Downgrade {
		t.Errorf("pre-release ahead of latest: %+v %v", p, err)
	}
	// Force reinstalls the same version.
	res, err := fx.apply(Request{Force: true})
	if err != nil || res.To != "0.2.0" {
		t.Errorf("forced reinstall = %+v %v", res, err)
	}
}

// --force never implies a downgrade: an older "latest" (a rolled-back
// release, or a mirror that lags) is refused unless --allow-downgrade is given.
func TestForceDoesNotDowngradeToOlderLatest(t *testing.T) {
	fx := newFixture(t, "0.3.0")
	fx.release("v0.2.0", false)
	for _, req := range []Request{{Force: true}, {}} {
		_, err := fx.apply(req)
		e := mustKind(t, err, KindUsage)
		if !strings.Contains(e.Hint(), "--allow-downgrade") {
			t.Errorf("%+v: hint = %q", req, e.Hint())
		}
		if readFile(t, fx.exe) != string(fakeBinary("0.3.0")) {
			t.Fatalf("%+v: a refused downgrade must change nothing", req)
		}
	}
	res, err := fx.apply(Request{Force: true, AllowDowngrade: true})
	if err != nil || res.To != "0.2.0" || readFile(t, fx.exe) != string(fakeBinary("0.2.0")) {
		t.Fatalf("--force --allow-downgrade = %+v %v", res, err)
	}
}

func TestDiscoverVersionSelection(t *testing.T) {
	fx := newFixture(t, "0.2.0")
	fx.release("v0.1.0", false)
	fx.release("v0.2.0", false)
	fx.release("v0.3.0-rc.1", false)
	fx.release("v1.0.0", false)
	fx.srv.latest = "v1.0.0"
	ctx := context.Background()

	p, err := fx.u.Discover(ctx, Request{})
	if err != nil || p.Target.Tag != "v1.0.0" || p.UpToDate {
		t.Errorf("newer major offered manually: %+v %v", p, err)
	}
	p, err = fx.u.Discover(ctx, Request{Version: "v0.1.0"})
	if err != nil || !p.Downgrade || p.UpToDate {
		t.Errorf("explicit older version: %+v %v", p, err)
	}
	p, err = fx.u.Discover(ctx, Request{Version: "0.2.0"})
	if err != nil || !p.UpToDate || p.Downgrade {
		t.Errorf("explicit same version: %+v %v", p, err)
	}
	p, err = fx.u.Discover(ctx, Request{Version: "v0.3.0-rc.1"})
	if err != nil || p.UpToDate || p.Target.Tag != "v0.3.0-rc.1" {
		t.Errorf("explicit pre-release: %+v %v", p, err)
	}
	p, err = fx.u.Discover(ctx, Request{Prerelease: true})
	if err != nil || p.Target.Tag != "v1.0.0" {
		t.Errorf("prerelease search picks the highest version: %+v %v", p, err)
	}
	_, err = fx.u.Discover(ctx, Request{Version: "banana"})
	mustKind(t, err, KindUsage)
	_, err = fx.u.Discover(ctx, Request{Version: "v9.9.9"})
	e := mustKind(t, err, KindFailure)
	if !strings.Contains(e.Msg, "v9.9.9") {
		t.Errorf("message = %q", e.Msg)
	}
}

func TestDowngrade(t *testing.T) {
	fx := newFixture(t, "0.2.0")
	fx.release("v0.1.0", false)
	fx.release("v0.2.0", false)
	_, err := fx.apply(Request{Version: "v0.1.0"})
	e := mustKind(t, err, KindUsage)
	if !strings.Contains(e.Hint(), "--allow-downgrade") {
		t.Errorf("hint = %q", e.Hint())
	}
	if readFile(t, fx.exe) != string(fakeBinary("0.2.0")) {
		t.Error("a refused downgrade must change nothing")
	}
	res, err := fx.apply(Request{Version: "v0.1.0", AllowDowngrade: true})
	if err != nil || res.To != "0.1.0" || readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
		t.Fatalf("allowed downgrade = %+v %v", res, err)
	}
}

func TestChecksumMismatch(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	r := fx.release("v0.2.0", false)
	ar, _ := ArchiveFor(Version{Major: 0, Minor: 2}, runtime.GOOS, runtime.GOARCH)
	r.assets[ChecksumsName] = []byte(strings.Repeat("0", 64) + "  " + ar.Name + "\n")
	_, err := fx.apply(Request{})
	e := mustKind(t, err, KindVerify)
	if !errors.Is(err, ErrChecksum) {
		t.Errorf("error = %v", e)
	}
	if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
		t.Error("a checksum mismatch must change nothing")
	}
	if _, err := os.Stat(fx.exe + ".old"); !os.IsNotExist(err) {
		t.Error("no backup may be made when nothing was replaced")
	}
	fx.noLeftovers()
}

func TestChecksumFileProblems(t *testing.T) {
	for name, content := range map[string]func(ar Archive, sum string) string{
		"no entry":  func(Archive, string) string { return strings.Repeat("a", 64) + "  other.tar.gz\n" },
		"duplicate": func(ar Archive, sum string) string { return sum + "  " + ar.Name + "\n" + sum + "  " + ar.Name + "\n" },
		"garbage":   func(Archive, string) string { return "not a checksum file" },
	} {
		fx := newFixture(t, "0.1.0")
		r := fx.release("v0.2.0", false)
		ar, _ := ArchiveFor(Version{Minor: 2}, runtime.GOOS, runtime.GOARCH)
		r.assets[ChecksumsName] = []byte(content(ar, sha256Hex(r.assets[ar.Name])))
		_, err := fx.apply(Request{})
		mustKind(t, err, KindVerify)
		if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
			t.Errorf("%s: the binary changed", name)
		}
	}
}

func TestTruncatedAndOversizeDownloads(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	r := fx.release("v0.2.0", false)
	ar, _ := ArchiveFor(Version{Minor: 2}, runtime.GOOS, runtime.GOARCH)
	full := r.assets[ar.Name]

	// 1. The connection ends before the declared length.
	fx.srv.handler = func(w http.ResponseWriter, req *http.Request) bool {
		if !strings.HasSuffix(req.URL.Path, ar.Name) {
			return false
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(full)))
		_, _ = w.Write(full[:len(full)/2])
		hj, _, _ := w.(http.Hijacker).Hijack()
		hj.Close()
		return true
	}
	_, err := fx.apply(Request{})
	mustKind(t, err, KindFailure)
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("truncated: %v", err)
	}

	// 2. A server that quietly sends fewer bytes than the release lists.
	fx.srv.handler = func(w http.ResponseWriter, req *http.Request) bool {
		if !strings.HasSuffix(req.URL.Path, ar.Name) {
			return false
		}
		_, _ = w.Write(full[:len(full)-1])
		return true
	}
	if _, err = fx.apply(Request{}); !errors.Is(err, ErrTruncated) {
		t.Errorf("shorter than listed: %v", err)
	}

	// 3. An archive declared bigger than the limit is refused before reading.
	fx.srv.handler = func(w http.ResponseWriter, req *http.Request) bool {
		if !strings.HasSuffix(req.URL.Path, ar.Name) {
			return false
		}
		w.Header().Set("Content-Length", fmt.Sprint(MaxArchiveBytes+1))
		w.WriteHeader(200)
		hj, _, _ := w.(http.Hijacker).Hijack()
		hj.Close()
		return true
	}
	if _, err = fx.apply(Request{}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversize: %v", err)
	}
	if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
		t.Error("a failed download must change nothing")
	}
	fx.noLeftovers()
}

func TestRedirectToAnotherHostFails(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	var contacted bool
	evil := newFakeServer(t)
	evil.handler = func(w http.ResponseWriter, _ *http.Request) bool {
		contacted = true
		fmt.Fprint(w, "evil")
		return true
	}
	fx.srv.handler = func(w http.ResponseWriter, req *http.Request) bool {
		if strings.Contains(req.URL.Path, "/releases/download/") {
			http.Redirect(w, req, evil.URL()+req.URL.Path, http.StatusFound)
			return true
		}
		return false
	}
	// The default client of the fixture would follow it; use the production
	// policy.
	fx.u.Client = NewClient(fx.u.Source, 10*time.Second)
	_, err := fx.apply(Request{})
	if !errors.Is(err, ErrRedirect) {
		t.Fatalf("error = %v, want ErrRedirect", err)
	}
	if contacted {
		t.Error("the redirect target was contacted")
	}
}

func TestPlainHTTPIsRefused(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.u.Source.AllowLoopbackHTTP = false
	_, err := fx.u.Discover(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "not https") {
		t.Errorf("error = %v", err)
	}
	if fx.srv.hitCount() != 0 {
		t.Error("no request may be made over plain http")
	}
}

func TestHostileArchives(t *testing.T) {
	ar, _ := ArchiveFor(Version{Minor: 2}, runtime.GOOS, runtime.GOARCH)
	bin := fakeBinary("0.2.0")
	var bad map[string][]byte
	if ar.Zip {
		bad = map[string][]byte{
			"traversal": makeZip(t, zentry{"../ccshelf.exe", bin, 0o755}),
			"extra":     makeZip(t, zentry{ar.Binary, bin, 0o755}, zentry{"evil.dll", []byte("x"), 0o644}),
			"missing":   makeZip(t, zentry{"LICENSE", []byte("x"), 0o644}),
			"symlink":   makeZip(t, zentry{ar.Binary, bin, 0o755}, zentry{"LICENSE", []byte("/etc/passwd"), os.ModeSymlink | 0o777}),
		}
	} else {
		bad = map[string][]byte{
			"traversal": makeTarGz(t, entry{name: "../ccshelf", body: bin}),
			"extra":     makeTarGz(t, entry{name: "ccshelf", body: bin}, entry{name: "install.sh", body: []byte("x")}),
			"missing":   makeTarGz(t, entry{name: "LICENSE", body: []byte("x")}),
			"symlink":   makeTarGz(t, entry{name: "ccshelf", body: bin}, entry{name: "LICENSE", typ: '2', link: "/etc/passwd"}),
			"hardlink":  makeTarGz(t, entry{name: "ccshelf", body: bin}, entry{name: "README.md", typ: '1', link: "ccshelf"}),
			"not gzip":  []byte("plain"),
		}
	}
	for name, content := range bad {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t, "0.1.0")
			r := fx.release("v0.2.0", false)
			// The checksum is right: the archive itself is the problem.
			r.assets[ar.Name] = content
			r.assets[ChecksumsName] = []byte(checksumsFor(map[string][]byte{ar.Name: content}))
			_, err := fx.apply(Request{})
			mustKind(t, err, KindVerify)
			if !errors.Is(err, ErrArchive) {
				t.Errorf("error = %v, want ErrArchive", err)
			}
			if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
				t.Error("the binary changed")
			}
			fx.noLeftovers()
		})
	}
}

func TestNewBinaryMustReportTheExpectedVersion(t *testing.T) {
	for name, run := range map[string]func(context.Context, string, []string) (string, error){
		"wrong version": func(context.Context, string, []string) (string, error) { return "0.9.9", nil },
		"unparsable":    func(context.Context, string, []string) (string, error) { return "banana", nil },
		"cannot run":    func(context.Context, string, []string) (string, error) { return "", errors.New("exec format error") },
		"not ccshelf":   func(context.Context, string, []string) (string, error) { return "", ErrNotCcshelf },
	} {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.2.0", false)
		fx.u.RunVersion = run
		_, err := fx.apply(Request{})
		mustKind(t, err, KindVerify)
		if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
			t.Errorf("%s: the binary changed", name)
		}
		fx.noLeftovers()
	}
}

func TestReleaseWithoutAnAssetForThisPlatform(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	r := fx.release("v0.2.0", false)
	ar, _ := ArchiveFor(Version{Minor: 2}, runtime.GOOS, runtime.GOARCH)
	delete(r.assets, ar.Name)
	_, err := fx.apply(Request{})
	e := mustKind(t, err, KindFailure)
	if !strings.Contains(e.Msg, ar.Name) {
		t.Errorf("message = %q", e.Msg)
	}
	r2 := fx.release("v0.3.0", false)
	delete(r2.assets, ChecksumsName)
	_, err = fx.apply(Request{})
	mustKind(t, err, KindFailure)
}

func TestUnsupportedPlatform(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	fx.u.GOOS, fx.u.GOARCH = "plan9", "mips"
	_, err := fx.u.Discover(context.Background(), Request{})
	mustKind(t, err, KindFailure)
}

func TestReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix permissions and a non-root user")
	}
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	if err := os.Chmod(fx.binDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(fx.binDir, 0o700) })
	before := fx.srv.hitCount()
	p, err := fx.u.Discover(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	discoverHits := fx.srv.hitCount() - before
	_, err = fx.u.Apply(context.Background(), p, Request{}, nil)
	mustKind(t, err, KindNotWritable)
	var nw *NotWritableError
	if !errors.As(err, &nw) || nw.Dir != fx.binDir {
		t.Errorf("error = %v, want a NotWritableError for %s", err, fx.binDir)
	}
	if fx.srv.hitCount()-before != discoverHits {
		t.Error("nothing may be downloaded when the directory is not writable")
	}
}

func TestPackageManagedAndDevBuilds(t *testing.T) {
	t.Run("homebrew", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.2.0", false)
		cellar := filepath.Join(t.TempDir(), "Cellar", "ccshelf", "0.1.0", "bin")
		if err := os.MkdirAll(cellar, 0o755); err != nil {
			t.Fatal(err)
		}
		exe := writeExe(t, cellar, "ccshelf", string(fakeBinary("0.1.0")), 0o755)
		fx.u.Executable = func() (string, error) { return exe, nil }
		fx.u.GOOS = runtime.GOOS
		// The Cellar rule only applies on darwin and linux; drive the detector
		// with those even on a Windows runner.
		if runtime.GOOS == "windows" {
			t.Skip("the Homebrew rule is for darwin and linux")
		}
		_, err := fx.apply(Request{})
		e := mustKind(t, err, KindManaged)
		if !strings.Contains(e.Hint(), "brew upgrade ccshelf") || !strings.Contains(e.Hint(), "--force") {
			t.Errorf("hint = %q", e.Hint())
		}
		if readFile(t, exe) != string(fakeBinary("0.1.0")) {
			t.Error("the binary changed")
		}
		// --force replaces it anyway.
		if _, err := fx.apply(Request{Force: true}); err != nil {
			t.Errorf("forced: %v", err)
		}
		if readFile(t, exe) != string(fakeBinary("0.2.0")) {
			t.Error("--force did not replace the binary")
		}
	})
	t.Run("dev build", func(t *testing.T) {
		for _, cur := range []string{"dev", "0.1.0+dirty", "v0.0.0-20260101120000-abcdef123456", ""} {
			fx := newFixture(t, "0.1.0")
			fx.u.Current = cur
			fx.release("v0.2.0", false)
			p, err := fx.u.Discover(context.Background(), Request{})
			if err != nil || !p.Dev || p.UpToDate {
				t.Fatalf("%q: plan = %+v %v", cur, p, err)
			}
			_, err = fx.u.Apply(context.Background(), p, Request{}, nil)
			mustKind(t, err, KindManaged)
			if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
				t.Errorf("%q: the binary changed", cur)
			}
			if _, err := fx.u.Apply(context.Background(), p, Request{Force: true}, nil); err != nil {
				t.Errorf("%q forced: %v", cur, err)
			}
		}
	})
}

func TestSymlinkedExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "ccshelf")
	if err := os.Symlink(fx.exe, link); err != nil {
		t.Fatal(err)
	}
	fx.u.Executable = func() (string, error) { return link, nil }
	res, err := fx.apply(Request{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Exe != fx.exe {
		t.Errorf("replaced %s, want the link target %s", res.Exe, fx.exe)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink itself must stay a symlink")
	}
	if readFile(t, link) != string(fakeBinary("0.2.0")) {
		t.Error("the link does not lead to the new binary")
	}
}

func TestRefusedSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix ownership")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	link := filepath.Join(t.TempDir(), "ccshelf")
	if err := os.Symlink("/bin/sh", link); err != nil {
		t.Fatal(err)
	}
	fx.u.Executable = func() (string, error) { return link, nil }
	_, err := fx.apply(Request{Force: true})
	if err == nil || !strings.Contains(err.Error(), "cannot replace") {
		t.Errorf("error = %v, want a refusal", err)
	}
}

func TestRollback(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	ctx := context.Background()

	if _, err := fx.u.PrepareRollback(ctx); err == nil {
		t.Fatal("no backup yet")
	} else {
		mustKind(t, err, KindNoBackup)
	}
	if _, err := fx.apply(Request{}); err != nil {
		t.Fatal(err)
	}
	fx.u.Current = "0.2.0"
	rp, err := fx.u.PrepareRollback(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rp.Verified || rp.BackupVersion != "0.1.0" || rp.Exe != fx.exe || rp.Backup != fx.exe+".old" {
		t.Fatalf("rollback plan = %+v", rp)
	}
	if err := fx.u.GuardRollback(rp, false); err != nil {
		t.Errorf("guard: %v", err)
	}
	if err := fx.u.Rollback(rp); err != nil {
		t.Fatal(err)
	}
	if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) || readFile(t, fx.exe+".old") != string(fakeBinary("0.2.0")) {
		t.Errorf("after rollback exe=%q backup=%q", readFile(t, fx.exe), readFile(t, fx.exe+".old"))
	}
	fx.noLeftovers()
	// And back again.
	rp, _ = fx.u.PrepareRollback(ctx)
	if err := fx.u.Rollback(rp); err != nil || readFile(t, fx.exe) != string(fakeBinary("0.2.0")) {
		t.Errorf("rolling forward: %v %q", err, readFile(t, fx.exe))
	}
}

func TestRollbackVerification(t *testing.T) {
	ctx := context.Background()
	fx := newFixture(t, "0.2.0")
	writeExe(t, fx.binDir, filepath.Base(fx.exe)+".old", "i am not ccshelf", 0o755)
	_, err := fx.u.PrepareRollback(ctx)
	mustKind(t, err, KindVerify)
	if readFile(t, fx.exe) != string(fakeBinary("0.2.0")) {
		t.Error("a refused rollback changed the binary")
	}

	// A backup that cannot be run (another platform, damaged) is returned
	// unverified so that the command can insist on --yes.
	fx.u.RunVersion = func(_ context.Context, path string, _ []string) (string, error) {
		if strings.HasSuffix(path, ".old") {
			return "", errors.New("exec format error")
		}
		return "0.2.0", nil
	}
	rp, err := fx.u.PrepareRollback(ctx)
	if err != nil || rp.Verified || rp.VerifyErr == nil {
		t.Fatalf("rollback plan = %+v %v", rp, err)
	}

	// A backup that is a directory or a symlink is not a backup.
	fx2 := newFixture(t, "0.2.0")
	if err := os.Mkdir(fx2.exe+".old", 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = fx2.u.PrepareRollback(ctx)
	mustKind(t, err, KindNoBackup)

	// A package-managed install refuses unless forced.
	rp = &RollbackPlan{Method: Method{Kind: MethodHomebrew, Command: "brew upgrade ccshelf"}}
	e := mustKind(t, fx.u.GuardRollback(rp, false), KindManaged)
	if !strings.Contains(e.Hint(), "brew upgrade ccshelf") {
		t.Errorf("hint = %q", e.Hint())
	}
	if err := fx.u.GuardRollback(rp, true); err != nil {
		t.Errorf("forced guard: %v", err)
	}
}

func TestConcurrentUpdatesNeverReplaceTwice(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	r := fx.release("v0.2.0", false)
	ar, _ := ArchiveFor(Version{Minor: 2}, runtime.GOOS, runtime.GOARCH)
	_ = r
	inDownload := make(chan struct{})
	proceed := make(chan struct{})
	first := true
	fx.srv.handler = func(w http.ResponseWriter, req *http.Request) bool {
		if strings.HasSuffix(req.URL.Path, ar.Name) && first {
			first = false
			close(inDownload)
			<-proceed
		}
		return false
	}
	done := make(chan error, 1)
	go func() {
		_, err := fx.apply(Request{})
		done <- err
	}()
	<-inDownload
	_, err := fx.apply(Request{})
	e := mustKind(t, err, KindLocked)
	if !strings.Contains(e.Hint(), "10 minutes") {
		t.Errorf("hint = %q", e.Hint())
	}
	if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
		t.Error("the second process replaced the binary while the first was working")
	}
	close(proceed)
	if err := <-done; err != nil {
		t.Fatalf("the first update failed: %v", err)
	}
	if readFile(t, fx.exe) != string(fakeBinary("0.2.0")) {
		t.Error("the first update did not complete")
	}
	fx.noLeftovers()
	// A rollback cannot run while an update holds the lock either.
	rel, _ := AcquireLock(fx.state, time.Now)
	defer rel()
	rp := &RollbackPlan{Exe: fx.exe, Backup: fx.exe + ".old"}
	mustKind(t, fx.u.Rollback(rp), KindLocked)
}

func TestCosignPaths(t *testing.T) {
	ident := func(fx *fixture, tag string) string { return fx.u.Source.CosignIdentity(tag) }
	sums := func(r *relSpec) string { return sha256Hex(r.assets[ChecksumsName]) }

	t.Run("verified", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		r := fx.release("v0.2.0", true)
		fx.useCosign(fakeCosignConfig{Identity: ident(fx, "v0.2.0"), Issuer: OIDCIssuer, SumsSHA256: sums(r)})
		res, err := fx.apply(Request{})
		if err != nil || res.Signature != "cosign" {
			t.Fatalf("= %+v %v", res, err)
		}
		args := readFile(t, filepath.Join(fx.cosign, "cosign.args"))
		if !strings.Contains(args, "release.yml@refs/tags/v0.2.0") {
			t.Errorf("identity not pinned to the tag:\n%s", args)
		}
		if strings.Contains(args, "SECRET_TOKEN") {
			t.Error("cosign must not see unrelated environment variables")
		}
	})
	t.Run("identity of another tag is refused", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		r := fx.release("v0.2.0", true)
		fx.useCosign(fakeCosignConfig{Identity: ident(fx, "v0.1.9"), Issuer: OIDCIssuer, SumsSHA256: sums(r)})
		_, err := fx.apply(Request{})
		mustKind(t, err, KindVerify)
		if !errors.Is(err, ErrSignature) || readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
			t.Errorf("err = %v; the binary must be unchanged", err)
		}
		fx.noLeftovers()
	})
	t.Run("signature over other content", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.2.0", true)
		fx.useCosign(fakeCosignConfig{Identity: ident(fx, "v0.2.0"), Issuer: OIDCIssuer, SumsSHA256: sha256Hex([]byte("something else"))})
		_, err := fx.apply(Request{})
		mustKind(t, err, KindVerify)
		if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
			t.Error("the binary changed")
		}
	})
	t.Run("cosign present but no bundle published", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		r := fx.release("v0.2.0", false)
		fx.useCosign(fakeCosignConfig{Identity: ident(fx, "v0.2.0"), Issuer: OIDCIssuer, SumsSHA256: sums(r)})
		_, err := fx.apply(Request{})
		e := mustKind(t, err, KindVerify)
		if !strings.Contains(e.Hint(), "no signature") {
			t.Errorf("hint = %q", e.Hint())
		}
		if readFile(t, fx.exe) != string(fakeBinary("0.1.0")) {
			t.Error("the binary changed")
		}
	})
	t.Run("a good signature does not excuse a bad checksum", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		r := fx.release("v0.2.0", true)
		ar, _ := ArchiveFor(Version{Minor: 2}, runtime.GOOS, runtime.GOARCH)
		r.assets[ChecksumsName] = []byte(strings.Repeat("1", 64) + "  " + ar.Name + "\n")
		fx.useCosign(fakeCosignConfig{Identity: ident(fx, "v0.2.0"), Issuer: OIDCIssuer, SumsSHA256: sums(r)})
		_, err := fx.apply(Request{})
		if !errors.Is(err, ErrChecksum) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("require-signature without cosign", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		fx.release("v0.2.0", true)
		p, err := fx.u.Discover(context.Background(), Request{})
		if err != nil {
			t.Fatal(err)
		}
		before := fx.srv.hitCount()
		_, err = fx.u.Apply(context.Background(), p, Request{RequireSignature: true}, nil)
		e := mustKind(t, err, KindSignatureRequired)
		if !strings.Contains(e.Hint(), "cosign") {
			t.Errorf("hint = %q", e.Hint())
		}
		if fx.srv.hitCount() != before {
			t.Error("nothing may be downloaded before the signature requirement is met")
		}
	})
	t.Run("require-signature with cosign", func(t *testing.T) {
		fx := newFixture(t, "0.1.0")
		r := fx.release("v0.2.0", true)
		fx.useCosign(fakeCosignConfig{Identity: ident(fx, "v0.2.0"), Issuer: OIDCIssuer, SumsSHA256: sums(r)})
		if res, err := fx.apply(Request{RequireSignature: true}); err != nil || res.Signature != "cosign" {
			t.Errorf("= %+v %v", res, err)
		}
	})
}

func TestStaleTempFilesAreCleaned(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.release("v0.2.0", false)
	stale := writeExe(t, fx.binDir, tempPrefix+"crashed.tmp", "x", 0o600)
	old := fx.now.Add(-3 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.apply(Request{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a stale temporary file from a crashed update must be removed")
	}
}

func TestNetworkErrorsHaveHints(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	fx.srv.handler = func(w http.ResponseWriter, _ *http.Request) bool {
		w.WriteHeader(http.StatusForbidden)
		return true
	}
	_, err := fx.u.Discover(context.Background(), Request{})
	e := mustKind(t, err, KindFailure)
	if !strings.Contains(e.Hint(), "limits") {
		t.Errorf("403 hint = %q", e.Hint())
	}
	fx.srv.handler = func(w http.ResponseWriter, _ *http.Request) bool {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	}
	_, err = fx.u.Discover(context.Background(), Request{})
	e = mustKind(t, err, KindFailure)
	if !strings.Contains(e.Hint(), "network") {
		t.Errorf("500 hint = %q", e.Hint())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fx.u.Discover(ctx, Request{})
	mustKind(t, err, KindFailure)
	if KindOf(errors.New("plain")) != KindFailure {
		t.Error("KindOf of a plain error")
	}
}

func TestRunVersionWithTheRealBinary(t *testing.T) {
	ctx := context.Background()
	environ := []string{"PATH=" + os.Getenv("PATH"), "GITHUB_TOKEN=ghp_secret", "HOME=/h"}
	exe := fakeCcshelfDir(t, "0.4.2", "")
	v, err := RunVersion(ctx, exe, environ)
	if err != nil || v != "0.4.2" {
		t.Fatalf("RunVersion = %q %v", v, err)
	}
	env := readFile(t, filepath.Join(filepath.Dir(exe), "env.txt"))
	if !strings.Contains(env, "CCSHELF_NO_UPDATE_CHECK=1") {
		t.Error("the version check must switch the update check off")
	}
	if strings.Contains(env, "GITHUB_TOKEN") {
		t.Error("the version check must run with a scrubbed environment")
	}
	for mode, want := range map[string]error{"garbage": ErrNotCcshelf, "wrongkind": ErrNotCcshelf} {
		exe := fakeCcshelfDir(t, "0.4.2", mode)
		if _, err := RunVersion(ctx, exe, environ); !errors.Is(err, want) {
			t.Errorf("%s: %v, want %v", mode, err, want)
		}
	}
	if _, err := RunVersion(ctx, fakeCcshelfDir(t, "0.4.2", "exit1"), environ); err == nil || errors.Is(err, ErrNotCcshelf) {
		t.Errorf("a failing binary is a run failure, not 'not ccshelf': %v", err)
	}
	if _, err := RunVersion(ctx, filepath.Join(t.TempDir(), "missing"), environ); err == nil {
		t.Error("a missing binary must fail")
	}
}

func TestNewerMajorIsInstallableWhenAsked(t *testing.T) {
	fx := newFixture(t, "0.9.0")
	fx.release("v1.0.0", false)
	res, err := fx.apply(Request{})
	if err != nil || res.To != "1.0.0" {
		t.Fatalf("a manual update across a major version must work: %+v %v", res, err)
	}
	if readFile(t, fx.exe) != string(fakeBinary("1.0.0")) {
		t.Error("not installed")
	}
	// The automatic path never does this on its own.
	fx2 := newFixture(t, "0.9.0")
	fx2.release("v1.0.0", false)
	fx2.u.Auto(context.Background(), install(true), func(string) {})
	if readFile(t, fx2.exe) != string(fakeBinary("0.9.0")) {
		t.Error("the automatic update crossed a major version")
	}
}

package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMain lets the test binary double as a fake cosign: when it is started
// under the name cosign (a copy of the binary, see fakeCosignDir), it behaves
// like "cosign verify-blob" according to cosign.json next to it.
func TestMain(m *testing.M) {
	if dir := os.Getenv(lockHelperEnv); dir != "" {
		os.Exit(lockHelper(dir))
	}
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
	switch base {
	case "cosign":
		os.Exit(runFakeCosign())
	case "ccshelf":
		os.Exit(runFakeCcshelf())
	}
	os.Exit(m.Run())
}

// lockHelperEnv makes the test binary act as a lock-racing child process.
const lockHelperEnv = "CCSHELF_TEST_LOCK_HELPER"

type fakeCosignConfig struct {
	Identity   string `json:"identity"`
	Issuer     string `json:"issuer"`
	SumsSHA256 string `json:"sums_sha256"`
	Fail       bool   `json:"fail"`
}

func runFakeCosign() int {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	var cfg fakeCosignConfig
	b, err := os.ReadFile(filepath.Join(dir, "cosign.json"))
	if err != nil || json.Unmarshal(b, &cfg) != nil {
		fmt.Fprintln(os.Stderr, "fake cosign: no config")
		return 3
	}
	_ = os.WriteFile(filepath.Join(dir, "cosign.args"), []byte(strings.Join(os.Args[1:], "\n")+"\nENV:"+strings.Join(os.Environ(), "\x00")), 0o600)
	args := os.Args[1:]
	get := func(flag string) string {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	if len(args) == 0 || args[0] != "verify-blob" {
		fmt.Fprintln(os.Stderr, "fake cosign: want verify-blob")
		return 4
	}
	if cfg.Fail {
		fmt.Fprintln(os.Stderr, "Error: no matching signatures")
		return 1
	}
	if get("--certificate-identity") != cfg.Identity {
		fmt.Fprintf(os.Stderr, "Error: none of the expected identities matched what was in the certificate, got %q\n", get("--certificate-identity"))
		return 1
	}
	if get("--certificate-oidc-issuer") != cfg.Issuer {
		fmt.Fprintln(os.Stderr, "Error: issuer mismatch")
		return 1
	}
	if _, err := os.Stat(get("--bundle")); err != nil {
		fmt.Fprintln(os.Stderr, "Error: no bundle")
		return 1
	}
	blob, err := os.ReadFile(args[len(args)-1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: no blob")
		return 1
	}
	sum := sha256.Sum256(blob)
	if hex.EncodeToString(sum[:]) != cfg.SumsSHA256 {
		fmt.Fprintln(os.Stderr, "Error: invalid signature when validating ASN.1 encoded signature")
		return 1
	}
	return 0
}

var (
	cosignOnce sync.Once
	cosignDir  string
	cosignErr  error
)

// fakeCosignDir returns a directory holding a fake cosign executable (a copy
// of the test binary), created once per test process.
func fakeCosignDir(t *testing.T) string {
	t.Helper()
	cosignOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ccshelf-fake-cosign-")
		if err != nil {
			cosignErr = err
			return
		}
		cosignDir = dir
		self, err := os.Executable()
		if err != nil {
			cosignErr = err
			return
		}
		src, err := os.ReadFile(self)
		if err != nil {
			cosignErr = err
			return
		}
		name := "cosign"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		cosignErr = os.WriteFile(filepath.Join(dir, name), src, 0o755)
	})
	if cosignErr != nil {
		t.Fatalf("fake cosign: %v", cosignErr)
	}
	return cosignDir
}

func setFakeCosign(t *testing.T, cfg fakeCosignConfig) (dir string) {
	t.Helper()
	dir = fakeCosignDir(t)
	b, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(dir, "cosign.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dir, "cosign.args"))
	return dir
}

func cosignPath(dir string) string {
	n := "cosign"
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return filepath.Join(dir, n)
}

// ---- archives ----------------------------------------------------------

type entry struct {
	name string
	body []byte
	// typ is the tar type flag (default regular); link is the link target.
	typ  byte
	link string
	mode int64
}

func makeTarGz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o755
		}
		h := &tar.Header{Name: e.name, Typeflag: typ, Mode: mode, Linkname: e.link}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type zentry struct {
	name string
	body []byte
	mode os.FileMode
}

func makeZip(t *testing.T, entries ...zentry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// fakeBinary is the content of a fake ccshelf of the given version; the fake
// RunVersion below reads the version back from it.
func fakeBinary(version string) []byte { return []byte("fake-ccshelf-" + version + "\n") }

// runVersionFromContent is a RunVersion seam that reads the version out of a
// fakeBinary.
func runVersionFromContent(_ context.Context, path string, _ []string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := string(b)
	if !strings.HasPrefix(s, "fake-ccshelf-") {
		return "", ErrNotCcshelf
	}
	return strings.TrimSpace(strings.TrimPrefix(s, "fake-ccshelf-")), nil
}

// ---- fake release server -----------------------------------------------

type relSpec struct {
	tag        string
	prerelease bool
	draft      bool
	// assets by name; sizes are reported from the content unless
	// sizeOverride has an entry.
	assets       map[string][]byte
	sizeOverride map[string]int64
}

type fakeServer struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	releases []*relSpec
	latest   string // tag served by /releases/latest
	// handler, when set, runs first; returning true means it answered.
	handler func(w http.ResponseWriter, r *http.Request) bool
	hits    []string
	headers []http.Header
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{t: t}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) URL() string { return f.srv.URL }

func (f *fakeServer) add(r *relSpec) *relSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releases = append(f.releases, r)
	return r
}

func (f *fakeServer) hitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hits)
}

func (f *fakeServer) find(tag string) *relSpec {
	for _, r := range f.releases {
		if r.tag == tag {
			return r
		}
	}
	return nil
}

func (r *relSpec) json() map[string]any {
	var assets []map[string]any
	for name, b := range r.assets {
		size := int64(len(b))
		if o, ok := r.sizeOverride[name]; ok {
			size = o
		}
		assets = append(assets, map[string]any{"name": name, "size": size})
	}
	return map[string]any{"tag_name": r.tag, "draft": r.draft, "prerelease": r.prerelease, "assets": assets}
}

func (f *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.hits = append(f.hits, r.URL.Path)
	f.headers = append(f.headers, r.Header.Clone())
	h := f.handler
	f.mu.Unlock()
	if h != nil && h(w, r) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	const api = "/api/v3/repos/yorch/ccshelf/releases"
	p := r.URL.Path
	writeJSON := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case p == api+"/latest":
		if rel := f.find(f.latest); rel != nil {
			writeJSON(rel.json())
			return
		}
	case p == api:
		var list []map[string]any
		for _, rel := range f.releases {
			list = append(list, rel.json())
		}
		writeJSON(list)
		return
	case strings.HasPrefix(p, api+"/tags/"):
		if rel := f.find(strings.TrimPrefix(p, api+"/tags/")); rel != nil {
			writeJSON(rel.json())
			return
		}
	case strings.HasPrefix(p, "/yorch/ccshelf/releases/download/"):
		parts := strings.SplitN(strings.TrimPrefix(p, "/yorch/ccshelf/releases/download/"), "/", 2)
		if rel := f.find(parts[0]); rel != nil && len(parts) == 2 {
			if b, ok := rel.assets[parts[1]]; ok {
				w.Header().Set("Content-Length", fmt.Sprint(len(b)))
				_, _ = w.Write(b)
				return
			}
		}
	}
	http.NotFound(w, r)
}

// standardRelease builds a release with a valid archive for the running
// platform, a checksums.txt and (optionally) a signature bundle.
func standardRelease(t *testing.T, tag string, signed bool) *relSpec {
	t.Helper()
	v, err := ParseVersion(tag)
	if err != nil {
		t.Fatal(err)
	}
	ar, err := ArchiveFor(v, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("no release archive for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	var arch []byte
	bin := fakeBinary(v.String())
	if ar.Zip {
		arch = makeZip(t, zentry{ar.Binary, bin, 0o755}, zentry{"LICENSE", []byte("MIT"), 0o644}, zentry{"README.md", []byte("hi"), 0o644})
	} else {
		arch = makeTarGz(t, entry{name: ar.Binary, body: bin}, entry{name: "LICENSE", body: []byte("MIT"), mode: 0o644}, entry{name: "README.md", body: []byte("hi"), mode: 0o644})
	}
	r := &relSpec{tag: tag, assets: map[string][]byte{
		ar.Name:       arch,
		ChecksumsName: []byte(checksumsFor(map[string][]byte{ar.Name: arch})),
	}, sizeOverride: map[string]int64{}}
	if v.IsPrerelease() {
		r.prerelease = true
	}
	if signed {
		r.assets[SignatureName] = []byte(`{"fake":"bundle"}`)
	}
	return r
}

func checksumsFor(files map[string][]byte) string {
	var b strings.Builder
	for name, content := range files {
		sum := sha256.Sum256(content)
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return b.String()
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// ---- updater fixture ---------------------------------------------------

type fixture struct {
	t      *testing.T
	srv    *fakeServer
	u      *Updater
	exe    string
	binDir string
	state  string
	now    time.Time
	cosign string // directory of the fake cosign, or ""
}

// newFixture builds an Updater whose running binary is a fake ccshelf of
// version cur, talking to a fake server.
func newFixture(t *testing.T, cur string) *fixture {
	t.Helper()
	srv := newFakeServer(t)
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	binDir := filepath.Join(root, "bin")
	state := filepath.Join(root, "cache")
	for _, d := range []string{binDir, state} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	exe := filepath.Join(binDir, "ccshelf")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := os.WriteFile(exe, fakeBinary(cur), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := NewSource("yorch/ccshelf", srv.URL(), true)
	if err != nil {
		t.Fatal(err)
	}
	fx := &fixture{t: t, srv: srv, exe: exe, binDir: binDir, state: state, now: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)}
	fx.u = &Updater{
		Source:     src,
		Client:     srv.srv.Client(),
		UserAgent:  "ccshelf/" + cur,
		Current:    cur,
		GOOS:       runtime.GOOS,
		GOARCH:     runtime.GOARCH,
		Executable: func() (string, error) { return exe, nil },
		Environ:    func() []string { return []string{"PATH=" + os.Getenv("PATH"), "SECRET_TOKEN=hunter2"} },
		StateDir:   state,
		Now:        func() time.Time { return fx.now },
		LookCosign: func() (string, bool) { return "", false },
		RunVersion: runVersionFromContent,
	}
	return fx
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// runFakeCcshelf is the test binary started as "ccshelf": it answers
// "version --json" from version.txt next to it (mode in mode.txt), and records
// its environment in env.txt.
func runFakeCcshelf() int {
	exe, _ := os.Executable()
	dir := filepath.Dir(exe)
	_ = os.WriteFile(filepath.Join(dir, "env.txt"), []byte(strings.Join(os.Environ(), "\n")), 0o600)
	args := os.Args[1:]
	if len(args) != 2 || args[0] != "version" || args[1] != "--json" {
		return 2
	}
	mode, _ := os.ReadFile(filepath.Join(dir, "mode.txt"))
	ver, _ := os.ReadFile(filepath.Join(dir, "version.txt"))
	switch strings.TrimSpace(string(mode)) {
	case "garbage":
		fmt.Println("hello world")
	case "wrongkind":
		fmt.Printf(`{"version":1,"kind":"other","data":{"version":%q}}`+"\n", strings.TrimSpace(string(ver)))
	case "exit1":
		return 1
	default:
		fmt.Printf(`{"version":1,"kind":"version","data":{"version":%q,"commit":"abc"}}`+"\n", strings.TrimSpace(string(ver)))
	}
	return 0
}

// fakeCcshelfDir returns a directory holding a copy of the test binary named
// ccshelf, with version.txt and mode.txt set as given.
func fakeCcshelfDir(t *testing.T, version, mode string) string {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	name := "ccshelf"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), src, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, c := range map[string]string{"version.txt": version, "mode.txt": mode} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, name)
}

// Package updatetest is a fake GitHub release server for tests of the update
// flow: it speaks the few REST routes ccshelf uses (latest, list, by tag) and
// serves real tar.gz and zip archives, a checksums.txt and, on request, a
// signature bundle placeholder. It depends on the standard library only, so
// both the unit tests and the end-to-end tests can use it. Nothing here is
// used by the ccshelf binary.
package updatetest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Repo is the repository the fake serves.
const Repo = "yorch/ccshelf"

// Release is one published release.
type Release struct {
	Tag        string
	Prerelease bool
	Draft      bool
	// Assets by file name.
	Assets map[string][]byte
}

// Server is a running fake.
type Server struct {
	t   testing.TB
	srv *httptest.Server

	mu       sync.Mutex
	releases []*Release
	latest   string
	hits     []string
	// Handler, when set, is tried first for every request; returning true
	// means it answered.
	Handler func(w http.ResponseWriter, r *http.Request) bool
}

// NewServer starts a fake release server on a loopback port, serving https
// with a throw-away certificate that Client trusts. It is closed when the test
// ends. The API lives under /api/v3 and downloads under
// /<owner>/<repo>/releases/download/<tag>/<file>, the layout of a GitHub
// Enterprise Server, which is what a base URL of https://127.0.0.1:<port>
// selects.
func NewServer(t testing.TB) *Server {
	t.Helper()
	s := &Server{t: t}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// NewPlainServer is NewServer over plain http. Only a binary built with
// -tags e2eloopback accepts such a base URL (see internal/config).
func NewPlainServer(t testing.TB) *Server {
	t.Helper()
	s := &Server{t: t}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the base URL to put in [update] base_url.
func (s *Server) URL() string { return s.srv.URL }

// Client is an HTTP client for the server.
func (s *Server) Client() *http.Client { return s.srv.Client() }

// Hits returns the request paths seen so far.
func (s *Server) Hits() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

// Add publishes r and makes it the latest.
func (s *Server) Add(r *Release) *Release {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases = append(s.releases, r)
	if !r.Prerelease && !r.Draft {
		s.latest = r.Tag
	}
	return r
}

// SetLatest chooses the release served as "latest".
func (s *Server) SetLatest(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = tag
}

// ArchiveName is the release asset name for a platform (the name_template of
// .goreleaser.yaml).
func ArchiveName(version, goos, goarch string) string {
	n := fmt.Sprintf("ccshelf_%s_%s_%s", strings.TrimPrefix(version, "v"), goos, goarch)
	if goos == "windows" {
		return n + ".zip"
	}
	return n + ".tar.gz"
}

// BinaryName is the executable inside the archive.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "ccshelf.exe"
	}
	return "ccshelf"
}

// Publish builds a release for tag whose archive for goos/goarch contains bin
// (plus the LICENSE and README.md goreleaser adds), with a matching
// checksums.txt, and, when signed, a placeholder checksums.txt.sigstore.json.
func (s *Server) Publish(tag string, bin []byte, goos, goarch string, signed bool) *Release {
	s.t.Helper()
	name := ArchiveName(tag, goos, goarch)
	var arch []byte
	if goos == "windows" {
		arch = Zip(s.t, Entry{"ccshelf.exe", bin, 0o755}, Entry{"LICENSE", []byte("MIT"), 0o644}, Entry{"README.md", []byte("readme"), 0o644})
	} else {
		arch = TarGz(s.t, Entry{"ccshelf", bin, 0o755}, Entry{"LICENSE", []byte("MIT"), 0o644}, Entry{"README.md", []byte("readme"), 0o644})
	}
	r := &Release{Tag: tag, Prerelease: strings.Contains(tag, "-"), Assets: map[string][]byte{
		name:            arch,
		"checksums.txt": []byte(Checksums(map[string][]byte{name: arch})),
	}}
	if signed {
		r.Assets["checksums.txt.sigstore.json"] = []byte(`{"placeholder":true}`)
	}
	return s.Add(r)
}

// Checksums renders the sha256sum-style text for files.
func Checksums(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(sum[:]), n)
	}
	return b.String()
}

// Entry is one archive member.
type Entry struct {
	Name string
	Body []byte
	Mode int64
}

// TarGz builds a gzip-compressed tar archive.
func TarGz(t testing.TB, entries ...Entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		h := &tar.Header{Name: e.Name, Typeflag: tar.TypeReg, Mode: e.Mode, Size: int64(len(e.Body))}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.Body); err != nil {
			t.Fatal(err)
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

// Zip builds a zip archive.
func Zip(t testing.TB, entries ...Entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.Name, Method: zip.Deflate}
		h.SetMode(0o755)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.Body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (r *Release) json() map[string]any {
	var assets []map[string]any
	names := make([]string, 0, len(r.Assets))
	for n := range r.Assets {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		assets = append(assets, map[string]any{"name": n, "size": len(r.Assets[n])})
	}
	return map[string]any{"tag_name": r.Tag, "draft": r.Draft, "prerelease": r.Prerelease, "assets": assets}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.hits = append(s.hits, r.URL.Path)
	h := s.Handler
	s.mu.Unlock()
	if h != nil && h(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	api := "/api/v3/repos/" + Repo + "/releases"
	find := func(tag string) *Release {
		for _, rel := range s.releases {
			if rel.Tag == tag {
				return rel
			}
		}
		return nil
	}
	writeJSON := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	p := r.URL.Path
	switch {
	case p == api+"/latest":
		if rel := find(s.latest); rel != nil {
			writeJSON(rel.json())
			return
		}
	case p == api:
		list := []map[string]any{}
		for _, rel := range s.releases {
			list = append(list, rel.json())
		}
		writeJSON(list)
		return
	case strings.HasPrefix(p, api+"/tags/"):
		if rel := find(strings.TrimPrefix(p, api+"/tags/")); rel != nil {
			writeJSON(rel.json())
			return
		}
	case strings.HasPrefix(p, "/"+Repo+"/releases/download/"):
		parts := strings.SplitN(strings.TrimPrefix(p, "/"+Repo+"/releases/download/"), "/", 2)
		if rel := find(parts[0]); rel != nil && len(parts) == 2 {
			if b, ok := rel.Assets[parts[1]]; ok {
				w.Header().Set("Content-Length", fmt.Sprint(len(b)))
				_, _ = w.Write(b)
				return
			}
		}
	}
	http.NotFound(w, r)
}

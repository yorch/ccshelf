package update

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fetcherFor(t *testing.T, srv *httptest.Server, assetHosts ...string) *Fetcher {
	t.Helper()
	src, err := NewSource("yorch/ccshelf", srv.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	src.AssetHosts = assetHosts
	// A real client with the production redirect policy, pointed at the test
	// server (plain http on loopback).
	return &Fetcher{Client: NewClient(src, 10*time.Second), Source: src, UserAgent: "ccshelf/9.9.9"}
}

func TestGetSendsNoCredentialsAndIdentifiesItself(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	f := fetcherFor(t, srv)
	b, err := f.Get(context.Background(), srv.URL+"/x", "application/json", 100)
	if err != nil || string(b) != "ok" {
		t.Fatalf("Get = %q %v", b, err)
	}
	if got.Get("User-Agent") != "ccshelf/9.9.9" {
		t.Errorf("User-Agent = %q", got.Get("User-Agent"))
	}
	if got.Get("Authorization") != "" || got.Get("Cookie") != "" {
		t.Errorf("credentials sent: %v", got)
	}
	if got.Get("Accept-Encoding") != "identity" {
		t.Errorf("Accept-Encoding = %q", got.Get("Accept-Encoding"))
	}
	if got.Get("Accept") != "application/json" {
		t.Errorf("Accept = %q", got.Get("Accept"))
	}
	if len(got) > 5 {
		t.Errorf("more headers than needed: %v", got)
	}
}

func TestGetLimits(t *testing.T) {
	body := strings.Repeat("a", 1000)
	chunked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// No Content-Length: the limit has to hold while reading.
		w.(http.Flusher).Flush()
		fmt.Fprint(w, body)
	}))
	defer chunked.Close()
	declared := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		fmt.Fprint(w, body)
	}))
	defer declared.Close()
	for name, srv := range map[string]*httptest.Server{"streamed": chunked, "declared": declared} {
		f := fetcherFor(t, srv)
		if _, err := f.Get(context.Background(), srv.URL, "x", 999); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s over the limit: %v", name, err)
		}
		if b, err := f.Get(context.Background(), srv.URL, "x", 1000); err != nil || len(b) != 1000 {
			t.Errorf("%s at the limit: %d %v", name, len(b), err)
		}
	}
}

func TestDownloadTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		fmt.Fprint(w, strings.Repeat("a", 400))
		// Handler returns early: the server closes the connection short.
		hj, _, _ := w.(http.Hijacker).Hijack()
		hj.Close()
	}))
	defer srv.Close()
	f := fetcherFor(t, srv)
	var sink limitedBuffer
	sink.max = 5000
	if _, err := f.Download(context.Background(), srv.URL, "x", 5000, &sink); !errors.Is(err, ErrTruncated) {
		t.Errorf("a short body = %v, want ErrTruncated", err)
	}
}

func TestGetStatusErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := map[string]int{"/404": 404, "/403": 403, "/500": 500, "/204": 204}[r.URL.Path]
		w.WriteHeader(code)
		fmt.Fprint(w, "secret body that must not be echoed")
	}))
	defer srv.Close()
	f := fetcherFor(t, srv)
	for path, want := range map[string]int{"/404": 404, "/403": 403, "/500": 500} {
		_, err := f.Get(context.Background(), srv.URL+path, "x", 1000)
		var he *HTTPError
		if !errors.As(err, &he) || he.Status != want {
			t.Errorf("%s: %v", path, err)
		}
		if err != nil && strings.Contains(err.Error(), "secret body") {
			t.Errorf("the response body leaked into the error: %v", err)
		}
	}
	if _, err := f.Get(context.Background(), srv.URL+"/204", "x", 1000); err == nil {
		t.Error("a non-200 success status is not a release document")
	}
}

func TestRefusesNonHTTPS(t *testing.T) {
	src, _ := NewSource("yorch/ccshelf", "", false)
	f := &Fetcher{Source: src, UserAgent: "x"}
	for _, u := range []string{"http://github.com/x", "http://127.0.0.1:1/x", "ftp://github.com/x", "file:///etc/passwd", "//github.com/x", ""} {
		if _, err := f.Get(context.Background(), u, "x", 10); err == nil || !strings.Contains(err.Error(), "not https") && !strings.Contains(err.Error(), "invalid URL") {
			t.Errorf("%q: error = %v", u, err)
		}
	}
}

func TestRedirectPolicy(t *testing.T) {
	var other atomic.Int32
	otherSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		other.Add(1)
		fmt.Fprint(w, "from the other host")
	}))
	defer otherSrv.Close()
	otherHost := strings.TrimPrefix(otherSrv.URL, "http://")

	var self *httptest.Server
	self = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			fmt.Fprint(w, "final")
		case "/other":
			http.Redirect(w, r, otherSrv.URL+"/x", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/five":
			n := 0
			fmt.Sscanf(r.URL.Query().Get("n"), "%d", &n)
			if n >= 5 {
				fmt.Fprint(w, "arrived")
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/five?n=%d", n+1), http.StatusFound)
		case "/six":
			n := 0
			fmt.Sscanf(r.URL.Query().Get("n"), "%d", &n)
			if n >= 6 {
				fmt.Fprint(w, "arrived")
				return
			}
			http.Redirect(w, r, fmt.Sprintf("/six?n=%d", n+1), http.StatusFound)
		case "/creds":
			u, _ := url.Parse(self.URL + "/final")
			u.User = url.UserPassword("a", "b")
			http.Redirect(w, r, u.String(), http.StatusFound)
		case "/scheme":
			http.Redirect(w, r, "ftp://127.0.0.1/x", http.StatusFound)
		}
	}))
	defer self.Close()

	f := fetcherFor(t, self)
	get := func(f *Fetcher, path string) (string, error) {
		b, err := f.Get(context.Background(), self.URL+path, "x", 1000)
		return string(b), err
	}
	if s, err := get(f, "/same"); err != nil || s != "final" {
		t.Errorf("same-host redirect = %q %v", s, err)
	}
	if _, err := get(f, "/other"); !errors.Is(err, ErrRedirect) {
		t.Errorf("redirect to another host = %v, want ErrRedirect", err)
	}
	if other.Load() != 0 {
		t.Error("the other host must never be contacted")
	}
	if s, err := get(fetcherFor(t, self, otherHost), "/other"); err != nil || s != "from the other host" {
		t.Errorf("redirect to a release-asset host = %q %v", s, err)
	}
	if _, err := get(f, "/loop"); !errors.Is(err, ErrRedirect) {
		t.Errorf("redirect loop = %v", err)
	}
	if s, err := get(f, "/five"); err != nil || s != "arrived" {
		t.Errorf("five redirects = %q %v, want success", s, err)
	}
	if _, err := get(f, "/six"); !errors.Is(err, ErrRedirect) {
		t.Errorf("six redirects = %v, want ErrRedirect", err)
	}
	if _, err := get(f, "/creds"); !errors.Is(err, ErrRedirect) {
		t.Errorf("redirect carrying credentials = %v", err)
	}
	if _, err := get(f, "/scheme"); err == nil {
		t.Error("redirect to another scheme must fail")
	}
}

func TestRedirectPolicyTable(t *testing.T) {
	gh, _ := NewSource("yorch/ccshelf", "", false)
	ghe, _ := NewSource("yorch/ccshelf", "https://ghe.example.com", false)
	loop, _ := NewSource("yorch/ccshelf", "http://127.0.0.1:1", true)
	mk := func(raw string) *http.Request {
		r, err := http.NewRequest("GET", raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	via := func(raw string) []*http.Request { return []*http.Request{mk(raw)} }
	for _, tc := range []struct {
		name string
		src  Source
		to   string
		via  []*http.Request
		ok   bool
	}{
		{"github to asset host", gh, "https://objects.githubusercontent.com/x", via("https://github.com/a"), true},
		{"github to release-assets", gh, "https://release-assets.githubusercontent.com/x?sig=1", via("https://github.com/a"), true},
		{"github to same host", gh, "https://github.com/b", via("https://github.com/a"), true},
		{"github to other github host", gh, "https://api.github.com/x", via("https://github.com/a"), false},
		{"github to a lookalike", gh, "https://objects.githubusercontent.com.evil.example/x", via("https://github.com/a"), false},
		{"github to user content", gh, "https://raw.githubusercontent.com/x", via("https://github.com/a"), false},
		{"github to http", gh, "http://github.com/x", via("https://github.com/a"), false},
		{"ghe to same", ghe, "https://ghe.example.com/x", via("https://ghe.example.com/a"), true},
		{"ghe to github asset host", ghe, "https://objects.githubusercontent.com/x", via("https://ghe.example.com/a"), false},
		{"ghe case-insensitive", ghe, "https://GHE.example.com/x", via("https://ghe.example.com/a"), true},
		{"ghe other port", ghe, "https://ghe.example.com:8443/x", via("https://ghe.example.com/a"), false},
		{"loopback http allowed", loop, "http://127.0.0.1:1/x", via("http://127.0.0.1:1/a"), true},
		{"loopback http to other loopback port", loop, "http://127.0.0.1:2/x", via("http://127.0.0.1:1/a"), false},
		{"loopback to non-loopback http", loop, "http://example.com/x", via("http://127.0.0.1:1/a"), false},
		{"production never allows loopback http", gh, "http://127.0.0.1:1/x", via("https://github.com/a"), false},
	} {
		err := RedirectPolicy(tc.src)(mk(tc.to), tc.via)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	if err := RedirectPolicy(gh)(mk("https://github.com/x"), make([]*http.Request, MaxRedirects+1)); !errors.Is(err, ErrRedirect) {
		t.Errorf("more than %d redirects = %v", MaxRedirects, err)
	}
}

func TestNewClientSettings(t *testing.T) {
	src, _ := NewSource("yorch/ccshelf", "", false)
	c := NewClient(src, 7*time.Second)
	if c.Timeout != 7*time.Second || c.Jar != nil || c.CheckRedirect == nil {
		t.Errorf("client = %+v", c)
	}
	tr := c.Transport.(*http.Transport)
	if !tr.DisableCompression || tr.Proxy == nil {
		t.Errorf("transport: compression off=%v proxy=%v", tr.DisableCompression, tr.Proxy != nil)
	}
	if tr.TLSClientConfig != nil && tr.TLSClientConfig.InsecureSkipVerify {
		t.Error("certificate verification must stay on")
	}
	_ = tls.VersionTLS12
}

func TestLatestAndByTag(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	r1 := fx.srv.add(&relSpec{tag: "v0.1.0", assets: map[string][]byte{}})
	_ = r1
	fx.srv.add(&relSpec{tag: "v0.2.0", assets: map[string][]byte{"a": []byte("12345")}})
	fx.srv.add(&relSpec{tag: "v0.3.0-rc.1", prerelease: true, assets: map[string][]byte{}})
	fx.srv.add(&relSpec{tag: "v0.4.0", draft: true, assets: map[string][]byte{}})
	fx.srv.add(&relSpec{tag: "nightly", assets: map[string][]byte{}})
	fx.srv.latest = "v0.2.0"
	f := fx.u.fetcher()
	ctx := context.Background()

	rel, err := f.Latest(ctx, false)
	if err != nil || rel.Tag != "v0.2.0" || rel.Prerelease || rel.URL != fx.u.Source.ReleasePage("v0.2.0") {
		t.Errorf("Latest(stable) = %+v %v", rel, err)
	}
	if a, ok := rel.Asset("a"); !ok || a.Size != 5 {
		t.Errorf("assets = %+v", rel.Assets)
	}
	if _, ok := rel.Asset("missing"); ok {
		t.Error("Asset(missing)")
	}
	rel, err = f.Latest(ctx, true)
	if err != nil || rel.Tag != "v0.3.0-rc.1" || !rel.Prerelease {
		t.Errorf("Latest(prerelease) = %+v %v: drafts and non-version tags are skipped, the highest wins", rel, err)
	}
	rel, err = f.ByTag(ctx, "0.2.0")
	if err != nil || rel.Tag != "v0.2.0" {
		t.Errorf("ByTag = %+v %v", rel, err)
	}
	if _, err := f.ByTag(ctx, "v9.9.9"); err == nil {
		t.Error("an unknown tag must fail")
	}
	if _, err := f.ByTag(ctx, "v0.4.0"); err == nil || !strings.Contains(err.Error(), "draft") {
		t.Errorf("a draft = %v", err)
	}
	if _, err := f.ByTag(ctx, "../etc"); err == nil {
		t.Error("an invalid tag must be refused before any request")
	}
	fx.srv.latest = "v0.4.0"
	if _, err := f.Latest(ctx, false); err == nil || !strings.Contains(err.Error(), "draft") {
		t.Errorf("a draft as latest = %v", err)
	}
	fx.srv.latest = "nightly"
	if _, err := f.Latest(ctx, false); err == nil {
		t.Error("a latest tag that is not a version must be refused")
	}
}

func TestLatestRejectsHostileResponses(t *testing.T) {
	fx := newFixture(t, "0.1.0")
	f := fx.u.fetcher()
	for name, body := range map[string]string{
		"not json":     "<html>",
		"wrong tag":    `{"tag_name":"v1.0.0/../../x"}`,
		"empty":        `{}`,
		"leading zero": `{"tag_name":"v01.0.0"}`,
		"build meta":   `{"tag_name":"v1.0.0+evil"}`,
	} {
		fx.srv.handler = func(w http.ResponseWriter, _ *http.Request) bool {
			fmt.Fprint(w, body)
			return true
		}
		if _, err := f.Latest(context.Background(), false); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	fx.srv.handler = func(w http.ResponseWriter, _ *http.Request) bool {
		fmt.Fprint(w, `{"tag_name":"v1.0.0","x":"`+strings.Repeat("a", MaxMetadataBytes)+`"}`)
		return true
	}
	if _, err := f.Latest(context.Background(), false); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an enormous document = %v", err)
	}
	fx.srv.handler = func(w http.ResponseWriter, r *http.Request) bool {
		fmt.Fprint(w, `{"tag_name":"v0.9.0"}`)
		return true
	}
	if _, err := f.ByTag(context.Background(), "v1.0.0"); err == nil || !strings.Contains(err.Error(), "answered with") {
		t.Errorf("a server answering for another tag = %v", err)
	}
}

// roundTripFunc is a transport made of a function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadShortBodyWithoutTransportError(t *testing.T) {
	// A transport that hands back fewer bytes than Content-Length without
	// reporting an error must still be caught.
	src, _ := NewSource("yorch/ccshelf", "", false)
	f := &Fetcher{Source: src, UserAgent: "x", Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200, ContentLength: 1000, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(strings.Repeat("a", 400))), Request: r,
		}, nil
	})}}
	var sink limitedBuffer
	sink.max = 5000
	n, err := f.Download(context.Background(), "https://github.com/x", "x", 5000, &sink)
	if !errors.Is(err, ErrTruncated) || n != 400 {
		t.Errorf("Download = %d, %v; want 400 bytes and ErrTruncated", n, err)
	}
}

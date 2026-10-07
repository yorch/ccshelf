package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yorch/ccshelf/internal/config"
)

// Hard limits on what is read from the network.
const (
	// MaxMetadataBytes bounds a release API response.
	MaxMetadataBytes = 2 << 20
	// MaxChecksumsBytes bounds checksums.txt and the signature bundle.
	MaxChecksumsBytes = 256 << 10
	// MaxArchiveBytes bounds a downloaded archive.
	MaxArchiveBytes = 128 << 20
	// MaxBinaryBytes bounds the extracted binary.
	MaxBinaryBytes = 256 << 20
	// MaxRedirects is the most redirects one request follows.
	MaxRedirects = 5
	// MetadataTimeout is the default time budget of one metadata request.
	MetadataTimeout = 20 * time.Second
	// DownloadTimeout is the default time budget of an asset download.
	DownloadTimeout = 5 * time.Minute
)

// Fetch errors callers can test with errors.Is.
var (
	// ErrTooLarge means a response was bigger than its limit.
	ErrTooLarge = errors.New("response is larger than the allowed size")
	// ErrTruncated means a response ended before its declared length.
	ErrTruncated = errors.New("download was cut short")
	// ErrRedirect means a redirect was refused by the redirect policy.
	ErrRedirect = errors.New("redirect refused")
)

// HTTPError is a non-200 response.
type HTTPError struct {
	URL    string
	Status int
}

// Error implements error.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("GET %s: HTTP %d %s", e.URL, e.Status, http.StatusText(e.Status))
}

// Fetcher reads release metadata and assets. It sends no credentials, no
// cookies and no identifier beyond the User-Agent "ccshelf/<version>".
type Fetcher struct {
	// Client performs the requests (default [NewClient]). Tests pass the
	// httptest client or a fake transport.
	Client *http.Client
	Source Source
	// UserAgent is sent with every request ("ccshelf/<version>").
	UserAgent string
}

// NewClient returns an HTTP client for src: proxies from the environment, no
// transparent compression (a declared length must match the bytes), no
// cookies, an overall timeout, and a redirect policy that follows at most
// [MaxRedirects] redirects, only to https (or loopback http when the source
// allows it), and only to the original host or one of src's asset hosts.
func NewClient(src Source, timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert // the default transport is always an *http.Transport
	tr.DisableCompression = true
	tr.Proxy = http.ProxyFromEnvironment
	return &http.Client{Transport: tr, Timeout: timeout, CheckRedirect: RedirectPolicy(src)}
}

// RedirectPolicy is the CheckRedirect function of [NewClient].
func RedirectPolicy(src Source) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > MaxRedirects {
			return fmt.Errorf("%w: more than %d redirects", ErrRedirect, MaxRedirects)
		}
		u := req.URL
		httpsOK := u.Scheme == "https" || (u.Scheme == "http" && src.AllowLoopbackHTTP && config.IsLoopbackHost(u.Hostname()))
		if !httpsOK {
			return fmt.Errorf("%w: to %s, which is not https", ErrRedirect, safeURL(u))
		}
		if u.User != nil {
			return fmt.Errorf("%w: the target carries credentials", ErrRedirect)
		}
		if len(via) > 0 && strings.EqualFold(u.Host, via[0].URL.Host) {
			return nil
		}
		for _, h := range src.AssetHosts {
			if strings.EqualFold(u.Host, h) {
				return nil
			}
		}
		return fmt.Errorf("%w: to %s, which is neither the release host nor a release-asset host", ErrRedirect, safeURL(u))
	}
}

// safeURL renders u without its query or user information for messages.
func safeURL(u *url.URL) string {
	c := *u
	c.RawQuery, c.User, c.Fragment = "", nil, ""
	return c.String()
}

func (f *Fetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return NewClient(f.Source, DownloadTimeout)
}

func (f *Fetcher) open(ctx context.Context, rawURL, accept string) (*http.Response, error) {
	if err := f.checkURL(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", f.UserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := f.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", rawURL, unwrapURLError(err))
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, &HTTPError{URL: rawURL, Status: resp.StatusCode}
	}
	return resp, nil
}

// unwrapURLError drops the *url.Error wrapper, whose text repeats the URL.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// checkURL refuses anything but https (or loopback http for tests) so that a
// bug elsewhere can never turn into a plain-text download.
func (f *Fetcher) checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid URL %q", raw)
	}
	if u.Scheme == "https" || (u.Scheme == "http" && f.Source.AllowLoopbackHTTP && config.IsLoopbackHost(u.Hostname())) {
		return nil
	}
	return fmt.Errorf("refusing to fetch %s: not https", safeURL(u))
}

// Get returns the body of rawURL, at most max bytes.
func (f *Fetcher) Get(ctx context.Context, rawURL, accept string, max int64) ([]byte, error) {
	var b limitedBuffer
	b.max = max
	if _, err := f.Download(ctx, rawURL, accept, max, &b); err != nil {
		return nil, err
	}
	return b.buf, nil
}

// Download streams rawURL into w and returns the number of bytes written. It
// fails with [ErrTooLarge] when the body (or its declared length) exceeds max
// and with [ErrTruncated] when it is shorter than declared.
func (f *Fetcher) Download(ctx context.Context, rawURL, accept string, max int64, w io.Writer) (int64, error) {
	resp, err := f.open(ctx, rawURL, accept)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.ContentLength > max {
		return 0, fmt.Errorf("GET %s: %w (declared %d bytes, limit %d)", rawURL, ErrTooLarge, resp.ContentLength, max)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, max+1))
	switch {
	case n > max:
		return n, fmt.Errorf("GET %s: %w (limit %d bytes)", rawURL, ErrTooLarge, max)
	case err != nil && (errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)):
		return n, fmt.Errorf("GET %s: %w after %d bytes", rawURL, ErrTruncated, n)
	case err != nil:
		return n, fmt.Errorf("GET %s: %w", rawURL, err)
	case resp.ContentLength >= 0 && n != resp.ContentLength:
		return n, fmt.Errorf("GET %s: %w (%d of %d bytes)", rawURL, ErrTruncated, n, resp.ContentLength)
	}
	return n, nil
}

// limitedBuffer collects up to max bytes and then reports ErrTooLarge.
type limitedBuffer struct {
	buf []byte
	max int64
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if int64(len(b.buf))+int64(len(p)) > b.max {
		return 0, ErrTooLarge
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// Asset is one file of a release.
type Asset struct {
	Name string
	Size int64
}

// Release is a published release.
type Release struct {
	// Tag is the git tag exactly as published ("v0.2.0").
	Tag     string
	Version Version
	// Prerelease is the publisher's pre-release flag.
	Prerelease bool
	Assets     []Asset
	// URL is the web address of the release notes.
	URL string
}

// Asset returns the named asset.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

type rawRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (f *Fetcher) release(raw rawRelease) (Release, error) {
	v, err := ParseVersion(raw.Tag)
	if err != nil {
		return Release{}, fmt.Errorf("release tag %q: %w", raw.Tag, err)
	}
	if raw.Tag != v.Tag() && raw.Tag != v.String() {
		return Release{}, fmt.Errorf("release tag %q is not in the form v%s", raw.Tag, v)
	}
	r := Release{Tag: raw.Tag, Version: v, Prerelease: raw.Prerelease || v.IsPrerelease()}
	for _, a := range raw.Assets {
		r.Assets = append(r.Assets, Asset{Name: a.Name, Size: a.Size})
	}
	r.URL = f.Source.ReleasePage(raw.Tag)
	return r, nil
}

func decodeJSON(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the release server sent something that is not valid JSON: %w", err)
	}
	return nil
}

// Latest returns the newest release: the one GitHub marks as latest, or with
// includePrerelease the highest version among the recent non-draft releases.
func (f *Fetcher) Latest(ctx context.Context, includePrerelease bool) (Release, error) {
	const accept = "application/vnd.github+json"
	if !includePrerelease {
		b, err := f.Get(ctx, f.Source.LatestURL(), accept, MaxMetadataBytes)
		if err != nil {
			return Release{}, err
		}
		var raw rawRelease
		if err := decodeJSON(b, &raw); err != nil {
			return Release{}, err
		}
		if raw.Draft {
			return Release{}, errors.New("the latest release is a draft")
		}
		return f.release(raw)
	}
	b, err := f.Get(ctx, f.Source.ListURL(), accept, MaxMetadataBytes)
	if err != nil {
		return Release{}, err
	}
	var list []rawRelease
	if err := decodeJSON(b, &list); err != nil {
		return Release{}, err
	}
	var best *Release
	for _, raw := range list {
		if raw.Draft {
			continue
		}
		r, err := f.release(raw)
		if err != nil {
			continue // a tag that is not a version is not ours
		}
		if best == nil || r.Version.Compare(best.Version) > 0 {
			rr := r
			best = &rr
		}
	}
	if best == nil {
		return Release{}, errors.New("no release found")
	}
	return *best, nil
}

// ByTag returns the release published for tag.
func (f *Fetcher) ByTag(ctx context.Context, tag string) (Release, error) {
	v, err := ParseVersion(tag)
	if err != nil {
		return Release{}, fmt.Errorf("version %q: %w", tag, err)
	}
	b, err := f.Get(ctx, f.Source.TagURL(v.Tag()), "application/vnd.github+json", MaxMetadataBytes)
	if err != nil {
		return Release{}, err
	}
	var raw rawRelease
	if err := decodeJSON(b, &raw); err != nil {
		return Release{}, err
	}
	if raw.Draft {
		return Release{}, fmt.Errorf("release %s is a draft", v.Tag())
	}
	r, err := f.release(raw)
	if err != nil {
		return Release{}, err
	}
	if r.Version.Compare(v) != 0 {
		return Release{}, fmt.Errorf("asked for %s but the server answered with %s", v.Tag(), r.Tag)
	}
	return r, nil
}

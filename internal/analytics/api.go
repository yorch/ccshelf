package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
)

// Defaults and limits of Fetch.
const (
	// DefaultBaseURL is the Anthropic API host.
	DefaultBaseURL = "https://api.anthropic.com"
	// DefaultKeyEnv is the environment variable holding the admin key.
	DefaultKeyEnv = "CCSHELF_ANALYTICS_KEY"
	// APIVersion is sent as anthropic-version.
	APIVersion = "2023-06-01"
	// MaxResponseSize caps the bytes read from one response page.
	MaxResponseSize = 8 << 20
	// DefaultTimeout bounds one request.
	DefaultTimeout = 30 * time.Second

	pluginsPath    = "/v1/organizations/analytics/plugins"
	maxPages       = 50
	maxRedirects   = 3
	maxRangeDays   = 366
	earliestDate   = "2026-01-01"
	defaultWindow  = 30
	maxErrorDetail = 300
)

// Options tune Fetch.
type Options struct {
	// BaseURL defaults to DefaultBaseURL. It must be https unless
	// AllowInsecure is set.
	BaseURL string
	// AllowInsecure permits an http BaseURL. For tests with httptest only.
	AllowInsecure bool
	// KeyEnv names the environment variable that holds the admin key. The
	// default is DefaultKeyEnv. Fetch reads the variable at call time.
	KeyEnv string
	// Getenv replaces os.Getenv, for tests.
	Getenv func(string) string
	// From (inclusive) and To (exclusive) select the window. Zero To means
	// today (UTC). Zero From means 30 days before To.
	From, To time.Time
	// Now replaces the clock, for tests.
	Now func() time.Time
	// Product, when set (claude_code or cowork), filters rows to that
	// surface. Leave empty for both.
	Product string
	// PageSize is the limit parameter (1 to 1000). The default is 1000.
	PageSize int
	// Timeout bounds each request. The default is DefaultTimeout.
	Timeout time.Duration
	// HTTPClient replaces the default client (its redirect policy is
	// replaced as well, so redirects stay confined to the same host).
	HTTPClient *http.Client
}

// ErrNoKey is returned when the key environment variable is empty. The text
// names the variable, never a value.
var ErrNoKey = errors.New("analytics admin key not set")

// Fetch reads plugin usage from the Enterprise Analytics API. It makes only
// GET requests, to BaseURL, with the key in the x-api-key header. Errors never
// contain the key.
func Fetch(ctx context.Context, opt Options) (*Usage, error) {
	keyEnv := opt.KeyEnv
	if keyEnv == "" {
		keyEnv = DefaultKeyEnv
	}
	getenv := opt.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	key := strings.TrimSpace(getenv(keyEnv))
	if key == "" {
		return nil, fmt.Errorf("%w: set the environment variable %s to an admin key with the read:analytics scope", ErrNoKey, keyEnv)
	}
	for _, r := range key {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return nil, fmt.Errorf("the value of %s is not a valid key (it contains whitespace or control characters)", keyEnv)
		}
	}
	f := &fetcher{key: key, opt: opt}
	u, err := f.fetch(ctx)
	if err != nil {
		return nil, f.redactErr(err)
	}
	return u, nil
}

type fetcher struct {
	key string
	opt Options
}

func (f *fetcher) redact(s string) string {
	return strings.ReplaceAll(s, f.key, "[redacted]")
}

func (f *fetcher) redactErr(err error) error {
	msg := f.redact(err.Error())
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}

func (f *fetcher) base() (*url.URL, error) {
	raw := f.opt.BaseURL
	if raw == "" {
		raw = DefaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, errors.New("analytics base URL is not a valid URL")
	}
	if u.User != nil {
		return nil, errors.New("analytics base URL must not contain credentials")
	}
	if u.Scheme != "https" && !(f.opt.AllowInsecure && u.Scheme == "http") {
		return nil, errors.New("analytics base URL must use https")
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/") + pluginsPath
	return u, nil
}

func (f *fetcher) window() (from, to string, err error) {
	now := time.Now
	if f.opt.Now != nil {
		now = f.opt.Now
	}
	t := f.opt.To
	if t.IsZero() {
		t = now()
	}
	t = t.UTC().Truncate(24 * time.Hour)
	s := f.opt.From
	if s.IsZero() {
		s = t.AddDate(0, 0, -defaultWindow)
	}
	s = s.UTC().Truncate(24 * time.Hour)
	if !s.Before(t) {
		return "", "", errors.New("the analytics window is empty: from must be before to")
	}
	if t.Sub(s) > maxRangeDays*24*time.Hour {
		return "", "", fmt.Errorf("the analytics window is longer than %d days", maxRangeDays)
	}
	if s.Format(dateLayout) < earliestDate {
		return "", "", fmt.Errorf("the analytics API has no data before %s", earliestDate)
	}
	return s.Format(dateLayout), t.Format(dateLayout), nil
}

const dateLayout = "2006-01-02"

func (f *fetcher) client(base *url.URL) *http.Client {
	var c http.Client
	if f.opt.HTTPClient != nil {
		c = *f.opt.HTTPClient
	}
	if c.Timeout == 0 {
		c.Timeout = f.opt.Timeout
		if c.Timeout == 0 {
			c.Timeout = DefaultTimeout
		}
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errors.New("too many redirects")
		}
		if req.URL.Host != base.Host || req.URL.Scheme != base.Scheme {
			return errors.New("refusing a redirect to another host or scheme")
		}
		return nil
	}
	return &c
}

type pluginRow struct {
	PluginName      string `json:"plugin_name"`
	PluginID        string `json:"plugin_id"`
	InstallCount    *int64 `json:"install_count"`
	InvocationCount *int64 `json:"invocation_count"`
}

type pluginPage struct {
	Data     []pluginRow `json:"data"`
	NextPage *string     `json:"next_page"`
}

func (f *fetcher) fetch(ctx context.Context) (*Usage, error) {
	base, err := f.base()
	if err != nil {
		return nil, err
	}
	from, to, err := f.window()
	if err != nil {
		return nil, err
	}
	switch f.opt.Product {
	case "", "claude_code", "cowork":
	default:
		return nil, fmt.Errorf("unknown product %q: use claude_code or cowork", f.opt.Product)
	}
	size := f.opt.PageSize
	if size <= 0 || size > 1000 {
		size = 1000
	}
	client := f.client(base)
	u := newUsage("api")
	u.From, u.To = from, to
	cursor := ""
	seen := map[string]bool{}
	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf("the analytics API returned more than %d pages: narrow the window", maxPages)
		}
		q := url.Values{}
		q.Set("starting_date", from)
		q.Set("ending_date", to)
		q.Set("limit", fmt.Sprint(size))
		if f.opt.Product != "" {
			q.Add("filter[]", "product:"+f.opt.Product)
		}
		if cursor != "" {
			q.Set("page", cursor)
		}
		req := *base
		req.RawQuery = q.Encode()
		body, err := f.get(ctx, client, req.String())
		if err != nil {
			return nil, err
		}
		var pg pluginPage
		if err := json.Unmarshal(body, &pg); err != nil {
			return nil, fmt.Errorf("decoding the analytics response: %w", err)
		}
		for _, r := range pg.Data {
			key := r.PluginID
			if key == "" {
				key = r.PluginName
			}
			if key == "" {
				continue
			}
			u.add(key, Counts{Installs: val(r.InstallCount), Invocations: val(r.InvocationCount)})
		}
		if pg.NextPage == nil || *pg.NextPage == "" {
			return u, nil
		}
		cursor = *pg.NextPage
		if seen[cursor] {
			return nil, errors.New("the analytics API repeated a pagination cursor")
		}
		seen[cursor] = true
	}
}

func val(p *int64) int64 {
	if p == nil || *p < 0 {
		return 0
	}
	return *p
}

func (f *fetcher) get(ctx context.Context, c *http.Client, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, errors.New("building the analytics request failed")
	}
	req.Header.Set("x-api-key", f.key)
	req.Header.Set("anthropic-version", APIVersion)
	req.Header.Set("accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		// The *url.Error text holds the URL, which has no secret; redact
		// anyway in case a server or proxy echoes the key.
		return nil, fmt.Errorf("calling the analytics API: %w", f.redactErr(err))
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading the analytics response: %w", f.redactErr(err))
	}
	if len(data) > MaxResponseSize {
		return nil, fmt.Errorf("the analytics response is larger than %d bytes", MaxResponseSize)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, f.statusError(resp.StatusCode, data)
	}
	return data, nil
}

func (f *fetcher) statusError(code int, body []byte) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	detail := ""
	if json.Unmarshal(body, &e) == nil {
		detail = f.redact(e.Error.Message)
	}
	detail = cleanText(detail, maxErrorDetail)
	hint := ""
	switch code {
	case http.StatusUnauthorized, http.StatusForbidden:
		hint = " (the key needs the read:analytics scope)"
	case http.StatusTooManyRequests:
		hint = " (rate limited, try again later)"
	}
	if detail != "" {
		return fmt.Errorf("the analytics API answered %d%s: %s", code, hint, detail)
	}
	return fmt.Errorf("the analytics API answered %d%s", code, hint)
}

func cleanText(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) {
			r = ' '
		}
		if n >= max {
			b.WriteString("...")
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

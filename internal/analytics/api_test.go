package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "sk-ant-admin01-SECRETSECRETSECRET"

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func fixedNow() time.Time { return time.Date(2026, 10, 6, 15, 0, 0, 0, time.UTC) }

type recorder struct {
	mu   sync.Mutex
	reqs []*http.Request
}

func (r *recorder) add(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reqs = append(r.reqs, req.Clone(context.Background()))
}

func opts(srv *httptest.Server) Options {
	return Options{BaseURL: srv.URL, AllowInsecure: true, Getenv: env(map[string]string{DefaultKeyEnv: testKey}), Now: fixedNow}
}

func TestFetchPaginatesAndSums(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r)
		w.Header().Set("content-type", "application/json")
		if r.URL.Query().Get("page") == "" {
			fmt.Fprint(w, `{"data":[{"plugin_name":"sre-kit","plugin_id":"sre-kit@acme","install_count":4,"invocation_count":10,"future_field":1},{"plugin_name":"third-party","plugin_id":null,"install_count":null,"invocation_count":7}],"next_page":"c2"}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"plugin_name":"sre-kit","plugin_id":"sre-kit@acme","install_count":1,"invocation_count":2},{"plugin_name":"","plugin_id":""}],"next_page":null}`)
	}))
	defer srv.Close()
	o := opts(srv)
	o.Product = "claude_code"
	o.PageSize = 50
	u, err := Fetch(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.PerPlugin["sre-kit@acme"]; got != (Counts{Installs: 5, Invocations: 12}) {
		t.Errorf("sre-kit = %+v", got)
	}
	if got := u.PerPlugin["third-party"]; got.Invocations != 7 {
		t.Errorf("third-party = %+v", got)
	}
	if u.From != "2026-09-06" || u.To != "2026-10-06" || u.Source != "api" {
		t.Errorf("window = %q %q %q", u.From, u.To, u.Source)
	}
	if len(rec.reqs) != 2 {
		t.Fatalf("%d requests", len(rec.reqs))
	}
	r := rec.reqs[0]
	if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/analytics/plugins" {
		t.Errorf("request = %s %s", r.Method, r.URL.Path)
	}
	if r.Header.Get("x-api-key") != testKey || r.Header.Get("anthropic-version") != APIVersion {
		t.Errorf("headers = %v", r.Header)
	}
	q := r.URL.Query()
	if q.Get("starting_date") != "2026-09-06" || q.Get("ending_date") != "2026-10-06" || q.Get("limit") != "50" ||
		q.Get("filter[]") != "product:claude_code" {
		t.Errorf("query = %v", q)
	}
	if strings.Contains(r.URL.String(), "SECRET") {
		t.Error("the key is in the URL")
	}
	if rec.reqs[1].URL.Query().Get("page") != "c2" {
		t.Errorf("second page query = %v", rec.reqs[1].URL.Query())
	}
}

func TestFetchKeyNeverLeaks(t *testing.T) {
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(os.Stderr)

	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"error body echoes key", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"error":{"message":"invalid key %s"}}`, r.Header.Get("x-api-key"))
		}},
		{"bad json echoes key", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"data": %s`, r.Header.Get("x-api-key"))
		}},
		{"server error", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			_, err := Fetch(context.Background(), opts(srv))
			if err == nil {
				t.Fatal("want error")
			}
			if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "SECRETSECRET") {
				t.Errorf("key in error: %v", err)
			}
		})
	}
	if strings.Contains(logBuf.String(), "SECRET") {
		t.Error("key in log output")
	}

	// A transport error whose text carries the key is redacted.
	o := Options{BaseURL: "https://example.invalid", Getenv: env(map[string]string{DefaultKeyEnv: testKey}), Now: fixedNow,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("boom " + testKey)
		})}}
	_, err := Fetch(context.Background(), o)
	if err == nil || strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("transport error = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchStatusHints(t *testing.T) {
	for code, want := range map[int]string{403: "read:analytics", 429: "rate limited", 400: "bad window"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			fmt.Fprint(w, `{"error":{"message":"bad window\n\u0007"}}`)
		}))
		_, err := Fetch(context.Background(), opts(srv))
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), fmt.Sprint(code)) {
			t.Errorf("status %d: %v", code, err)
		}
	}
}

func TestFetchKeyHandling(t *testing.T) {
	_, err := Fetch(context.Background(), Options{Getenv: env(nil)})
	if !errors.Is(err, ErrNoKey) || !strings.Contains(err.Error(), DefaultKeyEnv) {
		t.Errorf("no key: %v", err)
	}
	_, err = Fetch(context.Background(), Options{KeyEnv: "MY_KEY", Getenv: env(nil)})
	if !errors.Is(err, ErrNoKey) || !strings.Contains(err.Error(), "MY_KEY") {
		t.Errorf("custom env: %v", err)
	}
	_, err = Fetch(context.Background(), Options{Getenv: env(map[string]string{DefaultKeyEnv: "abc\ndef-SECRET"})})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Errorf("bad key: %v", err)
	}
}

func TestFetchKeyReadFromRealEnvironment(t *testing.T) {
	t.Setenv("MY_ANALYTICS", testKey)
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-api-key")
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer srv.Close()
	o := Options{BaseURL: srv.URL, AllowInsecure: true, KeyEnv: "MY_ANALYTICS", Now: fixedNow}
	if _, err := Fetch(context.Background(), o); err != nil || got != testKey {
		t.Errorf("err=%v key=%q", err, got)
	}
}

func TestFetchOptionValidation(t *testing.T) {
	good := map[string]string{DefaultKeyEnv: testKey}
	d := func(s string) time.Time { v, _ := time.Parse(dateLayout, s); return v }
	tests := []struct {
		name string
		opt  Options
		want string
	}{
		{"http base", Options{BaseURL: "http://example.com"}, "https"},
		{"credentials", Options{BaseURL: "https://u:p@example.com"}, "credentials"},
		{"bad base", Options{BaseURL: "::"}, "valid URL"},
		{"no host", Options{BaseURL: "https://"}, "valid URL"},
		{"empty window", Options{From: d("2026-05-01"), To: d("2026-05-01")}, "empty"},
		{"too long", Options{From: d("2026-01-01"), To: d("2027-06-01")}, "366"},
		{"too early", Options{From: d("2025-12-01"), To: d("2026-02-01")}, "no data before"},
		{"product", Options{Product: "chat"}, "unknown product"},
	}
	for _, tc := range tests {
		tc.opt.Getenv = env(good)
		tc.opt.Now = fixedNow
		_, err := Fetch(context.Background(), tc.opt)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestFetchRedirects(t *testing.T) {
	var otherHit bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherHit = true
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	// Two httptest servers differ by port, so the host differs.
	_, err := Fetch(context.Background(), opts(srv))
	if err == nil || !strings.Contains(err.Error(), "redirect") || otherHit {
		t.Errorf("err=%v otherHit=%v", err, otherHit)
	}

	// Same-host redirects work; loops stop.
	n := 0
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			http.Redirect(w, r, r.URL.Path+"?"+r.URL.RawQuery, http.StatusFound)
			return
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer loop.Close()
	if _, err := Fetch(context.Background(), opts(loop)); err != nil {
		t.Errorf("same-host redirect: %v", err)
	}
	always := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, r.URL.Path+"?"+r.URL.RawQuery, http.StatusFound)
	}))
	defer always.Close()
	if _, err := Fetch(context.Background(), opts(always)); err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Errorf("loop: %v", err)
	}
}

func TestFetchLimitsAndFailures(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(bytes.Repeat([]byte(" "), MaxResponseSize+10))
		}))
		defer srv.Close()
		if _, err := Fetch(context.Background(), opts(srv)); err == nil || !strings.Contains(err.Error(), "larger") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("repeated cursor", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"data":[],"next_page":"same"}`)
		}))
		defer srv.Close()
		if _, err := Fetch(context.Background(), opts(srv)); err == nil || !strings.Contains(err.Error(), "repeated") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("too many pages", func(t *testing.T) {
		n := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			n++
			fmt.Fprintf(w, `{"data":[],"next_page":"p%d"}`, n)
		}))
		defer srv.Close()
		if _, err := Fetch(context.Background(), opts(srv)); err == nil || !strings.Contains(err.Error(), "pages") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		done := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-done }))
		defer srv.Close()
		defer close(done)
		o := opts(srv)
		o.Timeout = 50 * time.Millisecond
		if _, err := Fetch(context.Background(), o); err == nil {
			t.Error("want timeout error")
		}
	})
	t.Run("canceled", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		defer srv.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Fetch(ctx, opts(srv)); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("not json", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>") }))
		defer srv.Close()
		if _, err := Fetch(context.Background(), opts(srv)); err == nil || !strings.Contains(err.Error(), "decoding") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestUsageJSON(t *testing.T) {
	u := newUsage("api")
	u.add("a@m", Counts{Invocations: 1})
	b, err := json.Marshal(u)
	if err != nil || !strings.Contains(string(b), `"per_plugin":{"a@m":{"installs":0,"invocations":1}}`) {
		t.Errorf("%s %v", b, err)
	}
}

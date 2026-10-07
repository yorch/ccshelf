package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/catalog"
)

func TestFetchCatalog(t *testing.T) {
	want := catalog.Catalog{
		Version:  catalog.Version,
		Title:    "Example catalog",
		Plugins:  []catalog.Entry{{Name: "docs", Description: "Documentation tools"}},
		Profiles: []catalog.ProfileInfo{{Name: "writer", WhenToUse: []string{"documentation"}}},
	}
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer server.Close()

	got, err := (&launcher{opt: Options{CatalogHTTPClient: server.Client()}}).fetchCatalog(context.Background(), server.URL+"/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Source, "remote catalog 127.0.0.1:") || got.Catalog.Title != want.Title || len(got.Catalog.Plugins) != 1 || got.Catalog.Plugins[0].Name != "docs" {
		t.Fatalf("got %+v", got)
	}
}

func TestFetchCatalogRejectsInvalidURL(t *testing.T) {
	for _, raw := range []string{
		"http://catalog.example/catalog.json",
		"https://user:pass@catalog.example/catalog.json",
		"https://catalog.example/catalog.json?token=secret",
		"https://catalog.example/catalog.json#latest",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := (&launcher{}).fetchCatalog(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "URL is invalid") {
				t.Fatalf("err = %v; want invalid URL", err)
			}
		})
	}
}

func TestFetchCatalogRejectsBadResponses(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer plain.Close()
	redirect := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", plain.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()
	notFound := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()

	tooLarge := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", maxRemoteCatalogSize+1))
	}))
	defer tooLarge.Close()

	badVersion, _ := json.Marshal(catalog.Catalog{Version: catalog.Version + 1, Plugins: []catalog.Entry{}})
	wrongVersion := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(badVersion)
	}))
	defer wrongVersion.Close()

	for name, server := range map[string]*httptest.Server{
		"http status":         notFound,
		"https downgrade":     redirect,
		"oversized response":  tooLarge,
		"unsupported version": wrongVersion,
	} {
		t.Run(name, func(t *testing.T) {
			client := server.Client()
			if client == nil {
				client = &http.Client{}
			}
			_, err := (&launcher{opt: Options{CatalogHTTPClient: client}}).fetchCatalog(context.Background(), server.URL)
			if err == nil {
				t.Fatal("fetch succeeded for invalid response")
			}
		})
	}
}

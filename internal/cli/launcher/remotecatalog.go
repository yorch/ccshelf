package launcher

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/yorch/ccshelf/internal/catalog"
	"github.com/yorch/ccshelf/internal/cli/clicore"
)

const maxRemoteCatalogSize = 8 << 20

func (l *launcher) fetchCatalog(ctx context.Context, rawURL string) (*clicore.CatalogData, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("configured remote catalog URL is invalid")
	}
	client := l.opt.CatalogHTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	} else {
		copy := *client
		client = &copy
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return fmt.Errorf("remote catalog redirected away from HTTPS")
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		if len(via) >= 5 {
			return fmt.Errorf("too many remote catalog redirects")
		}
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating remote catalog request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("retrieving remote catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("retrieving remote catalog: HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteCatalogSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading remote catalog: %w", err)
	}
	if len(body) > maxRemoteCatalogSize {
		return nil, fmt.Errorf("remote catalog exceeds %d bytes", maxRemoteCatalogSize)
	}
	var data catalog.Catalog
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decoding remote catalog: %w", err)
	}
	if data.Version != catalog.Version {
		return nil, fmt.Errorf("remote catalog version %d is unsupported (expected %d)", data.Version, catalog.Version)
	}
	if data.Plugins == nil {
		return nil, fmt.Errorf("remote catalog has no plugins list")
	}
	return &clicore.CatalogData{Catalog: &data, Source: "remote catalog " + u.Host}, nil
}

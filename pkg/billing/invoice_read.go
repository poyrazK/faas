package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/httpjson"
)

// InvoiceFactsReader bounds a single refresh operation. URLs are constructed
// inside provider adapters, never read from request bodies or pagination links.
type InvoiceFactsReader struct {
	BaseURL, Key, Provider, Version string
	Client                          *http.Client
	requests                        int
}

func (r *InvoiceFactsReader) Get(ctx context.Context, path string) (json.RawMessage, error) {
	if r.Key == "" {
		return nil, ErrNoAPIKey
	}
	if r.requests >= api.MaxInvoiceRefreshRequests {
		return nil, &InvoiceRefreshLimitError{Kind: "provider requests", Limit: api.MaxInvoiceRefreshRequests, Observed: r.requests + 1}
	}
	r.requests++
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(r.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if r.Provider == "stripe" {
		req.SetBasicAuth(r.Key, "")
		req.Header.Set("Stripe-Version", r.Version)
	} else {
		req.Header.Set("Authorization", "Bearer "+r.Key)
		if r.Provider == "paddle" {
			req.Header.Set("Paddle-Version", "1")
		}
	}
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: api.InvoiceProviderRequestTimeout}
	}
	// Never forward billing credentials to redirects, including same-host
	// redirects that could point to a different provider resource.
	bounded := *client
	if bounded.Timeout <= 0 || bounded.Timeout > api.InvoiceProviderRequestTimeout {
		bounded.Timeout = api.InvoiceProviderRequestTimeout
	}
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := bounded.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: read invoice: %w", r.Provider, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: invoice read returned HTTP %d", r.Provider, resp.StatusCode)
	}
	var body json.RawMessage
	if err := httpjson.Decode(resp.Body, api.MaxInvoiceProviderResponseBytes, &body); err != nil {
		return nil, fmt.Errorf("%s: decode invoice facts: %w", r.Provider, err)
	}
	return body, nil
}

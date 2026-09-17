package scaleup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPFetcher is the minimal slice of *http.Client the scraper
// needs. Tests inject a fake to avoid a live gateway.
type HTTPFetcher interface {
	Get(ctx context.Context, url string) (*http.Response, error)
}

// HTTPPromScraper parses `gateway_requests_total{app="..."}` from
// gatewayd-internal's /metrics endpoint and returns the per-app cumulative
// request count. The scraper is purely a parser — the trigger's
// ring buffer owns the per-app deltas + windowing.
//
// The result is `(appID → count)`. App labels are bounded by the
// number of apps with traffic (same cardinality as the metric
// itself). The scraper is provided as a struct so tests can swap
// the Fetcher without a custom adapter.
type HTTPPromScraper struct {
	URL    string
	Client HTTPFetcher
}

// metricsResponseMaxBytes is deliberately well above the expected metrics
// payload while still bounding memory if the endpoint is misconfigured or
// compromised. A metrics scrape is a control-plane input, not a streaming
// payload, so an oversized response fails closed.
const metricsResponseMaxBytes = 1 << 20

// Scrape implements PromScraper. Reads the metrics body into memory up to
// metricsResponseMaxBytes (gatewayd-internal's /metrics is small — every
// request counter line is one row per app per code, so the body is bounded by
// `apps × 5 codes`). Returns a non-nil empty map on any HTTP / parse error so the
// trigger's Touch path treats the tick as a no-op without a spammy
// error log.
func (s *HTTPPromScraper) Scrape(ctx context.Context) (map[string]int64, error) {
	if s == nil || s.URL == "" || s.Client == nil {
		return map[string]int64{}, nil
	}
	resp, err := s.Client.Get(ctx, s.URL)
	if err != nil {
		return map[string]int64{}, fmt.Errorf("scaleup: scrape GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return map[string]int64{}, fmt.Errorf("scaleup: scrape status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, metricsResponseMaxBytes+1))
	if err != nil {
		return map[string]int64{}, fmt.Errorf("scaleup: scrape read: %w", err)
	}
	if int64(len(body)) > metricsResponseMaxBytes {
		return map[string]int64{}, fmt.Errorf("scaleup: scrape response exceeds %d bytes", metricsResponseMaxBytes)
	}
	return parseGatewayRequestsTotal(string(body)), nil
}

// parseGatewayRequestsTotal parses the Prometheus exposition for
// `gateway_requests_total{app="..."} ... <value>` rows. Sums across
// every label set that has an `app` label (the metric is per-app
// per-code; the trigger wants the per-app total). Returns
// `appID → total count`.
//
// The parser is intentionally tiny — gatewayd-internal emits only one metric
// family with the `app` label, and the surrounding exposition is
// stable. A more sophisticated parser (e.g. prometheus/common) would
// pull in a substantial dependency for what is fundamentally a
// 20-line parse loop.
func parseGatewayRequestsTotal(body string) map[string]int64 {
	out := map[string]int64{}
	for _, line := range strings.Split(body, "\n") {
		// Skip comments + empty lines + HELP/TYPE metadata.
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		// Match lines starting with gateway_requests_total. There
		// is no other metric in this exposition that prefix-
		// matches, so a startswith check is sufficient.
		const prefix = "gateway_requests_total{"
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		// Find the app="..." label. The label order is
		// unspecified, so a substring search is the safe
		// shape.
		idx := strings.Index(line, `app="`)
		if idx < 0 {
			continue
		}
		start := idx + len(`app="`)
		end := strings.IndexByte(line[start:], '"')
		if end < 0 {
			continue
		}
		appID := line[start : start+end]
		// The value is the last whitespace-separated token on the
		// line. Sum across all label tuples for this app.
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		out[appID] += int64(v)
	}
	return out
}

// NewHTTPPromScraper constructs the production scraper with a
// shared *http.Client wrapped in an adapter so the underlying
// transport keeps the connection to gatewayd-internal alive across ticks.
// Without this, every 1Hz tick would open a fresh TCP connection
// (60 conn/min to the local listener). The timeout is short (2s)
// because the scheduler cannot afford to wait on a slow gatewayd-internal —
// the trigger tick is 1s.
func NewHTTPPromScraper(url string) *HTTPPromScraper {
	return &HTTPPromScraper{
		URL:    url,
		Client: &httpClientAdapter{Client: &http.Client{Timeout: 2 * time.Second}},
	}
}

// httpClientAdapter bridges *http.Client to the HTTPFetcher
// interface. *http.Client.Get takes only a URL; the interface
// requires a context-aware signature so the caller can cancel the
// scrape on shutdown.
type httpClientAdapter struct {
	Client *http.Client
}

func (a *httpClientAdapter) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return a.Client.Do(req)
}

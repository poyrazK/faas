package scaleup

import (
	"bufio"
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

// metricsResponseMaxBytes bounds how much of the response a scrape reads.
// The body is parsed as it streams, so memory follows the number of apps,
// not the body size. The exposition holds one row per app per exact HTTP
// status code, so at density it outgrew the former 1 MiB cap, and an
// oversized scrape fails closed: scale-up and the recent-load mirror went
// blind together (ADR 20261104091530). This cap is far above any real node.
const metricsResponseMaxBytes = 32 << 20

// metricsLineMaxBytes bounds one exposition line; a longer one fails the
// scrape instead of growing the scanner's buffer.
const metricsLineMaxBytes = 64 << 10

// Scrape implements PromScraper. Reads the metrics body into memory up to
// metricsResponseMaxBytes (gatewayd-internal's /metrics is small — every
// request counter line is one row per app per code, so the body is bounded by
// `apps × 5 codes`). Returns a non-nil empty map on any HTTP / parse error so the
// trigger's Touch path treats the tick as a no-op without a spammy
// error log.
func (s *HTTPPromScraper) Scrape(ctx context.Context) (map[string]int64, error) {
	counts, _, err := s.scrape(ctx)
	return counts, err
}

// ScrapeLoad returns the per-app cumulative request counts and the per-app
// in-flight requests (`gateway_app_inflight_requests`) from one scrape. The
// scale-in mirror needs both: completions collapse while an overloaded app
// stalls, but its in-flight requests do not.
func (s *HTTPPromScraper) ScrapeLoad(ctx context.Context) (counts, inflight map[string]int64, err error) {
	return s.scrape(ctx)
}

// scrape fetches the exposition and parses both families as it streams.
// Any error returns empty maps.
func (s *HTTPPromScraper) scrape(ctx context.Context) (counts, inflight map[string]int64, err error) {
	if s == nil || s.URL == "" || s.Client == nil {
		return map[string]int64{}, map[string]int64{}, nil
	}
	resp, err := s.Client.Get(ctx, s.URL)
	if err != nil {
		return map[string]int64{}, map[string]int64{}, fmt.Errorf("scaleup: scrape GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return map[string]int64{}, map[string]int64{}, fmt.Errorf("scaleup: scrape status %d", resp.StatusCode)
	}
	limited := &io.LimitedReader{R: resp.Body, N: metricsResponseMaxBytes + 1}
	counts, inflight, err = parseGatewayLoad(limited)
	if err == nil && limited.N <= 0 {
		err = fmt.Errorf("scaleup: scrape response exceeds %d bytes", metricsResponseMaxBytes)
	}
	if err != nil {
		return map[string]int64{}, map[string]int64{}, fmt.Errorf("scaleup: scrape read: %w", err)
	}
	return counts, inflight, nil
}

// parseGatewayRequestsTotal parses the Prometheus exposition for
// `gateway_requests_total{app="..."} ... <value>` rows. Sums across
// every label set that has an `app` label (the metric is per-app
// per-code; the trigger wants the per-app total). Returns
// `appID → total count`.
//
// The parser is intentionally tiny — the exposition holds only the two
// app-labelled families the scheduler reads, and its shape is stable. A
// more sophisticated parser (e.g. prometheus/common) would pull in a
// substantial dependency for what is fundamentally a 20-line parse loop.
func parseGatewayRequestsTotal(body string) map[string]int64 {
	counts, _, _ := parseGatewayLoad(strings.NewReader(body))
	return counts
}

// parseGatewayAppInflight parses `gateway_app_inflight_requests{app="..."}`
// rows into `appID → requests in flight`. An app absent from a successful
// scrape has no in-flight requests on that gateway.
func parseGatewayAppInflight(body string) map[string]int64 {
	_, inflight, _ := parseGatewayLoad(strings.NewReader(body))
	return inflight
}

const (
	gatewayRequestsPrefix = "gateway_requests_total{"
	gatewayInflightPrefix = "gateway_app_inflight_requests{"
)

// parseGatewayLoad reads the exposition line by line and sums both
// families' samples by their app label, without holding the body.
func parseGatewayLoad(r io.Reader) (counts, inflight map[string]int64, err error) {
	counts, inflight = map[string]int64{}, map[string]int64{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), metricsLineMaxBytes)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, gatewayRequestsPrefix):
			addGatewayAppSample(counts, line)
		case strings.HasPrefix(line, gatewayInflightPrefix):
			addGatewayAppSample(inflight, line)
		}
	}
	return counts, inflight, scanner.Err()
}

// addGatewayAppSample adds one sample line's value to out under its app label.
func addGatewayAppSample(out map[string]int64, line string) {
	// Find the app="..." label. The label order is
	// unspecified, so a substring search is the safe
	// shape.
	idx := strings.Index(line, `app="`)
	if idx < 0 {
		return
	}
	start := idx + len(`app="`)
	end := strings.IndexByte(line[start:], '"')
	if end < 0 {
		return
	}
	appID := line[start : start+end]
	// The value is the last whitespace-separated token on the
	// line. Sum across all label tuples for this app.
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return
	}
	v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
	if err != nil {
		return
	}
	out[appID] += int64(v)
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

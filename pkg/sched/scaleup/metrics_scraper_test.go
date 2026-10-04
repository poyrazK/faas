// adr: 160 — scale-up metrics ingestion is bounded and fails closed.

package scaleup

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeHTTPFetcher is a minimal HTTPFetcher for table-driven tests
// of the parser. Reads a body + returns a 200 — the parser is the
// unit under test, not the HTTP client.
type fakeHTTPFetcher struct {
	body string
	err  error
}

func (f *fakeHTTPFetcher) Get(_ context.Context, _ string) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(f.body)),
	}, nil
}

// TestParseGatewayRequestsTotal pins the parser contract: every
// `gateway_requests_total{app="X"} <value>` row contributes to the
// per-app sum, comments and HELP/TYPE metadata are skipped, and
// non-matching metrics are ignored.
func TestParseGatewayRequestsTotal(t *testing.T) {
	body := `# HELP gateway_requests_total Total HTTP requests.
# TYPE gateway_requests_total counter
gateway_requests_total{app="app1",code="200"} 100
gateway_requests_total{app="app1",code="500"} 5
gateway_requests_total{app="app2",code="200"} 42
gateway_wake_latency_seconds_bucket{le="0.001"} 99
gateway_requests_total{plan="pro"} 7
`
	got := parseGatewayRequestsTotal(body)
	want := map[string]int64{
		"app1": 105, // 100 + 5
		"app2": 42,
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: got=%v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%q] = %d, want %d", k, got[k], v)
		}
	}
}

// TestHTTPPromScraper_ParseIntegration verifies the full path:
// the scraper reads the body, parses it, and returns the per-app
// map. Uses fakeHTTPFetcher so no live gatewayd-internal is needed.
func TestHTTPPromScraper_ParseIntegration(t *testing.T) {
	body := `gateway_requests_total{app="a1"} 100
gateway_requests_total{app="a1"} 50
gateway_requests_total{app="a2"} 7
`
	s := &HTTPPromScraper{
		URL:    "http://localhost/metrics",
		Client: &fakeHTTPFetcher{body: body},
	}
	got, err := s.Scrape(context.Background())
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if got["a1"] != 150 || got["a2"] != 7 {
		t.Errorf("got = %v, want {a1: 150, a2: 7}", got)
	}
}

// TestHTTPPromScraper_ScrapeLoad pins the scale-in input: one scrape yields
// both the per-app completion counts and the per-app in-flight requests, and
// neither family leaks into the other.
func TestHTTPPromScraper_ScrapeLoad(t *testing.T) {
	body := `# TYPE gateway_requests_total counter
gateway_requests_total{app="a1",code="200",plan="scale"} 100
gateway_requests_total{app="a1",code="504",plan="scale"} 4
# TYPE gateway_app_inflight_requests gauge
gateway_app_inflight_requests{app="a1"} 160
gateway_app_inflight_requests{app="a2"} 0
`
	s := &HTTPPromScraper{
		URL:    "http://localhost/metrics/gateway-requests",
		Client: &fakeHTTPFetcher{body: body},
	}
	counts, inflight, err := s.ScrapeLoad(context.Background())
	if err != nil {
		t.Fatalf("ScrapeLoad: %v", err)
	}
	if len(counts) != 1 || counts["a1"] != 104 {
		t.Errorf("counts = %v, want {a1: 104}", counts)
	}
	if len(inflight) != 2 || inflight["a1"] != 160 || inflight["a2"] != 0 {
		t.Errorf("inflight = %v, want {a1: 160, a2: 0}", inflight)
	}
	scrapeOnly, err := s.Scrape(context.Background())
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if len(scrapeOnly) != 1 || scrapeOnly["a1"] != 104 {
		t.Errorf("Scrape = %v, want {a1: 104} without in-flight samples", scrapeOnly)
	}

	failing := &HTTPPromScraper{URL: "http://localhost/metrics", Client: &fakeHTTPFetcher{err: io.ErrUnexpectedEOF}}
	if counts, inflight, err := failing.ScrapeLoad(context.Background()); err == nil || counts == nil || inflight == nil {
		t.Errorf("failing ScrapeLoad = (%v, %v, %v), want non-nil empty maps and an error", counts, inflight, err)
	}
}

// TestHTTPPromScraper_NilSafe verifies the nil-receiver contract.
func TestHTTPPromScraper_NilSafe(t *testing.T) {
	var s *HTTPPromScraper
	got, err := s.Scrape(context.Background())
	if err != nil {
		t.Errorf("nil Scrape returned error: %v", err)
	}
	if got == nil {
		t.Errorf("nil Scrape returned nil map, want empty map")
	}
	if len(got) != 0 {
		t.Errorf("nil Scrape returned %v, want empty", got)
	}
}

// TestHTTPPromScraper_EmptyURL verifies the empty-URL guard: a
// scraper constructed with URL="" returns an empty map without
// hitting the network.
func TestHTTPPromScraper_EmptyURL(t *testing.T) {
	s := &HTTPPromScraper{URL: "", Client: &fakeHTTPFetcher{body: "x"}}
	got, err := s.Scrape(context.Background())
	if err != nil {
		t.Errorf("empty URL returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty URL returned %v, want empty", got)
	}
}

func TestHTTPPromScraper_RejectsOversizedResponse(t *testing.T) {
	s := &HTTPPromScraper{
		URL:    "http://localhost/metrics",
		Client: &fakeHTTPFetcher{body: strings.Repeat("x", metricsResponseMaxBytes+1)},
	}
	got, err := s.Scrape(context.Background())
	if err == nil {
		t.Fatal("Scrape unexpectedly accepted an oversized response")
	}
	if len(got) != 0 {
		t.Fatalf("oversized response map = %v, want empty", got)
	}
}

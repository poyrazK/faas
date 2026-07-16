package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeBackend simulates routing + a parked app that wakes on demand.
type fakeBackend struct {
	mu       sync.Mutex
	app      App
	host     string
	upstream string // set once "woken"
	running  bool
	wakeErr  error
	wakes    int32
}

func (b *fakeBackend) Lookup(_ context.Context, host string) (App, bool) {
	if host == b.host {
		return b.app, true
	}
	return App{}, false
}

func (b *fakeBackend) Target(string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return b.upstream, true
	}
	return "", false
}

func (b *fakeBackend) Wake(_ context.Context, _ string) error {
	atomic.AddInt32(&b.wakes, 1)
	if b.wakeErr != nil {
		return b.wakeErr
	}
	b.mu.Lock()
	b.running = true // now Target will succeed
	b.mu.Unlock()
	return nil
}

func newTestHandler(t *testing.T) (*Handler, *fakeBackend, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from app"))
	}))
	t.Cleanup(upstream.Close)

	b := &fakeBackend{
		app:      App{ID: "app-1", Plan: api.PlanPro},
		host:     "jane-api.apps.dom",
		upstream: upstream.Listener.Addr().String(),
	}
	// Quiet logger: tests don't need slog output; the metrics assertion is the
	// real check. Production uses slog.Default() via NewHandler.
	return NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil))), b, upstream
}

func TestColdWakeReturns200AndHeader(t *testing.T) {
	h, b, _ := newTestHandler(t)

	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body)
	}
	if rec.Body.String() != "hello from app" {
		t.Errorf("body = %q", rec.Body.String())
	}
	if rec.Header().Get("x-faas-wake") != "cold" {
		t.Error("first request after park should carry x-faas-wake: cold (UX §6)")
	}
	if atomic.LoadInt32(&b.wakes) != 1 {
		t.Errorf("expected exactly 1 wake, got %d", b.wakes)
	}
}

func TestHotPathDoesNotWakeOrTagCold(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.running = true // already hot

	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("x-faas-wake") != "" {
		t.Error("warm request must not carry the cold header")
	}
	if atomic.LoadInt32(&b.wakes) != 0 {
		t.Error("hot path must not trigger a wake")
	}
}

func TestUnknownHost404(t *testing.T) {
	h, _, _ := newTestHandler(t)
	req := httptest.NewRequest("GET", "http://nope.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown host = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("error should be problem+json, got %q", ct)
	}
}

// TestAppsSuffixFilter asserts the spec §4.1 wildcard host guard: with a
// configured appsSuffix, any Host that doesn't match is 404'd without
// touching the routing table.
func TestAppsSuffixFilter(t *testing.T) {
	h, b, _ := newTestHandler(t)
	h.WithAppsSuffix(".apps.dom")

	// Matches suffix → reaches the fake backend → proxied.
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("matched suffix = %d, want 200", rec.Code)
	}

	// Doesn't match suffix → 404 (without ever calling b.Lookup).
	b.wakes = 0
	req = httptest.NewRequest("GET", "http://attacker.example/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("non-matching suffix = %d, want 404", rec.Code)
	}
	if atomic.LoadInt32(&b.wakes) != 0 {
		t.Error("non-matching suffix must not wake the app")
	}
}

// TestRequestIDRoundTrip asserts that x-faas-request-id is generated for every
// response and an inbound header overrides it (lets clients thread their own
// trace id).
func TestRequestIDRoundTrip(t *testing.T) {
	h, _, _ := newTestHandler(t)

	// 1) No inbound header → response carries a generated 32-char hex.
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	got := rec.Header().Get("x-faas-request-id")
	if len(got) != 32 {
		t.Errorf("generated rid len = %d, want 32 hex chars (got %q)", len(got), got)
	}

	// 2) Inbound header → response echoes it.
	req = httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	req.Header.Set("x-faas-request-id", "my-trace-id")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("x-faas-request-id"); got != "my-trace-id" {
		t.Errorf("inbound rid not echoed: got %q", got)
	}
}

func TestRateLimitReturns429(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.running = true
	b.app.Plan = api.PlanFree // burst 20

	got429 := false
	for i := 0; i < 30; i++ {
		req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			got429 = true
			if rec.Header().Get("Retry-After") == "" {
				t.Error("429 should include Retry-After")
			}
			break
		}
	}
	if !got429 {
		t.Error("exceeding the Free burst should yield 429")
	}
}

func TestConcurrentColdRequestsCoalesceToOneWake(t *testing.T) {
	h, b, _ := newTestHandler(t)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&b.wakes); got != 1 {
		t.Errorf("50 concurrent cold requests should trigger 1 wake, got %d", got)
	}
}

// TestMetricsSpec12 asserts the §12 metric names increment with the expected
// label sets on cold/404/429 paths. Names are dashboard dependencies — DO NOT
// rename without coordinating with deploy/grafana/.
func TestMetricsSpec12(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.SetWakeGateHook()

	// Cold path: +requests_total{200} +cold_wake_total +wake_latency_count.
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := testutil.ToFloat64(h.metrics.requests.WithLabelValues("app-1", "pro", "200")); got != 1 {
		t.Errorf("requests_total{200}=%v, want 1", got)
	}
	if got := testutil.ToFloat64(h.metrics.coldWake.WithLabelValues("app-1")); got != 1 {
		t.Errorf("cold_wake_total=%v, want 1", got)
	}
	if got := testutil.CollectAndCount(h.metrics.wakeLatency); got != 1 {
		t.Errorf("wake_latency series count=%v, want 1 (one observation)", got)
	}

	// Unknown host: +requests_total{404}.
	req = httptest.NewRequest("GET", "http://nope.apps.dom/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := testutil.ToFloat64(h.metrics.requests.WithLabelValues("-", "-", "404")); got != 1 {
		t.Errorf("requests_total{404}=%v, want 1", got)
	}

	// Rate limit (Free plan burst 20, 25 requests): +rate_limited_total{1}.
	h2, b2, _ := newTestHandler(t)
	h2.SetWakeGateHook()
	b2.app.Plan = api.PlanFree
	for i := 0; i < 25; i++ {
		req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
		rec = httptest.NewRecorder()
		h2.ServeHTTP(rec, req)
	}
	if got := testutil.ToFloat64(h2.metrics.rateLimited.WithLabelValues("app-1", "free")); got < 1 {
		t.Errorf("rate_limited_total=%v, want >=1", got)
	}
}

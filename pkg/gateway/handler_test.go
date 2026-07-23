package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// fakeBackend simulates routing + a parked app that wakes on demand.
type fakeBackend struct {
	mu        sync.Mutex
	app       App
	host      string
	upstream  string // set once "woken"
	running   bool
	wakeErr   error
	wakes     int32
	wakeIDOut string // value Wake() returns; empty → "fake-wake-id"
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

func (b *fakeBackend) Wake(_ context.Context, _ string) (string, error) {
	atomic.AddInt32(&b.wakes, 1)
	if b.wakeErr != nil {
		return "", b.wakeErr
	}
	b.mu.Lock()
	b.running = true // now Target will succeed
	b.mu.Unlock()
	if b.wakeIDOut != "" {
		return b.wakeIDOut, nil
	}
	return "fake-wake-id", nil
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
	// Per-wake stable ID flows from schedd's Wake() through the gateway
	// handler onto the response. fakeBackend's Wake returns the literal
	// "fake-wake-id" so this assertion locks down the wiring contract:
	// the response header must mirror what schedd returned, not be
	// regenerated or omitted by the gateway.
	if got := rec.Header().Get("x-faas-wake-id"); got != "fake-wake-id" {
		t.Errorf("x-faas-wake-id = %q, want fake-wake-id", got)
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
	if got := rec.Header().Get("x-faas-wake-id"); got != "" {
		t.Errorf("warm request must not carry x-faas-wake-id, got %q", got)
	}
	if atomic.LoadInt32(&b.wakes) != 0 {
		t.Error("hot path must not trigger a wake")
	}
}

// TestColdWakePropagatesUUIDv7WakeID asserts the response header matches
// the value the scheduler returned byte-for-byte. In production schedd
// mints a UUIDv7 (via google/uuid), so the contract is: header == whatever
// Wake returned, header is non-empty, header is a valid UUID. Catching
// drift between the gateway and the scheduler — e.g. if gatewayd starts
// regenerating IDs locally — is the whole point of this test.
func TestColdWakePropagatesUUIDv7WakeID(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.wakeIDOut = "0193f7c0-1234-7abc-9def-0123456789ab"

	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	got := rec.Header().Get("x-faas-wake-id")
	if got != b.wakeIDOut {
		t.Errorf("x-faas-wake-id = %q, want %q (scheduler value must flow through verbatim)", got, b.wakeIDOut)
	}
	if _, err := uuid.Parse(got); err != nil {
		t.Errorf("x-faas-wake-id %q is not a valid UUID: %v", got, err)
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

// --- writeWakeError -------------------------------------------------------

func TestWriteWakeError_QueueFull(t *testing.T) {
	rec := httptest.NewRecorder()
	writeWakeError(rec, ErrQueueFull)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "5" {
		t.Errorf("Retry-After = %q, want 5", rec.Header().Get("Retry-After"))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want problem+json", ct)
	}
}

func TestWriteWakeError_ProblemPassthrough(t *testing.T) {
	rec := httptest.NewRecorder()
	prob := api.NewProblem(http.StatusBadRequest, api.CodePlanLimitRAM, "plan", "hobby")
	writeWakeError(rec, prob)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "plan_limit_ram") {
		t.Errorf("body = %q, want code plan_limit_ram", rec.Body.String())
	}
}

func TestWriteWakeError_GenericError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeWakeError(rec, errors.New("upstream exploded"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "capacity") {
		t.Errorf("body = %q, want capacity error", rec.Body.String())
	}
}

// TestHostname — covers the hostname() helper that the handler uses to
// route requests by Host header (ignoring port).
func TestHostname(t *testing.T) {
	for _, tc := range []struct {
		host, want string
	}{
		{"example.com", "example.com"},
		{"example.com:8080", "example.com"},
		{"127.0.0.1:443", "127.0.0.1"},
		{"", ""},
	} {
		if got := hostname(tc.host); got != tc.want {
			t.Errorf("hostname(%q) = %q, want %q", tc.host, got, tc.want)
		}
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
	if got := histogramObservationCount(t, h.metrics.wakeLatency); got != 1 {
		t.Errorf("wake_latency _count = %v, want 1 (one observation)", got)
	}
	if got := histogramMeanObservation(t, h.metrics.wakeLatency); got <= 0 || got > 100*time.Millisecond {
		t.Errorf("wake_latency observation = %v, want (0, 100ms] for localhost stub", got)
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

// histogramObservationCount reads the histogram's _count via the Prometheus
// dto format. Used by the wake-latency regression to assert the histogram
// actually received an observation, not just emitted a series.
func histogramObservationCount(t *testing.T, h prometheus.Histogram) uint64 {
	t.Helper()
	m := &dto.Metric{}
	if err := h.(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("histogram write: %v", err)
	}
	if m.Histogram == nil {
		return 0
	}
	return m.Histogram.GetSampleCount()
}

// histogramMeanObservation returns the mean observation across every sample
// in the histogram (sum / count), in the histogram's base unit of seconds
// converted to time.Duration. With a single observation that's equivalent
// to that observation's value; with multiple observations it's the running
// mean. Empty histograms yield 0. The name says what the function does:
// a histogram's Prometheus exposition does not carry a per-sample
// timestamp, so callers that want "the most recent observation" need to
// scrape, store the previous exposure, and diff — this helper does not.
func histogramMeanObservation(t *testing.T, h prometheus.Histogram) time.Duration {
	t.Helper()
	m := &dto.Metric{}
	if err := h.(prometheus.Metric).Write(m); err != nil {
		t.Fatalf("histogram write: %v", err)
	}
	if m.Histogram == nil || m.Histogram.GetSampleCount() == 0 {
		return 0
	}
	return time.Duration(m.Histogram.GetSampleSum() / float64(m.Histogram.GetSampleCount()) * float64(time.Second))
}

// TestMetricsSpec12_FirstByteNotFullBody is the wake-timing regression: the
// histogram must reflect the time to first upstream response byte, not the
// time to drain the full upstream body. We construct an upstream that
// flushes headers immediately, then sleeps 100ms before writing the body,
// and assert the observed wake latency is well under what a full-body
// measurement would have produced.
func TestMetricsSpec12_FirstByteNotFullBody(t *testing.T) {
	const bodyGap = 100 * time.Millisecond

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush() // headers + status on the wire
		}
		time.Sleep(bodyGap) // upstream app "thinking"
		_, _ = io.WriteString(w, "body-after-delay")
	}))
	t.Cleanup(upstream.Close)

	b := &fakeBackend{
		app:      App{ID: "app-fb", Plan: api.PlanPro},
		host:     "firstbyte.apps.dom",
		upstream: upstream.Listener.Addr().String(),
	}
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))

	req := httptest.NewRequest("GET", "http://firstbyte.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// First-byte observation must be much shorter than the body gap would
	// suggest for a full-body measurement. We allow generous slack for
	// localhost jitter and Go scheduler stalls, but a full-body measurement
	// would land >= bodyGap.
	got := histogramMeanObservation(t, h.metrics.wakeLatency)
	if got == 0 {
		t.Fatal("wake_latency observation missing")
	}
	if got >= bodyGap {
		t.Errorf("wake_latency observation = %v, want < %v (first-byte, not full body)", got, bodyGap)
	}
	// Sanity: the observation should not be so small as to suggest the
	// trace fired before wakeStart (negative durations would be < 0; the
	// trace fires after the request's outbound socket connects, which is
	// after the handler's wake gate returns).
	if got < 0 {
		t.Errorf("wake_latency observation = %v, want > 0", got)
	}
}

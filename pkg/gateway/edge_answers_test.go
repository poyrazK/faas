package gateway

// spec: §12.6

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestEdgeAnswerFaviconDoesNotWake(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.Favicon = []byte("icon")
	// The shared test upstream is only reachable through a wake; a parked
	// favicon request must not increment the fake admission count.

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/favicon.ico", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "icon" {
		t.Fatalf("favicon = %d %q, want 200 icon", rec.Code, rec.Body.String())
	}
	if got := atomic.LoadInt32(b.Admits()); got != 0 {
		t.Fatalf("favicon admits = %d, want 0", got)
	}
	if got := testutil.ToFloat64(h.Metrics().edgeAnswered.WithLabelValues("favicon")); got != 1 {
		t.Fatalf("favicon metric = %v, want 1", got)
	}
}

func TestEdgeAnswerRobotsDefaultAndCustom(t *testing.T) {
	h, b, _ := newTestHandler(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/robots.txt", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != defaultRobotsTxt {
		t.Fatalf("default robots = %d %q, want allow-all", rec.Code, rec.Body.String())
	}
	b.app.RobotsTxt = "User-agent: *\nDisallow: /private\n"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/robots.txt", nil))
	if rec.Body.String() != b.app.RobotsTxt {
		t.Fatalf("custom robots = %q, want %q", rec.Body.String(), b.app.RobotsTxt)
	}
	if got := atomic.LoadInt32(b.Admits()); got != 0 {
		t.Fatalf("robots admits = %d, want 0", got)
	}
}

func TestParkedHeadUsesLastLiveHeadersWithoutWake(t *testing.T) {
	h, b, _ := newTestHandler(t)
	// A normal live request seeds the process-local header cache.
	rec := httptest.NewRecorder()
	h.proxyFor = func(addr string, _ int64) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("ETag", "\"live\"")
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "live")
		})
	}
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("seed request status = %d", rec.Code)
	}
	admits := atomic.LoadInt32(b.Admits())

	// Park the fake app and ask for HEAD /. HealthyCount is now zero, so the
	// gateway must use the cached headers and must not call Admit again.
	b.mu.Lock()
	b.targets = nil
	b.running = false
	b.mu.Unlock()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "http://jane-api.apps.dom/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("parked HEAD status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("ETag"); got != "\"live\"" {
		t.Fatalf("parked HEAD ETag = %q, want live header", got)
	}
	if got := atomic.LoadInt32(b.Admits()); got != admits {
		t.Fatalf("parked HEAD admits = %d, want %d", got, admits)
	}
	if got := testutil.ToFloat64(h.Metrics().edgeAnswered.WithLabelValues("head")); got != 1 {
		t.Fatalf("head metric = %v, want 1", got)
	}
}

func TestParkedHeadWithoutCacheReturns204(t *testing.T) {
	h, b, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "http://jane-api.apps.dom/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("uncached parked HEAD status = %d, want 204", rec.Code)
	}
	if got := atomic.LoadInt32(b.Admits()); got != 0 {
		t.Fatalf("uncached parked HEAD admits = %d, want 0", got)
	}
}

func TestHeadWakesOptInFallsThrough(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.HeadWakes = true
	h.proxyFor = func(addr string, _ int64) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
		})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "http://jane-api.apps.dom/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("waking HEAD status = %d, want 200", rec.Code)
	}
	if got := atomic.LoadInt32(b.Admits()); got != 1 {
		t.Fatalf("waking HEAD admits = %d, want 1", got)
	}
}

func TestHealthAnswerUsesLastWakeStateWithoutWaking(t *testing.T) {
	h, b, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("parked health status = %d, want 503", rec.Code)
	}
	if rec.Header().Get("X-Faas-Health-Source") != "edge" {
		t.Fatalf("health source = %q, want edge", rec.Header().Get("X-Faas-Health-Source"))
	}
	if got := atomic.LoadInt32(b.Admits()); got != 0 {
		t.Fatalf("health admits = %d, want 0", got)
	}
	if got := testutil.ToFloat64(h.Metrics().healthEdgeAnswered.WithLabelValues("app-1", "unhealthy")); got != 1 {
		t.Fatalf("health unhealthy metric = %v, want 1", got)
	}
}

func TestHealthAnswerRemembersSuccessfulWakeAfterParking(t *testing.T) {
	h, b, _ := newTestHandler(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("wake request status = %d, want 200", rec.Code)
	}
	b.mu.Lock()
	b.targets = nil
	b.running = false
	b.mu.Unlock()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() == "" {
		t.Fatalf("remembered health = %d %q, want edge 200 body", rec.Code, rec.Body.String())
	}
	if got := atomic.LoadInt32(b.Admits()); got != 1 {
		t.Fatalf("remembered health admits = %d, want 1", got)
	}
	if got := testutil.ToFloat64(h.Metrics().healthEdgeAnswered.WithLabelValues("app-1", "healthy")); got != 1 {
		t.Fatalf("health healthy metric = %v, want 1", got)
	}
}

func TestHealthPathWakesFallsThroughToOrigin(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.app.HealthPathWakes = true
	h.proxyFor = func(addr string, _ int64) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("waking health status = %d, want 200", rec.Code)
	}
	if got := atomic.LoadInt32(b.Admits()); got != 1 {
		t.Fatalf("waking health admits = %d, want 1", got)
	}
	if got := testutil.ToFloat64(h.Metrics().healthEdgeAnswered.WithLabelValues("app-1", "healthy")); got != 0 {
		t.Fatalf("waking health edge metric = %v, want 0", got)
	}
}

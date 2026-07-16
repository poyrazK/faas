package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
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
	return NewHandler(b), b, upstream
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

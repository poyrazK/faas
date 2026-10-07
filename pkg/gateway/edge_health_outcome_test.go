package gateway

// adr: 636 — the edge health answer follows the app's last instance.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func serveHealth(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/healthz", nil))
	return rec
}

// A gateway that never observed a wake (restarted, or not the node that ran
// it) answered 503 "unknown" for every parked app (H5-6). The app's last
// instance tells every gateway the same thing.
func TestHealthAnswerUsesLastInstanceOutcome(t *testing.T) {
	cases := []struct {
		name       string
		state      string
		found      bool
		err        error
		wantStatus int
		wantReason string
	}{
		{name: "parked", state: "parked", found: true, wantStatus: http.StatusOK},
		{name: "stopped", state: "stopped", found: true, wantStatus: http.StatusOK},
		{name: "failed", state: "failed", found: true, wantStatus: http.StatusServiceUnavailable, wantReason: "last_instance_failed"},
		{name: "never ran", wantStatus: http.StatusServiceUnavailable, wantReason: "unknown"},
		{name: "lookup error", err: errors.New("database unavailable"), wantStatus: http.StatusServiceUnavailable, wantReason: "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			h.WithHealthOutcomeLookup(func(context.Context, string) (string, bool, error) {
				return tc.state, tc.found, tc.err
			})
			rec := serveHealth(t, h)
			if rec.Code != tc.wantStatus {
				t.Fatalf("health status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := rec.Header().Get("X-Faas-Health-Reason"); got != tc.wantReason {
				t.Fatalf("health reason = %q, want %q", got, tc.wantReason)
			}
			if got := atomic.LoadInt32(b.Admits()); got != 0 {
				t.Fatalf("health admits = %d, want 0: an edge answer never wakes", got)
			}
		})
	}
}

// The durable outcome outranks a stale local observation: a wake that failed
// here days ago must not hide the successful wakes another gateway ran since.
func TestHealthAnswerPrefersLastInstanceOverLocalWakeFailure(t *testing.T) {
	h, _, _ := newTestHandler(t)
	h.markHealthFailure("app-1", errors.New("boot failed"))
	h.WithHealthOutcomeLookup(func(context.Context, string) (string, bool, error) {
		return "parked", true, nil
	})
	if rec := serveHealth(t, h); rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200 from the last instance", rec.Code)
	}
}

func TestHealthOutcomeLookupIsCachedPerApp(t *testing.T) {
	var calls atomic.Int32
	h, _, _ := newTestHandler(t)
	h.WithHealthOutcomeLookup(func(context.Context, string) (string, bool, error) {
		calls.Add(1)
		return "parked", true, nil
	})
	now := time.Now()
	h.healthOutcomes.now = func() time.Time { return now }
	for range 3 {
		serveHealth(t, h)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("lookups inside the TTL = %d, want 1", got)
	}
	now = now.Add(healthOutcomeTTL)
	serveHealth(t, h)
	if got := calls.Load(); got != 2 {
		t.Fatalf("lookups after the TTL = %d, want 2", got)
	}
}

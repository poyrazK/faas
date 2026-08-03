// Tests for pkg/gateway trace_handler.go (issue #555 PR-2).
//
// The handler is the customer-facing endpoint for trace retrieval.
// A regression in the auth gate (404 vs 401 vs 200) is a security
// bug — the tests pin each branch explicitly.
package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func setupHandler(t *testing.T, token string) (*gateway.TraceHandler, *gateway.TraceRing) {
	t.Helper()
	ring := gateway.NewTraceRing(10)
	now := time.Now()
	ring.Add(&gateway.Trace{
		TraceID: "trace-1",
		Spans: []gateway.SpanRow{
			{
				TraceID:  "trace-1",
				SpanID:   "span-1",
				Name:     "gateway.route",
				StartTime: now,
				EndTime:   now.Add(5 * time.Millisecond),
			},
		},
		Started:  now,
		LastSeen: now,
	})
	return gateway.NewTraceHandler(gateway.TraceHandlerConfig{
		Ring:          ring,
		ObserverToken: token,
	}), ring
}

func TestTraceHandler_Hit(t *testing.T) {
	h, _ := setupHandler(t, "secret-token")
	r := httptest.NewRequest(http.MethodGet, "/v1/traces/trace-1", nil)
	r.Header.Set("X-Faas-Trace-Auth", "secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got gateway.Trace
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, w.Body.String())
	}
	if got.TraceID != "trace-1" {
		t.Errorf("trace_id = %q, want trace-1", got.TraceID)
	}
}

func TestTraceHandler_Unauthorized(t *testing.T) {
	h, _ := setupHandler(t, "secret-token")
	r := httptest.NewRequest(http.MethodGet, "/v1/traces/trace-1", nil)
	r.Header.Set("X-Faas-Trace-Auth", "wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestTraceHandler_MissingAuthHeader(t *testing.T) {
	h, _ := setupHandler(t, "secret-token")
	r := httptest.NewRequest(http.MethodGet, "/v1/traces/trace-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestTraceHandler_Disabled(t *testing.T) {
	h, _ := setupHandler(t, "")
	r := httptest.NewRequest(http.MethodGet, "/v1/traces/trace-1", nil)
	r.Header.Set("X-Faas-Trace-Auth", "anything")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when observer token is empty", w.Code)
	}
}

func TestTraceHandler_NotFound(t *testing.T) {
	h, _ := setupHandler(t, "secret-token")
	r := httptest.NewRequest(http.MethodGet, "/v1/traces/no-such", nil)
	r.Header.Set("X-Faas-Trace-Auth", "secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestTraceHandler_MethodNotAllowed(t *testing.T) {
	h, _ := setupHandler(t, "secret-token")
	r := httptest.NewRequest(http.MethodPost, "/v1/traces/trace-1", nil)
	r.Header.Set("X-Faas-Trace-Auth", "secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
	if w.Header().Get("Allow") != "GET" {
		t.Errorf("Allow header = %q, want GET", w.Header().Get("Allow"))
	}
}

func TestTraceHandler_TrailingSlash(t *testing.T) {
	// /v1/traces/trace-1/ should match the same trace ID as
	// /v1/traces/trace-1. The mux passes the suffix; the handler
	// trims both ends.
	h, _ := setupHandler(t, "secret-token")
	r := httptest.NewRequest(http.MethodGet, "/v1/traces/trace-1/", nil)
	r.Header.Set("X-Faas-Trace-Auth", "secret-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("trailing-slash status = %d, want 200", w.Code)
	}
}

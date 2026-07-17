package middleware_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/middleware"
)

// TestRequestID_GeneratesWhenAbsent confirms an inbound request with
// no x-faas-request-id gets a fresh one and that it round-trips on the
// response header + context.
func TestRequestID_GeneratesWhenAbsent(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)

	var seen string
	h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = middleware.RequestIDFrom(r)
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(rec, r)

	got := rec.Header().Get("x-faas-request-id")
	if got == "" {
		t.Fatal("response missing x-faas-request-id header")
	}
	if len(got) != 32 {
		t.Errorf("request id length = %d, want 32 (16-byte hex)", len(got))
	}
	if seen != got {
		t.Errorf("ctx id = %q, want = %q", seen, got)
	}
}

// TestRequestID_PropagatesInbound confirms a client-supplied
// x-faas-request-id is preserved end-to-end.
func TestRequestID_PropagatesInbound(t *testing.T) {
	const inbound = "deadbeefdeadbeefdeadbeefdeadbeef"
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	r.Header.Set("x-faas-request-id", inbound)

	h := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := middleware.RequestIDFrom(r); got != inbound {
			t.Errorf("ctx id = %q, want = %q", got, inbound)
		}
	}))
	h.ServeHTTP(rec, r)

	if got := rec.Header().Get("x-faas-request-id"); got != inbound {
		t.Errorf("response id = %q, want = %q", got, inbound)
	}
}

// TestRecovery_Returns500OnPanic confirms a panicking handler produces
// a 500 RFC 7807 body and doesn't propagate the panic.
func TestRecovery_Returns500OnPanic(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/panic", nil)

	h := middleware.Recovery(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))

	// Must not panic out of ServeHTTP.
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":500`) {
		t.Errorf("body = %q, missing 500 in RFC 7807 payload", body)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/problem+json") {
		t.Errorf("content-type = %q, want application/problem+json", rec.Header().Get("Content-Type"))
	}
}

// TestRecovery_PassesHappyPath confirms non-panicking responses are
// unchanged (status, body, headers intact).
func TestRecovery_PassesHappyPath(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ok", nil)

	h := middleware.Recovery(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("x-custom", "yes")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	}))
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418", rec.Code)
	}
	if rec.Body.String() != "hi" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hi")
	}
	if rec.Header().Get("x-custom") != "yes" {
		t.Errorf("x-custom = %q, want yes", rec.Header().Get("x-custom"))
	}
}

// TestAuthLimit_BlocksAfterThreshold confirms 11 401s inside a 1m
// window turn the 11th-and-after into 429s. Drives a fake clock so the
// test doesn't sleep.
func TestAuthLimit_BlocksAfterThreshold(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := middleware.AuthLimitConfig{
		Window:      time.Minute,
		MaxFailures: 10,
		Now:         func() time.Time { return now },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	// Always 401.
	gate := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusUnauthorized) }
	// Build the middleware ONCE so the limiter state accumulates across
	// the loop (each call to middleware.AuthLimit returns a fresh limiter).
	h := middleware.AuthLimit(cfg)(http.HandlerFunc(gate))

	for i := 1; i <= 10; i++ {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = "203.0.113.10:55555"
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, want 401", i, rec.Code)
		}
		now = now.Add(time.Second)
	}
	// 11th attempt — within window — must be 429 with Retry-After.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.RemoteAddr = "203.0.113.10:55555"
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th: code = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "60" {
		t.Errorf("Retry-After = %q, want 60", rec.Header().Get("Retry-After"))
	}
}

// TestAuthLimit_WindowExpires confirms the limiter forgets failures
// once they age out of the window.
func TestAuthLimit_WindowExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := middleware.AuthLimitConfig{
		Window:      time.Minute,
		MaxFailures: 2,
		Now:         func() time.Time { return now },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	gate := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusUnauthorized) }
	// Build the middleware ONCE so failures accumulate.
	h := middleware.AuthLimit(cfg)(http.HandlerFunc(gate))

	fire := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = "203.0.113.20:55555"
		h.ServeHTTP(rec, r)
		return rec
	}
	if c := fire().Code; c != http.StatusUnauthorized {
		t.Fatalf("first: code = %d, want 401", c)
	}
	now = now.Add(10 * time.Second)
	if c := fire().Code; c != http.StatusUnauthorized {
		t.Fatalf("second: code = %d, want 401", c)
	}
	now = now.Add(10 * time.Second)
	// 3rd within window → limited.
	if c := fire().Code; c != http.StatusTooManyRequests {
		t.Fatalf("third: code = %d, want 429", c)
	}
	// Advance past the window — failures expire.
	now = now.Add(time.Minute)
	if c := fire().Code; c != http.StatusUnauthorized {
		t.Fatalf("after expiry: code = %d, want 401 (window reset)", c)
	}
}

// TestAuthLimit_DoesNotCountSuccess confirms non-401 responses don't
// accumulate against the bucket.
func TestAuthLimit_DoesNotCountSuccess(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := middleware.AuthLimitConfig{
		Window:      time.Minute,
		MaxFailures: 3,
		Now:         func() time.Time { return now },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for i := 1; i <= 50; i++ {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/probe", nil)
		r.RemoteAddr = "203.0.113.30:55555"
		middleware.AuthLimit(cfg)(ok).ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: code = %d, want 200", i, rec.Code)
		}
	}
}

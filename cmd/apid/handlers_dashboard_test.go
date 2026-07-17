package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDashboardHandler_RendersIndex confirms GET /dashboard/ returns
// 200 + the layout chrome (HTMX script, nav links) and the body
// from the index template.
func TestDashboardHandler_RendersIndex(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com", noopNotifier{}).handler()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"<!doctype html>",
		"htmx.org@2.0.4",
		"/dashboard/",
		"Dashboard is up",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestDashboardHandler_LoginPage confirms GET /login renders the
// magic-link form placeholder (slice-3 wires the real flow).
func TestDashboardHandler_LoginPage(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com", noopNotifier{}).handler()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<form method="POST" action="/login"`) {
		t.Errorf("body missing login form\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, `name="email"`) {
		t.Errorf("body missing email input\n--- body ---\n%s", body)
	}
}

// TestDashboardHandler_GeneratesRequestID confirms every dashboard
// response carries an x-faas-request-id header. The middleware
// generates one if the client didn't supply it.
func TestDashboardHandler_GeneratesRequestID(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com", noopNotifier{}).handler()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	srv.ServeHTTP(rec, r)

	rid := rec.Header().Get("x-faas-request-id")
	if rid == "" {
		t.Fatal("missing x-faas-request-id on dashboard response")
	}
	if len(rid) != 32 {
		t.Errorf("request id length = %d, want 32 (16-byte hex)", len(rid))
	}
}

// TestDashboardHandler_PropagatesInboundRequestID confirms an inbound
// x-faas-request-id round-trips on the response.
func TestDashboardHandler_PropagatesInboundRequestID(t *testing.T) {
	const inbound = "deadbeefdeadbeefdeadbeefdeadbeef"
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com", noopNotifier{}).handler()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	r.Header.Set("x-faas-request-id", inbound)
	srv.ServeHTTP(rec, r)

	if got := rec.Header().Get("x-faas-request-id"); got != inbound {
		t.Errorf("response id = %q, want = %q", got, inbound)
	}
}

// TestDashboardHandler_RecoversFromPanic confirms a panicking handler
// is caught by Recovery middleware and rendered as a 500 RFC 7807
// problem. Uses the dashboard handler directly (the handler we have
// today doesn't panic, so this test injects a panic via the route
// surface to confirm the chain).
func TestDashboardHandler_RecoversFromPanic(t *testing.T) {
	// Build a chain that mimics dashboardChain but with a panicking
	// inner handler. Same imports / pattern the production code uses.
	panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("template boom")
	})
	var h http.Handler = panicHandler
	h = middleware.RequestID(h)
	h = middleware.Recovery(slog.New(slog.NewTextHandler(io.Discard, nil)))(h)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/panic", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"status":500`) {
		t.Errorf("body = %q, want 500 in RFC 7807", rec.Body.String())
	}
}

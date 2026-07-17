package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// newAuthedDashboardServer seeds an account into a MemStore, builds a
// server with a real session.Manager, mints a cookie for that
// account, and returns (handler, cookie) so the authed tests can
// hit the gated /dashboard/* routes.
//
// Tests that intentionally probe the unauthenticated surface
// (TestDashboardHandler_LoginPage + TestDashboardHandler_RecoversFromPanic
// use the raw chain) call newServer() directly.
func newAuthedDashboardServer(t *testing.T) (http.Handler, *http.Cookie) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	cookie, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, 15*60_000_000_000)
	return srv.handler(), &http.Cookie{Name: sessionCookie, Value: cookie}
}

// TestDashboardHandler_RendersIndex confirms an authenticated
// GET /dashboard/ returns 200 + the layout chrome (HTMX script, nav
// links) and the body from the index template.
func TestDashboardHandler_RendersIndex(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	r.AddCookie(cookie)
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
		"Signed in as",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestDashboardHandler_LoginPage confirms GET /login renders the
// magic-link form (slice-3 wires the real flow).
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
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	r.AddCookie(cookie)
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
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	r.Header.Set("x-faas-request-id", inbound)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if got := rec.Header().Get("x-faas-request-id"); got != inbound {
		t.Errorf("response id = %q, want = %q", got, inbound)
	}
}

// TestDashboardHandler_AppsList confirms GET /dashboard/apps renders
// 200 for an authed user, even when there are no apps yet (the
// "create your first app" copy). Slice 4 wires the page; this is a
// smoke-level guard against regressions where the dashboardChain
// silently drops the route.
func TestDashboardHandler_AppsList(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/apps", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Apps") {
		t.Errorf("body missing apps-list header\n%s", body)
	}
	if !strings.Contains(body, "No apps yet") {
		t.Errorf("body missing empty-state copy\n%s", body)
	}
}

// TestDashboardHandler_UsageAndBillingAndAccount probe the three
// remaining dashboard routes — usage, billing, account. Slice 4 only
// requires these to render the layout (no data assertions beyond
// "header text is there" + 200); slice 6 wires SSE live updates.
func TestDashboardHandler_OtherPages(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	for _, path := range []string{"/dashboard/usage", "/dashboard/billing", "/dashboard/account"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, path, nil)
			r.AddCookie(cookie)
			srv.ServeHTTP(rec, r)
			if rec.Code != http.StatusOK {
				t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestDashboardHandler_RecoversFromPanic confirms a panicking handler
// is caught by Recovery middleware and rendered as a 500 RFC 7807
// problem. This intentionally uses a raw panicHandler — it does NOT
// hit the dashboard route, so no session cookie is required; it
// validates the middleware chain itself.
func TestDashboardHandler_RecoversFromPanic(t *testing.T) {
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

package main

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	srv := newServerWithDeps(store, log, "example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*60_000_000_000, "")
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

// TestDashboardAccountDPA_RendersMarkdown confirms the new
// session-authed DPA route (PR follow-up) renders the configured
// template inside the dashboard chrome. Sets a tmp DPA file via
// dpaPath on the server so the test does NOT depend on the production
// /etc/faas layout. The dashboard must surface the markdown text
// body (between <pre class="dpa">…</pre>) and the back-link to
// /dashboard/account. This regresses the "support@DOMAIN" gap —
// the dashboard now has a real link, not a placeholder.
func TestDashboardAccountDPA_RendersMarkdown(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "dpa@example.com", "free")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	cookie, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	tmp := t.TempDir()
	dpaPath := filepath.Join(tmp, "DPA.md")
	if err := os.WriteFile(dpaPath, []byte("# DPA\n\nThe operator processes your data for X."), 0o644); err != nil {
		t.Fatalf("write DPA: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*60_000_000_000, dpaPath)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/account/dpa", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	srv.handler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<pre class=\"dpa\">") {
		t.Errorf("body missing <pre class=\"dpa\">; got %s", body)
	}
	if !strings.Contains(body, "# DPA") {
		t.Errorf("body missing the markdown text\n--- body ---\n%s", body)
	}
	if !strings.Contains(body, "/dashboard/account") {
		t.Errorf("body missing back-link to /dashboard/account\n%s", body)
	}
}

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

// --- pure-unit tests for the dashboard helpers -----------------------

// TestAppListItem_WithLatestInstance covers the live-instance badge path:
// the helper should consult the latest map and render the matching
// state badge. Without the map entry the default badge is used.
func TestAppListItem_WithLatestInstance(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{})
	app := state.App{ID: "a1", Slug: "my-api", Status: state.AppActive}
	latest := map[string]state.Instance{
		"a1": {ID: "i1", AppID: "a1", State: string(state.StateRunning)},
	}
	item := srv.appListItem(t.Context(), app, latest, time.Now())
	if item.StateBadgeLabel == "" {
		t.Errorf("StateBadgeLabel empty, want a label for state=running")
	}
	if item.URL != "https://my-api.apps.example.com" {
		t.Errorf("URL = %q, want apps.example.com hostname", item.URL)
	}
}

// TestAppListItem_DefaultBadge: no latest instance → default badge.
func TestAppListItem_DefaultBadge(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{})
	app := state.App{ID: "a1", Slug: "ghost", Status: state.AppActive}
	item := srv.appListItem(t.Context(), app, map[string]state.Instance{}, time.Time{})
	if item.StateBadgeLabel == "" {
		t.Error("default badge label should not be empty")
	}
	if item.LastDeployed != "" {
		t.Errorf("LastDeployed = %q, want empty for zero time", item.LastDeployed)
	}
}

// TestRenderProblem_PureUnit exercises the standalone helper. Already
// covered by the dashboard route tests above, but pinning the wire
// shape here keeps it stable when the route wiring changes.
func TestRenderProblem_PureUnit(t *testing.T) {
	rec := httptest.NewRecorder()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	renderProblem(rec, log, errors.New("boom"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want 500", rec.Code)
	}
}

// TestDashboardManifestView confirms the state→dashboard adapter.
func TestDashboardManifestView(t *testing.T) {
	app := state.App{
		Manifest: state.AppManifest{
			Entrypoint: []string{"node", "server.js"},
			Env:        map[string]string{"FOO": "bar"},
			WorkingDir: "/app",
			Port:       8080,
			Healthz:    "/healthz",
			User:       "node",
		},
	}
	v := dashboardManifestView(app)
	if len(v.Entrypoint) != 2 || v.Entrypoint[0] != "node" || v.Port != 8080 || v.Healthz != "/healthz" {
		t.Errorf("got %+v", v)
	}
	if v.Env["FOO"] != "bar" {
		t.Errorf("env not propagated: %+v", v.Env)
	}
}

// TestHexPrefix covers both branches: short hash returns the zero
// sentinel; long hash renders the first 6 bytes as 12 hex chars.
func TestHexPrefix(t *testing.T) {
	// short hash
	if got := hexPrefix([]byte{1, 2, 3}); got != "000000000000" {
		t.Errorf("short hash = %q, want 000000000000", got)
	}
	// exactly 6 bytes
	got := hexPrefix([]byte{0xab, 0xcd, 0xef, 0x01, 0x23, 0x45})
	if got != "abcdef012345" {
		t.Errorf("6-byte hash = %q, want abcdef012345", got)
	}
}

// TestDashboardAccountView_NegativeCountClampedToZero: review finding #5
// path — a missing count (caller passes -1) must render as 0, never
// leak the sentinel.
func TestDashboardAccountView_NegativeCountClampedToZero(t *testing.T) {
	v := dashboardAccountView(state.Account{ID: "a1", Email: "x@example.com", Plan: "pro"}, -5)
	if v.AppCount != 0 {
		t.Errorf("AppCount = %d, want 0 (negative clamped)", v.AppCount)
	}
}

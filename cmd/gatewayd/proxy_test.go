package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestApidProxy_ForwardsApidPaths confirms requests on every prefix
// isApidPath covers reach the upstream (a fake apid) and the
// response body round-trips. The next handler is never invoked.
//
// Each prefix family gets a representative sample; together they
// pin the full public surface that apidProxy owns per issue #85.
func TestApidProxy_ForwardsApidPaths(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		if r.Header.Get("x-faas-request-id") == "" {
			t.Error("upstream request missing x-faas-request-id header (gatewayd should generate one)")
		}
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("apid: ok"))
	}))
	t.Cleanup(upstream.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next handler invoked; should have been proxied")
		w.WriteHeader(http.StatusTeapot)
	})
	handler := newApidProxy(upstream.URL, next, log)

	paths := []string{
		// /v1
		"/v1", "/v1/", "/v1/apps", "/v1/account", "/v1/events",
		"/v1/deployments/abc123/logs",
		// /dashboard
		"/dashboard", "/dashboard/", "/dashboard/apps", "/dashboard/apps/foo",
		// /oauth
		"/oauth/callback",
		// /login
		"/login", "/login/",
		// /auth/verify
		"/auth/verify", "/auth/verify/",
		// /logout
		"/logout", "/logout/",
		// /status
		"/status", "/status/", "/status/slo.json",
		// /healthz (CD probe target, issue #85)
		"/healthz",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("code = %d, want 200", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "apid: ok") {
				t.Errorf("body = %q, want proxied apid body", rec.Body.String())
			}
		})
	}
	if want := len(paths); upstreamHits != want {
		t.Errorf("upstream hits = %d, want %d", upstreamHits, want)
	}
}

// TestApidProxy_ForwardsNonGetMethods confirms the proxy passes the
// HTTP method through unchanged. POST is the load-bearing case for
// the /v1/* write surface (apps, deployments, secrets, crons,
// webhooks/stripe) and for /login + /logout, so a future regression
// that accidentally filters methods would be loud here.
func TestApidProxy_ForwardsNonGetMethods(t *testing.T) {
	var upstreamHits int
	var seenMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		seenMethod = r.Method
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("apid: ok"))
	}))
	t.Cleanup(upstream.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("next handler invoked; should have been proxied")
		w.WriteHeader(http.StatusTeapot)
	})
	handler := newApidProxy(upstream.URL, next, log)

	cases := []struct {
		method string
		path   string
	}{
		// /v1 writes (auth-required in production; the proxy
		// doesn't care — it just forwards).
		{http.MethodPost, "/v1/apps"},
		{http.MethodPatch, "/v1/apps/foo"},
		{http.MethodDelete, "/v1/apps/foo"},
		// Magic-link + session auth POSTs.
		{http.MethodPost, "/login"},
		{http.MethodPost, "/logout"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(c.method, c.path, nil)
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("code = %d, want 200", rec.Code)
			}
			if seenMethod != c.method {
				t.Errorf("upstream saw method %q, want %q", seenMethod, c.method)
			}
		})
	}
	if upstreamHits != len(cases) {
		t.Errorf("upstream hits = %d, want %d", upstreamHits, len(cases))
	}
}

// TestApidProxy_PassesThroughNonApidPaths confirms requests outside
// the apid prefix set fall through to the next handler (e.g.
// gateway.Handler's wake/proxy path) without touching apid.
//
// Pinning these negative cases defends the prefix discipline — bare
// HasPrefix("/v1") would match "/v1.zip" and silently steal
// customer-app paths. See isApidPath for the anchor.
func TestApidProxy_PassesThroughNonApidPaths(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		upstreamHits++
	}))
	t.Cleanup(upstream.Close)

	var nextHits int
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextHits++
		w.WriteHeader(http.StatusOK)
	})
	handler := newApidProxy(upstream.URL, next, log)

	// /v1 shadowing regressions
	paths := []string{
		"/", "/api/v1/apps", "/v1.zip", "/v1x",
		// /dashboard shadowing regressions
		"/dashboard.zip", "/dashboards", "/dashboardx",
		// /login, /logout, /status shadowing regressions
		"/loginfoo", "/logoutbar", "/status.json",
		// /auth/verify shadowing
		"/auth/verifyother",
		// /oauth without trailing slash (no exact /oauth route today)
		"/oauth",
		// /healthz shadowing
		"/healthzz",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("code = %d, want 200", rec.Code)
			}
		})
	}
	if upstreamHits != 0 {
		t.Errorf("upstream hits = %d, want 0 (paths should fall through)", upstreamHits)
	}
	if want := len(paths); nextHits != want {
		t.Errorf("next hits = %d, want %d", nextHits, want)
	}
}

// TestIsApidPath_TableDriven is the unit-test coverage for the
// issue #85 path-set. Pin both positive (every prefix the apid
// public surface needs) and negative (review-finding-#6-style
// shadowing regressions: bare HasPrefix("/v1") would match
// "/v1.zip").
//
// Anchor discipline: every anchored root matches exact + the
// "/" subtree. See hasApidPrefix.
func TestIsApidPath_TableDriven(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// /v1
		{"/v1", true},
		{"/v1/", true},
		{"/v1/apps", true},
		{"/v1/apps/foo", true},
		{"/v1/events", true},
		{"/v1/deployments/abc/logs", true},
		{"/v1.zip", false}, // shadowing regression
		{"/v1x", false},    // shadowing regression
		{"/api/v1/apps", false},

		// /dashboard
		{"/dashboard", true},
		{"/dashboard/", true},
		{"/dashboard/apps", true},
		{"/dashboard/apps/foo", true},
		// /cli-auth is the device-code approval page (spec §2.2).
		// Lives at the apid root, not under /dashboard/, so it
		// needs its own gatewayd allowlist entry — otherwise the
		// URL would 404 from the wake path on the public listener.
		{"/cli-auth", true},
		{"/cli-auth.zip", false}, // anchor regression (review finding #6)
		// Negative cases — review finding #6 regression tests.
		{"/dashboard.zip", false},
		{"/dashboards", false},
		{"/dashboardx", false},
		{"/Dashboard", false}, // case-sensitive

		// /oauth
		{"/oauth/callback", true},
		{"/oauth/", true},
		{"/oauth", false}, // no exact route

		// /login
		{"/login", true},
		{"/login/", true},
		{"/loginfoo", false},

		// /auth/verify
		{"/auth/verify", true},
		{"/auth/verify/", true},
		{"/auth/verifyother", false},

		// /logout
		{"/logout", true},
		{"/logout/", true},
		{"/logoutbar", false},

		// /status
		{"/status", true},
		{"/status/", true},
		{"/status/slo.json", true},
		{"/status.json", false}, // NOT under /status/

		// /healthz (issue #85: CD probe)
		{"/healthz", true},
		{"/healthz/", true},
		{"/healthzz", false},

		// Generic
		{"/", false},
		{"/cli-auth.zip", false}, // exact-match guard
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := isApidPath(c.path); got != c.want {
				t.Errorf("isApidPath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

// TestApidProxy_DisabledWhenTargetEmpty confirms the wrapper is a
// no-op when target is empty — every request goes to next.
func TestApidProxy_DisabledWhenTargetEmpty(t *testing.T) {
	var nextHits int
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextHits++
		w.WriteHeader(http.StatusOK)
	})
	handler := newApidProxy("", next, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", rec.Code)
	}
	if nextHits != 1 {
		t.Errorf("next hits = %d, want 1", nextHits)
	}
}

// TestApidProxy_UpstreamDown confirms a 503 RFC 7807 problem is
// emitted when apid is unreachable (instead of the stdlib's bare
// "EOF" connection-reset text).
func TestApidProxy_UpstreamDown(t *testing.T) {
	// Reserve a port we know is free, then immediately close the listener
	// so nothing answers.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := "http://" + ln.Addr().String()
	_ = ln.Close()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newApidProxy(addr, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next should not be called for apid paths")
	}), log)

	// /healthz is the canonical public probe (issue #85).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Errorf("content-type = %q, want application/problem+json", ct)
	}
	if !strings.Contains(rec.Body.String(), "apid_unavailable") {
		t.Errorf("body = %q, want apid_unavailable problem code", rec.Body.String())
	}
}

// TestApidProxy_HealthzEndToEnd exercises the full path that the
// cd-digitalocean.yml smoke test relies on (issue #85): real
// httptest upstream serving /healthz, apidProxy in front, request
// arrives via the public surface.
func TestApidProxy_HealthzEndToEnd(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("upstream path = %q, want /healthz", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	t.Cleanup(upstream.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newApidProxy(upstream.URL, http.NewServeMux(), log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %q, want JSON status:ok", rec.Body.String())
	}
}

// TestApidProxy_PassesRealClientIPInXForwardedFor pins issue #89's
// gatewayd half: every /v1/* (and /login, /auth/verify, /dashboard,
// etc.) request the apidProxy forwards must carry an X-Forwarded-For
// header whose value is the real client IP — the host portion of
// r.RemoteAddr at the gatewayd edge. apid's defaultClientIP trusts
// this header only when its own RemoteAddr is loopback (which it
// always is on this hop), so the pin here is what restores per-IP
// AuthLimit keying across the loopback hop.
//
// Failure mode: if a future regression stops pinning the header, or
// appends instead of overwrites (creating a multi-hop chain that
// apid's predicate rejects), every customer's bucket collapses and
// the spec §11 "10/min/IP" guarantee is silently violated.
func TestApidProxy_PassesRealClientIPInXForwardedFor(t *testing.T) {
	var seenXFF string
	var seenHeaderCount int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenXFF = r.Header.Get("X-Forwarded-For")
		seenHeaderCount = len(r.Header.Values("X-Forwarded-For"))
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("apid: ok"))
	}))
	t.Cleanup(upstream.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newApidProxy(upstream.URL, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler invoked; should have been proxied")
	}), log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	// Real client IP at the gatewayd edge — what gatewayd sees in
	// r.RemoteAddr before the loopback hop.
	req.RemoteAddr = "203.0.113.10:55555"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if seenXFF != "203.0.113.10" {
		t.Errorf("upstream X-Forwarded-For = %q, want %q", seenXFF, "203.0.113.10")
	}
	if seenHeaderCount != 1 {
		t.Errorf("upstream saw X-Forwarded-For %d times, want 1 (must overwrite, not append)", seenHeaderCount)
	}
}

// TestApidProxy_DoesNotSetXForwardedForWhenNoRemoteAddr pins the
// defensive side of issue #89's gatewayd half: when r.RemoteAddr is
// empty (the proxy is unreachable, or a test synthesises a bare
// request) the proxy must NOT inject an X-Forwarded-For — that
// would let a request without a real client IP trick apid's
// defaultClientIP into trusting an empty header (the header would
// be empty, the predicate falls back, but this test still confirms
// the gatewayd side doesn't write a bogus value).
//
// Failure mode: if a future regression unconditionally writes a
// header, a request with RemoteAddr="" would carry an empty
// X-Forwarded-For and a downstream apid's predicate would fall
// back to r.RemoteAddr (which is empty → "unknown"). The bucket
// still works, but the loopback-only trust predicate never had a
// chance to fire. This test pins that gatewayd never synthesises
// what apid might mistake for a trustable pin.
func TestApidProxy_DoesNotSetXForwardedForWhenNoRemoteAddr(t *testing.T) {
	var seenXFF string
	var seenHeaderCount int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenXFF = r.Header.Get("X-Forwarded-For")
		seenHeaderCount = len(r.Header.Values("X-Forwarded-For"))
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("apid: ok"))
	}))
	t.Cleanup(upstream.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newApidProxy(upstream.URL, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("next handler invoked; should have been proxied")
	}), log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/account", nil)
	// Empty RemoteAddr — the degenerate case. gatewayd must not
	// inject a header value, because there's no real IP to pin.
	req.RemoteAddr = ""
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if seenXFF != "" {
		t.Errorf("upstream X-Forwarded-For = %q, want empty (no RemoteAddr to pin from)", seenXFF)
	}
	if seenHeaderCount != 0 {
		t.Errorf("upstream saw X-Forwarded-For %d times, want 0", seenHeaderCount)
	}
}

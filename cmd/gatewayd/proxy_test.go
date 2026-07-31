package main

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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
		// /v1/auth/* — IAM-3 (ADR-039, issue #187 + #244 merged)
		// session surface. Subsumed by /v1; pinned explicitly
		// here so the contract survives any future apid-side
		// route-table refactor (PR #180 review finding #6).
		{"/v1/auth/logout", true},
		{"/v1/auth/sessions", true},
		{"/v1/auth/sessions/11111111-1111-1111-1111-111111111111", true},
		{"/v1/auth/sessions/revoke_all", true},
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

		// /signup (PR #180 — issue #165 PR #2 password surface).
		// Sibling anchored root of /login; matches exact + subtree.
		{"/signup", true},
		{"/signup/", true},
		{"/signupfoo", false},

		// /login/forgot (PR #180). Subtree of /login; the anchored
		// /login root already proxies it. apid's own router decides
		// whether the path is a real route.
		{"/login/forgot", true},
		{"/login/forgot/", true},
		{"/login/forgotfoo", true}, // falls under /login subtree

		// /auth/verify (legacy magic-link consume, M7.5).
		{"/auth/verify", true},
		{"/auth/verify/", true},
		{"/auth/verifyother", false},

		// /auth/reset (PR #180 — issue #165 PR #2 password reset).
		// Sibling anchored root of /auth/verify; matches exact +
		// subtree. /auth/reset/anything reaches apid; /auth/resetfoo
		// does NOT (anchor regression, review finding #6).
		{"/auth/reset", true},
		{"/auth/reset/", true},
		{"/auth/reset/abc", true},
		{"/auth/resetfoo", false},

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

// TestIsApidLogsPath pins the AppLogsHandler carve-out matcher
// (issue #254 / Move 4 PR-2). The handler claims the
// `/v1/apps/{slug}/logs` family; the apidProxy short-circuits
// these requests to it before the loopback hop. Anchor
// discipline: a bare `/v1/apps/{slug}/logsbar` MUST NOT match
// (review finding #6 from the proxy_test.go history).
func TestIsApidLogsPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Happy paths.
		{"/v1/apps/foo/logs", true},
		{"/v1/apps/foo/logs/", true},
		// Query stripping happens at r.URL.Path — the matcher
		// never sees `?follow=1`. Pin that the matcher is just
		// on the path component.
		// Shadowing regressions.
		{"/v1/apps/foo/logsbar", false},
		{"/v1/apps//logs", false},
		{"/v1/apps/foo", false},
		{"/v1/apps", false},
		{"/v1", false},
		{"/v1.zip", false},
		// Per-spec the matched route is GET-only; the path
		// match shape is method-agnostic, so the apidProxy's
		// GET filter is a separate concern (the
		// `mux.Handle("GET /v1/apps/{slug}/logs", ...)` line
		// in main.go enforces that).
		{"/dashboard/foo/logs", false},
		{"", false},
		{"/", false},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := isApidLogsPath(c.path); got != c.want {
				t.Errorf("isApidLogsPath(%q) = %v, want %v", c.path, got, c.want)
			}
		})
	}
}

// TestApidProxy_LogsCarveOutToHandler pins the wiring: when
// isApidLogsPath matches AND logsHandler is wired, the proxy
// dispatches to logsHandler and does NOT touch the apid
// upstream. When logsHandler is nil, the carve-out is silently
// disabled (the path falls through to apid like every other
// /v1/* path).
func TestApidProxy_LogsCarveOutToHandler(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("apid: ok"))
	}))
	t.Cleanup(upstream.Close)

	var logsHits int
	logsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logsHits++
		if r.URL.Path != "/v1/apps/foo/logs" {
			t.Errorf("logsHandler saw path %q, want /v1/apps/foo/logs", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("logs: streamed"))
	})

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newApidProxyWithLogs(upstream.URL,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("next handler invoked; should have been proxied")
		}),
		logsHandler, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/apps/foo/logs", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", rec.Code)
	}
	if logsHits != 1 {
		t.Errorf("logsHandler hits = %d, want 1", logsHits)
	}
	if upstreamHits != 0 {
		t.Errorf("upstream hits = %d, want 0 (logs path must not proxy to apid)", upstreamHits)
	}
	if !strings.Contains(rec.Body.String(), "logs: streamed") {
		t.Errorf("body = %q, want the logs handler envelope", rec.Body.String())
	}
}

// TestApidProxy_LogsCarveOutPrecedesAPIDRouting — table-driven
// expansion of TestApidProxy_LogsCarveOutToHandler that pins the
// dispatch order for the negative cases the single-shot test
// doesn't cover. apidProxy.ServeHTTP MUST check isApidLogsPath
// BEFORE isApidPath (cmd/gatewayd/proxy.go:110-120) — the table
// rows prove that:
//   - paths that look like logs routes dispatch to logsHandler;
//   - paths that match the apid prefix but NOT the logs matcher
//     dispatch to apid;
//   - paths that match neither fall through to next.
//
// Without this test a future refactor that swaps the order in
// ServeHTTP would silently send /v1/apps/foo/logs to apid and the
// customer-facing log stream would 404. The single-shot test only
// pins the happy path; this test pins the precedence at the
// dispatch layer, not the matcher layer.
func TestApidProxy_LogsCarveOutPrecedesAPIDRouting(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		wantTarget string // "logs", "apid", or "next"
	}{
		// Logs carve-out wins.
		{"/v1/apps/foo/logs exact", "/v1/apps/foo/logs", "logs"},
		{"/v1/apps/foo/logs/ trailing slash", "/v1/apps/foo/logs/", "logs"},
		// Anchored prefix accepts any slug — same as isApidLogsPath
		// matches anything that satisfies the (prefix + /logs) shape.
		{"/v1/apps/foobar/logs", "/v1/apps/foobar/logs", "logs"},
		// apid prefix wins (not logs, not next).
		{"/v1/apps/foo no logs suffix", "/v1/apps/foo", "apid"},
		{"/v1/apps/foo/logsbar anchor regression guard", "/v1/apps/foo/logsbar", "apid"},
		{"/v1/apps//logs empty slug", "/v1/apps//logs", "apid"},
		// No /v1 prefix at all — fall through to next.
		{"/api/v2/foo outside /v1", "/api/v2/foo", "next"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var upstreamHits, logsHits, nextHits int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamHits++
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("apid: ok"))
			}))
			t.Cleanup(upstream.Close)

			logsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logsHits++
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("logs: ok"))
			})

			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextHits++
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("next: ok"))
			})

			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler := newApidProxyWithLogs(upstream.URL, next, logsHandler, log)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			handler.ServeHTTP(rec, req)

			switch tc.wantTarget {
			case "logs":
				if logsHits != 1 {
					t.Errorf("logs handler hits = %d, want 1 (path %q should route to logs)", logsHits, tc.path)
				}
				if upstreamHits != 0 {
					t.Errorf("apid upstream hits = %d, want 0 (logs carve-out must beat apid routing) for path %q", upstreamHits, tc.path)
				}
				if nextHits != 0 {
					t.Errorf("next hits = %d, want 0 (logs carve-out must not fall through) for path %q", nextHits, tc.path)
				}
				if !strings.Contains(rec.Body.String(), "logs: ok") {
					t.Errorf("body = %q, want logs handler envelope", rec.Body.String())
				}
			case "apid":
				if upstreamHits != 1 {
					t.Errorf("apid upstream hits = %d, want 1 (path %q should proxy to apid)", upstreamHits, tc.path)
				}
				if logsHits != 0 {
					t.Errorf("logs handler hits = %d, want 0 (path %q is not a logs route)", logsHits, tc.path)
				}
				if nextHits != 0 {
					t.Errorf("next hits = %d, want 0 (path %q matched the apid prefix)", nextHits, tc.path)
				}
			case "next":
				if nextHits != 1 {
					t.Errorf("next hits = %d, want 1 (path %q must fall through)", nextHits, tc.path)
				}
				if upstreamHits != 0 {
					t.Errorf("apid upstream hits = %d, want 0 (path %q is not an apid path)", upstreamHits, tc.path)
				}
				if logsHits != 0 {
					t.Errorf("logs handler hits = %d, want 0 (path %q is not a logs route)", logsHits, tc.path)
				}
			default:
				t.Fatalf("unknown wantTarget %q", tc.wantTarget)
			}
		})
	}
}

// TestApidProxy_LogsCarveOutMethodFilter pins the method-filter
// contract: isApidLogsPath matches on path only (no method check),
// so a POST to /v1/apps/{slug}/logs also dispatches through the
// carve-out to the logsHandler. The logsHandler in production is a
// *http.ServeMux that registered the route with
// `mux.Handle("GET /v1/apps/{slug}/logs", ...)` — the mux's
// method-aware dispatcher returns 405 Method Not Allowed for the
// POST. This test mirrors that shape so a future regression that
// filters methods in the proxy (or drops the GET filter on the
// mux registration) is loud.
//
// Failure mode: a future refactor that adds a method filter to
// the carve-out would dispatch POST /v1/apps/{slug}/logs to apid
// (where the route is gone) and the customer would see a 404
// instead of the 405 the SDK treats as "method not allowed".
func TestApidProxy_LogsCarveOutMethodFilter(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		upstreamHits++
	}))
	t.Cleanup(upstream.Close)

	// Production-shape logs handler: a tiny ServeMux with a
	// GET-only registration. The mux returns 405 for non-GET
	// requests (stdlib contract), which is the contract we pin.
	logsMux := http.NewServeMux()
	logsMux.HandleFunc("GET /v1/apps/{slug}/logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("logs: streamed"))
	})

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newApidProxyWithLogs(upstream.URL,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("next handler invoked; should have been proxied to logs handler")
		}),
		logsMux, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/apps/foo/logs", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /v1/apps/foo/logs code = %d, want 405 (mux GET filter)", rec.Code)
	}
	if upstreamHits != 0 {
		t.Errorf("upstream hits = %d, want 0 (carve-out must dispatch before apid)", upstreamHits)
	}
}

// TestApidProxy_LogsCarveOutDisabledWhenHandlerNil pins the
// disabled case: when logsHandler is nil, the carve-out is
// silently skipped and the path flows through to apid like
// every other /v1/* path. This is the test-suite default — the
// production gatewayd wires the handler; tests don't.
func TestApidProxy_LogsCarveOutDisabledWhenHandlerNil(t *testing.T) {
	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("apid: ok"))
	}))
	t.Cleanup(upstream.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := newApidProxyWithLogs(upstream.URL,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("next handler invoked; should have been proxied")
		}),
		nil, log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/apps/foo/logs", nil)
	handler.ServeHTTP(rec, req)

	if upstreamHits != 1 {
		t.Errorf("upstream hits = %d, want 1 (logs path falls through to apid when handler is nil)", upstreamHits)
	}
	if !strings.Contains(rec.Body.String(), "apid: ok") {
		t.Errorf("body = %q, want the apid upstream envelope", rec.Body.String())
	}
}

// TestApidPathReservations_Documented is the drift-protection guard
// for spec §4.1.1: every apidRoot* constant declared in
// cmd/gatewayd/proxy.go (the matcher source-of-truth) must appear
// verbatim in docs/faas_implementation_spec.md so customer-facing
// docs match the platform's reservation list.
//
// The matcher is the source of truth; the spec is documentation
// that must match. If a future change adds a new apidRoot* constant
// without updating the spec, this test fails. The test reads the
// spec from the repo root relative to this file (the cmd/gatewayd/
// test binary is built and run from the repo root, so the relative
// path resolves to the repo-root copy of the spec).
//
// The reverse direction (spec mentions a path the matcher doesn't
// cover) is NOT pinned here — the spec might intentionally document
// upcoming reservations, and the matcher is the live contract. The
// one-way drift is what matters for "the customer-facing note in
// the spec stays accurate to what gatewayd actually reserves".
func TestApidPathReservations_Documented(t *testing.T) {
	specPath := "../../docs/faas_implementation_spec.md"
	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}
	body := string(data)

	// Find the §4.1.1 reservation section. We scan for the heading
	// and assert each constant appears within the next ~10kB so a
	// future spec rewrite that drops the heading (or moves the
	// section) is loud.
	const heading = "#### 4.1.1 Platform path reservations"
	idx := strings.Index(body, heading)
	if idx < 0 {
		t.Fatalf("spec missing %q section", heading)
	}
	end := idx + 10_000
	if end > len(body) {
		end = len(body)
	}
	section := body[idx:end]

	// Pulled from cmd/gatewayd/proxy.go's apidRoot* const block
	// (proxy.go:233-246). Keep in sync with that block — this test
	// IS the guard that catches drift.
	wantConsts := []string{
		"/v1",
		"/dashboard",
		"/oauth/", // the matcher has apidRootOAuthPrefix = "/oauth/"; the spec subsection says "/oauth/... subtree (NOT bare /oauth)"
		"/login",
		"/signup",
		"/login/forgot",
		"/auth/verify",
		"/auth/reset",
		"/logout",
		"/status",
		"/healthz",
		"/cli-auth",
	}
	for _, want := range wantConsts {
		if !strings.Contains(section, want) {
			t.Errorf("spec §4.1.1 missing apidRoot constant %q — update the docs in the same PR that adds the constant", want)
		}
	}
}

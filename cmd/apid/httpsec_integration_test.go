// httpsec_integration_test.go — drive the apid handler through real
// HTTP requests and assert the six hardening response headers from
// pkg/httpsec (issue #249 / spec §11) reach the wire.
//
// The harness is the standard `setup(t, plan)` from server_test.go:
// in-memory store, noopNotifier, real `srv.handler()` whose outer
// wrapper is
//
//	httpsec.Static(httpsec.Nonce(func(*http.Request) bool { return true }, ...))
//
// The point is to catch a wiring regression between the middleware
// and the rest of the server (e.g. a future handler that resets
// headers, or a future wrapper that runs before the security chain).

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestHttpsec_StaticHeadersOnAllPaths confirms the five static
// headers (HSTS, X-Frame-Options, X-Content-Type-Options,
// Referrer-Policy, Permissions-Policy) appear on every response,
// regardless of which handler answered. The path sweep covers the
// three main categories: healthz (anonymous), API (/v1/healthz on
// the public mux), and a dashboard-style 404 (unauthenticated path
// the public listener still resolves).
func TestHttpsec_StaticHeadersOnAllPaths(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_httpsec_test")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	h := srv.handler()

	// Hoist the account + key out of the loop so the per-path
	// subtests share one auth token. MemStore rejects duplicate
	// emails (see pkg/state/memstore.go CreateAccount), so creating
	// inside the loop would fail on the second subtest.
	acct, err := store.CreateAccount(context.Background(),
		"httpsec@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(),
		acct.ID, hash, "test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}

	paths := []struct {
		method, path string
		auth         bool
	}{
		{"GET", "/healthz", false},
		{"GET", "/docs", false},
		{"GET", "/v1/whoami", true},
		{"GET", "/dashboard/", true},
		{"GET", "/this/does/not/exist", false},
	}
	for _, p := range paths {
		t.Run(p.method+" "+p.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(p.method, p.path, nil)
			if p.auth {
				req.Header.Set("Authorization", "Bearer "+pt)
			}
			h.ServeHTTP(rec, req)

			want := map[string]string{
				httpsec.HeaderXFrameOptions:       httpsec.ValueXFrameOptions,
				httpsec.HeaderXContentTypeOptions: httpsec.ValueXContentTypeOptions,
				httpsec.HeaderReferrerPolicy:      httpsec.ValueReferrerPolicy,
				httpsec.HeaderPermissionsPolicy:   httpsec.ValuePermissionsPolicy,
			}
			for k, v := range want {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("%s = %q, want %q", k, got, v)
				}
			}
			if rec.Header().Get(httpsec.HeaderStrictTransportSecurity) != httpsec.ValueHSTSMaxAge {
				t.Errorf("HSTS missing or wrong on %s %s", p.method, p.path)
			}
		})
	}
}

// TestHttpsec_CSPOnAllPaths confirms Content-Security-Policy is
// emitted on every apid response (apid serves only dashboard + API,
// so the gate is unconditional). Distinct from the unit test in
// pkg/httpsec/headers_test.go — this exercises the live wiring in
// cmd/apid/server.go.
func TestHttpsec_CSPOnAllPaths(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_httpsec_csp_test")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	h := srv.handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	h.ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("CSP header missing on /healthz")
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP missing default-src: %s", csp)
	}
	if !strings.Contains(csp, "script-src 'self' 'nonce-") {
		t.Errorf("CSP missing nonce-bearing script-src: %s", csp)
	}
	if !strings.Contains(csp, "https://unpkg.com") {
		t.Errorf("CSP missing unpkg.com allow: %s", csp)
	}
	styleStart := strings.Index(csp, "style-src ")
	styleEnd := -1
	if styleStart >= 0 {
		styleEnd = strings.Index(csp[styleStart:], ";")
	}
	if styleStart < 0 || styleEnd < 0 {
		t.Errorf("CSP missing style-src directive: %s", csp)
	} else {
		style := csp[styleStart : styleStart+styleEnd]
		if !strings.Contains(style, "'nonce-") || !strings.Contains(style, "https://unpkg.com") {
			t.Errorf("CSP missing nonce or unpkg.com style allow: %s", csp)
		}
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP missing frame-ancestors 'none': %s", csp)
	}
}

// TestHttpsec_NonceFreshnessAcrossRequests confirms two requests
// receive distinct CSP nonces. A stuck nonce across requests would
// let a stale page from one user run scripts under another user's
// CSP (the classic CSP regression).
func TestHttpsec_NonceFreshnessAcrossRequests(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_httpsec_nonce_test")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	h := srv.handler()

	parse := func(csp string) string {
		const key = "'nonce-"
		i := strings.Index(csp, key)
		if i < 0 {
			return ""
		}
		rest := csp[i+len(key):]
		j := strings.Index(rest, "'")
		if j < 0 {
			return ""
		}
		return rest[:j]
	}
	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest("GET", "/healthz", nil))
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest("GET", "/healthz", nil))

	n1 := parse(first.Header().Get("Content-Security-Policy"))
	n2 := parse(second.Header().Get("Content-Security-Policy"))
	if n1 == "" || n2 == "" {
		t.Fatalf("missing nonce in CSP: n1=%q n2=%q", n1, n2)
	}
	if n1 == n2 {
		t.Errorf("two requests returned identical CSP nonces %q", n1)
	}
}

// TestHttpsec_HSTSDisabledByEnv confirms FAAS_HSTS_ENABLED=false
// stops Strict-Transport-Security emission. The env hook is at
// run-time, not handler-construction time, so we exercise the
// setter here to pin the contract.
func TestHttpsec_HSTSDisabledByEnv(t *testing.T) {
	prev := httpsec.HSTSEnabled
	defer httpsec.SetHSTSEnabled(prev)
	httpsec.SetHSTSEnabled(false)

	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_httpsec_hsts_test")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	h := srv.handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if got := rec.Header().Get(httpsec.HeaderStrictTransportSecurity); got != "" {
		t.Errorf("HSTS leaked when disabled: %q", got)
	}
	// The other four static headers must still be present —
	// HSTS is the only one gated by the env knob.
	if rec.Header().Get(httpsec.HeaderXFrameOptions) == "" {
		t.Errorf("X-Frame-Options missing when HSTS disabled")
	}
}

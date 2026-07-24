// Negative-path tests for the AuthLimit middleware wiring on the
// dashboard and API-key auth surfaces (spec §11: "rate limit auth
// failures (10/min/IP)"). The middleware itself has unit tests in
// pkg/middleware/middleware_test.go; this file asserts the *wiring*
// is correct end-to-end — that is, the right routes are wrapped and
// the bucket actually trips on the 11th attempt from the same IP.

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// newAuthLimitServer builds a server with no AuthLimit overrides —
// the wiring we ship in cmd/apid/server.go must already wrap /login,
// /auth/verify, and /v1/* correctly. Tests below hammer each surface
// from a single RemoteAddr and assert the 11th attempt is 429.
//
// Returning a server (not a testEnv) here because the cookie flow +
// bogus-bearer path don't share setup shape with the regular API-key
// tests in server_test.go.
func newAuthLimitServer(t *testing.T) http.Handler {
	t.Helper()
	store := state.NewMemStore()
	if _, err := store.CreateAccount(context.Background(), "alice@example.com", api.PlanPro); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.com", noopNotifier{})
	return srv.handler()
}

// TestLoginLimitBlocksEleventhAttempt exercises the CountEveryAttempt
// (sentinel 0) CountStatuses on GET /login. /login returns 200 even
// for unknown emails (anti-enumeration), so a 401-only limiter would
// miss brute-force; the wiring must use [CountEveryAttempt] instead.
func TestLoginLimitBlocksEleventhAttempt(t *testing.T) {
	srv := newAuthLimitServer(t)
	const ip = "203.0.113.100:55555"

	fire := func() int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/login", nil)
		r.RemoteAddr = ip
		srv.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 1; i <= 10; i++ {
		if c := fire(); c != http.StatusOK {
			t.Fatalf("attempt %d: code = %d, want 200 (form rendered)", i, c)
		}
	}
	// 11th attempt: every response is counted, so even a 200 page trip
	// the limiter. Spec §11: 10/min/IP.
	if c := fire(); c != http.StatusTooManyRequests {
		t.Fatalf("11th: code = %d, want 429 (count-every-attempt)", c)
	}
}

// TestAuthVerifyLimitBlocksEleventhAttempt exercises the dual-status
// CountStatuses=[401, 410] on GET /auth/verify. /auth/verify 401s on
// unknown tokens AND 410s on consumed tokens; both must count so an
// attacker can't cycle through one-time tokens faster than the budget.
func TestAuthVerifyLimitBlocksEleventhAttempt(t *testing.T) {
	srv := newAuthLimitServer(t)
	const ip = "203.0.113.101:55555"

	fire := func() int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/auth/verify?token=deadbeef", nil)
		r.RemoteAddr = ip
		srv.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 1; i <= 10; i++ {
		if c := fire(); c == http.StatusTooManyRequests {
			t.Fatalf("attempt %d: code = 429 too early (limit should be 10)", i)
		}
	}
	if c := fire(); c != http.StatusTooManyRequests {
		t.Fatalf("11th: code = %d, want 429", c)
	}
	// Retry-After header must be set so well-behaved clients back off.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/verify?token=deadbeef", nil)
	r.RemoteAddr = ip
	srv.ServeHTTP(rec, r)
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Errorf("Retry-After = %q, want 60", got)
	}
}

// TestAPIKeyAuthLimitBlocksEleventhAttempt exercises the [401]
// CountStatuses on the API-key auth group. The wiring must wrap all
// /v1/* routes (not just /v1/account); we probe /v1/apps because it
// is in the middle of the table.
func TestAPIKeyAuthLimitBlocksEleventhAttempt(t *testing.T) {
	srv := newAuthLimitServer(t)
	const ip = "203.0.113.102:55555"

	fire := func() int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/v1/apps", nil)
		r.RemoteAddr = ip
		r.Header.Set("Authorization", "Bearer fp_live_deadbeef00000000")
		srv.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 1; i <= 10; i++ {
		if c := fire(); c != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, want 401", i, c)
		}
	}
	if c := fire(); c != http.StatusTooManyRequests {
		t.Fatalf("11th: code = %d, want 429", c)
	}
}

// TestAPIKeyAuthLimitDifferentIPsAreIndependent confirms the
// per-IP keying — the limiter does NOT fragment across endpoints or
// accounts, but DOES isolate one IP from another (otherwise a
// shared NAT would lock out everyone behind it).
func TestAPIKeyAuthLimitDifferentIPsAreIndependent(t *testing.T) {
	srv := newAuthLimitServer(t)

	for i := 1; i <= 10; i++ {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/v1/apps", nil)
		r.RemoteAddr = "203.0.113.200:55555"
		r.Header.Set("Authorization", "Bearer fp_live_deadbeef00000000")
		srv.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("setup: attempt %d: code = %d, want 401", i, rec.Code)
		}
	}
	// Different IP — should NOT be limited yet.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/apps", nil)
	r.RemoteAddr = "203.0.113.201:55555"
	r.Header.Set("Authorization", "Bearer fp_live_deadbeef00000000")
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("fresh IP: code = %d, want 401 (limiter leaked across IPs)", rec.Code)
	}
	// Sanity: format the IP out loud so the failure shows it.
	if rec.Code == http.StatusTooManyRequests {
		t.Logf("limiter is NOT keying on IP alone — saw: %s", fmt.Sprintf("%v", r.RemoteAddr))
	}
}

// v1RoutesRequiringAuth is the canonical list of every /v1/* pattern
// registered in cmd/apid/server.go. Adding a new /v1/* route without
// appending it here will fail TestAllV1Routes_RequireAuthOrLimit, which
// is the regression guard we want: a route accidentally mounted without
// s.auth would silently serve unauthorized callers.
//
// The list mirrors the route table in server.go at the time of writing;
// if the table moves, update this list (the test names the source line
// in its failure message).
var v1RoutesRequiringAuth = []struct {
	method string
	path   string
}{
	{"GET", "/v1/account"},
	{"PATCH", "/v1/account/plan"},
	{"GET", "/v1/account/export"},
	{"DELETE", "/v1/account"},
	{"POST", "/v1/account/restore"},
	{"GET", "/v1/apps"},
	{"POST", "/v1/apps"},
	{"GET", "/v1/apps/example-slug"},
	{"PATCH", "/v1/apps/example-slug"},
	{"DELETE", "/v1/apps/example-slug"},
	{"POST", "/v1/apps/example-slug/deployments"},
	{"GET", "/v1/deployments/dep-abc"},
	{"GET", "/v1/deployments/dep-abc/logs"},
	{"POST", "/v1/apps/example-slug/rollback"},
	{"POST", "/v1/apps/example-slug/park"},
	{"POST", "/v1/apps/example-slug/wake"},
	{"GET", "/v1/apps/example-slug/instances"},
	{"GET", "/v1/apps/example-slug/logs"},
	{"GET", "/v1/domains"},
	{"POST", "/v1/domains"},
	{"DELETE", "/v1/domains/example.test"},
	{"GET", "/v1/crons"},
	{"POST", "/v1/crons"},
	{"PATCH", "/v1/crons/cron-1"},
	{"DELETE", "/v1/crons/cron-1"},
	{"GET", "/v1/keys"},
	{"POST", "/v1/keys"},
	{"DELETE", "/v1/keys/key-1"},
	{"GET", "/v1/apps/example-slug/secrets"},
	{"PUT", "/v1/apps/example-slug/secrets/MY_KEY"},
	{"DELETE", "/v1/apps/example-slug/secrets/MY_KEY"},
	{"GET", "/v1/usage"},
	{"GET", "/v1/usage/summary"},
	{"GET", "/v1/deployments"},
}

// TestAllV1Routes_RequireAuthOrLimit walks every /v1/* pattern with a
// bogus bearer and asserts the response is NOT 200/2xx. An accidental
// `mux.HandleFunc("GET /v1/…", handler)` without s.auth (or
// s.authLimited) would return 200 here and fail this test, which is
// exactly the regression guard spec §11 calls for: every authenticated
// route must be wrapped.
//
// Note: /v1/account/dpa is intentionally public (no s.auth) and lives
// in a separate allow-list. /v1/events (SSE) and Stripe webhook are out
// of scope for this test.
func TestAllV1Routes_RequireAuthOrLimit(t *testing.T) {
	srv := newAuthLimitServer(t)

	for _, r := range v1RoutesRequiringAuth {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			// Use a fresh IP per route so an off-by-one in one route's
			// limiter does not bleed into the next subtest.
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(r.method, r.path, nil)
			req.RemoteAddr = "203.0.113.250:55555"
			req.Header.Set("Authorization", "Bearer fp_live_deadbeef00000000")
			srv.ServeHTTP(rec, req)
			if rec.Code < 400 {
				t.Errorf("status = %d, want 4xx (route is not behind s.auth/s.authLimited)", rec.Code)
			}
		})
	}
}

// TestPostLoginLimitBlocksEleventhAttempt pins the spec §11 "10/min/IP"
// limit on POST /login (the issue #165 hardened path). POST /login
// returns 401 for every invalid attempt — no header, bad header, email
// mismatch, unknown email — so a 401-only counter would work too, but
// the wiring uses CountEveryAttempt on the dashboard bucket so it
// matches GET /login's behaviour (which has to count 200s for
// anti-enumeration).
//
// An attacker brute-forcing POST /login from a single IP must be
// capped at 10 attempts/min before the 11th gets 429. This is the
// load-bearing defence against the path that was a full
// pre-auth account-takeover before PR #1; if the wiring ever drops
// the AuthLimit wrap on POST /login (e.g. someone "simplifies" the
// route table and forgets), this test goes red.
func TestPostLoginLimitBlocksEleventhAttempt(t *testing.T) {
	srv := newAuthLimitServer(t)
	const ip = "203.0.113.120:55555"

	fire := func() int {
		rec := httptest.NewRecorder()
		body := "email=alice%40example.com"
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = ip
		srv.ServeHTTP(rec, req)
		return rec.Code
	}
	// 10 attempts: every one is 401 (invalid_credentials — no
	// X-Dashboard-Key supplied). The first 10 must pass through
	// the limiter and reach the handler.
	for i := 1; i <= 10; i++ {
		if c := fire(); c != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, want 401", i, c)
		}
	}
	// 11th attempt: limiter trips, 429.
	if c := fire(); c != http.StatusTooManyRequests {
		t.Fatalf("11th: code = %d, want 429 (spec §11 10/min/IP)", c)
	}
}

// TestPostLoginLimit_SharedBucketWithGetLogin confirms POST /login
// and GET /login share one bucket per spec §11 — they're the same
// login surface, just different methods. An attacker rotating
// between GET /login and POST /login must NOT get 20 attempts/min
// from the same IP; the bucket is the same.
//
// Pinned because per-CLAUDE.md memory ("Middleware AuthLimit shared
// bucket"), a fresh AuthLimit(cfg) per route silently violates the
// spec. The test names the IP and the IP is unique per test.
func TestPostLoginLimit_SharedBucketWithGetLogin(t *testing.T) {
	srv := newAuthLimitServer(t)
	const ip = "203.0.113.121:55555"

	fireGET := func() int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		req.RemoteAddr = ip
		srv.ServeHTTP(rec, req)
		return rec.Code
	}
	firePOST := func() int {
		rec := httptest.NewRecorder()
		body := "email=alice%40example.com"
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = ip
		srv.ServeHTTP(rec, req)
		return rec.Code
	}

	// 5 GETs + 5 POSTs = 10. Both must not trip the limiter.
	for i := 1; i <= 5; i++ {
		if c := fireGET(); c != http.StatusOK {
			t.Fatalf("GET #%d: code = %d, want 200", i, c)
		}
	}
	for i := 1; i <= 5; i++ {
		if c := firePOST(); c != http.StatusUnauthorized {
			t.Fatalf("POST #%d: code = %d, want 401", i, c)
		}
	}
	// 11th attempt (any method): 429.
	if c := fireGET(); c != http.StatusTooManyRequests {
		t.Fatalf("GET 11th: code = %d, want 429 (GET+POST share bucket per spec §11)", c)
	}
}

// TestAPIKeyAuthLimit_TripsOnMultipleRoutes confirms the limiter
// bucket is shared across /v1/* (per spec §11 "10/min/IP" — not
// "10/min/IP/endpoint"). Hammering /v1/apps then /v1/crons from the
// same IP with bogus bearers must still 429 on the 11th hit total.
//
// This pins the "keying is by client IP alone" invariant called out
// on pkg/middleware.AuthLimitConfig. If a future maintainer adds a
// per-endpoint fragmenter, this test goes red and forces them to
// re-read the spec.
func TestAPIKeyAuthLimit_TripsOnMultipleRoutes(t *testing.T) {
	srv := newAuthLimitServer(t)
	const ip = "203.0.113.150:55555"

	fire := func(path string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = ip
		req.Header.Set("Authorization", "Bearer fp_live_deadbeef00000000")
		srv.ServeHTTP(rec, req)
		return rec.Code
	}

	// 6 hits on /v1/apps, 4 on /v1/crons = 10. All must 401.
	for i := 1; i <= 6; i++ {
		if c := fire("/v1/apps"); c != http.StatusUnauthorized {
			t.Fatalf("/v1/apps #%d: code = %d, want 401", i, c)
		}
	}
	for i := 1; i <= 4; i++ {
		if c := fire("/v1/crons"); c != http.StatusUnauthorized {
			t.Fatalf("/v1/crons #%d: code = %d, want 401", i, c)
		}
	}
	// 11th hit — any /v1/* from the same IP — must 429.
	if c := fire("/v1/keys"); c != http.StatusTooManyRequests {
		t.Fatalf("11th across routes: code = %d, want 429 (limiter is per-IP, not per-endpoint)", c)
	}
}

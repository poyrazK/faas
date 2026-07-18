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

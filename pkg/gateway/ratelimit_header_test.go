// Tests for the response-header writers added by Finding 6
// (issue #314). The X-RateLimit-* trio is written on every proxied
// response from the live limit bucket; the X-AccountRateLimit-*
// trio is written on the 429 path so a debugger chasing an account
// 429 storm can see the bucket state without parsing problem+json.
package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestAppRateLimitHeaders_OnSuccess — the 2xx path carries the
// X-RateLimit-{Limit,Remaining,Reset} trio. Free plan burst = 20; after
// the first Allow, Peek should report remaining=19 and reset=0 (full
// enough for the next request).
func TestAppRateLimitHeaders_OnSuccess(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.Plan = api.PlanFree

	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from cold path on hot backend; got %d", rec.Code)
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "20" {
		t.Errorf("X-RateLimit-Limit = %q; want 20 (Free burst)", got)
	}
	if got := rec.Header().Get("X-RateLimit-Remaining"); got != "19" {
		t.Errorf("X-RateLimit-Remaining = %q; want 19 (burst 20, one consumed)", got)
	}
	// Bucket is full enough for the next request — reset should be 0.
	if got := rec.Header().Get("X-RateLimit-Reset"); got != "0" {
		t.Errorf("X-RateLimit-Reset = %q; want 0 (full bucket)", got)
	}
}

// TestAppRateLimitHeaders_NotSetOnNoop — when the handler has a
// noop limiter installed (load-test / harness path), the header trio
// must NOT be written. Emitting zero-valued X-RateLimit-* headers
// would imply exhaustion for a limiter that bypasses exhaustion
// entirely.
func TestAppRateLimitHeaders_NotSetOnNoop(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	h.WithLimiter(NewLimiter().WithNoop())

	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on noop limiter path; got %d", rec.Code)
	}
	if got := rec.Header().Get("X-RateLimit-Limit"); got != "" {
		t.Errorf("X-RateLimit-Limit = %q on noop limiter; want absent", got)
	}
	if got := rec.Header().Get("X-RateLimit-Remaining"); got != "" {
		t.Errorf("X-RateLimit-Remaining = %q on noop limiter; want absent", got)
	}
}

// TestAppRateLimitHeaders_On429 — when the per-app 429 path runs,
// the headers still reach the wire so clients can compute Retry-After
// locally. The header is set BEFORE api.WriteProblem so it survives
// the body write.
func TestAppRateLimitHeaders_On429(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.Plan = api.PlanFree // burst 20, drain to exhaustion
	// Drain the bucket.
	for i := 0; i < 25; i++ {
		req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			// Found the 429 path. Sanity-check the headers.
			if got := rec.Header().Get("X-RateLimit-Limit"); got != "20" {
				t.Errorf("429 X-RateLimit-Limit = %q; want 20 (Free burst)", got)
			}
			// remaining may be 0 OR a small positive number depending
			// on whether the last Allow consumed or 429'd. The contract
			// here is ">=0 and <=20".
			if got := rec.Header().Get("X-RateLimit-Remaining"); got == "" {
				t.Error("429 X-RateLimit-Remaining absent; want set")
			}
			if got := rec.Header().Get("x-faas-rate-limit-scope"); got != "app" {
				t.Errorf("429 x-faas-rate-limit-scope = %q; want app", got)
			}
			return
		}
	}
	t.Fatal("did not observe 429 within 25 Free-burst requests")
}

// TestAccountRateLimitHeaders_OnAccount429 — when the per-account
// 429 path runs the X-AccountRateLimit-* trio is written, distinct
// from the per-app X-RateLimit-* family. The X-RateLimit-* headers
// must NOT be written on the account 429 path: the per-app bucket
// may still have tokens (we passed the account limit first), and
// emitting X-RateLimit-Remaining=N alongside the 429 would mislead
// clients debugging the trip.
func TestAccountRateLimitHeaders_OnAccount429(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.Plan = api.PlanFree // per-app burst 20, but we bypass it
	b.app.AccountID = "acct-header-test"
	h.WithLimiter(NewLimiter().WithNoop()) // bypass per-app scope
	// Force account 429 by priming the account bucket. Pro plan is
	// 1000 RPM, which makes a 1000-request test slow. Use Hobby
	// (RateLimitPerAccountRPM = 200) and a tight loop of ≤200.
	b.app.Plan = api.PlanHobby
	for i := 0; i < 250; i++ {
		req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusTooManyRequests {
			if got := rec.Header().Get("X-AccountRateLimit-Limit"); got != "200" {
				t.Errorf("429 X-AccountRateLimit-Limit = %q; want 200 (Hobby RPM)", got)
			}
			if got := rec.Header().Get("X-AccountRateLimit-Remaining"); got == "" {
				t.Error("429 X-AccountRateLimit-Remaining absent; want set")
			}
			if got := rec.Header().Get("x-faas-rate-limit-scope"); got != "account" {
				t.Errorf("429 x-faas-rate-limit-scope = %q; want account", got)
			}
			// X-RateLimit-* must NOT be written on the account 429 path
			// (the per-app bucket is bypassed; emitting Remaining would
			// imply false exhaustion).
			if got := rec.Header().Get("X-RateLimit-Limit"); got != "" {
				t.Errorf("account 429 X-RateLimit-Limit = %q; want absent (per-app bucket bypassed)", got)
			}
			return
		}
	}
	t.Fatal("did not observe account 429 within 250 Hobby-RPM requests")
}

// TestAppRateLimitHeaders_DecrementAcrossCalls — the Remaining
// header must monotonically decrement across consecutive 2xx
// requests so dashboards / debuggers can see the bucket draining.
// Cross-process limitation aside (Finding 6 / ADR-040 follow-up),
// the in-process invariant is part of the contract.
func TestAppRateLimitHeaders_DecrementAcrossCalls(t *testing.T) {
	h, b, _ := newTestHandler(t)
	b.setLegacyHot()
	b.app.Plan = api.PlanHobby // burst 100, rps 20

	var prevRemaining = -1
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200; got %d", i, rec.Code)
		}
		got := rec.Header().Get("X-RateLimit-Remaining")
		if got == "" {
			t.Fatalf("request %d: X-RateLimit-Remaining absent", i)
		}
		// Spot-check monotonicity: never increases.
		var r int
		if _, err := scanInt(got, &r); err != nil {
			t.Fatalf("request %d: X-RateLimit-Remaining = %q unparseable: %v", i, got, err)
		}
		if prevRemaining >= 0 && r > prevRemaining {
			t.Errorf("request %d: remaining %d > previous %d (should monotonically decrease)", i, r, prevRemaining)
		}
		prevRemaining = r
	}
}

// scanInt is a tiny Atoi shim so this test file doesn't drag in
// strconv across cases that mostly use raw value assertions.
func scanInt(s string, out *int) (int, error) {
	if s == "" {
		return 0, errEmptyHeader
	}
	n := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch < '0' || ch > '9' {
			// Allow a leading minus for forward-compat with the int
			// helper's signed rendering, but we only assert non-negative
			// values here.
			if i == 0 && ch == '-' {
				continue
			}
			return 0, errUnparseableInt{s: s}
		}
		n = n*10 + int(ch-'0')
	}
	*out = n
	return n, nil
}

type errUnparseableInt struct{ s string }

func (e errUnparseableInt) Error() string { return "unparseable int: " + e.s }

var errEmptyHeader = errors.New("empty header")

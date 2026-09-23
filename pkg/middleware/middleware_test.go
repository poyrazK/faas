package middleware_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

func TestRequestID_NestedWrappersReuseContextID(t *testing.T) {
	var seen string
	h := middleware.RequestID(middleware.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = middleware.RequestIDFrom(r)
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/account", nil))
	if seen == "" || seen != rec.Header().Get("x-faas-request-id") {
		t.Fatalf("nested middleware id = %q, response = %q", seen, rec.Header().Get("x-faas-request-id"))
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
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if problem.Code != api.CodeInternal {
		t.Errorf("code = %q, want %q", problem.Code, api.CodeInternal)
	}
	if problem.Title != "Internal Error" {
		t.Errorf("title = %q, want %q", problem.Title, "Internal Error")
	}
	if problem.Hint == "" {
		t.Error("hint is empty; recovery errors must give the customer a next action")
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("body leaked panic value: %q", rec.Body.String())
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
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/problem+json") {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode limited response: %v", err)
	}
	if problem.Code != api.CodeAuthRateLimited {
		t.Errorf("code = %q, want %q", problem.Code, api.CodeAuthRateLimited)
	}
	if problem.Hint == "" {
		t.Error("hint is empty; rate-limit response must tell the customer what to do next")
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

// TestAuthLimit_CountsCustomStatus extends the bucket's failure-trigger
// list beyond [401] (used by /auth/verify, which 410s on consumed tokens
// in addition to 401ing on unknown ones).
func TestAuthLimit_CountsCustomStatus(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := middleware.AuthLimitConfig{
		Window:        time.Minute,
		MaxFailures:   2,
		Now:           func() time.Time { return now },
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		CountStatuses: []int{http.StatusUnauthorized, http.StatusGone},
	}
	gate := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "gone", http.StatusGone) }
	h := middleware.AuthLimit(cfg)(http.HandlerFunc(gate))

	fire := func() int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/auth/verify", nil)
		r.RemoteAddr = "203.0.113.40:55555"
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if c := fire(); c != http.StatusGone {
		t.Fatalf("first: code = %d, want 410", c)
	}
	now = now.Add(time.Second)
	if c := fire(); c != http.StatusGone {
		t.Fatalf("second: code = %d, want 410", c)
	}
	now = now.Add(time.Second)
	// Third attempt — 410s counted, must 429.
	if c := fire(); c != http.StatusTooManyRequests {
		t.Fatalf("third: code = %d, want 429", c)
	}
}

// TestAuthLimit_CountsAllAttempts covers the [0] sentinel (CountEveryAttempt)
// which counts every response regardless of status. Used on /login so
// anti-enumeration (200 even for unknown emails) doesn't blind the
// limiter to brute-force.
func TestAuthLimit_CountsAllAttempts(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := middleware.AuthLimitConfig{
		Window:        time.Minute,
		MaxFailures:   3,
		Now:           func() time.Time { return now },
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		CountStatuses: []int{middleware.CountEveryAttempt},
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := middleware.AuthLimit(cfg)(ok)

	fire := func() int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = "203.0.113.50:55555"
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 1; i <= 3; i++ {
		if c := fire(); c != http.StatusOK {
			t.Fatalf("attempt %d: code = %d, want 200", i, c)
		}
		now = now.Add(time.Second)
	}
	// 4th attempt — every response counted, must 429 even though status
	// is the happy 200.
	if c := fire(); c != http.StatusTooManyRequests {
		t.Fatalf("4th: code = %d, want 429 (count-every-attempt)", c)
	}
}

// TestAuthLimit_BlockLogStripsControlChars covers the CWE-117
// (CodeQL go/log-injection) regression. An attacker that can set
// x-faas-request-id could otherwise smuggle CR/LF into a JSON log
// line and produce extra events downstream of slog. The middleware
// must sanitize the path + request id before handing them to
// slog.Logger.Warn. We capture the JSON-encoded record and assert:
//  1. raw control characters are replaced with U+00B7 (middle dot),
//  2. nothing in the record contains a bare \n or \r before the
//     closing brace (one-line-per-event invariant).
//
// net/http refuses raw CR/LF in URL paths and header values at parse
// time (the actual defense-in-depth — see the request header parser
// in net/textproto), so we drive the sanitizer with VERTICAL TAB
// (U+000B), which is benign-looking and passes through the parser
// unchanged. That's the attacker-influenced byte the sanitizer must
// strip.
func TestAuthLimit_BlockLogStripsControlChars(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var buf bytes.Buffer
	cfg := middleware.AuthLimitConfig{
		Window:      time.Minute,
		MaxFailures: 1,
		Now:         func() time.Time { return now },
		Log:         slog.New(slog.NewJSONHandler(&buf, nil)),
	}
	h := middleware.AuthLimit(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))

	// First request primes the bucket: MaxFailures=1 means the NEXT
	// request from this IP is the one that logs the "auth_limit
	// blocked" warning.
	{
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = "203.0.113.60:55555"
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("priming request: code = %d, want 401", rec.Code)
		}
		now = now.Add(time.Second)
	}

	// Second request → 429 + warn log. Craft x-faas-request-id with an
	// attacker-influenced control character (vertical tab) that survives
	// header parsing but must be stripped before logging.
	buf.Reset()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.RemoteAddr = "203.0.113.60:55555"
	r.Header.Set("x-faas-request-id", "abc\x0bdef")
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("limited request: code = %d, want 429", rec.Code)
	}

	out := buf.String()
	if out == "" {
		t.Fatal("expected a warn log line, got none")
	}
	// One log record per event — slog JSON terminates each with \n.
	if strings.Contains(strings.TrimRight(out, "\n"), "\n") {
		t.Fatalf("log emitted multiple lines; log-injection regression: %q", out)
	}
	// slog.NewJSONHandler escapes \x0b as the literal sequence \u000b when
	// the raw byte reaches it. The unfixed code paths the unsanitized
	// RequestIDFrom value directly into slog, so the escaped sequence is
	// what leaks. The fixed code routes the value through
	// logsanitize.Field first, which replaces the VT with U+00B7 (·) so
	// the JSON encoder writes the raw middle-dot byte instead.
	if strings.Contains(out, `\u000b`) {
		t.Errorf("log contains unsanitized VT escape (CodeQL go/log-injection regression): %q", out)
	}
	if !strings.Contains(out, `request_id`) {
		t.Errorf("log missing request_id field: %q", out)
	}
}

// TestAuthLimit_ClientIPFromLoopbackHop_XForwardedFor pins the issue
// #89 fix: when apid receives a request via the gatewayd-internal → apid
// loopback hop (r.RemoteAddr is loopback), it must key the bucket on
// the X-Forwarded-For value gatewayd-internal pinned, NOT on the loopback
// address. Otherwise every customer's /v1/* traffic collapses to one
// bucket and one bad actor locks out the cohort.
//
// Failure mode: if a future regression stops defaultClientIP from
// trusting the loopback X-Forwarded-For, all 11 requests land in the
// 127.0.0.1 bucket and the 11th returns 429 — same symptom as the
// regression but inverted. This test asserts the BUCKET-WAS-CORRECT
// condition: two requests from different real IPs (via X-Forwarded-For)
// but the same loopback RemoteAddr land in DIFFERENT buckets (each
// gets its own 11-strike count before 429).
func TestAuthLimit_ClientIPFromLoopbackHop_XForwardedFor(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := middleware.AuthLimitConfig{
		Window:      time.Minute,
		MaxFailures: 2,
		Now:         func() time.Time { return now },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	gate := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusUnauthorized) }
	h := middleware.AuthLimit(cfg)(http.HandlerFunc(gate))

	// Two requests from "different customers" sharing the same
	// gatewayd-internal loopback hop. Each carries its real IP in
	// X-Forwarded-For; each must land in its own bucket.
	fire := func(xff string) int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/auth/verify", nil)
		r.RemoteAddr = "127.0.0.1:55555"
		r.Header.Set("X-Forwarded-For", xff)
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	// Customer A: 2 failures land in A's bucket (still < MaxFailures).
	if c := fire("203.0.113.10"); c != http.StatusUnauthorized {
		t.Fatalf("A first: code = %d, want 401", c)
	}
	if c := fire("203.0.113.10"); c != http.StatusUnauthorized {
		t.Fatalf("A second: code = %d, want 401", c)
	}
	// Customer B: starts a fresh bucket. If the bug regresses, B's
	// first request would land in A's already-full bucket and 429.
	if c := fire("198.51.100.7"); c != http.StatusUnauthorized {
		t.Fatalf("B first: code = %d, want 401 (would be 429 if X-Forwarded-For ignored)", c)
	}
	// Customer A: 3rd attempt trips A's bucket (now 3 >= MaxFailures).
	if c := fire("203.0.113.10"); c != http.StatusTooManyRequests {
		t.Fatalf("A third: code = %d, want 429", c)
	}
	// Customer B: still has 1 failure, must NOT be limited yet.
	if c := fire("198.51.100.7"); c != http.StatusUnauthorized {
		t.Fatalf("B second: code = %d, want 401 (still under threshold)", c)
	}
}

// TestAuthLimit_ClientIPFromNonLoopbackHop_IgnoresXForwardedFor pins
// the spoof-prevention claim of issue #89: a request that reaches
// apid from a NON-loopback RemoteAddr (e.g. a future deploy where
// apid binds a public interface, or a unit test that synthesises a
// direct connection) MUST NOT trust X-Forwarded-For — that header is
// trivially forgeable from any client. The bucket keys on
// r.RemoteAddr's host, full stop.
//
// Failure mode: if a future regression drops the loopback guard, an
// attacker can supply X-Forwarded-For to push their bucket key off
// their real IP and bypass the rate limit entirely. This test catches
// that by asserting the bucket keys on 203.0.113.99 (the RemoteAddr
// host), not on 198.51.100.7 (the spoofed header).
func TestAuthLimit_ClientIPFromNonLoopbackHop_IgnoresXForwardedFor(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := middleware.AuthLimitConfig{
		Window:      time.Minute,
		MaxFailures: 2,
		Now:         func() time.Time { return now },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	gate := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusUnauthorized) }
	h := middleware.AuthLimit(cfg)(http.HandlerFunc(gate))

	fire := func() int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/auth/verify", nil)
		// Non-loopback hop: a customer hitting apid directly, or a
		// unit test simulating one. apid must ignore X-Forwarded-For.
		r.RemoteAddr = "203.0.113.99:55555"
		// Attacker tries to spoof their bucket key by varying the
		// header. Both requests should land in the SAME bucket
		// (keyed on RemoteAddr=203.0.113.99), so the second trips
		// the 2-failure limit.
		r.Header.Set("X-Forwarded-For", "198.51.100.7")
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if c := fire(); c != http.StatusUnauthorized {
		t.Fatalf("first: code = %d, want 401", c)
	}
	if c := fire(); c != http.StatusUnauthorized {
		t.Fatalf("second: code = %d, want 401", c)
	}
	// Third attempt: if the header were trusted, this would land in
	// the spoofed bucket (still 1 failure) and return 401. But the
	// header is ignored on a non-loopback hop, so the real bucket
	// (203.0.113.99) trips at 3 >= MaxFailures.
	if c := fire(); c != http.StatusTooManyRequests {
		t.Fatalf("third: code = %d, want 429 (header must be ignored on non-loopback hop)", c)
	}
}

// TestAuthLimit_ClientIPFromLoopbackHop_MultipleXForwardedForFallsBack
// pins the "exactly one value" gate of issue #89's trust predicate:
// if X-Forwarded-For carries a multi-hop chain ("a, b") the value
// could have been forged by anyone upstream, so apid falls back to
// the loopback host. The customer sees the same defence-in-depth
// posture they would on a bare loopback RemoteAddr.
//
// Failure mode: if a future regression drops the comma check and
// trusts the leftmost value of a chain, an attacker can spoof by
// prepending their chosen IP. This test asserts that no element of
// the chain is trusted and the bucket stays on the loopback host.
func TestAuthLimit_ClientIPFromLoopbackHop_MultipleXForwardedForFallsBack(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := middleware.AuthLimitConfig{
		Window:      time.Minute,
		MaxFailures: 2,
		Now:         func() time.Time { return now },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	gate := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusUnauthorized) }
	h := middleware.AuthLimit(cfg)(http.HandlerFunc(gate))

	fire := func() int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/auth/verify", nil)
		r.RemoteAddr = "127.0.0.1:55555"
		// Multi-hop chain — apid must NOT trust any element of it.
		r.Header.Set("X-Forwarded-For", "203.0.113.10, 198.51.100.7")
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if c := fire(); c != http.StatusUnauthorized {
		t.Fatalf("first: code = %d, want 401", c)
	}
	if c := fire(); c != http.StatusUnauthorized {
		t.Fatalf("second: code = %d, want 401", c)
	}
	// Third attempt must trip the bucket — proving the bucket was
	// keyed on the loopback host (127.0.0.1), not on the leftmost
	// or rightmost element of the chain.
	if c := fire(); c != http.StatusTooManyRequests {
		t.Fatalf("third: code = %d, want 429 (multi-hop chain must not be trusted)", c)
	}
}

// TestAuthLimit_ClientIPFromLoopbackHop_XForwardedFor_IPv6 closes
// the IPv6 coverage gap in the issue #89 test set. The three
// load-bearing tests above use IPv4 RemoteAddr (127.0.0.1:<port>),
// but isLoopbackHost uses net.IP.IsLoopback() which handles ::1
// identically. A future regression that broke the IPv6 branch —
// e.g. someone hand-rolling a string prefix check instead of
// net.ParseIP — would silently break IPv6-loopback deployments
// (future dual-stack) without failing any existing test. This
// test pins that the predicate's IPv4/V6 symmetry actually holds.
//
// Shape mirrors TestAuthLimit_ClientIPFromLoopbackHop_XForwardedFor:
// two distinct IPv6 XFFs on the same loopback hop must land in
// separate buckets; the 3rd attempt from customer A must trip A's
// bucket (proving the XFF was trusted, not the loopback host).
func TestAuthLimit_ClientIPFromLoopbackHop_XForwardedFor_IPv6(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := middleware.AuthLimitConfig{
		Window:      time.Minute,
		MaxFailures: 2,
		Now:         func() time.Time { return now },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	gate := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", http.StatusUnauthorized) }
	h := middleware.AuthLimit(cfg)(http.HandlerFunc(gate))

	// Two IPv6 customers via the loopback hop, distinct real IPs,
	// distinct buckets.
	fire := func(xff string) int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/auth/verify", nil)
		r.RemoteAddr = "[::1]:55555"
		r.Header.Set("X-Forwarded-For", xff)
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	// Customer A: 2 failures land in A's bucket (still < MaxFailures).
	if c := fire("2001:db8::1"); c != http.StatusUnauthorized {
		t.Fatalf("A first: code = %d, want 401", c)
	}
	if c := fire("2001:db8::1"); c != http.StatusUnauthorized {
		t.Fatalf("A second: code = %d, want 401", c)
	}
	// Customer B: starts a fresh bucket. If the bug regressed for
	// IPv6 specifically, B's first request would land in A's
	// already-full bucket and 429.
	if c := fire("2001:db8::2"); c != http.StatusUnauthorized {
		t.Fatalf("B first: code = %d, want 401 (would be 429 if IPv6 loopback ignored)", c)
	}
	// Customer A: 3rd attempt trips A's bucket (now 3 >= MaxFailures).
	if c := fire("2001:db8::1"); c != http.StatusTooManyRequests {
		t.Fatalf("A third: code = %d, want 429", c)
	}
	// Customer B: still has 1 failure, must NOT be limited yet.
	if c := fire("2001:db8::2"); c != http.StatusUnauthorized {
		t.Fatalf("B second: code = %d, want 401 (still under threshold)", c)
	}
}

// TestAuthLimit_Snapshot_DeepCopiesHits pins the operator-obs Snapshot
// accessor added in ADR-091 §3.5 / PR #2.
//
// Three properties are locked down by this test:
//  1. Snapshot reflects only IPs with failures inside the configured
//     Window — expired entries are pruned by the same logic that
//     recordFailure / isLimited use (no off-by-one on the cutoff).
//  2. Snapshot is a deep copy: mutating the returned slice does not
//     affect the limiter's internal state.
//  3. Sort order is Hits DESC, IP ASC for stable operator-UI render.
func TestAuthLimit_Snapshot_DeepCopiesHits(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	cfg := middleware.AuthLimitConfig{
		Window:      time.Minute,
		MaxFailures: 100,
		Now:         func() time.Time { return now },
		Log:         slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	gate := func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusUnauthorized) }
	lim := middleware.NewLimiter(cfg)
	h := middleware.AuthLimitWithLimiter(cfg, lim)(http.HandlerFunc(gate))

	fire := func(remoteAddr string) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.RemoteAddr = remoteAddr
		h.ServeHTTP(rec, r)
	}

	// Two hits from IP "1.1.1.1", three from "2.2.2.2", one from
	// "3.3.3.3" — order so the hits-DESC sort yields 2.2.2.2 first.
	// These are recorded at `now` (oldNow). Snapshot fires at
	// (oldNow + 30s) which is well inside the 1m Window, so all
	// six failures are still live.
	fire("1.1.1.1:1")
	fire("1.1.1.1:1")
	fire("2.2.2.2:2")
	fire("2.2.2.2:2")
	fire("2.2.2.2:2")
	fire("3.3.3.3:3")

	// Inject an EXPIRED failure for "4.4.4.4" by recording one
	// at (now - 2m), then advance the clock past Window(now). After
	// the advance, every failure at (now - 2m) is past the Window
	// cutoff and must NOT appear in the snapshot. The closure over
	// the package-local `now` is what cfg.Now reads.
	oldNow := now
	now = oldNow.Add(-2 * time.Minute) // backdate so the next failure is stale
	fire("4.4.4.4:4")
	// Advance past `oldNow + 1m` (window cutoff) — 4.4.4.4's
	// single failure at (oldNow - 2m) is now strictly older than
	// the cutoff and must be dropped.
	now = oldNow.Add(30 * time.Second) // keep the 1.1.1.1/2.2.2.2/3.3.3.3 failures inside Window
	// Do NOT fire 4.4.4.4 again — the snapshot must show no entries
	// for 4.4.4.4 because its only failure is past the cutoff. This
	// pins the "drop expired entries from the front" behaviour the
	// snapshot shares with isLimited/recordFailure.

	snap := lim.Snapshot()
	if snap.Window != time.Minute || snap.MaxFailures != 100 {
		t.Fatalf("snapshot window/max: got %+v, want 1m/100", snap)
	}
	if snap.Now.IsZero() {
		t.Fatalf("snapshot.Now must be populated")
	}

	// Build a map for membership + count assertions.
	got := map[string]int{}
	for _, e := range snap.Entries {
		got[e.IP] = e.Hits
	}
	// 4.4.4.4 was recorded at (now - 2m) — after the clock advance,
	// the snapshot window is (now + 2m - 1m) = (now + 1m), so the
	// failure at (now - 2m) is past the cutoff and must NOT appear.
	if _, ok := got["4.4.4.4"]; ok {
		t.Fatalf("4.4.4.4 appeared in snapshot despite being past Window: %+v", snap.Entries)
	}
	if got["1.1.1.1"] != 2 {
		t.Fatalf("1.1.1.1 hits: got %d, want 2", got["1.1.1.1"])
	}
	if got["2.2.2.2"] != 3 {
		t.Fatalf("2.2.2.2 hits: got %d, want 3", got["2.2.2.2"])
	}
	if got["3.3.3.3"] != 1 {
		t.Fatalf("3.3.3.3 hits: got %d, want 1", got["3.3.3.3"])
	}

	// Sort order: 2.2.2.2 (3), 1.1.1.1 (2), then 3.3.3.3 (1,
	// no 4.4.4.4 because expired).
	if len(snap.Entries) != 3 {
		t.Fatalf("entries: got %d, want 3 (full: %+v)", len(snap.Entries), snap.Entries)
	}
	wantOrder := []string{"2.2.2.2", "1.1.1.1", "3.3.3.3"}
	for i, want := range wantOrder {
		if snap.Entries[i].IP != want {
			t.Fatalf("entries[%d].IP: got %q, want %q (full: %+v)", i, snap.Entries[i].IP, want, snap.Entries)
		}
	}

	// Mutating the returned snapshot must not affect the limiter.
	snap.Entries[0].Hits = 9999
	if lim.Snapshot().Entries[0].Hits != 3 {
		t.Fatalf("snapshot not deep-copied: post-mutation hits = %d, want 3", lim.Snapshot().Entries[0].Hits)
	}
}

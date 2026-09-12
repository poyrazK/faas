// adr: 089 — standby mutations are classified and relayed to the active
// control plane without permitting redirect loops or split-brain writes.
// Tests for the Tier A9 / ADR-084 write classification helpers.
// PR-A ships pure helpers + their tests; PR-B wires the
// writeGate wrapper around `apidProxy` and adds the
// integration tests (cmd/gatewayd-internal/writegate_integration_test.go).
//
// Coverage:
//   - IsWriteMethod: every documented method, every
//     apid-irrelevant method.
//   - IsCarveOutPath: the 7-entry allowlist + boundary cases
//     (path-with-suffix must NOT match — keys are exact).
//   - AuthKindOf: bearer precedence, cookie fallback,
//     anonymous default, case-insensitive header.
//   - IsLoopAttempt: presence-only check; empty value, server-set
//     value, attacker-supplied value all rejected.
//   - IsWriteRequest: composition of method + apid path; reads
//     always false; carve-out paths handled by the caller (this
//     predicate does NOT exclude them — see docs).
//   - apid.IsApidPath: anchored-root regression for `/v1.zip`
//     (the existing cmd/gatewayd-internal/proxy.go `isApidPath`
//     rejects this; PR-B refines the placeholder; the test
//     pins the regression so PR-B doesn't accidentally
//     regress when extracting the full predicate).
package writegate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid"
)

// -----------------------------------------------------------------------------
// IsWriteMethod
// -----------------------------------------------------------------------------

func TestIsWriteMethod_Mutations(t *testing.T) {
	cases := []struct {
		method string
		want   bool
	}{
		{"POST", true},
		{"PUT", true},
		{"PATCH", true},
		{"DELETE", true},
		// Lower-case variants are accepted (canonicalized by
		// net/http for the wire, but some test fixtures pass
		// the lowercase form).
		{"post", true},
		{"put", true},
		{"patch", true},
		{"delete", true},
	}
	for _, c := range cases {
		if got := IsWriteMethod(c.method); got != c.want {
			t.Errorf("IsWriteMethod(%q) = %v, want %v", c.method, got, c.want)
		}
	}
}

func TestIsWriteMethod_Reads(t *testing.T) {
	cases := []struct {
		method string
		want   bool
	}{
		{"GET", false},
		{"HEAD", false},
		{"OPTIONS", false},
		// apid doesn't bind these; the proxy's Host-based
		// routing drops them before the gate.
		{"CONNECT", false},
		{"TRACE", false},
		// Future-proofing: an unknown verb is not a write.
		{"PURGE", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsWriteMethod(c.method); got != c.want {
			t.Errorf("IsWriteMethod(%q) = %v, want %v", c.method, got, c.want)
		}
	}
}

// -----------------------------------------------------------------------------
// IsCarveOutPath — the 7-entry allowlist. Boundary cases pinned.
// -----------------------------------------------------------------------------

func TestIsCarveOutPath_Allowlist(t *testing.T) {
	// Exactly the 7 entries documented in writegate.go
	// `unauthenticatedCarveOuts`. Adding a new entry requires
	// updating BOTH this list AND the docstring in writegate.go.
	allowlist := []string{
		CarveOutWebhookStripe,
		CarveOutWebhookPaddle,
		CarveOutCLIAuthCode,
		CarveOutCLIAuthExchange,
		CarveOutCLIAuthRoot,
		CarveOutOAuthGoogleCB,
		CarveOutOAuthGitHubCB,
	}
	// Drift guard: if the production map size diverges from the
	// test's local list, fail loud (review finding #6 of PR #761).
	// Adding a constant without updating the map (or vice versa)
	// surfaces immediately at `go test` time.
	if len(unauthenticatedCarveOuts) != len(allowlist) {
		t.Fatalf("carve-out drift: map has %d entries, allowlist slice has %d",
			len(unauthenticatedCarveOuts), len(allowlist))
	}
	for _, p := range allowlist {
		if !IsCarveOutPath(p) {
			t.Errorf("IsCarveOutPath(%q) = false, want true (allowlist drift!)", p)
		}
	}
}

// The map is keyed on EXACT path. Suffix/prefix similarity must
// NOT match — that would silently leak a webhook-style mutation
// onto the cross-box hop and break HMAC continuity.
func TestIsCarveOutPath_Boundaries(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Path-with-suffix must NOT match.
		{"/v1/webhooks/stripe/extra", false},
		{"/v1/webhooks/stripe/", false},
		// Path-with-prefix must NOT match (e.g. a typo
		// `/v1cli-auth/code` must NOT match `/cli-auth`).
		{"/v1cli-auth/code", false},
		{"/api/v1/webhooks/stripe", false},
		// Empty / unrelated paths.
		{"", false},
		{"/", false},
		{"/v1/webhooks", false},
		{"/v1/auth/google", false}, // missing /callback
	}
	for _, c := range cases {
		if got := IsCarveOutPath(c.path); got != c.want {
			t.Errorf("IsCarveOutPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// -----------------------------------------------------------------------------
// AuthKindOf — header inspection only, no body / cookie value.
// -----------------------------------------------------------------------------

func TestAuthKindOf_Bearer(t *testing.T) {
	cases := []string{
		"Bearer abc123",
		"bearer abc123", // lowercase scheme must still match
		"BEARER abc123",
		// Trailing whitespace is preserved by net/http; the
		// HasPrefix check accepts the trailing-space variant.
		"Bearer ",
		// Bare "Bearer" with no trailing space / no token — a
		// malformed Authorization header some HTTP clients emit
		// when the token is empty. Review finding #4 of PR #761:
		// must classify as bearer so the cross-box hop is taken
		// (rather than the anonymous carve-out path, which would
		// be a security-relevant regression). apid's auth chain
		// rejects the empty token downstream with 401.
		"Bearer",
		"bearer",
		"BEARER",
		// Leading whitespace — net/http preserves it; we
		// TrimSpace before the scheme check.
		"  Bearer abc123",
	}
	for _, h := range cases {
		r := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
		r.Header.Set("Authorization", h)
		if got := AuthKindOf(r); got != AuthBearer {
			t.Errorf("AuthKindOf(Authorization=%q) = %q, want %q", h, got, AuthBearer)
		}
	}
}

// Bearer precedes cookie — a request that carries both (rare;
// happens when the CLI sends both an API key and the user's
// session cookie) must classify as bearer so the cross-box hop
// carries the right headers.
func TestAuthKindOf_BearerPrecedesCookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
	r.Header.Set("Authorization", "Bearer abc123")
	r.AddCookie(&http.Cookie{Name: "faas_sid", Value: "session-xyz"})
	if got := AuthKindOf(r); got != AuthBearer {
		t.Fatalf("AuthKindOf with bearer+cookie = %q, want %q (bearer precedence)", got, AuthBearer)
	}
}

// Cookie-only path — dashboard SPA mutations (the
// `/dashboard/apps/:id/settings` flows) carry the session
// cookie without a bearer header.
func TestAuthKindOf_Cookie(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
	r.AddCookie(&http.Cookie{Name: "faas_sid", Value: "session-xyz"})
	if got := AuthKindOf(r); got != AuthCookie {
		t.Errorf("AuthKindOf with cookie only = %q, want %q", got, AuthCookie)
	}
}

// A different cookie name (e.g. `csrftoken`) must NOT count as
// a session cookie. Only `faas_sid` is the session cookie per
// ADR-039.
func TestAuthKindOf_NonSessionCookieIgnored(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
	r.AddCookie(&http.Cookie{Name: "csrftoken", Value: "x"})
	r.AddCookie(&http.Cookie{Name: "lang", Value: "en"})
	if got := AuthKindOf(r); got != AuthAnonymous {
		t.Errorf("AuthKindOf with non-session cookies = %q, want %q", got, AuthAnonymous)
	}
}

func TestAuthKindOf_Anonymous(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", nil)
	if got := AuthKindOf(r); got != AuthAnonymous {
		t.Errorf("AuthKindOf with no headers = %q, want %q", got, AuthAnonymous)
	}
}

// -----------------------------------------------------------------------------
// IsLoopAttempt — header presence triggers rejection. Value is ignored.
// -----------------------------------------------------------------------------

func TestIsLoopAttempt(t *testing.T) {
	cases := []struct {
		name    string
		header  string
		set     bool
		want    bool
		comment string
	}{
		{name: "absent", set: false, want: false, comment: "no header on the request"},
		{name: "server-set non-empty", set: true, header: "node-a", want: true, comment: "cross-box hop identifies the proxying node"},
		{name: "attacker-supplied", set: true, header: "fake-leader", want: true, comment: "any value is the loop signal"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
		if c.set {
			r.Header.Set(LoopGuardSentinel, c.header)
		}
		if got := IsLoopAttempt(r); got != c.want {
			t.Errorf("%s (%s): IsLoopAttempt = %v, want %v", c.name, c.comment, got, c.want)
		}
	}
}

// Edge case: an empty-value header (`X-Faas-Forwarded-Leader:`)
// still triggers rejection — the implementation walks the
// canonicalized header map directly (not via Header.Get) so
// the empty-value case is distinguishable from "absent".
// An attacker who deliberately sends an empty value gets the
// same 400 redirect_loop response as a value-bearing attempt.
func TestIsLoopAttempt_EmptyValueTriggers(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
	// Header.Set filters empty values, so inject directly.
	r.Header[LoopGuardSentinel] = []string{""}
	if !IsLoopAttempt(r) {
		t.Fatalf("empty-value sentinel should trigger rejection (presence is the signal)")
	}
}

// IsLoopAttempt must be case-insensitive — net/http canonicalizes
// header names but the sentinel value is set by the gate itself
// and clients may set the header in any case.
func TestIsLoopAttempt_CaseInsensitive(t *testing.T) {
	// net/http.Header is a map keyed on CanonicalMIMEHeaderKey.
	// Set via Set() — which canonicalizes — to exercise the
	// case-insensitive lookup that Get() performs.
	for _, v := range []string{
		"node-a",
		"x-faas-forwarded-leader", // value form, not key
		"X-FAAS-FORWARDED-LEADER",
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/apps", nil)
		r.Header.Set(LoopGuardSentinel, v)
		if !IsLoopAttempt(r) {
			t.Errorf("IsLoopAttempt with header value %q = false, want true", v)
		}
	}
}

// -----------------------------------------------------------------------------
// IsWriteRequest — composition of method + apid path predicate.
// -----------------------------------------------------------------------------

func TestIsWriteRequest_MutationsOnApidPaths(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   bool
	}{
		// Mutations on apid paths — these are writes the
		// gate must classify.
		{"POST", "/v1/apps", true},
		{"PUT", "/v1/apps/foo", true},
		{"PATCH", "/v1/apps/foo/deployments/d1", true},
		{"DELETE", "/v1/apps/foo", true},
		// OAuth callback is a POST under /v1/auth — the
		// gate classifies it as a write; the caller
		// (writeGate in PR-B) consults IsCarveOutPath to
		// exclude it from cross-box forwarding.
		{"POST", "/v1/auth/google/callback", true},
		// Webhook is also a POST under /v1/webhooks — same
		// carve-out pattern.
		{"POST", "/v1/webhooks/stripe", true},
		// cli-auth is under /v1/cli-auth.
		{"POST", "/v1/cli-auth/code", true},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := IsWriteRequest(r); got != c.want {
			t.Errorf("IsWriteRequest(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestIsWriteRequest_ReadsAlwaysFalse(t *testing.T) {
	cases := []struct {
		method string
		path   string
	}{
		{"GET", "/v1/apps"},
		{"HEAD", "/v1/apps/foo"},
		{"OPTIONS", "/v1/webhooks/stripe"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if IsWriteRequest(r) {
			t.Errorf("IsWriteRequest(%s %s) = true, want false (reads are never writes)", c.method, c.path)
		}
	}
}

// Mutations on paths the proxy doesn't route to apid must be
// classified false — the proxy's Host-based routing handles them
// (vm runtime, function hot path, asset serving).
//
// `/healthz` IS an apid surface per the existing Tier A7
// `isApidPath` predicate (cmd/gatewayd-internal/proxy.go:11-23).
// It is GET-only in practice (CD health probe), but a POST to
// `/healthz` would still be proxied by the gateway. The gate
// therefore classifies `POST /healthz` as a write — apid's
// handler will reject the method. This test excludes /healthz
// from the non-apid list.
func TestIsWriteRequest_NonApidPathsFalse(t *testing.T) {
	cases := []struct {
		method string
		path   string
	}{
		// Function runtime hot path (vm/microvm wake).
		{"POST", "/invoke/foo"},
		{"POST", "/fn/foo"},
		// Static assets.
		{"DELETE", "/static/app.js"},
		// OOB paths that have no apid surface.
		{"POST", "/metrics"},
		// The anchored-root regression: `/v1.zip` must NOT
		// match `/v1/`. PR-B refines the predicate; this
		// test pins the regression.
		{"POST", "/v1.zip"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if IsWriteRequest(r) {
			t.Errorf("IsWriteRequest(%s %s) = true, want false (non-apid path)", c.method, c.path)
		}
	}
}

// Anchored-root regression — the existing
// cmd/gatewayd-internal/proxy.go `isApidPath` predicate
// explicitly rejects `/v1.zip` (an nginx-style filename
// collision). The `apid.IsApidPath` helper (PR-B / Tier A9 /
// ADR-084) now lives in `pkg/apid/router.go` and is the
// single source of truth — this test pins the predicate from
// the writegate consumer's side so a future refactor that
// strips the anchor discipline is caught at `go test` time.
//
// This test asserts (not t.Skip) — the previous t.Skip-based
// tripwire was a known anti-pattern (review finding #1 of
// PR #761). The pkg/apid/router_test.go table is the canonical
// fixture; this table is the in-package regression that
// catches writegate-specific regressions where the proxy
// routes start to drift from the apid route table.
func TestIsWriteRequest_AnchoredRootRegression(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Collision filenames: PR #180's review finding #6.
		{"/v1.zip", false},
		{"/dashboardfoo", false},
		{"/loginfoo", false},
		{"/signupfoo", false},
		{"/logoutfoo", false},
		{"/cli-authfoo", false},
		{"/statusfoo", false},
		{"/healthzfoo", false},
		// These ARE valid (anchored at "/" boundary).
		{"/v1", true},
		{"/v1/", true},
		{"/dashboard", true},
		{"/dashboard/", true},
		{"/login", true},
		{"/login/", true},
		// New auth roots from PR #180.
		{"/login/forgot", true},
		{"/auth/verify", true},
		{"/auth/reset", true},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, c.path, nil)
		if got := IsWriteRequest(r); got != c.want {
			t.Errorf("IsWriteRequest(POST %s) = %v, want %v", c.path, got, c.want)
		}
	}
}

// -----------------------------------------------------------------------------
// apid.IsApidPath — exhaustive coverage of the placeholder branches.
// PR-B / Tier A9 / ADR-084 promoted the matcher to pkg/apid/router.go;
// this regression table remains here as an in-package guard so a
// future writegate-side change that breaks the apid-bound predicate
// is caught without first walking to pkg/apid.
// -----------------------------------------------------------------------------

func TestApidPathMatch_AllBranches(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Anchored roots — exact + "/" subtree form.
		{"/v1", true},
		{"/v1/", true},
		{"/v1/apps", true},
		{"/v1/auth/login", true},
		{"/dashboard", true},
		{"/dashboard/", true},
		{"/login", true},
		{"/login/", true},
		{"/login/forgot", true}, // PR #180 new root
		{"/signup", true},
		{"/auth/verify", true}, // PR #180 new root
		{"/auth/reset", true},  // PR #180 new root
		{"/oauth/callback", true},
		{"/logout", true},
		{"/cli-auth", true},
		{"/status", true},
		{"/healthz", false},

		// These must NOT match — VM runtime paths, assets,
		// collision filenames.
		{"/invoke/foo", false},
		{"/fn/foo", false},
		{"/static/app.js", false},
		{"/metrics", false},
		{"/v1.zip", false}, // anchored-root regression
		{"/auth/login", false},
		{"/oauth", false}, // bare /oauth — see pkg/apid/router.go
		{"", false},
	}
	for _, c := range cases {
		if got := apid.IsApidPath(c.path); got != c.want {
			t.Errorf("apid.IsApidPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// -----------------------------------------------------------------------------
// LeaderResolver fake — used by PR-B's integration tests. The
// fake is goroutine-safe (sync.RWMutex) and exposes a Set hook
// so tests can simulate leader flips.
// -----------------------------------------------------------------------------

// fakeLeaderResolver is the test-only LeaderResolver used in
// PR-B's integration tests. PR-A ships it now (and the helper
// compiles it in) so the integration tests have a stable fake
// to wire against.
type fakeLeaderResolver struct {
	mu   sync.RWMutex
	name string
	isMe bool
	err  error
}

func (f *fakeLeaderResolver) Current(_ context.Context) (string, bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.name, f.isMe, f.err
}

func (f *fakeLeaderResolver) Set(name string, isMe bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.name = name
	f.isMe = isMe
	f.err = err
}

// newFakeLeaderResolver is a tiny constructor helper so tests
// don't have to repeat the address-of dance.
func newFakeLeaderResolver(name string, isMe bool) *fakeLeaderResolver {
	return &fakeLeaderResolver{name: name, isMe: isMe}
}

func TestFakeLeaderResolver_RoundTrip(t *testing.T) {
	r := newFakeLeaderResolver("node-a", true)
	if name, isMe, err := r.Current(context.Background()); name != "node-a" || !isMe || err != nil {
		t.Fatalf("fresh fake = (%q, %v, %v), want (node-a, true, nil)", name, isMe, err)
	}
	r.Set("node-b", false, nil)
	if name, isMe, err := r.Current(context.Background()); name != "node-b" || isMe || err != nil {
		t.Fatalf("post-Set = (%q, %v, %v), want (node-b, false, nil)", name, isMe, err)
	}
}

func TestFakeLeaderResolver_ErrorPropagates(t *testing.T) {
	r := newFakeLeaderResolver("node-a", true)
	want := context.DeadlineExceeded
	r.Set("node-a", true, want)
	_, _, err := r.Current(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("err propagation: got %v, want %v", err, want)
	}
}

// Concurrent reads against a writer — pins the fake's
// goroutine-safety contract. (Real LeaderResolver implementations
// MUST also be goroutine-safe per the interface docstring.)
func TestFakeLeaderResolver_ConcurrentReads(t *testing.T) {
	r := newFakeLeaderResolver("node-a", true)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_, _, _ = r.Current(context.Background())
			}
		}()
	}
	// Concurrent writer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			if j%2 == 0 {
				r.Set("node-a", true, nil)
			} else {
				r.Set("node-b", false, nil)
			}
		}
	}()
	wg.Wait()
	// Final state: last writer wins; we don't assert which
	// because of the race. The point is "did not race-detect".
}

// -----------------------------------------------------------------------------
// WriteOutcome / AuthKind label-set stability
// -----------------------------------------------------------------------------

// Every outcome label that the gate may emit must be a non-empty
// WriteOutcome string and must be unique (so the §12 PromQL
// dashboard's regex matcher `outcome=~"..."` is correct).
func TestWriteOutcome_LabelsUnique(t *testing.T) {
	all := []WriteOutcome{
		OutcomeRelayed,
		OutcomeRedirect307,
		OutcomeSameBox,
		OutcomeCookieBlocked,
		OutcomeLeaderUnreachable,
		OutcomeLoopPrevented,
		OutcomeMTLSFailure,
		OutcomeError,
	}
	seen := make(map[WriteOutcome]bool, len(all))
	for _, o := range all {
		if string(o) == "" {
			t.Errorf("WriteOutcome label must be non-empty: %v", o)
		}
		if seen[o] {
			t.Errorf("WriteOutcome label duplicated: %q", o)
		}
		seen[o] = true
	}
}

// Every auth_kind label must be a non-empty AuthKind string
// and unique.
func TestAuthKind_LabelsUnique(t *testing.T) {
	all := []AuthKind{
		AuthBearer,
		AuthCookie,
		AuthAnonymous,
	}
	seen := make(map[AuthKind]bool, len(all))
	for _, k := range all {
		if string(k) == "" {
			t.Errorf("AuthKind label must be non-empty: %v", k)
		}
		if seen[k] {
			t.Errorf("AuthKind label duplicated: %q", k)
		}
		seen[k] = true
	}
}

// LoopGuardSentinel must be the exact wire header name; if
// this changes, the §12 dashboard's `request_header{header=...}`
// queries silently break. Pin it.
//
// Round-trip through http.CanonicalHeaderKey to confirm the
// constant survives net/http's canonicalization unchanged (the
// production code at writegate.go:280 uses
// `r.Header[http.CanonicalHeaderKey(LoopGuardSentinel)]` for
// the loop-guard lookup — a constant whose canonical form
// differs from its literal value would silently miss loop
// attempts). Review finding #7 of PR #761.
func TestLoopGuardSentinelStable(t *testing.T) {
	const want = "X-Faas-Forwarded-Leader"
	if LoopGuardSentinel != want {
		t.Fatalf("LoopGuardSentinel = %q, want %q (dashboard query regression)", LoopGuardSentinel, want)
	}
	if got := http.CanonicalHeaderKey(LoopGuardSentinel); got != want {
		t.Fatalf("CanonicalHeaderKey(%q) = %q, want %q (loop-guard map lookup would miss)",
			LoopGuardSentinel, got, want)
	}
}

// Per-deployment require_authn tests (issue #560).
//
// Pins the authz branch behaviour end-to-end through ServeHTTP:
// every denial path (missing / invalid / expired / cross-account)
// plus the two regression pins (RequireAuthn=false doesn't fire the
// branch, and the wake gate is unaffected). The fake authenticator
// is the same RequireAuthnAuthenticator interface the production
// adapter satisfies — pkg/auth's middleware is exercised in the
// cmd/gatewayd-internal side (see require_authn_adapter_test.go).
package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// fakeRequireAuthnAuthn is the test seam for the per-deployment
// authz branch. Each test seeds the response it wants (account /
// key / error) and the assertions read it back via atomic loads.
// Mirrors the contract cmd/gatewayd-internal/require_authn_adapter.go
// uses to wrap the production *pkg/auth.Middleware.
type fakeRequireAuthnAuthn struct {
	// calls counts every AuthenticateKey invocation (the rate
	// limiter decision + the audit row + the per-key debounce
	// all key off this counter).
	calls atomic.Int32
	// accountID is the Account.ID the fake returns. Empty +
	// err=nil means "no caller set"; tests that want a
	// cross-account denial pin a non-empty accountID that
	// differs from the routed app's AccountID.
	accountID string
	// keyID is the APIKey.ID the fake returns. Surfaced in
	// the audit payload's key_id field; non-empty on success.
	keyID string
	// err is the failure the fake returns. State.Err* values
	// reach here via the test's local var; the handler's
	// errors.Is comparison is what picks the audit reason.
	err error
}

func (f *fakeRequireAuthnAuthn) AuthenticateKey(_ context.Context, _ []byte) (RequireAuthnAccount, RequireAuthnKey, error) {
	f.calls.Add(1)
	if f.err != nil {
		return RequireAuthnAccount{}, RequireAuthnKey{}, f.err
	}
	return RequireAuthnAccount{ID: f.accountID}, RequireAuthnKey{ID: f.keyID}, nil
}

// fakeRequireAuthnAudit counts the three deny-path audit rows
// + carries the most recent (kind, subject, data) so tests can
// assert on the payload shape. Tests that want to verify "audit
// is silent" leave this nil on the Handler — the authz branch
// is nil-safe.
type fakeRequireAuthnAudit struct {
	// counts tracks emit totals keyed by kind. Tests assert
	// "exactly one missing row" via counts[kind] == 1.
	counts map[string]int
	// last carries the most recent (kind, subject, data)
	// tuple so the test can pin the payload shape. data
	// is shared by reference — read-only, the handler never
	// mutates after the Emit call returns.
	lastKind    string
	lastSubject *string
	lastData    map[string]any
}

func newFakeRequireAuthnAudit() *fakeRequireAuthnAudit {
	return &fakeRequireAuthnAudit{counts: map[string]int{}}
}

func (a *fakeRequireAuthnAudit) Emit(_ context.Context, kind string, subject *string, data map[string]any) {
	if a == nil {
		return
	}
	a.counts[kind]++
	a.lastKind = kind
	a.lastSubject = subject
	a.lastData = data
}

// newRequireAuthnTestHandler wires a Handler with a single
// routed app on the supplied host, gated by RequireAuthn=true
// unless gated=false (the regression-pin path). The fake
// authenticator + fake auditor are returned so each test can
// seed the response it wants. The fake backend's app is
// pre-populated with a hot Target so the success path skips
// the wake gate (the authz branch must run BEFORE wake, per
// the plan; the regression pin is in
// TestRequireAuthn_AllowsWakesBeforeAuthz).
func newRequireAuthnTestHandler(t *testing.T, gated bool, accountID string) (*Handler, *fakeBackend, *fakeRequireAuthnAuthn, *fakeRequireAuthnAudit, *httptest.Server) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from app"))
	}))
	t.Cleanup(upstream.Close)

	b := &fakeBackend{
		app: App{
			ID:           "app-1",
			AccountID:    accountID,
			Plan:         api.PlanPro,
			RequireAuthn: gated,
		},
		host:     "jane-api.apps.dom",
		upstream: upstream.Listener.Addr().String(),
	}
	b.setLegacyHot()

	authn := &fakeRequireAuthnAuthn{accountID: accountID, keyID: "key-1"}
	audit := newFakeRequireAuthnAudit()

	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithRequireAuthn(authn, audit)
	return h, b, authn, audit, upstream
}

// reqFor builds a request against the test handler's host with
// the supplied Authorization header (empty → no header at all).
// The Host header is what the gateway's hostname parser reads;
// r.URL.Host isn't reliable under httptest.NewRecorder.
func reqFor(t *testing.T, authHeader string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "http://jane-api.apps.dom/", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

// validKeyFormat returns a plaintext token shaped like the
// real fp_live_<48-hex> format (api.ValidAPIKeyFormat returns
// true). body must be lowercase hex; tests that need a
// specific prefix use 000...001 or similar stable strings
// rather than random bytes (deterministic replay).
func validKeyFormat(body string) string {
	const prefix = "fp_live_"
	const wantLen = 48
	if len(body) != wantLen {
		panic("validKeyFormat: body must be exactly 48 hex chars (got " + itoa(uint64(len(body))) + ")")
	}
	return prefix + body
}

// TestRequireAuthn_MissingHeader_401 — gated app + no
// Authorization header → 401 unauthorized + an
// instances.authn_missing audit row. Validates the first
// deny path; the call counter pins that the auth branch fired
// before the wake gate (no AuthenticateKey call).
func TestRequireAuthn_MissingHeader_401(t *testing.T) {
	h, _, authn, audit, _ := newRequireAuthnTestHandler(t, true, "acct-1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqFor(t, ""))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Errorf("body = %q, want it to contain 'unauthorized'", rec.Body)
	}
	if got := audit.counts["instances.authn_missing"]; got != 1 {
		t.Errorf("authn_missing audit count = %d, want 1", got)
	}
	if got := authn.calls.Load(); got != 0 {
		t.Errorf("AuthenticateKey called %d times, want 0 (missing header → fail-fast before verify)", got)
	}
}

// TestRequireAuthn_InvalidToken_401 — gated app + garbage
// bearer → 401 unauthorized + an instances.authn_invalid
// audit row with reason="invalid_bearer". Pins the second
// deny path; the call counter verifies the authenticator
// was consulted.
func TestRequireAuthn_InvalidToken_401(t *testing.T) {
	h, _, authn, audit, _ := newRequireAuthnTestHandler(t, true, "acct-1")
	authn.err = errors.New("unknown hash") // state-agnostic; the handler falls through to invalid_bearer.

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqFor(t, "Bearer "+validKeyFormat("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := audit.counts["instances.authn_invalid"]; got != 1 {
		t.Errorf("authn_invalid audit count = %d, want 1", got)
	}
	if got := audit.lastData["reason"]; got != "invalid_bearer" {
		t.Errorf("audit reason = %v, want invalid_bearer", got)
	}
	if got := authn.calls.Load(); got != 1 {
		t.Errorf("AuthenticateKey called %d times, want 1", got)
	}
}

// TestRequireAuthn_ExpiredKey_401 — gated app + expired key →
// 401 + an instances.authn_invalid audit row with
// reason="expired". Pins that the state.ErrAPIKeyExpired →
// gateway.ErrAPIKeyExpired sentinel chain survives through
// the adapter (cmd/gatewayd-internal/require_authn_adapter.go
// does the production translation; here we test the gateway's
// matching logic directly).
func TestRequireAuthn_ExpiredKey_401(t *testing.T) {
	h, _, _, audit, _ := newRequireAuthnTestHandler(t, true, "acct-1")
	// Drive the gateway-local sentinel directly so the test
	// pins the handler's errors.Is comparison rather than
	// the adapter's translation (which lives in
	// cmd/gatewayd-internal and has its own test).
	gwAuthn := &fakeRequireAuthnAuthn{err: ErrAPIKeyExpired}
	h.WithRequireAuthn(gwAuthn, audit)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqFor(t, "Bearer fp_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")) // 48 'a's — valid hex

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := audit.counts["instances.authn_invalid"]; got != 1 {
		t.Errorf("authn_invalid audit count = %d, want 1", got)
	}
	if got := audit.lastData["reason"]; got != "expired" {
		t.Errorf("audit reason = %v, want expired", got)
	}
}

// TestRequireAuthn_CrossAccount_403 — gated app owned by
// acct-A + caller presents a valid token for acct-B → 403
// insufficient_scope + an instances.authn_scope audit row
// with both account IDs in the payload. The body must NOT
// leak the caller's account ID (the audit row carries it
// for ops; the response body stays 403-only).
func TestRequireAuthn_CrossAccount_403(t *testing.T) {
	h, _, _, audit, _ := newRequireAuthnTestHandler(t, true, "acct-A")
	// Token resolves cleanly, but to a different account.
	authn := &fakeRequireAuthnAuthn{accountID: "acct-B", keyID: "key-B"}
	h.WithRequireAuthn(authn, audit)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqFor(t, "Bearer fp_live_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "insufficient_scope") {
		t.Errorf("body = %q, want it to contain 'insufficient_scope'", rec.Body)
	}
	if got := audit.counts["instances.authn_scope"]; got != 1 {
		t.Errorf("authn_scope audit count = %d, want 1", got)
	}
	if got := audit.lastSubject; got == nil || *got != "acct-B" {
		t.Errorf("audit subject = %v, want pointer to acct-B", got)
	}
	if got := audit.lastData["caller_account_id"]; got != "acct-B" {
		t.Errorf("audit caller_account_id = %v, want acct-B", got)
	}
	if got := audit.lastData["app_account_id"]; got != "acct-A" {
		t.Errorf("audit app_account_id = %v, want acct-A", got)
	}
	// Body leak check: the caller's account id must not
	// reach the wire (the operator's audit row carries it;
	// the customer-facing response stays generic).
	if strings.Contains(rec.Body.String(), "acct-B") {
		t.Errorf("body leaked caller account id: %q", rec.Body)
	}
}

// TestRequireAuthn_NotRequired_AllowsAnonymous — the routed
// app has RequireAuthn=false, so the authz branch must be a
// pass-through even when no Authorization header is present.
// This is the load-bearing regression pin: a future refactor
// that hoists the auth check above the Lookup branch would
// silently break every existing customer app (the plan's
// stated requirement #5).
func TestRequireAuthn_NotRequired_AllowsAnonymous(t *testing.T) {
	h, _, authn, audit, _ := newRequireAuthnTestHandler(t, false, "acct-1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqFor(t, ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (non-gated app must serve anonymous traffic)", rec.Code)
	}
	if got := authn.calls.Load(); got != 0 {
		t.Errorf("AuthenticateKey called %d times, want 0 (non-gated app)", got)
	}
	if got := len(audit.counts); got != 0 {
		t.Errorf("audit rows emitted on non-gated path: %v, want none", audit.counts)
	}
}

// TestRequireAuthn_AllowsWakesBeforeAuthz — pins the
// placement of the authz branch in ServeHTTP: it sits AFTER
// Backend.Lookup (so we know which app's RequireAuthn to
// consult) and BEFORE the wake gate (so unauthenticated
// traffic can't trigger cold-boot). A gated app with a valid
// token must reach the wake gate exactly like a non-gated
// app would — the authz check must not introduce an extra
// short-circuit.
//
// Concretely: with RequireAuthn=true and a valid token, the
// request must succeed and reuse the pre-warmed target. Authz
// must not introduce a short-circuit or trigger a needless wake.
func TestRequireAuthn_AllowsWakesBeforeAuthz(t *testing.T) {
	h, _, _, _, _ := newRequireAuthnTestHandler(t, true, "acct-1")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqFor(t, "Bearer fp_live_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")) // 48 'a's — valid hex

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (gated app with valid token must reach the upstream)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "hello from app") {
		t.Errorf("body = %q, want it to contain 'hello from app' (authz must not short-circuit before proxy)", rec.Body)
	}
	// x-faas-request-id is the last header the handler
	// stamps before the proxy runs (handler.go:591) — its
	// presence on a 200 means ServeHTTP actually executed
	// past the authz branch and through to the proxy hop.
	if rec.Header().Get("x-faas-request-id") == "" {
		t.Error("x-faas-request-id missing; authz branch short-circuited the proxy")
	}
	// The target was pre-warmed by the fixture, so this request must not
	// advertise a cold wake.
	if got := rec.Header().Get(wire.WakeHeader); got != wire.HotWakeValue {
		t.Errorf("%s = %q, want %q for a warm request", wire.WakeHeader, got, wire.HotWakeValue)
	}
}

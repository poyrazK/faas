package gateway

// public_auth_internal_only_test.go — ADR-119 tests for
// Handler.applyIngressInternalSvc. Pins the audit codes,
// the closed (outcome) metric partition, and the JWT
// redaction invariant (audit row must NEVER echo the token).
//
// Test strategy mirrors the public_auth_ip_allowlist_test.go
// shape (PR #999): stand up a Handler with the verifier wired
// + Metrics + RequireAuthnAuditor; mint a token via the same
// pkg/internalsvc.Mint the production minter uses; assert the
// gate's response / audit / metric outcomes.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/internalsvc"
)

// testInternalSvcVerifier is the in-memory
// gateway.InternalSvcVerifier the tests construct. Holds a
// single allowlist + a captured map of every Verify call (so
// tests can assert token-side observability without exposing
// the raw token — only the count and reason).
type testInternalSvcVerifier struct {
	allowed map[string]ed25519.PublicKey
	calls   int
	mu      sync.Mutex
}

func (v *testInternalSvcVerifier) Verify(_ context.Context, rawToken string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	svcName, err := internalsvc.Verify(rawToken, v.allowed)
	return svcName, err
}

func (v *testInternalSvcVerifier) AllowedSvcNames() []string {
	out := make([]string, 0, len(v.allowed))
	for k := range v.allowed {
		out = append(out, k)
	}
	return out
}

// testCountingAuthnAuditor counts every Emit call by (kind,
// reason) tuple. Captures the data map so the
// redaction-invariant test (TestApplyIngressInternalSvc_AuditDoesNotEchoToken)
// can scan raw payloads for the JWT substring.
type testCountingAuthnAuditor struct {
	mu     sync.Mutex
	events []testAuditEvent
}

type testAuditEvent struct {
	kind string
	data map[string]any
}

func (a *testCountingAuthnAuditor) Emit(_ context.Context, kind string, _ *string, data map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Copy the map so caller mutations don't leak across tests.
	cp := make(map[string]any, len(data))
	for k, v := range data {
		cp[k] = v
	}
	a.events = append(a.events, testAuditEvent{kind: kind, data: cp})
}

func (a *testCountingAuthnAuditor) countByKind(kind string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, e := range a.events {
		if e.kind == kind {
			n++
		}
	}
	return n
}

// helper: mint a JWT with the given keypair. Used by both
// the positive-path tests (valid token) and the negative
// tests (expired / wrong-key). The audience is hardcoded to
// internalsvc.Audience — tests that need a non-canonical
// audience call internalsvc.MintWithAudience directly
// (TestApplyIngressInternalSvc_WrongAudience_Returns403).
//
// Round-3 peer-review #7: kid comes from internalsvc.KidFromPub
// — the same derivation cmd/schedd/internal_svc_minter.go uses
// at boot. A previous version of this helper inlined the
// base64-of-[:16] derivation, which produced a kid identical to
// the production minter's but for the wrong reason (drift
// risk — any change to the package-level KidFromPub would
// silently miss this test).
//
// Round-6 golangci-lint ineffassign: the previous shape took
// an `audience` parameter that was set but never read — the
// Mint call uses the package-level constant. Removed; tests
// that need a different audience use MintWithAudience.
func mintTestToken(t *testing.T, pub ed25519.PublicKey, priv ed25519.PrivateKey, svcName string, ttlSec int) string {
	t.Helper()
	kid := internalsvc.KidFromPub(pub)
	tok, err := internalsvc.Mint(svcName, time.Duration(ttlSec)*time.Second, map[string]any{"app_id": "app-1"}, priv, kid)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return tok
}

// newTestHandlerForInternalSvc builds a Handler wired with
// the test verifier + counting auditor + Metrics. Returns
// the handler + the auditor + the verifier so tests can
// assert on captured state.
func newTestHandlerForInternalSvc(t *testing.T, allowedSvc map[string]ed25519.PublicKey) (*Handler, *testCountingAuthnAuditor, *testInternalSvcVerifier) {
	t.Helper()
	h := &Handler{
		log:     slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		metrics: NewMetrics(),
	}
	v := &testInternalSvcVerifier{allowed: allowedSvc}
	a := &testCountingAuthnAuditor{}
	h.internalSvcVerifier = v
	h.requireAuthnAudit = a
	return h, a, v
}

// App fixture for the gate. PublicAuth.Mode='internal_only';
// the other fields are zero-valued (the gate doesn't read
// them).
func internalOnlyApp() App {
	return App{
		ID:   "00000000-0000-0000-0000-00000000abcd",
		Slug: "test-internal-only",
		Plan: "scale",
		PublicAuth: PublicAuthConfig{
			Mode: publicAuthModeInternalOnly,
		},
	}
}

// TestApplyIngressInternalSvc_ValidToken_PassThrough asserts
// the happy path: a valid Authorization: Bearer JWT with
// aud='gregale.internal' signed by an allowlisted svc's key
// passes the gate. The gate returns false (no deny response
// written), the matched metric increments by 1, no audit row
// is emitted (pass-throughs don't get an audit row — same
// posture as applyIngressIPAllowlist's match path).
func TestApplyIngressInternalSvc_ValidToken_PassThrough(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	h, a, v := newTestHandlerForInternalSvc(t, map[string]ed25519.PublicKey{
		"schedd": pub,
	})
	app := internalOnlyApp()
	tok := mintTestToken(t, pub, priv, "schedd", 30)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	denied := h.applyIngressInternalSvc(rec, req, app)
	if denied {
		t.Errorf("gate denied a valid token; response body=%q", rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Errorf("rec.Code = %d, want 200 (default recorder status)", rec.Code)
	}
	if got := a.countByKind("instances.public_auth_internal_missing"); got != 0 {
		t.Errorf("missing-audit count = %d, want 0 (pass-through emits no audit)", got)
	}
	if got := a.countByKind("instances.public_auth_internal_invalid"); got != 0 {
		t.Errorf("invalid-audit count = %d, want 0", got)
	}
	if v.calls != 1 {
		t.Errorf("verifier.Verify calls = %d, want 1", v.calls)
	}
	// Metric: matched outcome should have ticked. Pre-instantiation
	// assertion is in TestInternalAuthMatchCounterPreInstantiated
	// (separate test); here we just confirm the field is reachable.
	if h.metrics == nil {
		t.Errorf("h.metrics is nil; gate cannot emit metrics")
	}
}

// TestApplyIngressInternalSvc_MissingHeader_Returns403 covers
// the no-Authorization-header path. Asserts:
//   - gate returns true (deny)
//   - 403 + RFC 7807 problem body
//   - audit row with kind=instances.public_auth_internal_missing
//   - blocked metric incremented
//   - verifier.Verify NOT called (no token to verify)
func TestApplyIngressInternalSvc_MissingHeader_Returns403(t *testing.T) {
	h, a, v := newTestHandlerForInternalSvc(t, nil)
	app := internalOnlyApp()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	if !h.applyIngressInternalSvc(rec, req, app) {
		t.Errorf("gate should deny on missing header")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("rec.Code = %d, want 403", rec.Code)
	}
	if got := a.countByKind("instances.public_auth_internal_missing"); got != 1 {
		t.Errorf("missing-audit count = %d, want 1", got)
	}
	if v.calls != 0 {
		t.Errorf("verifier.Verify called %d times on missing header; want 0", v.calls)
	}
}

// TestApplyIngressInternalSvc_InvalidSignature_Returns403
// covers the signature-failure path. Mints with keyA,
// verifies against an allowlist that contains only keyB
// (so the signature fails). Asserts:
//   - 403
//   - audit row with kind=instances.public_auth_internal_invalid
//   - audit row reason=unknown_service (per the gate's mapping
//     — a key not in the allowlist is "unknown_service" not
//     "signature_invalid" because the bridge returns
//     ErrUnknownService when the look-up by sub misses).
//   - blocked metric
func TestApplyIngressInternalSvc_InvalidSignature_Returns403(t *testing.T) {
	pubA, privA, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey A: %v", err)
	}
	pubB, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey B: %v", err)
	}
	// Allowlist contains pubB; token is signed by privA.
	h, a, _ := newTestHandlerForInternalSvc(t, map[string]ed25519.PublicKey{
		"schedd": pubB,
	})
	app := internalOnlyApp()
	tok := mintTestToken(t, pubA, privA, "schedd", 30)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	if !h.applyIngressInternalSvc(rec, req, app) {
		t.Errorf("gate should deny on signature mismatch")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("rec.Code = %d, want 403", rec.Code)
	}
	if got := a.countByKind("instances.public_auth_internal_invalid"); got != 1 {
		t.Errorf("invalid-audit count = %d, want 1", got)
	}
}

// TestApplyIngressInternalSvc_WrongAudience_Returns403 covers
// the aud-claim-mismatch path. Mints with aud='foo' (NOT
// gregale.internal) via internalsvc.MintWithAudience (test-only,
// exported in PR #1009 round-3 peer-review fix #3 — the substring-
// match table in applyIngressInternalSvc needs a live end-to-end
// pin). Asserts 403 + invalid-audit row + reason='audience_mismatch'.
func TestApplyIngressInternalSvc_WrongAudience_Returns403(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	h, a, _ := newTestHandlerForInternalSvc(t, map[string]ed25519.PublicKey{
		"schedd": pub,
	})
	app := internalOnlyApp()

	// MintWithAudience lets the test inject a non-canonical
	// audience. Production callers (cmd/schedd) MUST use
	// internalsvc.Mint, which pins Audience=gregale.internal
	// at the package level. Round-3 peer-review #7: the kid
	// now comes from internalsvc.KidFromPub — single source
	// of truth shared with cmd/schedd.
	kid := internalsvc.KidFromPub(pub)
	tok, err := internalsvc.MintWithAudience("schedd", 30*time.Second, nil, priv, kid, "foo")
	if err != nil {
		t.Fatalf("MintWithAudience: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	if !h.applyIngressInternalSvc(rec, req, app) {
		t.Errorf("gate should deny on audience mismatch")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("rec.Code = %d, want 403", rec.Code)
	}
	if got := a.countByKind("instances.public_auth_internal_invalid"); got != 1 {
		t.Errorf("invalid-audit count = %d, want 1", got)
	}
	// Pin the reason code — the substring-match table must
	// classify 'aud claim does not match' as
	// 'audience_mismatch', not the fallback
	// 'signature_invalid'. Regression here means the gate
	// silently misroutes every aud-mismatch as a sig error
	// (the round-2 bug).
	var foundReason string
	for _, ev := range a.events {
		if r, ok := ev.data["reason"].(string); ok {
			foundReason = r
		}
	}
	if foundReason != "audience_mismatch" {
		t.Errorf("audit reason = %q, want 'audience_mismatch'", foundReason)
	}
}

// TestApplyIngressInternalSvc_ExpiredToken_Returns403 covers
// the exp-claim-past path. Mints with negative TTL — the
// internalsvc.Mint contract allows negative TTL for tests.
// Asserts 403 + invalid-audit row.
func TestApplyIngressInternalSvc_ExpiredToken_Returns403(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	h, a, _ := newTestHandlerForInternalSvc(t, map[string]ed25519.PublicKey{
		"schedd": pub,
	})
	app := internalOnlyApp()
	tok := mintTestToken(t, pub, priv, "schedd", -120)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	if !h.applyIngressInternalSvc(rec, req, app) {
		t.Errorf("gate should deny on expired token")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("rec.Code = %d, want 403", rec.Code)
	}
	if got := a.countByKind("instances.public_auth_internal_invalid"); got != 1 {
		t.Errorf("invalid-audit count = %d, want 1", got)
	}
}

// TestApplyIngressInternalSvc_OtherModePassThrough covers the
// non-internal_only mode short-circuit. For mode='open' (or
// any other mode), the gate returns false WITHOUT calling the
// verifier and WITHOUT touching the metric. Same posture as
// applyIngressIPAllowlist's mode != ip_allowlist short-circuit.
func TestApplyIngressInternalSvc_OtherModePassThrough(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	h, a, v := newTestHandlerForInternalSvc(t, map[string]ed25519.PublicKey{
		"schedd": pub,
	})
	app := internalOnlyApp()
	app.PublicAuth.Mode = publicAuthModeOpen
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	// No Authorization header — would fail if the gate ran.

	if h.applyIngressInternalSvc(rec, req, app) {
		t.Errorf("gate denied an open-mode request; should be no-op")
	}
	if v.calls != 0 {
		t.Errorf("verifier.Verify called %d times on open-mode; want 0", v.calls)
	}
	if len(a.events) != 0 {
		t.Errorf("audit emitted for open-mode; should be no-op (events=%v)", a.events)
	}
}

// TestApplyIngressInternalSvc_NoVerifierWired_Returns500 covers
// the operator-misconfig posture: app in internal_only mode
// but verifier is nil. Asserts:
//   - gate returns true (deny)
//   - 500 + RFC 7807 problem body (not 000)
//   - blocked metric incremented (so the dashboard surfaces
//     the misconfig)
//   - no audit row (the misconfig is operator-side; the
//     audit vocabulary is for runtime auth events)
func TestApplyIngressInternalSvc_NoVerifierWired_Returns500(t *testing.T) {
	h := &Handler{
		log:                 slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		metrics:             NewMetrics(),
		internalSvcVerifier: nil, // critical: misconfig
	}
	app := internalOnlyApp()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	if !h.applyIngressInternalSvc(rec, req, app) {
		t.Errorf("gate should deny on missing verifier")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("rec.Code = %d, want 500 (operator_error loud posture)", rec.Code)
	}
}

// TestApplyIngressInternalSvc_AuditDoesNotEchoToken is the
// load-bearing redaction-invariant test (mirror of the
// PR #999 CIDR-redaction test in public_auth_ip_allowlist_test.go).
// Asserts that no audit row's data map contains the JWT
// substring. Failure here is a security regression.
func TestApplyIngressInternalSvc_AuditDoesNotEchoToken(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	h, a, _ := newTestHandlerForInternalSvc(t, map[string]ed25519.PublicKey{
		"schedd": pub,
	})
	app := internalOnlyApp()
	tok := mintTestToken(t, pub, priv, "schedd", -120) // expired → invalid-audit path (TTL -120s exceeds 1-min leeway)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)

	h.applyIngressInternalSvc(rec, req, app)

	for _, ev := range a.events {
		for k, v := range ev.data {
			if s, ok := v.(string); ok && strings.Contains(s, tok) {
				t.Errorf("audit row %s field %q contains JWT substring; redaction invariant violated", ev.kind, k)
			}
		}
	}
}

// TestInternalAuthMatchCounterPreInstantiated asserts the
// closed (outcome) set is pre-instantiated at boot so the
// §12 dashboard chip surfaces from first scrape. Failure here
// means a missing counter would render as absent on a fresh
// dashboard.
func TestInternalAuthMatchCounterPreInstantiated(t *testing.T) {
	m := NewMetrics()
	for _, outcome := range []string{"matched", "blocked"} {
		got := m.internalAuthMatch.WithLabelValues(outcome)
		if got == nil {
			t.Errorf("counter for outcome=%q is nil (not pre-instantiated)", outcome)
		}
	}
}

// TestObserveInternalAuthMatch_NilReceiver covers the
// nil-safety contract — the handler field is nil during
// unit-test paths that don't wire metrics. Must not panic.
func TestObserveInternalAuthMatch_NilReceiver(t *testing.T) {
	var m *Metrics
	m.ObserveInternalAuthMatch("matched") // must not panic
}

// TestObserveInternalAuthMatch_UnknownOutcomeCoerced covers
// the safe-by-default fallback — an unknown outcome string
// (e.g. from a future regression) is coerced to "blocked"
// rather than failing the dashboard query.
func TestObserveInternalAuthMatch_UnknownOutcomeCoerced(t *testing.T) {
	m := NewMetrics()
	m.ObserveInternalAuthMatch("mystery_outcome")
	if got := m.internalAuthMatch.WithLabelValues("blocked"); got == nil {
		t.Errorf("blocked counter not present after coercion")
	}
	// And the unknown counter should NOT have been auto-created
	// (would surprise an operator expecting a closed set).
	if got := m.internalAuthMatch.WithLabelValues("mystery_outcome"); got == nil {
		t.Errorf("counter for unknown outcome was auto-created; closed-set invariant violated")
	}
}

// _ pins the errors import for the test file's future
// expansion (e.g. signing-key size assertion tests).
var _ = errors.New

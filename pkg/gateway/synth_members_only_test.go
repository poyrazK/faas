package gateway

// synth_members_only_test.go — ADR-120 tests for
// SynthServer.applyIngressMembersOnly (the cron-bypass
// closure for the org-membership ingress gate). Mirrors
// synth_internal_only_test.go's per-surface pattern:
// each of the three call sites (handleSynthesize /
// handleInvocationDispatch / handleInvocationDispatchBatch)
// gets the gate wired at the same point in the chain, and
// the audit "from" tag distinguishes them so the
// operator's dashboard can split cron vs human-fired
// denials.
//
// The gate is the dominant "deny" surface — cron has no
// human session, so every cron-fired /v1/synthesize
// against a members_only app hits the no_cookie_principal
// branch and 403s. The two human-fired paths
// (dispatch, dispatch_batch) need the membership
// predicate to fire so a non-member of the org can't
// "trigger wake" against a members_only app via the
// dashboard curl. Both failure modes get audit
// redaction tests; the load-bearing invariant is the
// cookie envelope NEVER appears in the audit payload.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
)

// synthMembersTestAuditor is the audit-emit stub the
// synth-side tests use. It records every Emit call in a
// thread-safe list so a test can assert the kind +
// data fields. Mirrors testCountingAuthnAuditor at
// synth_internal_only_test.go:30 with the closure-shape
// `synthAuditEmit` SynthServer uses (different from
// the HTTP-side `edgeRuleAudit` interface).
type synthMembersTestAuditor struct {
	mu     sync.Mutex
	events []synthMembersAuditEvent
}

type synthMembersAuditEvent struct {
	kind   string
	data   map[string]any
	called bool
}

func (a *synthMembersTestAuditor) Emit(ctx context.Context, kind string, subject *string, data map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, synthMembersAuditEvent{kind: kind, data: data, called: true})
}

func (a *synthMembersTestAuditor) countByKind(kind string) int {
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

// fixedOrgIDLookup returns the same orgID for every
// app_id. Production wiring at cmd/gatewayd-internal
// reads the per-app LRU cache (same shape as the
// Handler-side OrgID lookup); the test keeps a single
// constant so the gate's "empty OrgID" misconfig branch
// can be exercised with a different constructor.
func fixedOrgIDLookup(orgID string) func(ctx context.Context, appID string) string {
	return func(ctx context.Context, appID string) string {
		return orgID
	}
}

// newTestSynthServerForMembersOnly constructs a
// SynthServer with the minimum wiring needed to
// exercise applyIngressMembersOnly. Mirrors
// newTestSynthServerForInternalSvc at
// synth_internal_only_test.go:30 with the members-only
// bridge types (CookiePrincipalExtractor +
// OrgMemberChecker).
func newTestSynthServerForMembersOnly(t *testing.T, mode string, checker authz.OrgMemberChecker, principal CookiePrincipalExtractor, orgIDLookup func(ctx context.Context, appID string) string) (*SynthServer, *synthMembersTestAuditor) {
	t.Helper()
	a := &synthMembersTestAuditor{}
	srv := &SynthServer{
		log:                  slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)),
		metrics:              NewMetrics(),
		membersOnlyChecker:   checker,
		membersOnlyPrincipal: principal,
		appOrgID:             orgIDLookup,
		synthAuditEmit:       a.Emit,
	}
	return srv, a
}

// TestSynthApplyIngressMembersOnly_NoCookie_CronDenied pins
// the dominant cron-fired path. handleSynthesize from
// schedd cron carries no faas_sid → extractor returns
// ok=false → gate 403s with reason="no_cookie_principal"
// and emits the audit row tagged from="synth". The
// Wake must NOT fire (load-bearing invariant — the gate
// short-circuits before dispatcher.Wake). Mirrors
// TestSynthApplyIngressInternalSvc_MissingHeader at
// synth_internal_only_test.go:51.
func TestSynthApplyIngressMembersOnly_NoCookie_CronDenied(t *testing.T) {
	checker := &stubMembersChecker{member: true}
	principal := &fakePrincipalExtractor{accountID: "", ok: false}
	srv, a := newTestSynthServerForMembersOnly(t, publicAuthModeMembersOnly, checker, principal, fixedOrgIDLookup("org-1"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/synthesize", nil)

	if !srv.applyIngressMembersOnly(rec, req, "app-1", publicAuthModeMembersOnly, "synth") {
		t.Errorf("gate should deny on no-cookie cron path; body=%q", rec.Body.String())
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("rec.Code = %d, want 403", rec.Code)
	}
	if got := a.countByKind("instances.public_auth_members_blocked"); got != 1 {
		t.Errorf("members-blocked audit count = %d, want 1", got)
	}
	// The audit "from" tag is "synth" for the legacy
	// /v1/synthesize path; the operator's dashboard
	// uses this to split cron from human-fired
	// /v1/invocations:dispatch (from="synth_dispatch")
	// and /v1/invocations:dispatch_batch (from="synth_batch").
	for _, ev := range a.events {
		from, _ := ev.data["from"].(string)
		if from != "synth" {
			t.Errorf("audit row %s from = %q, want \"synth\"", ev.kind, from)
		}
		reason, _ := ev.data["reason"].(string)
		if reason != "no_cookie_principal" {
			t.Errorf("audit row %s reason = %q, want \"no_cookie_principal\"", ev.kind, reason)
		}
	}
	if len(checker.captures) != 0 {
		t.Errorf("checker called on no-cookie path; want 0 (no SQL without a principal)")
	}
}

// TestSynthApplyIngressMembersOnly_OtherModePassThrough
// pins the no-op branch: mode != publicAuthModeMembersOnly
// returns false without touching the checker. Same
// shape as applyIngressIPAllowlist's mode-gate at
// handler.go:2225 and the Handler-side members-only
// pass-through at public_auth_members_only_test.go.
func TestSynthApplyIngressMembersOnly_OtherModePassThrough(t *testing.T) {
	checker := &stubMembersChecker{member: false}
	principal := &fakePrincipalExtractor{accountID: "acct-x", ok: true}
	srv, _ := newTestSynthServerForMembersOnly(t, publicAuthModeIPAllowlist, checker, principal, fixedOrgIDLookup("org-1"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/synthesize", nil)

	if srv.applyIngressMembersOnly(rec, req, "app-1", publicAuthModeIPAllowlist, "synth") {
		t.Errorf("gate should be no-op for non-members_only mode")
	}
	if len(checker.captures) != 0 {
		t.Errorf("checker called on non-members_only mode; want 0 (no SQL)")
	}
}

// TestSynthApplyIngressMembersOnly_LookupError_FailClosed
// pins the fail-closed posture: the DB returns an
// error wrapped with authz.ErrMembershipLookup → gate
// 403s with reason="lookup_error" (synth-side uses 403,
// not 401 like the HTTP-side; cron has no human
// session, so the 401-vs-403 split collapses to 403).
// Mirrors ADR-119 round-2 fail-closed lesson at
// cmd/schedd/internal_svc_minter.go:6.
func TestSynthApplyIngressMembersOnly_LookupError_FailClosed(t *testing.T) {
	checker := &stubMembersChecker{member: false, err: authz.ErrMembershipLookup}
	principal := &fakePrincipalExtractor{accountID: "acct-x", ok: true}
	srv, a := newTestSynthServerForMembersOnly(t, publicAuthModeMembersOnly, checker, principal, fixedOrgIDLookup("org-1"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/synthesize", nil)

	if !srv.applyIngressMembersOnly(rec, req, "app-1", publicAuthModeMembersOnly, "synth") {
		t.Errorf("gate should deny on lookup error; body=%q", rec.Body.String())
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("rec.Code = %d, want 403 (synth-side fail-closed)", rec.Code)
	}
	// Confirm the audit reason is lookup_error.
	var foundReason string
	for _, ev := range a.events {
		if ev.kind == "instances.public_auth_members_blocked" {
			if r, ok := ev.data["reason"].(string); ok {
				foundReason = r
			}
		}
	}
	if foundReason != "lookup_error" {
		t.Errorf("audit reason = %q, want \"lookup_error\"", foundReason)
	}
}

// TestSynthApplyIngressMembersOnly_ActiveMember_PassThrough
// pins the human-fired /v1/synthesize happy path: a
// human operator with a valid session cookie + active
// membership passes through the gate. The dashboard's
// "trigger wake" button (or a future CLI surface)
// would use this path. Cron would never reach this
// branch (no cookie).
func TestSynthApplyIngressMembersOnly_ActiveMember_PassThrough(t *testing.T) {
	checker := &stubMembersChecker{member: true}
	principal := &fakePrincipalExtractor{accountID: "acct-member", ok: true}
	srv, a := newTestSynthServerForMembersOnly(t, publicAuthModeMembersOnly, checker, principal, fixedOrgIDLookup("org-1"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/synthesize", nil)

	if srv.applyIngressMembersOnly(rec, req, "app-1", publicAuthModeMembersOnly, "synth") {
		t.Errorf("gate denied an active member; body=%q", rec.Body.String())
	}
	if len(a.events) != 0 {
		t.Errorf("audit emitted on pass-through; should be silent (events=%v)", a.events)
	}
	if len(checker.captures) != 1 {
		t.Fatalf("checker captures = %d; want 1", len(checker.captures))
	}
	if got, want := checker.captures[0].orgID, "org-1"; got != want {
		t.Errorf("checker.orgID = %q; want %q", got, want)
	}
	if got, want := checker.captures[0].accountID, "acct-member"; got != want {
		t.Errorf("checker.accountID = %q; want %q", got, want)
	}
}

// TestSynthApplyIngressMembersOnly_NonMember_Returns403 pins
// the human-fired non-member path: a logged-in user
// who is NOT a member of the org tries to "trigger
// wake" a members_only app via the dashboard curl.
// 403 (authn passed, authz denied) — same wire surface
// as the HTTP-side gate.
func TestSynthApplyIngressMembersOnly_NonMember_Returns403(t *testing.T) {
	checker := &stubMembersChecker{member: false}
	principal := &fakePrincipalExtractor{accountID: "acct-other", ok: true}
	srv, a := newTestSynthServerForMembersOnly(t, publicAuthModeMembersOnly, checker, principal, fixedOrgIDLookup("org-1"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/synthesize", nil)

	if !srv.applyIngressMembersOnly(rec, req, "app-1", publicAuthModeMembersOnly, "synth") {
		t.Errorf("gate should deny on non-member; body=%q", rec.Body.String())
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("rec.Code = %d, want 403", rec.Code)
	}
	if got := a.countByKind("instances.public_auth_members_blocked"); got != 1 {
		t.Errorf("members-blocked audit count = %d, want 1", got)
	}
	var foundReason string
	for _, ev := range a.events {
		if ev.kind == "instances.public_auth_members_blocked" {
			if r, ok := ev.data["reason"].(string); ok {
				foundReason = r
			}
		}
	}
	if foundReason != "not_member" {
		t.Errorf("audit reason = %q, want \"not_member\"", foundReason)
	}
}

// TestSynthApplyIngressMembersOnly_EmptyOrgID_Returns500 pins
// the operator-misconfig branch: app row missing
// apps.org_id (pre-00099 migration drift) on a
// members_only mode → 500. Same loud-posture as the
// HTTP-side empty-OrgID branch at
// public_auth_members_only.go:215.
func TestSynthApplyIngressMembersOnly_EmptyOrgID_Returns500(t *testing.T) {
	checker := &stubMembersChecker{member: true}
	principal := &fakePrincipalExtractor{accountID: "acct-x", ok: true}
	srv, _ := newTestSynthServerForMembersOnly(t, publicAuthModeMembersOnly, checker, principal, fixedOrgIDLookup(""))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/synthesize", nil)

	if !srv.applyIngressMembersOnly(rec, req, "app-1", publicAuthModeMembersOnly, "synth") {
		t.Errorf("gate should deny on empty OrgID; body=%q", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("rec.Code = %d, want 500 (misconfig)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "misconfigured") {
		t.Errorf("body missing 'misconfigured' hint: %s", rec.Body.String())
	}
}

// TestSynthApplyIngressMembersOnly_WiringIncomplete_Returns500
// pins the operator-misconfig no-wired-checker branch:
// nil OrgMemberChecker on a members_only mode → 500.
// Same shape as the HTTP-side wiring-incomplete branch
// at public_auth_members_only_test.go.
func TestSynthApplyIngressMembersOnly_WiringIncomplete_Returns500(t *testing.T) {
	// principal wired so the test surfaces ONLY the
	// nil-checker branch.
	principal := &fakePrincipalExtractor{accountID: "acct-x", ok: true}
	srv, _ := newTestSynthServerForMembersOnly(t, publicAuthModeMembersOnly, nil, principal, fixedOrgIDLookup("org-1"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/synthesize", nil)

	if !srv.applyIngressMembersOnly(rec, req, "app-1", publicAuthModeMembersOnly, "synth") {
		t.Errorf("gate should deny on nil checker; body=%q", rec.Body.String())
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("rec.Code = %d, want 500 (misconfig)", rec.Code)
	}
}

// TestSynthApplyIngressMembersOnly_Dispatch_FromTag splits
// the audit "from" tag for /v1/invocations:dispatch
// (single-invocation surface) so the operator's
// dashboard can attribute denials to the right
// inbound path. Mirrors
// TestSynthApplyIngressInternalSvc_DispatchMissingHeader
// at synth_internal_only_test.go:266.
func TestSynthApplyIngressMembersOnly_Dispatch_FromTag(t *testing.T) {
	checker := &stubMembersChecker{member: true}
	principal := &fakePrincipalExtractor{accountID: "", ok: false}
	srv, a := newTestSynthServerForMembersOnly(t, publicAuthModeMembersOnly, checker, principal, fixedOrgIDLookup("org-1"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch", nil)

	if !srv.applyIngressMembersOnly(rec, req, "app-1", publicAuthModeMembersOnly, "synth_dispatch") {
		t.Errorf("dispatch gate should deny on no-cookie path; body=%q", rec.Body.String())
	}
	for _, ev := range a.events {
		from, _ := ev.data["from"].(string)
		if from != "synth_dispatch" {
			t.Errorf("audit row %s from = %q, want \"synth_dispatch\"", ev.kind, from)
		}
	}
}

// TestSynthApplyIngressMembersOnly_Batch_FromTag splits the
// audit "from" tag for /v1/invocations:dispatch_batch
// (batch surface). Mirrors
// TestSynthApplyIngressInternalSvc_BatchMissingHeader
// at synth_internal_only_test.go:381.
func TestSynthApplyIngressMembersOnly_Batch_FromTag(t *testing.T) {
	checker := &stubMembersChecker{member: true}
	principal := &fakePrincipalExtractor{accountID: "", ok: false}
	srv, a := newTestSynthServerForMembersOnly(t, publicAuthModeMembersOnly, checker, principal, fixedOrgIDLookup("org-1"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/invocations:dispatch_batch", nil)

	if !srv.applyIngressMembersOnly(rec, req, "app-1", publicAuthModeMembersOnly, "synth_batch") {
		t.Errorf("batch gate should deny on no-cookie path; body=%q", rec.Body.String())
	}
	for _, ev := range a.events {
		from, _ := ev.data["from"].(string)
		if from != "synth_batch" {
			t.Errorf("audit row %s from = %q, want \"synth_batch\"", ev.kind, from)
		}
	}
}

// TestSynthApplyIngressMembersOnly_AuditDoesNotEchoCookie
// pins the load-bearing redaction invariant on the
// synth side. Synthesises a fake "cookie" payload via
// the principal extractor (the cookie itself is a
// header value) and asserts the audit row NEVER
// contains that string. Failure here is a security
// regression.
func TestSynthApplyIngressMembersOnly_AuditDoesNotEchoCookie(t *testing.T) {
	const cookieSubstr = "REDACTED-FAAS-SID-12345"
	checker := &stubMembersChecker{member: false}
	principal := &fakePrincipalExtractor{accountID: "acct-x", ok: true}
	srv, a := newTestSynthServerForMembersOnly(t, publicAuthModeMembersOnly, checker, principal, fixedOrgIDLookup("org-1"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/synthesize", nil)
	req.AddCookie(&http.Cookie{Name: "faas_sid", Value: cookieSubstr})

	srv.applyIngressMembersOnly(rec, req, "app-1", publicAuthModeMembersOnly, "synth")

	for _, ev := range a.events {
		for k, v := range ev.data {
			if s, ok := v.(string); ok && strings.Contains(s, cookieSubstr) {
				t.Errorf("audit row %s field %q contains cookie substring; redaction invariant violated", ev.kind, k)
			}
		}
	}
}

// _ pins the api package import for the test file's
// expansion surface.
var _ = api.WriteProblem

// _ pins the errors package for the synth-side test
// expansion surface (the synthetic error chain for
// fail-closed tests).
var _ = errors.Is

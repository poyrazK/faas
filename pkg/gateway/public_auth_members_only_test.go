//go:build !no_pg

// public_auth_members_only_test.go — pin the
// applyIngressMembersOnly gate (Handler side) on the
// per-app `members_only` public-auth mode (ADR-123).
// Mirrors public_auth_internal_only_test.go for the
// same-file-per-feature convention; the synth-side
// mirror at synth_members_only.go has its own test
// file (synth_members_only_test.go) following
// synth_internal_only_test.go.
//
// Build tag matches the pgtest-using family
// (!no_pg); set FAAS_SKIP_PG_TESTS=1 to skip locally
// (see migrations/README.md). The test owns its own
// pgtest.Open + db.MigrateUp — no shared fixture with
// the synth-mirror file.
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakePrincipalExtractor is the cookie-side bridge
// stub the unit tests use to drive
// applyIngressMembersOnly without booting the cmd-side
// adapter. Production wires the bridge at
// cmd/gatewayd-internal/auth_principal_adapter.go; the
// unit-test side lives here in pkg/gateway (same pattern
// as internalSvcVerifier fake at
// public_auth_internal_only_test.go:130).
type fakePrincipalExtractor struct {
	accountID string
	ok        bool
}

func (f *fakePrincipalExtractor) FromRequest(r *http.Request) (string, bool) {
	return f.accountID, f.ok
}

// stubMembersChecker is the OrgMemberChecker stub the
// tests use to deterministically drive the membership
// predicate without touching the DB. Production wires
// pkg/authz.PoolOrgMemberChecker (pgxpool-backed);
// the stub mirrors its (bool, error) signature so the
// tests can pivot on the exact sentinel
// (authz.ErrMembershipLookup) as production does.
type stubMembersChecker struct {
	// member is what IsOrgMember returns when err is
	// nil. Override per test.
	member bool
	// err is what IsOrgMember returns when set. The
	// helper preserves the (false, err) shape so the
	// gate's errors.Is(err, ErrMembershipLookup) branch
	// fires correctly in the lookup_error test.
	err error
	// captures records the (orgID, accountID) tuple
	// every call saw, so a test can assert the gate
	// routed the request through this checker with
	// the right keys.
	captures []orgMemberCapture
}

type orgMemberCapture struct {
	orgID, accountID string
}

func (s *stubMembersChecker) IsOrgMember(ctx context.Context, orgID, accountID string) (bool, error) {
	s.captures = append(s.captures, orgMemberCapture{orgID: orgID, accountID: accountID})
	if s.err != nil {
		return false, s.err
	}
	return s.member, nil
}

// acctFromID is the local state.Account fixture helper.
// Mirrors pkg/auth/middleware/middleware_test.go::
// mkActiveAccount's shape (no Email validation; the
// service layer doesn't read it for the cookie path).
func acctFromID(id string) state.Account {
	return state.Account{ID: id, Email: id + "@x", Status: state.AccountActive}
}

// newMembersOnlyTestHandler constructs a Handler with
// the minimum wiring needed to exercise
// applyIngressMembersOnly. Uses internal/test-only
// helpers (no cmd-side wiring required).
func newMembersOnlyTestHandler(t *testing.T, mode string, checker authz.OrgMemberChecker, principal CookiePrincipalExtractor) *Handler {
	t.Helper()
	h := &Handler{
		membersOnlyChecker:   checker,
		membersOnlyPrincipal: principal,
	}
	// metrics + audit can be nil — the gate is nil-safe
	// (mirror applyIngressInternalSvc's nil-safe posture).
	return h
}

// App fixture helper. Returns an App with PublicAuth.Mode
// pinned to the test mode. OrgID is pinned to a sentinel
// the tests assert against.
func membersApp(mode, orgID string) App {
	return App{
		ID:         "app-test",
		AccountID:  "acct-owner",
		Plan:       api.PlanHobby,
		Slug:       "members-only-test",
		PublicAuth: PublicAuthConfig{Mode: mode},
		OrgID:      orgID,
	}
}

// TestApplyIngressMembersOnly_ActiveMember_PassThrough pins
// the happy path: valid cookie + active member returns
// false (no gate fired) so the request proceeds to the
// downstream public_auth/wake chain. Mirrors the
// TestApplyIngressInternalOnly_TokenVerified pattern at
// public_auth_internal_only_test.go.
func TestApplyIngressMembersOnly_ActiveMember_PassThrough(t *testing.T) {
	checker := &stubMembersChecker{member: true}
	principal := &fakePrincipalExtractor{accountID: "acct-member", ok: true}
	h := newMembersOnlyTestHandler(t, publicAuthModeMembersOnly, checker, principal)
	app := membersApp(publicAuthModeMembersOnly, "org-1")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(middleware.WithPrincipal(r.Context(), acctFromID("acct-member"), nil, nil))
	w := httptest.NewRecorder()

	if h.applyIngressMembersOnly(w, r, app) {
		t.Fatalf("applyIngressMembersOnly returned true on active member; want false (pass-through). body=%s", w.Body.String())
	}
	if len(checker.captures) != 1 {
		t.Fatalf("checker.captures = %d; want 1", len(checker.captures))
	}
	if got, want := checker.captures[0].orgID, "org-1"; got != want {
		t.Errorf("checker.orgID = %q; want %q", got, want)
	}
	if got, want := checker.captures[0].accountID, "acct-member"; got != want {
		t.Errorf("checker.accountID = %q; want %q", got, want)
	}
}

// TestApplyIngressMembersOnly_NoPrincipal_Returns401 pins the
// no-cookie path. The principal extractor returns ok=false
// (no faas_sid cookie was attached — typical of a
// fresh-anon request from an api curl, or a fired-by-bug
// pre-PR-5 route that never went through RequireSession).
// The gate returns 401 with the audit row carrying
// `reason=no_cookie` (the cookie-present-but-revoked
// subtype falls through RequireSession upstream).
func TestApplyIngressMembersOnly_NoPrincipal_Returns401(t *testing.T) {
	checker := &stubMembersChecker{member: true}
	principal := &fakePrincipalExtractor{accountID: "", ok: false}
	h := newMembersOnlyTestHandler(t, publicAuthModeMembersOnly, checker, principal)
	app := membersApp(publicAuthModeMembersOnly, "org-1")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	if !h.applyIngressMembersOnly(w, r, app) {
		t.Fatalf("applyIngressMembersOnly returned false on no-cookie; want true (gate denied)")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status code = %d; want 401", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "organization member") {
		t.Errorf("body missing member hint: %s", body)
	}
	if len(checker.captures) != 0 {
		t.Errorf("checker.captures = %d; want 0 (no principal → no SQL call)", len(checker.captures))
	}
}

// TestApplyIngressMembersOnly_NonMember_Returns403 pins the
// valid-cookie + non-member path. Mirrors the
// not-a-member audit reason / 403 split: cookie authn
// passed (401 would be wrong), but the authz check fails
// (membership row absent). The customer-facing Problem
// distinguishes "you forgot to log in" (401) vs "you
// are logged in but not in this org" (403) so the
// dashboard can render different CTAs.
func TestApplyIngressMembersOnly_NonMember_Returns403(t *testing.T) {
	checker := &stubMembersChecker{member: false}
	principal := &fakePrincipalExtractor{accountID: "acct-other", ok: true}
	h := newMembersOnlyTestHandler(t, publicAuthModeMembersOnly, checker, principal)
	app := membersApp(publicAuthModeMembersOnly, "org-1")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(middleware.WithPrincipal(r.Context(), acctFromID("acct-other"), nil, nil))
	w := httptest.NewRecorder()

	if !h.applyIngressMembersOnly(w, r, app) {
		t.Fatalf("applyIngressMembersOnly returned false on non-member; want true (gate denied)")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status code = %d; want 403", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Not a member") {
		t.Errorf("body missing 'Not a member' hint: %s", body)
	}
	if len(checker.captures) != 1 {
		t.Fatalf("checker.captures = %d; want 1", len(checker.captures))
	}
	if got, want := checker.captures[0].accountID, "acct-other"; got != want {
		t.Errorf("checker.accountID = %q; want %q", got, want)
	}
}

// TestApplyIngressMembersOnly_LookupError_FailClosed pins
// the fail-closed posture: the DB returns an error wrapped
// with authz.ErrMembershipLookup → gate returns 401 (same
// wire surface as no-cookie) and emits
// reason=lookup_error in the audit row. Mirror ADR-119
// round-2 fail-closed lesson at
// cmd/schedd/internal_svc_minter.go:6.
func TestApplyIngressMembersOnly_LookupError_FailClosed(t *testing.T) {
	checker := &stubMembersChecker{member: false, err: authz.ErrMembershipLookup}
	principal := &fakePrincipalExtractor{accountID: "acct-x", ok: true}
	h := newMembersOnlyTestHandler(t, publicAuthModeMembersOnly, checker, principal)
	app := membersApp(publicAuthModeMembersOnly, "org-1")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(middleware.WithPrincipal(r.Context(), acctFromID("acct-x"), nil, nil))
	w := httptest.NewRecorder()

	if !h.applyIngressMembersOnly(w, r, app) {
		t.Fatalf("applyIngressMembersOnly returned false on lookup error; want true (fail-closed)")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status code = %d; want 401 (fail-closed)", w.Code)
	}
}

// TestApplyIngressMembersOnly_EmptyOrgID_Returns500 pins
// the operator-misconfig branch: an app row missing
// apps.org_id (pre-00099 migration drift) on a
// members_only row triggers the loud 500 surface, not a
// silent pass-through. The mirror shape is
// applyIngressIPAllowlist's empty-CIDR branch and
// applyIngressInternalSvc's no-verifier branch — three
// ingress-gate surfaces that share the "the operator
// sees loud rather than the customer sees silent" rule.
func TestApplyIngressMembersOnly_EmptyOrgID_Returns500(t *testing.T) {
	checker := &stubMembersChecker{member: true}
	principal := &fakePrincipalExtractor{accountID: "acct-x", ok: true}
	h := newMembersOnlyTestHandler(t, publicAuthModeMembersOnly, checker, principal)
	app := membersApp(publicAuthModeMembersOnly, "") // empty OrgID = misconfig

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	if !h.applyIngressMembersOnly(w, r, app) {
		t.Fatalf("applyIngressMembersOnly returned false on empty OrgID; want true (misconfig)")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d; want 500 (misconfig)", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "misconfigured") {
		t.Errorf("body missing 'misconfigured' hint: %s", body)
	}
}

// TestApplyIngressMembersOnly_OtherModePassThrough pins
// the no-op branch: mode != publicAuthModeMembersOnly
// returns false without touching the checker. Same
// shape as applyIngressIPAllowlist's mode-gate at
// handler.go:2225 / applyIngressInternalSvc's mode-gate
// at internal_svc_auth.go:158.
func TestApplyIngressMembersOnly_OtherModePassThrough(t *testing.T) {
	checker := &stubMembersChecker{member: false}
	principal := &fakePrincipalExtractor{accountID: "acct-x", ok: true}
	h := newMembersOnlyTestHandler(t, publicAuthModeIPAllowlist, checker, principal)
	app := membersApp(publicAuthModeIPAllowlist, "org-1")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	if h.applyIngressMembersOnly(w, r, app) {
		t.Fatalf("applyIngressMembersOnly returned true on non-members_only mode; want false (no-op)")
	}
	if len(checker.captures) != 0 {
		t.Errorf("checker.captures = %d; want 0 (mode != members_only → no SQL)", len(checker.captures))
	}
}

// TestApplyIngressMembersOnly_WiringIncomplete_Returns500
// pins the operator-misconfig no-wired-checker branch:
// nil OrgMemberChecker on a members_only mode → 500.
// Same shape as applyIngressInternalSvc's
// no-verifier branch at internal_svc_auth.go:161 —
// the gate refuses to silently pass.
func TestApplyIngressMembersOnly_WiringIncomplete_Returns500(t *testing.T) {
	h := &Handler{
		// Checker intentionally nil; principal wired so
		// the test surfaces ONLY the nil-checker branch.
		membersOnlyPrincipal: &fakePrincipalExtractor{accountID: "x", ok: true},
	}
	app := membersApp(publicAuthModeMembersOnly, "org-1")

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	if !h.applyIngressMembersOnly(w, r, app) {
		t.Fatalf("applyIngressMembersOnly returned false on nil checker; want true (500 misconfig)")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status code = %d; want 500", w.Code)
	}
}

// TestApplyIngressMembersOnly_DBBacked_HappyPath pins the
// full path against a real pgxpool (the production
// surface) so a future pgx-pool change that breaks the
// OrgMemberChecker wiring surfaces here, not in
// cmd/e2e. Uses pgtest.Open + db.MigrateUp the same way
// pkg/authz/is_org_member_test.go does; they're paired
// tests — the helper here is intentionally minimal so
// both files compose cleanly with cross-file fixtures.
func TestApplyIngressMembersOnly_DBBacked_HappyPath(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	const (
		orgID  = "00000000-0000-0000-0000-00000000c101"
		acctID = "00000000-0000-0000-0000-00000000c201"
	)
	// Seed account + org + active membership.
	if _, err := pool.Exec(ctx,
		`INSERT INTO accounts (id, email, plan, created_at) VALUES ($1, $2, 'hobby', now()) ON CONFLICT (id) DO NOTHING`,
		acctID, "members-only-e2e@test.local",
	); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO orgs (id, slug, created_at) VALUES ($1, $2, now()) ON CONFLICT (id) DO NOTHING`,
		orgID, "members-only-e2e-org",
	); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO org_memberships (org_id, account_id, role, joined_at) VALUES ($1, $2, 'admin', now()) ON CONFLICT DO NOTHING`,
		orgID, acctID,
	); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	// Production OrgMemberChecker wiring.
	checker := authz.PoolOrgMemberChecker(pool)
	principal := &fakePrincipalExtractor{accountID: acctID, ok: true}
	h := &Handler{
		membersOnlyChecker:   checker,
		membersOnlyPrincipal: principal,
	}
	app := membersApp(publicAuthModeMembersOnly, orgID)

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(middleware.WithPrincipal(r.Context(), acctFromID(acctID), nil, nil))
	w := httptest.NewRecorder()

	if h.applyIngressMembersOnly(w, r, app) {
		t.Fatalf("applyIngressMembersOnly: gate denied active member; want pass-through. body=%s", w.Body.String())
	}
	// Same flow against non-member: gate denies with 403.
	if _, err := pool.Exec(ctx, `DELETE FROM org_memberships WHERE org_id = $1 AND account_id = $2`, orgID, acctID); err != nil {
		t.Fatalf("remove membership: %v", err)
	}
	w = httptest.NewRecorder()
	if !h.applyIngressMembersOnly(w, r, app) {
		t.Fatalf("applyIngressMembersOnly: gate pass-through on non-member; want 403")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("status code = %d; want 403", w.Code)
	}
}

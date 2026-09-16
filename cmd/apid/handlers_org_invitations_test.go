// Whitebox tests for the accept / revoke invitation handlers and
// the seat-usage visibility endpoint (IAM-6 / ADR-061, PR 7).
//
// Coverage:
//   - org.member.added + org.invitation.accepted on successful accept
//   - org.invitation.revoked on successful revoke
//   - audit-kind rename pins (org.member.{removed,role_changed} dotted
//     form; the legacy id-style strings must NOT be re-emitted)
//   - gate-before-emit guards (over-cap, after-revoke, cross-owner)
//   - seat-usage wire shape (Hobby happy path + Free fail-closed at 0)
//
// Pattern mirrors cmd/apid/handlers_audit_test.go:1016 — drive a
// handler via e.do, fetch events via ListEvents, find by kind via
// findEventByKind, assert via mustAuditEvent + payload unmarshal.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// seedInvitationForPrincipal creates a pending invitation for the
// supplied principal account at the given org/role. The plaintext
// token is the SHA-256 input the store will look up; the returned
// wireToken is the base64url-encoded form used by peek and accept,
// while invitation carries the stable row ID used by revoke.
// The store enforces email-match inside ConsumeOrgInvitation
// — the seed MUST use the accepting account's email or the accept
// path returns ErrOrgInvitationInvalid.
func seedInvitationForPrincipal(t *testing.T, store *state.MemStore, org *state.Org, ownerID, acceptingEmail string, role state.OrgRole) (wireToken string, invitation state.OrgInvitation) {
	t.Helper()
	plaintext := make([]byte, 32)
	for i := range plaintext {
		plaintext[i] = byte(i + 1)
	}
	sum := sha256.Sum256(plaintext)
	created, err := store.CreateOrgInvitation(context.Background(), state.OrgInvitation{
		OrgID:              org.ID,
		Email:              acceptingEmail,
		Role:               role,
		TokenHash:          sum[:],
		ExpiresAt:          time.Now().Add(time.Hour),
		InvitedByAccountID: &ownerID,
	})
	if err != nil {
		t.Fatalf("CreateOrgInvitation: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(plaintext), created
}

// seedInvitationNonce is the nonce-aware twin of
// seedInvitationForPrincipal for tests that need multiple pending
// invitations on the same org (the underlying UNIQUE on token_hash
// rejects identical tokens; nonce XORs into byte 0 so each call
// generates a distinct hash).
func seedInvitationNonce(t *testing.T, store *state.MemStore, org *state.Org, ownerID, email string, role state.OrgRole, nonce byte) {
	t.Helper()
	plaintext := make([]byte, 32)
	for i := range plaintext {
		plaintext[i] = byte(i + 1)
	}
	plaintext[0] = nonce
	sum := sha256.Sum256(plaintext)
	if _, err := store.CreateOrgInvitation(context.Background(), state.OrgInvitation{
		OrgID:              org.ID,
		Email:              email,
		Role:               role,
		TokenHash:          sum[:],
		ExpiresAt:          time.Now().Add(time.Hour),
		InvitedByAccountID: &ownerID,
	}); err != nil {
		t.Fatalf("CreateOrgInvitation: %v", err)
	}
}

// seedSharedOrgWithOwner creates a shared org on the given plan and
// adds the testEnv's principal as owner. Returns the org so the test
// can drive routes under /v1/orgs/{slug}/...
func seedSharedOrgWithOwner(t *testing.T, e testEnv, slug, name string, plan api.Plan) state.Org {
	t.Helper()
	ctx := context.Background()
	org, err := e.store.CreateOrg(ctx, state.Org{
		Slug: slug, Name: name, Plan: plan,
	})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	if err := e.store.AddOrgMember(ctx, org.ID, e.acct.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatalf("AddOrgMember owner: %v", err)
	}
	return org
}

// seedSharedOrgWithAdminOwner creates a shared org owned by an
// OUTSIDE owner (admin-only) so the testEnv principal can be
// invited as a member without tripping the store's already-member
// gate inside ConsumeOrgInvitation. Used by the accept-path tests
// where the bearer (e.acct) IS the invitee.
func seedSharedOrgWithOutsideOwner(t *testing.T, e testEnv, slug, name string, plan api.Plan) state.Org {
	t.Helper()
	ctx := context.Background()
	org, err := e.store.CreateOrg(ctx, state.Org{
		Slug: slug, Name: name, Plan: plan,
	})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	other, err := e.store.CreateAccount(ctx, "outside-owner-"+slug+"@acme.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount outside owner: %v", err)
	}
	if err := e.store.AddOrgMember(ctx, org.ID, other.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatalf("AddOrgMember outside owner: %v", err)
	}
	return org
}

// TestAuditEvents_OrgMemberAddedOnAcceptEmitsEvent (PR 7) drives
// the invite → accept roundtrip end-to-end and asserts
// org.member.added lands with the expected payload. Happy path.
//
// The bearer (e.acct) IS the invitee — the accept handler uses
// the bearer principal as `acceptingAccount`. The org must NOT
// have e.acct as an owner already (otherwise the store fires
// ErrOrgAlreadyMember on the email-match branch).
func TestAuditEvents_OrgMemberAddedOnAcceptEmitsEvent(t *testing.T) {
	e, cookie := setupWithSessionFreshStepUpForTest(t)
	org := seedSharedOrgWithOutsideOwner(t, e, "acme-pr7-add", "Acme PR7 Add", api.PlanPro)

	// Seed an invitation whose email matches the accepting
	// principal (pro@example.com from setup). The store's
	// email-match gate inside ConsumeOrgInvitation refuses any
	// mismatch with ErrOrgInvitationInvalid.
	wireToken, _ := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, e.acct.Email, state.OrgRoleDeveloper)

	// PR-9 §4: PR-9 closes the bearer-bypass on acceptInvitation,
	// so the PR-7 happy-path uses a fresh-stamp cookie.
	rec := e.doWithCookie(t, http.MethodPost, "/v1/invitations/"+wireToken+"/accept", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST accept: code=%d body=%s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := mustAuditEvent(t, findEventByKind(rows, "org.member.added"),
		"no org.member.added row; rows="+eventDump(rows))
	if found.Subject == nil || found.Subject.String() != uuidStringOf(e.acct.ID) {
		t.Errorf("Subject = %v, want %s", found.Subject, uuidStringOf(e.acct.ID))
	}
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["org_id"] != org.ID {
		t.Errorf("data.org_id = %v, want %s", data["org_id"], org.ID)
	}
	if data["role"] != string(state.OrgRoleDeveloper) {
		t.Errorf("data.role = %v, want %s", data["role"], state.OrgRoleDeveloper)
	}
	if data["email"] != e.acct.Email {
		t.Errorf("data.email = %v, want %s", data["email"], e.acct.Email)
	}
}

// TestAuditEvents_OrgInvitationAcceptedEmitsEvent (PR 7) — the
// invitation-side mirror of TestAuditEvents_OrgMemberAddedOnAccept.
// Both kinds fire from the same accept call site.
func TestAuditEvents_OrgInvitationAcceptedEmitsEvent(t *testing.T) {
	e, cookie := setupWithSessionFreshStepUpForTest(t)
	org := seedSharedOrgWithOutsideOwner(t, e, "acme-pr7-acc", "Acme PR7 Acc", api.PlanPro)
	wireToken, _ := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, e.acct.Email, state.OrgRoleDeveloper)

	// PR-9 §4: see TestAuditEvents_OrgMemberAddedOnAcceptEmitsEvent.
	rec := e.doWithCookie(t, http.MethodPost, "/v1/invitations/"+wireToken+"/accept", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST accept: code=%d body=%s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := mustAuditEvent(t, findEventByKind(rows, "org.invitation.accepted"),
		"no org.invitation.accepted row; rows="+eventDump(rows))
	var data map[string]any
	if err := json.Unmarshal(found.Data, &data); err != nil {
		t.Fatalf("Data not valid JSON: %v", err)
	}
	if data["org_id"] != org.ID {
		t.Errorf("data.org_id = %v, want %s", data["org_id"], org.ID)
	}
	if data["invitation"] == nil || data["invitation"] == "" {
		t.Errorf("data.invitation = %v, want non-empty invitation id", data["invitation"])
	}
}

// TestAuditEvents_OrgInvitationRevokedEmitsEvent (PR 7) drives
// DELETE /v1/orgs/{slug}/invitations/{invitation_id} via the org owner and
// asserts org.invitation.revoked identifies the row without exposing token
// material.
func TestAuditEvents_OrgInvitationRevokedEmitsEvent(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "acme-pr7-rev", "Acme PR7 Rev", api.PlanPro)
	_, invitation := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, "rev-target@acme.test", state.OrgRoleDeveloper)

	rec := e.do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/invitations/"+invitation.ID, nil, map[string]string{
		"X-Active-Org": org.Slug,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE invite: code=%d body=%s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	ev := mustAuditEvent(t, findEventByKind(rows, "org.invitation.revoked"),
		"no org.invitation.revoked row; rows="+eventDump(rows))
	if ev.Subject == nil || ev.Subject.String() != uuidStringOf(e.acct.ID) {
		t.Errorf("Subject = %v, want %s", ev.Subject, uuidStringOf(e.acct.ID))
	}
	var data map[string]any
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("Unmarshal Data: %v", err)
	}
	if data["org_id"] != org.ID {
		t.Errorf("data.org_id = %v, want %s", data["org_id"], org.ID)
	}
	if data["invitation_id"] != invitation.ID {
		t.Errorf("data.invitation_id = %v, want %s", data["invitation_id"], invitation.ID)
	}
	if _, exists := data["token_hash_prefix"]; exists {
		t.Error("audit event exposes token_hash_prefix")
	}
}

// TestAuditEvents_OrgMemberRoleChangedEmitsDottedKind (PR 7) pins
// the rename. PATCH /v1/orgs/{slug}/members/{user_id} must emit
// org.member.role_changed (dotted), NEVER the legacy
// org.member_role_changed.
func TestAuditEvents_OrgMemberRoleChangedEmitsDottedKind(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "acme-pr7-role", "Acme PR7 Role", api.PlanPro)
	dev, err := e.store.CreateAccount(context.Background(), "role-dev@acme.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount dev: %v", err)
	}
	if err := e.store.AddOrgMember(context.Background(), org.ID, dev.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatalf("AddOrgMember dev: %v", err)
	}
	rec := e.do(t, http.MethodPatch, "/v1/orgs/"+org.Slug+"/members/"+dev.ID,
		api.ChangeRoleRequest{Role: string(state.OrgRoleAdmin)},
		map[string]string{"X-Active-Org": org.Slug})
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH role: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if findEventByKind(rows, "org.member_role_changed") != nil {
		t.Errorf("legacy id-style kind org.member_role_changed was emitted; PR 7 must drop it")
	}
	mustAuditEvent(t, findEventByKind(rows, "org.member.role_changed"),
		"no org.member.role_changed (dotted) row; rows="+eventDump(rows))
}

// TestAuditEvents_OrgMemberRemovedEmitsDottedKind (PR 7) — same
// posture for the remove path.
func TestAuditEvents_OrgMemberRemovedEmitsDottedKind(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "acme-pr7-rm", "Acme PR7 Rm", api.PlanPro)
	dev, err := e.store.CreateAccount(context.Background(), "rm-dev@acme.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount dev: %v", err)
	}
	if err := e.store.AddOrgMember(context.Background(), org.ID, dev.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatalf("AddOrgMember dev: %v", err)
	}
	rec := e.do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/members/"+dev.ID, nil, map[string]string{
		"X-Active-Org": org.Slug,
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE member: code=%d body=%s", rec.Code, rec.Body.String())
	}
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if findEventByKind(rows, "org.member_removed") != nil {
		t.Errorf("legacy id-style kind org.member_removed was emitted; PR 7 must drop it")
	}
	mustAuditEvent(t, findEventByKind(rows, "org.member.removed"),
		"no org.member.removed (dotted) row; rows="+eventDump(rows))
}

// TestAcceptInvitation_AlreadyMemberSurfacesExistingRole (PR 7) —
// when the bearer accepts an invitation for an email they're
// ALREADY a member of, the store returns ErrOrgAlreadyMember with
// a zero OrgMembership. The handler must look up the existing role
// via OrgMemberByAccount so the wire message ships the actual role
// (e.g. "developer") rather than the sentinel's empty string.
//
// Seeding shape: e.acct is the OWNER of the org (seedSharedOrgWithOwner),
// so we mint an invitation for e.acct's email — the accept path
// hits the already-member branch in the store.
func TestAcceptInvitation_AlreadyMemberSurfacesExistingRole(t *testing.T) {
	e, cookie := setupWithSessionFreshStepUpForTest(t)
	org := seedSharedOrgWithOwner(t, e, "acme-pr7-already", "Acme PR7 Already", api.PlanPro)

	// Mint an invitation whose email matches e.acct (the owner).
	// ConsumeOrgInvitation refuses unknown emails; the email-match
	// gate passes, then the already-active-membership check fires.
	wireToken, _ := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, e.acct.Email, state.OrgRoleDeveloper)

	rec := e.doWithCookie(t, http.MethodPost, "/v1/invitations/"+wireToken+"/accept", cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("already-member accept: code=%d, want 409; body=%s", rec.Code, rec.Body.String())
	}

	var problem struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("Unmarshal problem: %v", err)
	}
	if problem.Code != "org_already_member" {
		t.Errorf("problem.code = %q, want org_already_member", problem.Code)
	}
	// The fix lands the OWNER role string in the detail — proving
	// OrgMemberByAccount ran and the handler used its return
	// value, not the zero-string sentinel-bearing error path.
	wantSubstr := `"owner"`
	if !strings.Contains(problem.Detail, wantSubstr) {
		t.Errorf("problem.detail = %q, want it to contain %s", problem.Detail, wantSubstr)
	}
}

// TestAcceptInvitation_GateFiresBeforeEmit (PR 7) — accepting an
// already-revoked token must surface ErrOrgInvitationInvalid (410)
// and emit NEITHER org.member.added NOR org.invitation.accepted.
// The store-side consume is the load-bearing check; the handler
// surfaces the sentinel before the audit emit at handlers_org_invitations.go:191.
func TestAcceptInvitation_GateFiresBeforeEmit(t *testing.T) {
	e, cookie := setupWithSessionFreshStepUpForTest(t)
	org := seedSharedOrgWithOwner(t, e, "acme-pr7-rev2", "Acme PR7 Rev2", api.PlanPro)
	wireToken, invitation := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, e.acct.Email, state.OrgRoleDeveloper)

	// Pre-revoke the invitation so the consume tx fires the
	// already-revoked branch.
	if err := e.store.RevokeOrgInvitation(context.Background(), org.ID, invitation.ID, e.acct.ID); err != nil {
		t.Fatalf("pre-revoke: %v", err)
	}

	rec := e.doWithCookie(t, http.MethodPost, "/v1/invitations/"+wireToken+"/accept", cookie)
	if rec.Code != http.StatusGone {
		t.Fatalf("accept after revoke: code=%d, want 410; body=%s", rec.Code, rec.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("Unmarshal problem: %v", err)
	}
	if problem.Code != "org_invitation_invalid" {
		t.Errorf("problem.code = %q, want org_invitation_invalid", problem.Code)
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if findEventByKind(rows, "org.member.added") != nil {
		t.Errorf("org.member.added emitted on revoked-token accept; gate failed")
	}
	if findEventByKind(rows, "org.invitation.accepted") != nil {
		t.Errorf("org.invitation.accepted emitted on revoked-token accept; gate failed")
	}
}

// TestAcceptInvitation_OverCapDoesNotEmit (PR 7) — pins the store-
// side cap-in-tx back-stop. Hobby plan caps members at 10; with 10
// members already on the org, one more accept attempt must surface
// ErrOrgMemberCapExceeded (403) and emit neither kind.
func TestAcceptInvitation_OverCapDoesNotEmit(t *testing.T) {
	e, cookie := setupWithSessionFreshStepUpForTest(t)
	org := seedSharedOrgWithOwner(t, e, "hobby-pr7-cap", "Hobby PR7 Cap", api.PlanHobby)

	// Fill to Hobby's OrgMembersMax=10 (1 owner + 9 dev). The owner
	// seed uses AddOrgMember which has no cap check, so the cap is
	// only enforced inside ConsumeOrgInvitation.
	for i := 0; i < 9; i++ {
		fillAcct, err := e.store.CreateAccount(context.Background(),
			"cap-"+string(rune('a'+i))+"@hobby.test", api.PlanFree)
		if err != nil {
			t.Fatalf("CreateAccount cap-fill %d: %v", i, err)
		}
		if err := e.store.AddOrgMember(context.Background(), org.ID, fillAcct.ID, state.OrgRoleDeveloper, nil); err != nil {
			t.Fatalf("AddOrgMember cap-fill %d: %v", i, err)
		}
	}

	wireToken, _ := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, e.acct.Email, state.OrgRoleDeveloper)
	rec := e.doWithCookie(t, http.MethodPost, "/v1/invitations/"+wireToken+"/accept", cookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("accept over-cap: code=%d, want 403; body=%s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if findEventByKind(rows, "org.member.added") != nil {
		t.Errorf("org.member.added emitted on over-cap accept; store-side cap-in-tx failed")
	}
	if findEventByKind(rows, "org.invitation.accepted") != nil {
		t.Errorf("org.invitation.accepted emitted on over-cap accept; store-side cap-in-tx failed")
	}
}

// TestRevokeInvitation_NonMemberDoesNotEmit (PR 7) — a principal
// that is NOT a member of the seeded org must hit the LoadOrg
// gate (403 org_role_forbidden) and never reach the audit emit.
// The harness's principal (e.acct) is the only bearer this test
// can drive, so we seed an org they're NOT a member of. The
// CrossOwner angle is also covered indirectly by the route table
// test TestOrgRoutes_GatedByAuthorize (server_org_authz_test.go:100)
// which walks every /v1/orgs/{slug}/* pattern and confirms 4xx on
// no-membership.
func TestRevokeInvitation_NonMemberDoesNotEmit(t *testing.T) {
	e := setup(t, api.PlanPro)
	// Seed an org WITHOUT adding e.acct as a member. LoadOrg's
	// membership lookup must trip 403 before the handler body runs.
	org, err := e.store.CreateOrg(context.Background(), state.Org{
		Slug: "foreign-pr7", Name: "Foreign PR7", Plan: api.PlanPro,
	})
	if err != nil {
		t.Fatalf("CreateOrg: %v", err)
	}
	wireToken, _ := seedInvitationForPrincipal(t, e.store, &org, "other-owner-id", "someone@foreign.test", state.OrgRoleDeveloper)

	rec := e.do(t, http.MethodDelete, "/v1/orgs/"+org.Slug+"/invitations/"+wireToken, nil, map[string]string{
		"X-Active-Org": org.Slug,
	})
	if rec.Code < 400 {
		t.Fatalf("DELETE invite (non-member): code=%d, want 4xx; body=%s", rec.Code, rec.Body.String())
	}

	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if findEventByKind(rows, "org.invitation.revoked") != nil {
		t.Errorf("org.invitation.revoked emitted for non-member; LoadOrg gate failed")
	}
}

// TestSeatUsage_HappyPath (PR 7) — GET /v1/orgs/{slug}/seat_usage
// returns {used=1, limit=10, plan="hobby"} for a single-owner shared
// org on Hobby.
func TestSeatUsage_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	org := seedSharedOrgWithOwner(t, e, "hobby-pr7-usage", "Hobby PR7 Usage", api.PlanHobby)

	rec := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/seat_usage", nil, map[string]string{
		"X-Active-Org": org.Slug,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET seat_usage: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body api.SeatUsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v (body=%s)", err, rec.Body.String())
	}
	if body.Used != 1 {
		t.Errorf("Used = %d, want 1", body.Used)
	}
	if body.Limit != 10 {
		t.Errorf("Limit = %d, want 10 (Hobby OrgMembersMax)", body.Limit)
	}
	if body.Plan != string(api.PlanHobby) {
		t.Errorf("Plan = %q, want %q", body.Plan, api.PlanHobby)
	}
}

// TestSeatUsage_FreePlanReturnsZero (PR 7) — Free plan returns
// limit=0 (fail-closed accessor); the dashboard renders "personal
// org only" instead of "0 of 0 used".
func TestSeatUsage_FreePlanReturnsZero(t *testing.T) {
	e := setup(t, api.PlanFree)
	org := seedSharedOrgWithOwner(t, e, "free-pr7-usage", "Free PR7 Usage", api.PlanFree)

	rec := e.do(t, http.MethodGet, "/v1/orgs/"+org.Slug+"/seat_usage", nil, map[string]string{
		"X-Active-Org": org.Slug,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("GET seat_usage: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body api.SeatUsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v (body=%s)", err, rec.Body.String())
	}
	if body.Limit != 0 {
		t.Errorf("Limit = %d, want 0 (Free fail-closed)", body.Limit)
	}
	if body.Plan != string(api.PlanFree) {
		t.Errorf("Plan = %q, want %q", body.Plan, api.PlanFree)
	}
	if body.Used != 1 {
		t.Errorf("Used = %d, want 1 (the owner)", body.Used)
	}
}

// TestAcceptInvitation_RequiresStepUp (PR 8) — pin the step-up
// gate on POST /v1/invitations/{token}/accept. ADR-077 closed the
// same threat model for the other 8 sensitive routes; PR-8 adds
// accept-invitation to that list. The gate trips BEFORE the
// store-side ConsumeOrgInvitation so a leaked plaintext token
// cannot mint a membership without a fresh TOTP on the bearer's
// session.
//
// Subtests:
//  1. cookie + missing step-up stamp  → 403 + auth.step_up_required audit row
//     (pre-PR-077 cookies fall into the "missing" bucket per the
//     bypass tolerance at middleware.go:836-847; PR-8's gate is
//     opt-in to the row, so the missing branch is the regression
//     net for "the audit row fires when the stamp is missing".)
//  2. cookie + expired step-up stamp  → 403 + reason="expired"
//  3. cookie + fresh step-up stamp    → 200 (happy path)
//
// Bearer-key principals skip the gate (an API key is step-up-
// equivalent proof). The existing PR-7 tests at line 109 / 151
// already cover the bearer-bypass path; PR-8 doesn't re-pin it.
func TestAcceptInvitation_RequiresStepUp(t *testing.T) {
	ensureRecoveryTestSecret(t)

	// --- Subtest 1: cookie with no step-up stamp → 403 -----------------
	t.Run("missing_step_up_returns_403", func(t *testing.T) {
		e, _, cookie := setupWithSessionForTest(t)
		// setupWithSession issues via IssueWithSession which has
		// no StepUpAt — the cookie decodes with StepUpAt zero,
		// which RequireStepUp classifies as "missing" (the pre-
		// PR-077 legacy branch). Per the bypass tolerance the
		// gate DOES fire (with reason=missing) on legacy cookies
		// once the route is wired to require step-up; the
		// missing-bypass is documented for stamps NEVER seen,
		// not for "stamp once then reissue without one". A fresh
		// issuance via IssueWithSession has no stamp at all, so
		// the audit row fires.
		org := seedSharedOrgWithOutsideOwner(t, e, "acme-pr8-stp-miss", "Acme PR8 Step-up Missing", api.PlanPro)
		wireToken, _ := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, e.acct.Email, state.OrgRoleDeveloper)

		rec := e.doWithCookie(t, http.MethodPost, "/v1/invitations/"+wireToken+"/accept", cookie)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("missing step-up: code=%d body=%s, want 403", rec.Code, rec.Body.String())
		}
		var problem struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
			t.Fatalf("Unmarshal problem: %v", err)
		}
		// PR-8 fix: the gate now uses CodeStepUpRequired (distinct
		// from CodeMFARequired) so the dashboard can render
		// "re-enter your authenticator code" copy distinctly
		// from "enable MFA to continue".
		if problem.Code != api.CodeStepUpRequired {
			t.Errorf("problem.code = %q, want %q", problem.Code, api.CodeStepUpRequired)
		}
		// Audit row lands with reason=missing.
		rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		ev := findEventByKind(rows, "auth.step_up_required")
		if ev == nil {
			t.Fatalf("no auth.step_up_required row; rows=%s", eventDump(rows))
		}
		var data map[string]any
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			t.Fatalf("Data not JSON: %v", err)
		}
		if data["path"] != "/v1/invitations/"+wireToken+"/accept" {
			t.Errorf("path = %v, want %s", data["path"], "/v1/invitations/"+wireToken+"/accept")
		}
		if data["method"] != http.MethodPost {
			t.Errorf("method = %v, want POST", data["method"])
		}
		if data["reason"] != "missing" {
			t.Errorf("reason = %v, want missing", data["reason"])
		}
		if data["ttl_sec"] != float64(300) {
			t.Errorf("ttl_sec = %v, want 300 (5-minute TTL)", data["ttl_sec"])
		}
	})

	// --- Subtest 2: cookie with EXPIRED step-up stamp → 403 -------------
	t.Run("expired_step_up_returns_403", func(t *testing.T) {
		e, mgr, cookie := setupWithSessionForTest(t)
		org := seedSharedOrgWithOutsideOwner(t, e, "acme-pr8-stp-exp", "Acme PR8 Step-up Expired", api.PlanPro)
		wireToken, _ := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, e.acct.Email, state.OrgRoleDeveloper)

		// Issue a fresh cookie with a stamp older than the TTL
		// (10 minutes ago vs 5-minute TTL).
		staleCookie := reissueCookieWithStepUp(t, cookie, mgr, time.Now().Add(-10*time.Minute))

		req := httptest.NewRequest(http.MethodPost, "/v1/invitations/"+wireToken+"/accept", nil)
		req.AddCookie(staleCookie)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expired step-up: code=%d body=%s, want 403", rec.Code, rec.Body.String())
		}
		rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		ev := findEventByKind(rows, "auth.step_up_required")
		if ev == nil {
			t.Fatalf("no auth.step_up_required row; rows=%s", eventDump(rows))
		}
		var data map[string]any
		if err := json.Unmarshal(ev.Data, &data); err != nil {
			t.Fatalf("Data not JSON: %v", err)
		}
		if data["reason"] != "expired" {
			t.Errorf("reason = %v, want expired", data["reason"])
		}
	})

	// --- Subtest 3: cookie with FRESH step-up stamp → 200 ---------------
	t.Run("fresh_step_up_passes", func(t *testing.T) {
		e, mgr, cookie := setupWithSessionForTest(t)
		org := seedSharedOrgWithOutsideOwner(t, e, "acme-pr8-stp-fresh", "Acme PR8 Step-up Fresh", api.PlanPro)
		wireToken, _ := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, e.acct.Email, state.OrgRoleDeveloper)

		// Issue a fresh cookie with a stamp 30 seconds old (well
		// within the 5-minute TTL).
		freshCookie := reissueCookieWithStepUp(t, cookie, mgr, time.Now().Add(-30*time.Second))

		req := httptest.NewRequest(http.MethodPost, "/v1/invitations/"+wireToken+"/accept", nil)
		req.AddCookie(freshCookie)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("fresh step-up: code=%d body=%s, want 200", rec.Code, rec.Body.String())
		}
		// No auth.step_up_required audit row fires on the happy path.
		rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
		if err != nil {
			t.Fatalf("ListEvents: %v", err)
		}
		if ev := findEventByKind(rows, "auth.step_up_required"); ev != nil {
			t.Errorf("unexpected auth.step_up_required on fresh-stamp happy path: %s", eventDump(rows))
		}
		// The two accept-side audit rows DO fire on the happy path.
		if findEventByKind(rows, "org.invitation.accepted") == nil {
			t.Errorf("org.invitation.accepted missing on fresh-stamp happy path")
		}
		if findEventByKind(rows, "org.member.added") == nil {
			t.Errorf("org.member.added missing on fresh-stamp happy path")
		}
	})
}

// TestAcceptInvitation_BearerKey_RequiresStepUp (PR-9 §4) — pin
// the bearer-key rejection on POST /v1/invitations/{token}/accept.
// PR-9 closes the v1 bearer-bypass path: a leaked invitation token
// alone is sufficient to grant org membership, so the bearer key
// is no longer step-up-equivalent proof. The other 8 requireStepUp
// mounts keep the documented bypass (PR-9 audits acceptInvitation
// only). The audit row fires with strict=true so the operator can
// filter for the new posture in audit queries.
func TestAcceptInvitation_BearerKey_RequiresStepUp(t *testing.T) {
	ensureRecoveryTestSecret(t)

	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOutsideOwner(t, e, "acme-pr9-stp-bearer", "Acme PR9 Bearer Step-up", api.PlanPro)
	wireToken, _ := seedInvitationForPrincipal(t, e.store, &org, e.acct.ID, e.acct.Email, state.OrgRoleDeveloper)

	// e.do sets Authorization: Bearer e.key — the v1 bypass
	// let this through. PR-9 must reject with 403.
	rec := e.do(t, http.MethodPost, "/v1/invitations/"+wireToken+"/accept", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("bearer key: code=%d body=%s, want 403", rec.Code, rec.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("Unmarshal problem: %v", err)
	}
	if problem.Code != api.CodeStepUpRequired {
		t.Errorf("problem.code = %q, want %q", problem.Code, api.CodeStepUpRequired)
	}

	// Audit row fires with strict=true so the operator can
	// filter for the PR-9 closure in audit queries.
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	ev := findEventByKind(rows, "auth.step_up_required")
	if ev == nil {
		t.Fatalf("no auth.step_up_required row; rows=%s", eventDump(rows))
	}
	var data map[string]any
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("Data not JSON: %v", err)
	}
	if data["path"] != "/v1/invitations/"+wireToken+"/accept" {
		t.Errorf("path = %v, want /v1/invitations/%s/accept", data["path"], wireToken)
	}
	if data["reason"] != "missing" {
		t.Errorf("reason = %v, want missing", data["reason"])
	}
	if data["strict"] != true {
		t.Errorf("strict = %v, want true (PR-9 §4 marker)", data["strict"])
	}
	if data["ttl_sec"] != float64(300) {
		t.Errorf("ttl_sec = %v, want 300", data["ttl_sec"])
	}

	// The accept-side audit rows MUST NOT fire on the rejected
	// request — the gate is BEFORE the store-side consume.
	if findEventByKind(rows, "org.invitation.accepted") != nil {
		t.Errorf("org.invitation.accepted emitted on rejected bearer request")
	}
	if findEventByKind(rows, "org.member.added") != nil {
		t.Errorf("org.member.added emitted on rejected bearer request")
	}
}

// reissueCookieWithStepUp unseals the source cookie via the
// supplied session manager, swaps the StepUpAt stamp to the
// requested time, and re-seals. Mirrors the
// reissueSessionCookieWithStepUp call site at handlers_mfa.go:712
// without going through /v1/account/mfa/verify.
//
// Tests use this to construct "expired stamp" / "fresh stamp"
// cookies for the PR-8 step-up gate assertions. The source cookie's
// sid + account + binding hash are preserved so the IAM-3 sid
// cross-check at requireSessionCookie still passes.
//
// The manager is supplied explicitly (not derived from a global)
// because NewEphemeralManager generates a fresh random key per
// call — a separate manager can't Open a cookie minted by another
// manager in the same test. setupWithSessionForTest (below) wires
// the manager into the testEnv so the test + helper share keys.
func reissueCookieWithStepUp(t *testing.T, source *http.Cookie, mgr *session.Manager, stepUpAt time.Time) *http.Cookie {
	t.Helper()
	env, err := mgr.Verify(source.Value)
	if err != nil {
		t.Fatalf("session.Verify: %v", err)
	}
	token, err := mgr.IssueWithSessionAndBindingHashAndStepUp(
		env.Sid, env.AccountID, env.BindingHash, stepUpAt, env.MfaPending)
	if err != nil {
		t.Fatalf("IssueWithSessionAndBindingHashAndStepUp: %v", err)
	}
	return &http.Cookie{Name: source.Name, Value: token}
}

// setupWithSessionForTest is the manager-aware twin of
// setupWithSession (handlers_scopes_test.go:56): it builds the
// server with the same deps and returns the session.Manager so
// reissueCookieWithStepUp can unseal + reseal cookies minted by
// the same key. The base setupWithSession deliberately drops the
// manager pointer to keep its signature minimal; PR-8 needs it.
func setupWithSessionForTest(t *testing.T) (testEnv, *session.Manager, *http.Cookie) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(),
		"session-cookie-pr8@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err := store.CreateSession(context.Background(), sid, acct.ID,
		"192.0.2.30", "session-pr8-test-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	token, err := mgr.IssueWithSession(sid, acct.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("apid_session_pr8_test")
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr,
		nil, 15*60_000_000_000, "").WithOpsMetrics(context.Background(), ops)
	return testEnv{h: srv.handler(), store: store, key: "", acct: acct, ops: ops},
		mgr, &http.Cookie{Name: sessionCookie, Value: token}
}

// setupWithSessionFreshStepUpForTest is the PR-9 sibling of
// setupWithSessionForTest: it mints a cookie with a fresh
// step-up stamp so the test reaches the handler past the
// requireStepUpStrict(5m) gate on POST /v1/invitations/{token}/accept.
// Used by the legacy PR-7 happy-path tests that previously
// relied on the bearer-key bypass; PR-9 closes the bypass.
func setupWithSessionFreshStepUpForTest(t *testing.T) (testEnv, *http.Cookie) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(),
		"session-fresh-pr9@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err := store.CreateSession(context.Background(), sid, acct.ID,
		"192.0.2.31", "session-pr9-fresh-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	stepUp := time.Now().Add(-30 * time.Second) // fresh within 5m TTL
	token, err := mgr.IssueWithSessionAndBindingHashAndStepUp(sid, acct.ID, "", stepUp, false)
	if err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("apid_session_pr9_test")
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr,
		nil, 15*60_000_000_000, "").WithOpsMetrics(context.Background(), ops)
	return testEnv{h: srv.handler(), store: store, key: "", acct: acct, ops: ops},
		&http.Cookie{Name: sessionCookie, Value: token}
}

// eventDump renders the rows slice as JSON for inclusion in a
// doWithCookie is the cookie-bearing sibling of testEnv.do for the
// routes that PR-9 flipped to require a fresh step-up stamp
// (acceptInvitation). Plugs the session cookie into the request
// before dispatching to the raw handler. Mirrors the testEnv.do
// shape (returns *httptest.ResponseRecorder) so the assertion
// sites stay one-liners.
func (e testEnv) doWithCookie(t *testing.T, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// t.Fatal message. Mirrors the SA5011 escape hatch used in the
// other audit-emit tests.
func eventDump(rows []state.Event) string {
	b, _ := json.Marshal(rows)
	return string(b)
}

// TestListOrgInvitations_HappyPath (PR 8) —
// GET /v1/orgs/{slug}/invitations returns the seeded invitations
// in created_at desc order with the org slug + derived status
// (pending/consumed/revoked/expired) on each row.
//
// Pins:
//   - response carries every invitation (no filtering by state)
//   - status is "pending" for unconsumed rows
//   - org_invitation.viewed audit row fires per render
//   - next_before is empty when the row count is < limit
func TestListOrgInvitations_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "list-pr8-happy", "List PR8 Happy", api.PlanPro)
	// 3 pending invitations (distinct nonces to satisfy the
	// UNIQUE on token_hash).
	for i, role := range []state.OrgRole{state.OrgRoleAdmin, state.OrgRoleDeveloper, state.OrgRoleBilling} {
		seedInvitationNonce(t, e.store, &org, e.acct.ID,
			fmt.Sprintf("list-pr8-%d@acme.test", i), role, byte(i+1))
	}

	rec := e.do(t, http.MethodGet, "/v1/orgs/list-pr8-happy/invitations", nil,
		map[string]string{"X-Active-Org": "list-pr8-happy"})
	if rec.Code != http.StatusOK {
		t.Fatalf("list: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Invitations []api.OrgInvitationResponse `json:"invitations"`
		NextBefore  string                      `json:"next_before"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v (body=%s)", err, rec.Body.String())
	}
	if len(body.Invitations) != 3 {
		t.Fatalf("Invitations len = %d, want 3", len(body.Invitations))
	}
	for i, row := range body.Invitations {
		if row.OrgSlug != "list-pr8-happy" {
			t.Errorf("Invitations[%d].OrgSlug = %q, want list-pr8-happy", i, row.OrgSlug)
		}
		if row.Status != "pending" {
			t.Errorf("Invitations[%d].Status = %q, want pending", i, row.Status)
		}
		if row.Email == "" {
			t.Errorf("Invitations[%d].Email empty", i)
		}
		if row.Role == "" {
			t.Errorf("Invitations[%d].Role empty", i)
		}
	}
	// 3 rows < default limit 25 → no next_before.
	if body.NextBefore != "" {
		t.Errorf("NextBefore = %q, want empty (row_count < limit)", body.NextBefore)
	}
	// Audit row fires.
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	ev := findEventByKind(rows, "org.invitation.viewed")
	if ev == nil {
		t.Fatalf("no org.invitation.viewed row; rows=%s", eventDump(rows))
	}
	var data map[string]any
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("Data not JSON: %v", err)
	}
	if data["org_id"] != org.ID {
		t.Errorf("data.org_id = %v, want %s", data["org_id"], org.ID)
	}
	if data["row_count"] != float64(3) {
		t.Errorf("data.row_count = %v, want 3", data["row_count"])
	}
	if data["had_next_page"] != false {
		t.Errorf("data.had_next_page = %v, want false", data["had_next_page"])
	}
}

// TestListOrgInvitations_NonMemberIs404 (PR 8) — access control:
// a caller who is not a member of the active org gets 404 (the
// loadOrg middleware's IDOR-safe contract; mirrors every other
// org-scoped route).
func TestListOrgInvitations_NonMemberIs404(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "list-pr8-idem", "List PR8 IDem", api.PlanPro)
	// Seed an invitation as the owner so the org has rows, but
	// then create a fresh account that's NOT a member and try to
	// list.
	if _, err := e.store.CreateOrgInvitation(context.Background(), state.OrgInvitation{
		OrgID: org.ID, Email: "list-pr8-idem@acme.test", Role: state.OrgRoleDeveloper,
		TokenHash: []byte("x"), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateOrgInvitation: %v", err)
	}
	outside, err := e.store.CreateAccount(context.Background(),
		"list-pr8-outside@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	outsideKey, outsideHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := e.store.CreateAPIKey(context.Background(), outside.ID, outsideHash,
		"non-member", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	outsideEnv := testEnv{h: e.h, s: e.s, store: e.store, key: outsideKey, acct: outside, ops: e.ops}
	rec := outsideEnv.do(t, http.MethodGet, "/v1/orgs/list-pr8-idem/invitations", nil,
		map[string]string{"X-Active-Org": "list-pr8-idem"})
	// Non-member gets 403 (the authz layer surfaces "you don't
	// belong here" distinctly from the 404 the loadOrg path
	// would return for an unknown slug). The list endpoint
	// deliberately differentiates "you're not a member" from
	// "the org doesn't exist" via the 403/404 split — matching
	// the same posture every other org-scoped route uses (e.g.
	// GET /v1/orgs/{slug}/members).
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member list: code=%d body=%s, want 403", rec.Code, rec.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if problem.Code != "org_role_forbidden" {
		t.Errorf("problem.code = %q, want org_role_forbidden", problem.Code)
	}
}

// TestListOrgInvitations_CursorPagination (PR 8; PR-9 cursor upgrade) —
// drive the cursor: limit=2 + 4 seeded invitations → 2 pages of 2 each.
// Pin: the second-page request includes ?before=<cursor-from-page-1>
// and returns the next 2 rows, with next_before empty on the
// final page.
//
// PR-9: the wire cursor is now a compound (created_at, id) key
// encoded by pkg/cursor. The test below asserts the cursor
// decodes to the correct (created_at, id) of the last row, so
// future PRs that mutate the cursor encoding get a clean error
// here instead of silently feeding the wrong id to PgStore's
// tuple predicate.
func TestListOrgInvitations_CursorPagination(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "list-pr8-cursor", "List PR8 Cursor", api.PlanPro)
	for i := 0; i < 4; i++ {
		seedInvitationNonce(t, e.store, &org, e.acct.ID,
			fmt.Sprintf("cursor-pr8-%d@acme.test", i), state.OrgRoleDeveloper, byte(i+10))
		// 1ms sleep between seeds so the four created_at
		// timestamps are distinct (the cursor walk is
		// (created_at desc, id desc); without distinct
		// timestamps the cursor walk degrades to a pure
		// id-only order, which is fine but the assertion
		// is more meaningful with distinct times).
		time.Sleep(time.Millisecond)
	}
	// Page 1.
	rec := e.do(t, http.MethodGet, "/v1/orgs/list-pr8-cursor/invitations?limit=2", nil,
		map[string]string{"X-Active-Org": "list-pr8-cursor"})
	if rec.Code != http.StatusOK {
		t.Fatalf("page1: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var p1 struct {
		Invitations []api.OrgInvitationResponse `json:"invitations"`
		NextBefore  string                      `json:"next_before"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p1); err != nil {
		t.Fatalf("Unmarshal p1: %v", err)
	}
	if len(p1.Invitations) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(p1.Invitations))
	}
	if p1.NextBefore == "" {
		t.Fatalf("page1 next_before empty; should carry the cursor id")
	}
	// PR-9: the cursor must decode to the (created_at, id) of
	// the last row on page 1. A regression that emits the bare
	// id (or any other drift) trips here.
	k1, err := cursor.Decode(p1.NextBefore)
	if err != nil {
		t.Fatalf("cursor.Decode(page1): %v", err)
	}
	last1 := p1.Invitations[len(p1.Invitations)-1]
	if k1.ID != last1.ID {
		t.Errorf("page1 cursor.ID = %q, want last row ID %q", k1.ID, last1.ID)
	}
	// The wire side of OrgInvitationResponse.CreatedAt is RFC3339
	// (whole-second precision via FormatAlertTime); the cursor's
	// CreatedAt is the raw store time. Compare at second
	// precision to bridge the wire-format rounding — the cursor
	// itself carries the high-precision value so the SQL
	// predicate has the right anchor.
	wireLast1, err := time.Parse(time.RFC3339, last1.CreatedAt)
	if err != nil {
		t.Fatalf("parse last1.CreatedAt %q: %v", last1.CreatedAt, err)
	}
	if k1.CreatedAt.Truncate(time.Second) != wireLast1.Truncate(time.Second) {
		t.Errorf("page1 cursor.CreatedAt (truncated) = %v, want last row CreatedAt %v (wire %v)",
			k1.CreatedAt.Truncate(time.Second), wireLast1.Truncate(time.Second), last1.CreatedAt)
	}
	// Page 2.
	rec = e.do(t, http.MethodGet,
		"/v1/orgs/list-pr8-cursor/invitations?limit=2&before="+p1.NextBefore, nil,
		map[string]string{"X-Active-Org": "list-pr8-cursor"})
	if rec.Code != http.StatusOK {
		t.Fatalf("page2: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var p2 struct {
		Invitations []api.OrgInvitationResponse `json:"invitations"`
		NextBefore  string                      `json:"next_before"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p2); err != nil {
		t.Fatalf("Unmarshal p2: %v", err)
	}
	if len(p2.Invitations) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(p2.Invitations))
	}
	// The handler's cursor-emit heuristic is the same one
	// pkg/api's pagination uses (handlers_account_scoped.go:75):
	// "if the page is full, emit a cursor; the client treats the
	// next 0-row page as terminal". With 4 rows + limit=2, page
	// 2 has 2 rows AND a non-empty NextBefore (the heuristic
	// fires); the client walker must follow the cursor to a 3rd
	// request that returns 0 rows + empty NextBefore.
	rec = e.do(t, http.MethodGet,
		"/v1/orgs/list-pr8-cursor/invitations?limit=2&before="+p2.NextBefore, nil,
		map[string]string{"X-Active-Org": "list-pr8-cursor"})
	if rec.Code != http.StatusOK {
		t.Fatalf("page3: code=%d body=%s", rec.Code, rec.Body.String())
	}
	var p3 struct {
		Invitations []api.OrgInvitationResponse `json:"invitations"`
		NextBefore  string                      `json:"next_before"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p3); err != nil {
		t.Fatalf("Unmarshal p3: %v", err)
	}
	if len(p3.Invitations) != 0 {
		t.Errorf("page3 len = %d, want 0 (terminal page)", len(p3.Invitations))
	}
	if p3.NextBefore != "" {
		t.Errorf("page3 next_before = %q, want empty (terminal)", p3.NextBefore)
	}
	// No row overlap between pages.
	seen := map[string]bool{}
	for _, r := range p1.Invitations {
		seen[r.ID] = true
	}
	for _, r := range p2.Invitations {
		if seen[r.ID] {
			t.Errorf("row %s appears on both pages", r.ID)
		}
	}

	// PR-9: a malformed cursor must surface as 400 invalid_cursor,
	// not silently return 0 rows (the v1 behavior). The client
	// can then retry with a fresh first-page request.
	rec = e.do(t, http.MethodGet,
		"/v1/orgs/list-pr8-cursor/invitations?limit=2&before=not!base64", nil,
		map[string]string{"X-Active-Org": "list-pr8-cursor"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad cursor: code=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	var badProblem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &badProblem); err != nil {
		t.Fatalf("Unmarshal bad cursor: %v", err)
	}
	if badProblem.Code != "invalid_cursor" {
		t.Errorf("bad cursor code = %q, want invalid_cursor", badProblem.Code)
	}
}

// TestListOrgInvitations_BadLimit (PR 8) — limit out of range
// returns 400 with CodeValidation (issue #393 strict-mode
// pagination contract).
func TestListOrgInvitations_BadLimit(t *testing.T) {
	e := setup(t, api.PlanPro)
	org := seedSharedOrgWithOwner(t, e, "list-pr8-bad", "List PR8 Bad", api.PlanPro)
	_ = org

	rec := e.do(t, http.MethodGet, "/v1/orgs/list-pr8-bad/invitations?limit=999", nil,
		map[string]string{"X-Active-Org": "list-pr8-bad"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit: code=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if problem.Code != "validation_failed" {
		t.Errorf("problem.code = %q, want validation_failed", problem.Code)
	}
}

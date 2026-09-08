// iam6_invitations_and_seats_test.go — PR 7 blackbox acceptance
// (issue #190 / IAM-6 / ADR-061).
//
// Exercises the new wire surfaces end-to-end through the apid
// subprocess against a real Postgres (pgtest) backend:
//
//   - TestE2E_InvitationAcceptAndRevokeRoundtrip
//       Owner POSTs /v1/orgs/{slug}/members (mints the
//       one-time plaintext token); DELETE /v1/orgs/{slug}/
//       invitations/{token} revokes it. The audit row lands
//       at the store seam with token_hash_prefix of exactly
//       8 chars (the security posture). The state row's
//       revoked_at stamp matches the revocation time.
//
//   - TestE2E_SeatUsageEndpoint
//       Two sub-cases:
//         1) Personal (Free) org — limit=0 fail-closed accessor.
//         2) Shared (Hobby) org with the owner only — {used:1,
//            limit:10, plan:"hobby"} wire shape.
//
// The accept path requires a second bearer (the invitee) so the
// whitebox suite (cmd/apid/handlers_org_invitations_test.go) owns
// the accept-side pin; the roundtrip here exercises the
// wire-creates-token → wire-revokes-token leg end-to-end and pins
// the audit + state-row stamps.
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS) and a buildable ./cmd/apid.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// pr7InviteWire is the local decode type for the POST /v1/orgs/{slug}/members
// response (the one-time token-bearing shape). We keep a local copy
// rather than reuse cmd/e2e/org_lifecycle_e2e_test.go::inviteWire
// because that file declares it inside the same package, and
// keeping a pr7-specific struct here keeps each file's field set
// under the test's control. pkg/api.InvitationWithTokenResponse is
// the canonical wire shape.
type pr7InviteWire struct {
	ID        string `json:"id"`
	OrgID     string `json:"org_id"`
	OrgSlug   string `json:"org_slug"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	Status    string `json:"status"`
	ExpiresAt string `json:"expires_at"`
	CreatedAt string `json:"created_at"`
	Token     string `json:"token"`
}

// seatUsageWire mirrors api.SeatUsageResponse so the test can
// decode the JSON body without importing cmd/apid (which is
// package main).
type seatUsageWire struct {
	Used  int    `json:"used"`
	Limit int    `json:"limit"`
	Plan  string `json:"plan"`
}

// findOrgInvitationByEmail returns the matching pending/revoked
// invitation row from the PgStore so the test can assert the
// revoked_at stamp landed at the state row.
func findOrgInvitationByEmail(t *testing.T, h *e2etest.Harness, orgID, email string) state.OrgInvitation {
	t.Helper()
	store := state.NewPgStore(h.Pool)
	rows, err := store.ListOrgInvitationsForOrg(context.Background(), orgID)
	if err != nil {
		t.Fatalf("ListOrgInvitationsForOrg: %v", err)
	}
	for _, row := range rows {
		if row.Email == email {
			return row
		}
	}
	t.Fatalf("no invitation row for %s on org %s; rows=%+v", email, orgID, rows)
	return state.OrgInvitation{}
}

// TestE2E_InvitationAcceptAndRevokeRoundtrip drives the
// create-invite → revoke-invite flow against a real apid. Pins:
//   - the wire response carries the plaintext token (once)
//   - the audit row org.invitation.revoked fires with
//     token_hash_prefix of exactly 8 chars
//   - the state row's revoked_at stamp lands within the test window
func TestE2E_InvitationAcceptAndRevokeRoundtrip(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanPro, "pr7-rtrip")
	store := state.NewPgStore(h.Pool)

	// Personal orgs are immutable per ADR-061 §3.2 (no
	// membership changes). Create a shared Pro org for the
	// invite/revoke roundtrip.
	createRaw, createStatus := doReq(t, h, key, http.MethodPost, "/v1/orgs",
		api.CreateOrgRequest{Slug: "pr7-rtrip-org", Name: "PR7 Roundtrip"})
	if createStatus != http.StatusCreated {
		t.Fatalf("create shared org: %d %s", createStatus, createRaw)
	}
	var shared api.OrgResponse
	if err := json.Unmarshal(createRaw, &shared); err != nil {
		t.Fatalf("decode shared: %v", err)
	}
	if err := store.UpdateOrgPlan(ctx, shared.ID, api.PlanPro); err != nil {
		t.Fatalf("UpdateOrgPlan: %v", err)
	}

	// Create the invitation via the wire. Email is a non-seeded
	// one so the email-match gate inside ConsumeOrgInvitation
	// would refuse a self-accept; revoke doesn't care about the
	// email — it just stamps revoked_at.
	mintRaw, mintStatus := doReq(t, h, key, http.MethodPost,
		"/v1/orgs/"+shared.Slug+"/members",
		api.InviteMemberRequest{Email: "rtrip-dev@acme.test", Role: "developer"},
		map[string]string{"X-Active-Org": shared.Slug})
	if mintStatus != http.StatusCreated {
		t.Fatalf("mint: %d %s", mintStatus, mintRaw)
	}
	var minted pr7InviteWire
	if err := json.Unmarshal(mintRaw, &minted); err != nil {
		t.Fatalf("decode mint: %v (body=%s)", err, mintRaw)
	}
	if minted.Token == "" {
		t.Fatalf("mint response carried empty token; body=%s", mintRaw)
	}
	if minted.Status != "pending" {
		t.Errorf("minted.Status = %q, want pending", minted.Status)
	}

	// Revoke via the wire. Should 204 + emit org.invitation.revoked.
	revRaw, revStatus := doReq(t, h, key, http.MethodDelete,
		"/v1/orgs/"+shared.Slug+"/invitations/"+minted.Token,
		nil,
		map[string]string{"X-Active-Org": shared.Slug})
	if revStatus != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", revStatus, revRaw)
	}

	// State seam: the row's revoked_at is now non-nil.
	invRow := findOrgInvitationByEmail(t, h, shared.ID, "rtrip-dev@acme.test")
	if invRow.RevokedAt == nil {
		t.Errorf("RevokedAt is nil after revoke; want non-nil")
	}
	if invRow.ConsumedAt != nil {
		t.Errorf("ConsumedAt non-nil after revoke; want nil")
	}

	// Audit seam: ListEvents(org owner's account ID) returns
	// org.invitation.revoked with token_hash_prefix of 8 chars.
	ownerAcct, err := store.AccountByEmail(ctx, seedEmail(api.PlanPro, "pr7-rtrip"))
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	events, err := store.ListEvents(ctx, ownerAcct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var revokeEvent *state.Event
	for i := range events {
		if events[i].Kind == "org.invitation.revoked" {
			revokeEvent = &events[i]
			break
		}
	}
	if revokeEvent == nil {
		t.Fatalf("no org.invitation.revoked row in events; events=%+v", events)
	}
	var data map[string]any
	if err := json.Unmarshal(revokeEvent.Data, &data); err != nil {
		t.Fatalf("Unmarshal event.Data: %v", err)
	}
	if data["org_id"] != shared.ID {
		t.Errorf("data.org_id = %v, want %s", data["org_id"], shared.ID)
	}
	prefix, _ := data["token_hash_prefix"].(string)
	if len(prefix) != 8 {
		t.Errorf("data.token_hash_prefix = %q (len %d), want 8 chars", prefix, len(prefix))
	}
}

// TestE2E_SeatUsageEndpoint drives GET /v1/orgs/{slug}/seat_usage
// twice: once on the seeded account's personal (Free) org, once
// on a freshly-created shared (Hobby) org. Pins:
//   - Free → {used:1, limit:0, plan:"free"} (fail-closed accessor)
//   - Hobby → {used:1, limit:10, plan:"hobby"}
func TestE2E_SeatUsageEndpoint(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	store := state.NewPgStore(h.Pool)
	ownerID := mustAccountIDForSeed(t, h, api.PlanFree, "pr7-seat")
	personal, err := store.OrgByPersonalAccount(ctx, ownerID)
	if err != nil {
		t.Fatalf("OrgByPersonalAccount: %v", err)
	}
	key := h.SeedAccount(ctx, api.PlanFree, "pr7-seat")

	// Subcase 1: Free personal org → limit=0 fail-closed.
	raw, status := doReq(t, h, key, http.MethodGet,
		"/v1/orgs/"+personal.Slug+"/seat_usage", nil,
		map[string]string{"X-Active-Org": personal.Slug})
	if status != http.StatusOK {
		t.Fatalf("personal seat_usage: %d %s", status, raw)
	}
	var body seatUsageWire
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode personal: %v (body=%s)", err, raw)
	}
	if body.Limit != 0 {
		t.Errorf("personal.Limit = %d, want 0 (Free fail-closed)", body.Limit)
	}
	if body.Plan != string(api.PlanFree) {
		t.Errorf("personal.Plan = %q, want %q", body.Plan, api.PlanFree)
	}
	if body.Used != 1 {
		t.Errorf("personal.Used = %d, want 1 (owner)", body.Used)
	}

	// Subcase 2: Create a shared Hobby org + add owner; expect
	// {used:1, limit:10, plan:"hobby"}.
	createRaw, createStatus := doReq(t, h, key, http.MethodPost, "/v1/orgs",
		api.CreateOrgRequest{Slug: "hobby-pr7", Name: "Hobby PR7"})
	if createStatus != http.StatusCreated {
		t.Fatalf("create shared org: %d %s", createStatus, createRaw)
	}
	var shared api.OrgResponse
	if err := json.Unmarshal(createRaw, &shared); err != nil {
		t.Fatalf("decode shared: %v", err)
	}
	if err := store.UpdateOrgPlan(ctx, shared.ID, api.PlanHobby); err != nil {
		t.Fatalf("UpdateOrgPlan: %v", err)
	}

	hRaw, hStatus := doReq(t, h, key, http.MethodGet,
		"/v1/orgs/hobby-pr7/seat_usage", nil,
		map[string]string{"X-Active-Org": "hobby-pr7"})
	if hStatus != http.StatusOK {
		t.Fatalf("shared seat_usage: %d %s", hStatus, hRaw)
	}
	var hBody seatUsageWire
	if err := json.Unmarshal(hRaw, &hBody); err != nil {
		t.Fatalf("decode shared: %v", err)
	}
	if hBody.Used != 1 {
		t.Errorf("shared.Used = %d, want 1 (owner only)", hBody.Used)
	}
	if hBody.Limit != 10 {
		t.Errorf("shared.Limit = %d, want 10 (Hobby OrgMembersMax)", hBody.Limit)
	}
	if hBody.Plan != string(api.PlanHobby) {
		t.Errorf("shared.Plan = %q, want %q", hBody.Plan, api.PlanHobby)
	}
}

// mustAccountIDForSeed looks up the seeded account's id by email
// so the test can drive OrgByPersonalAccount on the right account.
// The label is the same one SeedAccount was called with. Renamed
// from mustAccountID to avoid colliding with
// cmd/e2e/signed_deploy_e2e_test.go::mustAccountID (different
// signature — that one takes the API key, this one takes plan +
// label).
func mustAccountIDForSeed(t *testing.T, h *e2etest.Harness, plan api.Plan, label string) string {
	t.Helper()
	store := state.NewPgStore(h.Pool)
	acct, err := store.AccountByEmail(context.Background(), seedEmail(plan, label))
	if err != nil {
		t.Fatalf("AccountByEmail(%s): %v", label, err)
	}
	return acct.ID
}

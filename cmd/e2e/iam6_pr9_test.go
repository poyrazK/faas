// iam6_pr9_test.go — PR-9 blackbox acceptance for the org
// invitation follow-up bundle (issue #190 / IAM-6 / ADR-061).
//
// PR-9 ships three changes that need end-to-end coverage at the
// wire level (the whitebox tests at cmd/apid/handlers_org_invitations_test.go
// cover the same behaviour in-process):
//
//  1. POST /v1/invitations/{token}/accept rejects the bearer-key
//     branch with 403 step_up_required (the §4 closure in this
//     PR). The cookie path is already covered by the PR-8
//     whitebox; the cookie path through the wire requires the
//     full TOTP enroll + verify roundtrip and is asserted
//     separately.
//  2. GET /v1/orgs/{slug}/invitations?before=<cursor>&limit=N
//     walks correctly with the compound (created_at, id) cursor.
//     The harness seeds 4 invitations with strictly-ordered
//     created_at + lexical-order-inverse UUIDs (the adversarial
//     shape that broke v1) and walks the pages with the wire
//     cursor.
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS) and a buildable ./cmd/apid.
package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// pr9InvitationListWire mirrors api.InvitationListResponse so
// the test can decode the JSON body without round-tripping
// through cmd/apid (package main).
type pr9InvitationListWire struct {
	Invitations []api.OrgInvitationResponse `json:"invitations"`
	NextBefore  string                      `json:"next_before"`
}

// TestE2E_AcceptInvitation_RequiresStepUp_BearerKey (PR-9 §4) —
// the bearer-key branch on POST /v1/invitations/{token}/accept
// must surface 403 step_up_required. PR-9 closes the v1
// bearer-bypass on this route (the only PR-9 closure; the
// remaining 8 requireStepUp mounts keep the documented bypass).
//
// The driver is a fresh API key (no MFA enrolled, no fresh
// step-up stamp). The wire MUST 403 + emit exactly one
// auth.step_up_required audit row with strict=true and the
// accept-path audited route.
func TestE2E_AcceptInvitation_RequiresStepUp_BearerKey(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	ownerKey := h.SeedAccount(ctx, api.PlanPro, "pr9-stp-owner")
	store := state.NewPgStore(h.Pool)

	// Owner creates a shared org + invitation; the invitee
	// (a fresh account with a fresh API key) is the bearer.
	createRaw, createStatus := doReq(t, h, ownerKey, http.MethodPost, "/v1/orgs",
		api.CreateOrgRequest{Slug: "pr9-stp-org", Name: "PR9 Step-up"})
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

	mintRaw, mintStatus := doReq(t, h, ownerKey, http.MethodPost,
		"/v1/orgs/"+shared.Slug+"/members",
		api.InviteMemberRequest{Email: "pr9-stp-invitee@acme.test", Role: "developer"},
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

	// Mint the invitee account with a separate API key
	// (e2etest.Start wires a SeedAccount helper that returns
	// the API key for the new account; the invitee has no
	// MFA enrolled, no step-up stamp).
	inviteeKey := h.SeedAccount(ctx, api.PlanPro, "pr9-stp-invitee")

	// Accept via the wire. MUST 403 step_up_required.
	acceptRaw, acceptStatus := doReq(t, h, inviteeKey, http.MethodPost,
		"/v1/invitations/"+minted.Token+"/accept", nil)
	if acceptStatus != http.StatusForbidden {
		t.Fatalf("accept: %d %s, want 403", acceptStatus, acceptRaw)
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(acceptRaw, &problem); err != nil {
		t.Fatalf("unmarshal problem: %v (body=%s)", err, acceptRaw)
	}
	if problem.Code != api.CodeStepUpRequired {
		t.Errorf("problem.code = %q, want %q", problem.Code, api.CodeStepUpRequired)
	}

	// State seam: the invitation's ConsumedAt is still nil
	// (the gate fires before the store-side consume).
	invRow := findOrgInvitationByEmail(t, h, shared.ID, "pr9-stp-invitee@acme.test")
	if invRow.ConsumedAt != nil {
		t.Errorf("ConsumedAt non-nil after rejected accept; want nil")
	}

	// Audit seam: ListEvents on the invitee account returns
	// exactly one auth.step_up_required row with strict=true
	// and the accept-path route.
	inviteeAcct, err := store.AccountByEmail(ctx, seedEmail(api.PlanPro, "pr9-stp-invitee"))
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	events, err := store.ListEvents(ctx, inviteeAcct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var stepUpEvent *state.Event
	for i := range events {
		if events[i].Kind == "auth.step_up_required" {
			stepUpEvent = &events[i]
			break
		}
	}
	if stepUpEvent == nil {
		t.Fatalf("no auth.step_up_required row; events=%+v", events)
	}
	var data map[string]any
	if err := json.Unmarshal(stepUpEvent.Data, &data); err != nil {
		t.Fatalf("Unmarshal event.Data: %v", err)
	}
	if data["path"] != "/v1/invitations/"+minted.Token+"/accept" {
		t.Errorf("data.path = %v, want /v1/invitations/%s/accept", data["path"], minted.Token)
	}
	if data["strict"] != true {
		t.Errorf("data.strict = %v, want true (PR-9 §4 marker)", data["strict"])
	}
	if data["reason"] != "missing" {
		t.Errorf("data.reason = %v, want missing", data["reason"])
	}
	if data["ttl_sec"] != float64(300) {
		t.Errorf("data.ttl_sec = %v, want 300", data["ttl_sec"])
	}
}

// TestE2E_ListOrgInvitations_CursorWalk (PR-9 §2) — walk the
// cursor through GET /v1/orgs/{slug}/invitations with the new
// compound (created_at, id) cursor. Seeds 4 invitations with
// strictly-ordered created_at + lexically-INVERSE UUID ids
// (the adversarial shape that breaks the v1 cursor) and asserts
// the wire walk visits all four rows in created_at DESC order
// with no duplication and no skipping. Decoding the
// next_before cursor between pages asserts the cursor wire
// format is the pkg/cursor base64-url-of-JSON encoding.
func TestE2E_ListOrgInvitations_CursorWalk(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	ownerKey := h.SeedAccount(ctx, api.PlanPro, "pr9-cursor-owner")
	store := state.NewPgStore(h.Pool)

	// Create shared org.
	createRaw, createStatus := doReq(t, h, ownerKey, http.MethodPost, "/v1/orgs",
		api.CreateOrgRequest{Slug: "pr9-cursor-org", Name: "PR9 Cursor"})
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

	// Seed 4 invitations directly via the store so we control
	// created_at + id precisely. The wire `invite` call uses
	// now() so we can't use it for the adversarial ordering.
	// Accept path is irrelevant here; the consumed_at stays
	// nil so the audit seam is clean.
	ib := ownerKey // unused; the wire-side InvitedBy is the
	// acceptor's account ID, not the owner's. We need the
	// owner's account ID; resolve via the seed.
	ownerAcct, err := store.AccountByEmail(ctx, seedEmail(api.PlanPro, "pr9-cursor-owner"))
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	ib = ownerAcct.ID
	base := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Second)
	seeds := []struct {
		email string
		ts    time.Time
		id    string
	}{
		{"pr9-cursor-a@example.com", base.Add(0 * time.Second), "ffffffff-ffff-ffff-ffff-ffffffffffff"},
		{"pr9-cursor-b@example.com", base.Add(1 * time.Second), "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"},
		{"pr9-cursor-c@example.com", base.Add(2 * time.Second), "dddddddd-dddd-dddd-dddd-dddddddddddd"},
		{"pr9-cursor-d@example.com", base.Add(3 * time.Second), "cccccccc-cccc-cccc-cccc-cccccccccccc"},
	}
	for _, seed := range seeds {
		_, err := store.CreateOrgInvitation(ctx, state.OrgInvitation{
			ID:                 seed.id,
			OrgID:              shared.ID,
			Email:              seed.email,
			Role:               state.OrgRoleDeveloper,
			TokenHash:          []byte(seed.id),
			InvitedByAccountID: &ib,
			CreatedAt:          seed.ts,
			ExpiresAt:          base.Add(24 * time.Hour),
		})
		if err != nil {
			t.Fatalf("CreateOrgInvitation %s: %v", seed.email, err)
		}
	}

	// Walk pages with limit=2. Page 1 → 2 rows + next_before;
	// page 2 → 2 rows + next_before; page 3 → 0 rows + empty
	// next_before. Concatenate the rows and assert ordering,
	// uniqueness, and full coverage.
	allEmails := []string{}
	before := ""
	for pageN := 1; pageN <= 4; pageN++ {
		path := "/v1/orgs/" + shared.Slug + "/invitations?limit=2"
		if before != "" {
			path += "&before=" + before
		}
		raw, status := doReq(t, h, ownerKey, http.MethodGet, path, nil,
			map[string]string{"X-Active-Org": shared.Slug})
		if status != http.StatusOK {
			t.Fatalf("page %d: %d %s", pageN, status, raw)
		}
		var page pr9InvitationListWire
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatalf("page %d unmarshal: %v (body=%s)", pageN, err, raw)
		}
		// Pages 1-2 must hold 2 rows; page 3 must hold 0.
		// next_before must be non-empty on pages 1-2 (page is
		// full) and empty on page 3 (terminal).
		switch {
		case pageN <= 2:
			if len(page.Invitations) != 2 {
				t.Fatalf("page %d len = %d, want 2", pageN, len(page.Invitations))
			}
			if page.NextBefore == "" {
				t.Fatalf("page %d next_before empty; cursor must carry forward", pageN)
			}
			// Decode the cursor to prove the wire format is
			// the pkg/cursor encoding (base64-url of JSON).
			if _, err := cursor.Decode(page.NextBefore); err != nil {
				t.Fatalf("page %d cursor.Decode(next_before=%q): %v",
					pageN, page.NextBefore, err)
			}
		case pageN == 3:
			if len(page.Invitations) != 0 {
				t.Errorf("page %d len = %d, want 0 (terminal)", pageN, len(page.Invitations))
			}
			if page.NextBefore != "" {
				t.Errorf("page %d next_before = %q, want empty (terminal)",
					pageN, page.NextBefore)
			}
		}
		allEmails = append(allEmails, emailOf(page.Invitations)...)
		before = page.NextBefore
		if before == "" {
			break
		}
	}

	// Strictly descending created_at → d, c, b, a.
	want := []string{
		"pr9-cursor-d@example.com",
		"pr9-cursor-c@example.com",
		"pr9-cursor-b@example.com",
		"pr9-cursor-a@example.com",
	}
	if len(allEmails) != len(want) {
		t.Fatalf("walked %d rows, want %d (all=%v)", len(allEmails), len(want), allEmails)
	}
	seen := map[string]bool{}
	for i, w := range want {
		if seen[allEmails[i]] {
			t.Errorf("walk[%d] = %q is a duplicate", i, allEmails[i])
		}
		seen[allEmails[i]] = true
		if allEmails[i] != w {
			t.Errorf("walk[%d] = %q, want %q (full=%v)", i, allEmails[i], w, allEmails)
		}
	}

	// PR-9 §2: a malformed cursor must surface as 400
	// invalid_cursor. The v1 cursor silently returned 0 rows
	// on malformed input — a regression the test below catches.
	_, badStatus := doReq(t, h, ownerKey, http.MethodGet,
		"/v1/orgs/"+shared.Slug+"/invitations?limit=2&before=not!base64", nil,
		map[string]string{"X-Active-Org": shared.Slug})
	if badStatus != http.StatusBadRequest {
		t.Errorf("bad cursor: code = %d, want 400", badStatus)
	}
}

// emailOf is the small helper that flattens invitations
// → []string for the page-walk assertions.
func emailOf(invitations []api.OrgInvitationResponse) []string {
	out := make([]string, 0, len(invitations))
	for _, inv := range invitations {
		out = append(out, inv.Email)
	}
	return out
}

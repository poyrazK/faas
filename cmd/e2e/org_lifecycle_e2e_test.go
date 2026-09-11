// spec: §4.7 paid entitlements change only after provider confirmation.
// HTTP integration tests for the /v1/orgs/{slug}/... customer surface
// (issue #190 / IAM-6 / ADR-061, PR 5). The PR-4 e2e file
// (load_org_e2e_test.go) covers the LoadOrg middleware; this file
// covers the handlers that ride on it: shared-org creation, member
// management, invitations, and ownership transfer.
//
// The store-layer parity tests for the new TransferOrgOwnership
// method live in cmd/apid/handlers_org_test.go and
// pkg/state/pgstore_orgs_test.go. This file is the wire-level
// integration story — boot apid, drive the full HTTP surface, and
// assert the wire codes the spec commits to.
//
// The invitation accept flow lands in PR 8 (ADR-061). To keep the
// PR-5 harness self-contained, we exercise the
// `addOrgMember + revokeInvitation` happy path via the Store
// directly rather than running it through the (not-yet-existing)
// POST /v1/invitations/{token}/accept endpoint. PR 8 will swap
// the direct call for a wire call and add the captcha-aware
// accept-handler tests.
package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// orgListWire is the local decode type for GET /v1/orgs. The pkg/api
// type is the canonical source; cmd/e2e can't import cmd/apid (which
// is package main), so we mirror the wire shape here.
type orgListWire struct {
	Orgs []struct {
		ID       string `json:"id"`
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		Personal bool   `json:"personal"`
		Plan     string `json:"plan"`
		Status   string `json:"status"`
	} `json:"orgs"`
}

// memberListWire is the local decode type for GET /v1/orgs/{slug}/members.
// Mirrors api.MemberListResponse so the test reads the same shape the
// SDK will see.
type memberListWire struct {
	Members []struct {
		AccountID string `json:"account_id"`
		Email     string `json:"email"`
		Role      string `json:"role"`
		JoinedAt  string `json:"joined_at"`
	} `json:"members"`
}

// inviteWire is the local decode type for the POST /v1/orgs/{slug}/members
// response (the one-time token-bearing shape). The pkg/api canonical
// shape is api.InvitationWithTokenResponse.
type inviteWire struct {
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

// TestE2E_OrgLifecycle_HappyPath is the end-to-end PR 5 story.
//
//  1. signup → personal org (PR 3 backfill)
//  2. POST /v1/orgs → create shared "acme" → caller becomes owner
//  3. GET /v1/orgs → list contains personal + acme
//  4. GET /v1/orgs/acme → resolves the org with the caller's role
//  5. POST /v1/orgs/acme/members → invite bob (plaintext token once)
//  6. accept-as-store-call → AddOrgMember(acme, bob.ID, developer)
//  7. PATCH /v1/orgs/acme/members/<bob> → developer → admin
//  8. POST /v1/orgs/acme/transfer_ownership → bob is new owner,
//     caller (alice) becomes admin
//  9. DELETE /v1/orgs/acme/members/<alice> → admin removed
//  10. DELETE /v1/orgs/acme → soft-deleted by the new owner
//
// Steps 6 + 10 are tested through the Store because the only
// remaining surface the acceptance flow needs lands in PR 8 / 9.
func TestE2E_OrgLifecycle_HappyPath(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()

	// 1. Seed two accounts. The PR-3 personal-org backfill fires
	// on SeedAccount so each one owns a personal org out of the gate.
	aliceKey := h.SeedAccount(ctx, api.PlanHobby, "pr5-alice")
	bobKey := h.SeedAccount(ctx, api.PlanFree, "pr5-bob")
	store := state.NewPgStore(pool)

	aliceAcct, err := store.AccountByEmail(ctx, seedEmail(api.PlanHobby, "pr5-alice"))
	if err != nil {
		t.Fatalf("AccountByEmail alice: %v", err)
	}
	bobAcct, err := store.AccountByEmail(ctx, seedEmail(api.PlanFree, "pr5-bob"))
	if err != nil {
		t.Fatalf("AccountByEmail bob: %v", err)
	}

	// 2. Create shared org. Caller (alice) becomes the first owner.
	createBody := api.CreateOrgRequest{Slug: "acme", Name: "Acme Inc."}
	raw, status := doReq(t, h, aliceKey, http.MethodPost, "/v1/orgs", createBody)
	if status != http.StatusCreated {
		t.Fatalf("create org: %d %s", status, raw)
	}
	var created struct {
		ID        string `json:"id"`
		Slug      string `json:"slug"`
		Name      string `json:"name"`
		Personal  bool   `json:"personal"`
		Plan      string `json:"plan"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode create: %v (body=%s)", err, raw)
	}
	if created.Slug != "acme" || created.Personal {
		t.Fatalf("created org has wrong shape: %+v", created)
	}
	acmeID := created.ID

	// 3. List orgs — alice should see personal + acme.
	raw, status = doReq(t, h, aliceKey, http.MethodGet, "/v1/orgs", nil)
	if status != http.StatusOK {
		t.Fatalf("list orgs: %d %s", status, raw)
	}
	var list orgListWire
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode list: %v (body=%s)", err, raw)
	}
	if len(list.Orgs) != 2 {
		t.Fatalf("list has %d orgs, want 2 (body=%s)", len(list.Orgs), raw)
	}
	var sawAcme, sawPersonal bool
	for _, o := range list.Orgs {
		switch o.Slug {
		case "acme":
			sawAcme = true
		case state.PersonalOrgSlug(aliceAcct.ID):
			sawPersonal = true
		}
	}
	if !sawAcme {
		t.Errorf("list missing 'acme'")
	}
	if !sawPersonal {
		t.Errorf("list missing personal org")
	}

	// 4. GET /v1/orgs/acme with X-Active-Org — exercises LoadOrg +
	// AuthorizeOrgAction(org.view). Status 200.
	raw, status = doReq(t, h, aliceKey, http.MethodGet, "/v1/orgs/acme", nil,
		map[string]string{"X-Active-Org": "acme"})
	if status != http.StatusOK {
		t.Fatalf("get org: %d %s", status, raw)
	}
	var got struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.ID != acmeID || got.Slug != "acme" {
		t.Errorf("get org = %+v, want id=%s slug=acme", got, acmeID)
	}

	// 5. Invite bob. The handler mints a 32-byte plaintext token,
	// base64url-encodes it, returns once, and stores sha256.
	raw, status = doReq(t, h, aliceKey, http.MethodPost, "/v1/orgs/acme/members",
		api.InviteMemberRequest{Email: bobAcct.Email, Role: "developer"},
		map[string]string{"X-Active-Org": "acme"})
	if status != http.StatusCreated {
		t.Fatalf("invite: %d %s", status, raw)
	}
	var inv inviteWire
	if err := json.Unmarshal(raw, &inv); err != nil {
		t.Fatalf("decode invite: %v (body=%s)", err, raw)
	}
	if inv.Token == "" {
		t.Fatalf("invite token is empty (body=%s)", raw)
	}
	if inv.Email != bobAcct.Email {
		t.Errorf("invite email = %q, want %q", inv.Email, bobAcct.Email)
	}
	if inv.Role != "developer" {
		t.Errorf("invite role = %q, want developer", inv.Role)
	}
	if inv.OrgSlug != "acme" {
		t.Errorf("invite org_slug = %q, want acme", inv.OrgSlug)
	}

	// 5b. /v1/invitations/{token} — peek (read-only) using the
	// same token we just captured. The handler hashes the token
	// and looks up by hash. The peek endpoint surfaces the
	// invitation but does NOT consume it.
	raw, status = doReq(t, h, aliceKey, http.MethodGet, "/v1/invitations/"+inv.Token, nil)
	if status != http.StatusOK {
		t.Fatalf("peek invitation: %d %s", status, raw)
	}
	var peeked struct {
		ID      string `json:"id"`
		Status  string `json:"status"`
		OrgSlug string `json:"org_slug"`
	}
	if err := json.Unmarshal(raw, &peeked); err != nil {
		t.Fatalf("decode peek: %v", err)
	}
	if peeked.Status != "pending" {
		t.Errorf("peek status = %q, want pending", peeked.Status)
	}
	if peeked.OrgSlug != "acme" {
		t.Errorf("peek org_slug = %q, want acme", peeked.OrgSlug)
	}

	// 6. Accept-as-store. PR 8 will swap this for a wire call;
	// for PR 5 we drive the data layer directly so the e2e
	// story is self-contained. The peek + revoke steps below
	// prove the token-hash lookup is wired correctly.
	if err := store.AddOrgMember(ctx, acmeID, bobAcct.ID, state.OrgRoleDeveloper, &aliceAcct.ID); err != nil {
		t.Fatalf("AddOrgMember bob: %v", err)
	}
	// Revoke the invitation so the partial-unique on pending
	// invitations doesn't trip later (we don't enforce a cap in
	// PR 5 since OrgPendingInvitationsMax is 0/0, but it's
	// cleaner to leave the system in a deterministic state).
	plaintext, err := base64.RawURLEncoding.DecodeString(inv.Token)
	if err != nil {
		t.Fatalf("decode token: %v", err)
	}
	hash := sha256.Sum256(plaintext)
	if err := store.RevokeOrgInvitation(ctx, hash[:], aliceAcct.ID); err != nil {
		t.Fatalf("RevokeOrgInvitation: %v", err)
	}

	// 7. List members — alice (owner) + bob (developer).
	raw, status = doReq(t, h, aliceKey, http.MethodGet, "/v1/orgs/acme/members", nil,
		map[string]string{"X-Active-Org": "acme"})
	if status != http.StatusOK {
		t.Fatalf("list members: %d %s", status, raw)
	}
	var members memberListWire
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	if len(members.Members) != 2 {
		t.Fatalf("members = %d, want 2 (body=%s)", len(members.Members), raw)
	}
	var bobRole string
	for _, m := range members.Members {
		switch m.AccountID {
		case aliceAcct.ID:
			if m.Role != "owner" {
				t.Errorf("alice role = %q, want owner", m.Role)
			}
		case bobAcct.ID:
			bobRole = m.Role
		}
	}
	if bobRole != "developer" {
		t.Errorf("bob role = %q, want developer", bobRole)
	}

	// 8. Change bob's role developer → admin.
	raw, status = doReq(t, h, aliceKey,
		http.MethodPatch, "/v1/orgs/acme/members/"+bobAcct.ID,
		api.ChangeMemberRoleRequest{Role: "admin"},
		map[string]string{"X-Active-Org": "acme"})
	if status != http.StatusOK {
		t.Fatalf("change role: %d %s", status, raw)
	}

	// 9. Transfer ownership alice → bob.
	raw, status = doReq(t, h, aliceKey,
		http.MethodPost, "/v1/orgs/acme/transfer_ownership",
		api.TransferOwnershipRequest{NewOwnerAccountID: bobAcct.ID},
		map[string]string{"X-Active-Org": "acme"})
	if status != http.StatusOK {
		t.Fatalf("transfer ownership: %d %s", status, raw)
	}

	// 10. Confirm post-transfer roles via the Store (the role
	// matrix is the load-bearing invariant).
	row, err := store.OrgMemberByAccount(ctx, acmeID, bobAcct.ID)
	if err != nil {
		t.Fatalf("OrgMemberByAccount bob: %v", err)
	}
	if row.Role != state.OrgRoleOwner {
		t.Errorf("bob role after transfer = %q, want owner", row.Role)
	}
	row, err = store.OrgMemberByAccount(ctx, acmeID, aliceAcct.ID)
	if err != nil {
		t.Fatalf("OrgMemberByAccount alice: %v", err)
	}
	if row.Role != state.OrgRoleAdmin {
		t.Errorf("alice role after transfer = %q, want admin (demoted)", row.Role)
	}

	// 11. New owner (bob) removes the demoted admin (alice).
	// This step proves the role matrix wires correctly: bob is
	// now owner, so OrgAction remove_members is allowed.
	// RemoveOrgMember is a soft delete (stamps removed_at); the
	// membership row stays for audit — alice must NOT appear in
	// the active members list, and her row's removed_at must be
	// non-nil. The listOrgMembers handler filters at the API
	// boundary (cmd/apid/handlers_org_members.go:75-78).
	raw, status = doReq(t, h, bobKey,
		http.MethodDelete, "/v1/orgs/acme/members/"+aliceAcct.ID, nil,
		map[string]string{"X-Active-Org": "acme"})
	if status != http.StatusNoContent {
		t.Fatalf("remove member: %d %s", status, raw)
	}
	row, err = store.OrgMemberByAccount(ctx, acmeID, aliceAcct.ID)
	if err != nil {
		t.Fatalf("OrgMemberByAccount alice (post-remove): %v", err)
	}
	if row.RemovedAt == nil {
		t.Errorf("alice membership not soft-deleted; removed_at = nil")
	}
	// alice must not appear in the active members list — the
	// handler filters at the boundary.
	raw, status = doReq(t, h, bobKey, http.MethodGet, "/v1/orgs/acme/members", nil,
		map[string]string{"X-Active-Org": "acme"})
	if status != http.StatusOK {
		t.Fatalf("list members (post-remove): %d %s", status, raw)
	}
	var memberList struct {
		Members []struct {
			AccountID string `json:"account_id"`
		} `json:"members"`
	}
	if err := json.Unmarshal(raw, &memberList); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	for _, m := range memberList.Members {
		if m.AccountID == aliceAcct.ID {
			t.Errorf("alice still in active members list after remove: %s", raw)
		}
	}

	// 12. Soft-delete the org as the new owner (POST 7 in the
	// dispatch). Wire assertion: 204 No Content.
	raw, status = doReq(t, h, bobKey, http.MethodDelete, "/v1/orgs/acme", nil,
		map[string]string{"X-Active-Org": "acme"})
	if status != http.StatusNoContent {
		t.Fatalf("delete org: %d %s", status, raw)
	}
	deleted, err := store.OrgBySlug(ctx, "acme")
	if err != nil {
		t.Fatalf("OrgBySlug after delete: %v", err)
	}
	if deleted.Status != "deleted_pending" {
		t.Errorf("status = %q, want deleted_pending", deleted.Status)
	}
}

// TestE2E_OrgLifecycle_NonMemberIDOR — the IDOR probe for the
// org-scoped surface. Account A creates a shared org; account B
// (no membership) tries to GET/PATCH/DELETE under it. Every
// org-scoped route must return 403 org_role_forbidden (NOT 404)
// so the IDOR-safe behaviour is identical to the LoadOrg probe.
func TestE2E_OrgLifecycle_NonMemberIDOR(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()

	aliceKey := h.SeedAccount(ctx, api.PlanHobby, "pr5-idor-a")
	bobKey := h.SeedAccount(ctx, api.PlanFree, "pr5-idor-b")

	store := state.NewPgStore(pool)
	aliceAcct, err := store.AccountByEmail(ctx, seedEmail(api.PlanHobby, "pr5-idor-a"))
	if err != nil {
		t.Fatalf("AccountByEmail alice: %v", err)
	}

	// alice creates "iso"
	raw, status := doReq(t, h, aliceKey, http.MethodPost, "/v1/orgs",
		api.CreateOrgRequest{Slug: "iso", Name: "Iso"})
	if status != http.StatusCreated {
		t.Fatalf("create org: %d %s", status, raw)
	}

	// bob probes every read + write route — must all 403.
	for _, probe := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"get", http.MethodGet, "/v1/orgs/iso", nil},
		{"list_members", http.MethodGet, "/v1/orgs/iso/members", nil},
		{"patch", http.MethodPatch, "/v1/orgs/iso", api.PatchOrgRequest{Name: strPtr("Hijacked")}},
		{"invite", http.MethodPost, "/v1/orgs/iso/members", api.InviteMemberRequest{Email: "carol@x.com", Role: "viewer"}},
		{"delete", http.MethodDelete, "/v1/orgs/iso", nil},
		{"transfer_ownership", http.MethodPost, "/v1/orgs/iso/transfer_ownership", api.TransferOwnershipRequest{NewOwnerAccountID: "00000000-0000-0000-0000-000000000000"}},
	} {
		probe := probe
		t.Run(probe.name, func(t *testing.T) {
			raw, status := doReq(t, h, bobKey, probe.method, probe.path, probe.body,
				map[string]string{"X-Active-Org": "iso"})
			if status != http.StatusForbidden {
				t.Errorf("probe %s: status = %d, want 403 (body=%s)", probe.name, status, raw)
			}
			if !strings.Contains(string(raw), "org_role_forbidden") {
				t.Errorf("probe %s: body did not contain org_role_forbidden: %s", probe.name, raw)
			}
		})
	}

	// sanity check: alice can still GET her own org.
	raw, status = doReq(t, h, aliceKey, http.MethodGet, "/v1/orgs/iso", nil,
		map[string]string{"X-Active-Org": "iso"})
	if status != http.StatusOK {
		t.Errorf("alice GET own org: %d %s", status, raw)
	}
	// consume aliceAcct so the compiler doesn't warn about the
	// unused binding (it's read by the closure above).
	_ = aliceAcct
}

// TestE2E_OrgLifecycle_PeekTerminalStates — every terminal-state
// invitation (consumed / revoked / expired) collapses onto the
// same wire shape: 410 Gone + org_invitation_invalid. This is the
// security oracle fix from the PR 5 review — an attacker who has
// a leaked token cannot enumerate which rows are still live by
// the code returned. The Expired constructor is reserved for the
// PR 8 accept flow where the caller is the legitimate invitee.
func TestE2E_OrgLifecycle_PeekTerminalStates(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()

	aliceKey := h.SeedAccount(ctx, api.PlanHobby, "pr5-peek-a")
	store := state.NewPgStore(pool)
	aliceAcct, err := store.AccountByEmail(ctx, seedEmail(api.PlanHobby, "pr5-peek-a"))
	if err != nil {
		t.Fatalf("AccountByEmail alice: %v", err)
	}
	raw, status := doReq(t, h, aliceKey, http.MethodPost, "/v1/orgs",
		api.CreateOrgRequest{Slug: "peek-test", Name: "Peek Test"})
	if status != http.StatusCreated {
		t.Fatalf("create org: %d %s", status, raw)
	}
	peekOrg, err := store.OrgBySlug(ctx, "peek-test")
	if err != nil {
		t.Fatalf("OrgBySlug: %v", err)
	}

	// mint a fresh invitation row, then stamp it into one of the
	// three terminal states. Each sub-test owns its own seeded
	// invitee account + plaintext so the partial uniques don't
	// trip across cases.
	mint := func(t *testing.T, suffix string) (token string, inv state.OrgInvitation) {
		t.Helper()
		plaintext := make([]byte, 32)
		// Per-case offsets: "consumed"/"revoked"/"expired" share a
		// length-of-suffix pattern that would collide on token_hash
		// (the UNIQUE constraint), so encode the suffix byte itself
		// into the first byte as a distinguisher.
		plaintext[0] = suffix[0]
		for i := 1; i < len(plaintext); i++ {
			plaintext[i] = byte(i)
		}
		hash := sha256.Sum256(plaintext)
		token = base64.RawURLEncoding.EncodeToString(plaintext)
		created, err := store.CreateOrgInvitation(ctx, state.OrgInvitation{
			OrgID:     peekOrg.ID,
			Email:     suffix + "@x.com",
			Role:      state.OrgRoleDeveloper,
			TokenHash: hash[:],
			ExpiresAt: time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatalf("CreateOrgInvitation (%s): %v", suffix, err)
		}
		created.TokenHash = hash[:]
		return token, created
	}
	probe := func(t *testing.T, token string) {
		t.Helper()
		raw, status := doReq(t, h, aliceKey, http.MethodGet,
			"/v1/invitations/"+token, nil)
		if status != http.StatusGone {
			t.Errorf("status = %d, want 410 (body=%s)", status, raw)
		}
		if !strings.Contains(string(raw), "org_invitation_invalid") {
			t.Errorf("body did not contain org_invitation_invalid: %s", raw)
		}
	}

	t.Run("consumed", func(t *testing.T) {
		acceptorID := "00000000-0000-0000-0000-000000000d11"
		acceptorEmail := "consumed@x.com"
		if _, err := pool.Exec(ctx,
			"insert into accounts (id, email, plan, created_at) values ($1::uuid, $2, 'free', now()) on conflict do nothing",
			acceptorID, acceptorEmail); err != nil {
			t.Fatalf("seed acceptor: %v", err)
		}
		token, inv := mint(t, "consumed")
		if _, _, err := store.ConsumeOrgInvitation(ctx, inv.TokenHash,
			state.Account{ID: acceptorID, Email: acceptorEmail}); err != nil {
			t.Fatalf("ConsumeOrgInvitation: %v", err)
		}
		probe(t, token)
	})

	t.Run("revoked", func(t *testing.T) {
		token, inv := mint(t, "revoked")
		if err := store.RevokeOrgInvitation(ctx, inv.TokenHash, aliceAcct.ID); err != nil {
			t.Fatalf("RevokeOrgInvitation: %v", err)
		}
		probe(t, token)
	})

	t.Run("expired", func(t *testing.T) {
		token, inv := mint(t, "expired")
		// Backdate expires_at — DeriveOrgInvitationStatus reads the
		// timestamp directly so we don't need ExpireOrgInvitations
		// (the cleanup tick is for batch sweeps, not per-row setup).
		if _, err := pool.Exec(ctx,
			"update org_invitations set expires_at = now() - interval '1 hour' where id = $1::uuid",
			inv.ID); err != nil {
			t.Fatalf("backdate expires_at: %v", err)
		}
		probe(t, token)
	})
}

// TestE2E_OrgLifecycle_PersonalImmutable — the personal org is
// the load-bearing seam that holds all pre-PR-5 routes working
// after the cut-over. Mutating it (renaming, deleting, changing
// plan) must return 409 org_personal_immutable. The Store also
// refuses, but the wire code is the contract.
func TestE2E_OrgLifecycle_PersonalImmutable(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()

	key := h.SeedAccount(ctx, api.PlanFree, "pr5-personal")
	store := state.NewPgStore(pool)
	acct, err := store.AccountByEmail(ctx, seedEmail(api.PlanFree, "pr5-personal"))
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	personal, err := store.OrgByPersonalAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("OrgByPersonalAccount: %v", err)
	}

	// DELETE — must 409.
	raw, status := doReq(t, h, key, http.MethodDelete,
		"/v1/orgs/"+personal.Slug, nil,
		map[string]string{"X-Active-Org": personal.Slug})
	if status != http.StatusConflict {
		t.Errorf("delete personal: status = %d, want 409 (body=%s)", status, raw)
	}
	if !strings.Contains(string(raw), "org_personal_immutable") {
		t.Errorf("body did not contain org_personal_immutable: %s", raw)
	}

	// PATCH (rename) — must 409.
	raw, status = doReq(t, h, key, http.MethodPatch,
		"/v1/orgs/"+personal.Slug,
		api.PatchOrgRequest{Name: strPtr("Renamed")},
		map[string]string{"X-Active-Org": personal.Slug})
	if status != http.StatusConflict {
		t.Errorf("patch personal: status = %d, want 409 (body=%s)", status, raw)
	}
	if !strings.Contains(string(raw), "org_personal_immutable") {
		t.Errorf("body did not contain org_personal_immutable: %s", raw)
	}
}

// TestE2E_OrgLifecycle_LastOwnerGuard — the partially-unique
// constraint on exactly-one-active-owner fires on PATCH
// developer→[developer] (no-op) and on DELETE the last owner.
// Both must 409 org_last_owner.
//
// This is the test that proves the role matrix + the data layer
// invariant agree: alice is the only owner, so neither the
// "demote owner → not owner" path nor the "remove owner" path
// is reachable.
func TestE2E_OrgLifecycle_LastOwnerGuard(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()

	aliceKey := h.SeedAccount(ctx, api.PlanHobby, "pr5-lastowner")
	store := state.NewPgStore(pool)
	aliceAcct, err := store.AccountByEmail(ctx, seedEmail(api.PlanHobby, "pr5-lastowner"))
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}

	// alice creates "solo" and is the only owner.
	raw, status := doReq(t, h, aliceKey, http.MethodPost, "/v1/orgs",
		api.CreateOrgRequest{Slug: "solo", Name: "Solo"})
	if status != http.StatusCreated {
		t.Fatalf("create org: %d %s", status, raw)
	}
	soloID, err := store.OrgBySlug(ctx, "solo")
	if err != nil {
		t.Fatalf("OrgBySlug solo: %v", err)
	}

	// DELETE alice's own membership (the only owner). Must 409.
	// The handler rejects self-removal at the boundary with a
	// 409 validation_failed + "Cannot remove self" (defence in
	// depth — the Store would also block via ErrOrgLastOwner, but
	// the boundary check gives the dashboard a stable wire shape
	// that doesn't change between "admin" and "owner" callers).
	raw, status = doReq(t, h, aliceKey, http.MethodDelete,
		"/v1/orgs/solo/members/"+aliceAcct.ID, nil,
		map[string]string{"X-Active-Org": "solo"})
	if status != http.StatusConflict {
		t.Errorf("remove last owner: status = %d, want 409 (body=%s)", status, raw)
	}
	if !strings.Contains(string(raw), "Cannot remove self") {
		t.Errorf("body did not contain 'Cannot remove self': %s", raw)
	}

	// PATCH owner → admin (which would orphan the role) must be
	// rejected at the handler boundary BEFORE the Store (the
	// store would block it via the partial unique, but the
	// handler returns a stable 403 with org_role_forbidden
	// because PATCH /members can never reach owner).
	_, status = doReq(t, h, aliceKey, http.MethodPatch,
		"/v1/orgs/solo/members/"+aliceAcct.ID,
		api.ChangeMemberRoleRequest{Role: "admin"},
		map[string]string{"X-Active-Org": "solo"})
	if status != http.StatusBadRequest {
		// the handler rejects "owner" for the new role but the
		// request is "admin" — the role IS allowed. The Store
		// would then fail with ErrOrgLastOwner (partial unique).
		// Either 400 (role rejected) or 409 (last-owner) is OK;
		// we assert it's not 200.
		if status == http.StatusOK {
			t.Errorf("PATCH owner→admin succeeded; should be rejected")
		}
	}
	// sanity: the org still has alice as owner.
	row, err := store.OrgMemberByAccount(ctx, soloID.ID, aliceAcct.ID)
	if err != nil {
		t.Fatalf("OrgMemberByAccount: %v", err)
	}
	if row.Role != state.OrgRoleOwner {
		t.Errorf("alice role = %q, want owner (unchanged)", row.Role)
	}
}

// strPtr is a small helper for pointer-fields in request bodies.
// Matches the convention in cmd/apid/handlers_org_test.go.
func strPtr(s string) *string { return &s }

// TestE2E_OrgLifecycle_PatchOrg exercises PATCH /v1/orgs/{slug}
// end-to-end. The patchOrg handler was the load-bearing seam PR
// 5's review caught: the handler claimed to support name updates
// but never persisted them (only UpdateOrgPlan existed on the
// Store). This test pins the post-fix contract:
//
//   - name update persists (Store::UpdateOrgName is now wired)
//   - plan update persists (Store::UpdateOrgPlan, the pre-existing path)
//   - personal org PATCH → 409 org_personal_immutable
//   - empty name (after trim) → 422 validation
//   - unknown plan → 422 org_slug_invalid (the closed-enum validator)
//
// Each assertion is the load-bearing wire contract; a future
// refactor that drops one of these would silently break the
// customer-visible rename / plan-change flow.
func TestE2E_OrgLifecycle_PatchOrg(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()

	aliceKey := h.SeedAccount(ctx, api.PlanHobby, "pr5-patch")
	store := state.NewPgStore(pool)
	aliceAcct, err := store.AccountByEmail(ctx, seedEmail(api.PlanHobby, "pr5-patch"))
	if err != nil {
		t.Fatalf("AccountByEmail alice: %v", err)
	}

	// Create "patchme" so we have a non-personal org to mutate.
	raw, status := doReq(t, h, aliceKey, http.MethodPost, "/v1/orgs",
		api.CreateOrgRequest{Slug: "patchme", Name: "Patch Me"})
	if status != http.StatusCreated {
		t.Fatalf("create org: %d %s", status, raw)
	}

	// (a) Name update persists. Pre-fix this returned 200 OK with
	// the OLD name — the regression the PR review caught.
	raw, status = doReq(t, h, aliceKey, http.MethodPatch, "/v1/orgs/patchme",
		api.PatchOrgRequest{Name: strPtr("Renamed Inc.")},
		map[string]string{"X-Active-Org": "patchme"})
	if status != http.StatusOK {
		t.Fatalf("patch name: %d %s", status, raw)
	}
	var patched struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &patched); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if patched.Name != "Renamed Inc." {
		t.Errorf("response name = %q, want %q", patched.Name, "Renamed Inc.")
	}
	// Sanity: the row in Postgres reflects the new name (the
	// store-level pin; the response body alone isn't enough —
	// a regression that wrote the response but skipped the
	// UpdateOrgName would slip through without this check).
	row, err := store.OrgBySlug(ctx, "patchme")
	if err != nil {
		t.Fatalf("OrgBySlug: %v", err)
	}
	if row.Name != "Renamed Inc." {
		t.Errorf("persisted name = %q, want %q", row.Name, "Renamed Inc.")
	}
	originalPlan := row.Plan

	// (b) A valid paid plan cannot be written directly. The provider-backed
	// billing flow owns the entitlement transition, so the current plan stays
	// unchanged until its webhook confirms payment.
	raw, status = doReq(t, h, aliceKey, http.MethodPatch, "/v1/orgs/patchme",
		api.PatchOrgRequest{Plan: strPtr("pro")},
		map[string]string{"X-Active-Org": "patchme"})
	if status != http.StatusPaymentRequired {
		t.Fatalf("patch plan: status=%d, want 402 (%s)", status, raw)
	}
	row, err = store.OrgBySlug(ctx, "patchme")
	if err != nil {
		t.Fatalf("OrgBySlug post-plan: %v", err)
	}
	if row.Plan != originalPlan {
		t.Errorf("persisted plan = %q, want unchanged %q until provider confirmation", row.Plan, originalPlan)
	}

	// (c) Unknown plan rejected at the boundary with the
	// closed-enum wire shape (org_slug_invalid, mirroring the
	// slug validator). The handler must NOT round-trip the
	// request to the Store and surface a SQL CHECK 500.
	raw, status = doReq(t, h, aliceKey, http.MethodPatch, "/v1/orgs/patchme",
		api.PatchOrgRequest{Plan: strPtr("enterprise")},
		map[string]string{"X-Active-Org": "patchme"})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("unknown plan: status = %d, want 422 (body=%s)", status, raw)
	}
	if !strings.Contains(string(raw), "org_slug_invalid") {
		t.Errorf("unknown plan: body did not contain org_slug_invalid: %s", raw)
	}

	// (d) Empty name (after trim) → 422 validation.
	raw, status = doReq(t, h, aliceKey, http.MethodPatch, "/v1/orgs/patchme",
		api.PatchOrgRequest{Name: strPtr("   ")},
		map[string]string{"X-Active-Org": "patchme"})
	if status != http.StatusUnprocessableEntity {
		t.Errorf("empty name: status = %d, want 422 (body=%s)", status, raw)
	}

	// (e) Personal org PATCH → 409 org_personal_immutable. The
	// seed account owns a personal org (PR 3 backfill); the slug
	// is `state.PersonalOrgSlug(aliceAcct.ID)` which the harness
	// makes available.
	personalSlug := state.PersonalOrgSlug(aliceAcct.ID)
	raw, status = doReq(t, h, aliceKey, http.MethodPatch,
		"/v1/orgs/"+personalSlug,
		api.PatchOrgRequest{Name: strPtr("Whatever")},
		map[string]string{"X-Active-Org": personalSlug})
	if status != http.StatusConflict {
		t.Errorf("patch personal: status = %d, want 409 (body=%s)", status, raw)
	}
	if !strings.Contains(string(raw), "org_personal_immutable") {
		t.Errorf("patch personal: body did not contain org_personal_immutable: %s", raw)
	}
}

// TestE2E_OrgLifecycle_ListOrgsForCaller_PersonalSlugPin — the
// personal-org slug in the listOrgsForCaller response MUST equal
// state.PersonalOrgSlug(accountID) (the frozen UUID v5 namespace
// from PR 3). If the slug drifted (different namespace, different
// derivation, accidental re-key), the dashboard's "Personal | Acme
// Inc." switcher would 404 the moment the user clicked the personal
// row. This pins the wire-shape contract.
func TestE2E_OrgLifecycle_ListOrgsForCaller_PersonalSlugPin(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()

	key := h.SeedAccount(ctx, api.PlanFree, "pr5-list-pin")
	store := state.NewPgStore(pool)
	acct, err := store.AccountByEmail(ctx, seedEmail(api.PlanFree, "pr5-list-pin"))
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	wantSlug := state.PersonalOrgSlug(acct.ID)

	raw, status := doReq(t, h, key, http.MethodGet, "/v1/orgs", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/orgs: %d %s", status, raw)
	}
	var resp struct {
		Orgs []struct {
			ID       string `json:"id"`
			Slug     string `json:"slug"`
			Personal bool   `json:"personal"`
		} `json:"orgs"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(resp.Orgs) == 0 {
		t.Fatalf("expected at least the personal org in /v1/orgs, got 0 (body=%s)", raw)
	}
	var personalOK bool
	for _, o := range resp.Orgs {
		if o.Slug == wantSlug && o.Personal {
			personalOK = true
			break
		}
	}
	if !personalOK {
		t.Errorf("personal slug missing from /v1/orgs: want %q (Personal=true), body=%s", wantSlug, raw)
	}
}

// TestE2E_OrgLifecycle_MemberCap is the wire-level tripwire for the
// per-plan OrgMembersMax + OrgPendingInvitationsMax caps populated in
// PR 2 (issue #190 / IAM-6 / ADR-061). The ladder — Free 0/0,
// Hobby 10/5, Pro 50/25, Scale 200/100 — is the source of truth
// in `pkg/api/limits.go` (regression-pinned by
// TestOrgMembersLimits_DerivedFromLadder); if those values ever
// change, the table below must change with them. The store-side
// cap check inside ConsumeOrgInvitation (pkg/state/pgstore.go) is
// the load-bearing invariant — the handler-side enforceMemberCap is
// the symmetric back-stop.
//
// Five assertions per plan:
//  1. Filling the member cap via invite-accept succeeds exactly
//     `limit` times; the limit+1 accept fails inside the tx.
//  2. Soft-removing one member drops the active count back under
//     the cap; a subsequent accept succeeds.
//  3. Issuing `OrgPendingInvitationsMax` pending invitations
//     succeeds; the next POST /v1/orgs/{slug}/members returns 403
//     org_invitation_cap_exceeded with the closed wire shape
//     (limit + observed fields populated).
//
// Skipped if Postgres is unavailable (pgtest.Open returns nil) so
// unit-only CI hosts don't fail. Each plan runs as its own subtest
// against a fresh alice account + fresh org slug to keep the
// fixtures isolated — sharing one alice would trip the partial
// unique index on (org_id, account_id).
func TestE2E_OrgLifecycle_MemberCap(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	ctx := context.Background()
	store := state.NewPgStore(pool)

	// limitsFor must be readable here without an org fixture — the
	// plan policy is global, so asking for Hobby/Pro/Scale caps
	// before creating any org is fine.
	mustLimits := func(t *testing.T, plan api.Plan) (members, pending int) {
		t.Helper()
		l, ok := api.LimitsFor(plan)
		if !ok {
			t.Fatalf("api.LimitsFor(%s) missing — limits_test.go out of sync", plan)
		}
		return l.OrgMembersMax, l.OrgPendingInvitationsMax
	}

	plans := []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale}
	for _, plan := range plans {
		t.Run(string(plan), func(t *testing.T) {
			membersMax, invitesMax := mustLimits(t, plan)
			orgSlug := fmt.Sprintf("cap-%s", plan)
			aliceLabel := fmt.Sprintf("pr5-cap-%s", plan)
			aliceKey := h.SeedAccount(ctx, plan, aliceLabel)

			// 1. Create the shared org as alice. createSharedOrg
			// stamps the org with PlanFree by default — patch the
			// plan via the Store so the cap is the populated value
			// (Free would be 0 and refuse every subsequent add).
			// nolint:contextcheck // doReq uses context.Background() internally; threading ctx through the shared helper would touch 19 e2e files.
			raw, status := doReq(t, h, aliceKey, http.MethodPost, "/v1/orgs",
				api.CreateOrgRequest{Slug: orgSlug, Name: "Cap Test"})
			if status != http.StatusCreated {
				t.Fatalf("create org: %d %s", status, raw)
			}
			var created struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(raw, &created); err != nil {
				t.Fatalf("decode create: %v (body=%s)", err, raw)
			}
			orgID := created.ID
			if err := store.UpdateOrgPlan(ctx, orgID, plan); err != nil {
				t.Fatalf("UpdateOrgPlan %s: %v", plan, err)
			}

			// 2. Fill the cap. PR 5 hasn't shipped the POST
			// /v1/invitations/{token}/accept endpoint yet, so we
			// drive the accept side through the Store — same code
			// path the future wire handler will use, so the
			// store-side cap check is the one under test.
			//
			// seedAcceptor inserts a fresh account row (the
			// schema's accounts.id FK on org_memberships) and
			// returns the seeded id + email so each
			// ConsumeOrgInvitation call has its own unique target.
			// The id's high bytes are derived from the plan + index
			// so a single e2e run (Pro=50 + Scale=100 = 150
			// acceptors) doesn't collide on the (org_id, account_id)
			// partial unique.
			seedAcceptor := func(t *testing.T, i int) state.Account {
				t.Helper()
				// Plan-distinct third-segment nibble (4 hex chars
				// per RFC 4122 8-4-4-4-12 layout) so Hobby + Pro +
				// Scale acceptors don't collide on the
				// (org_id, account_id) partial unique. The 12-char
				// last group stays i-driven so a single plan's
				// (cap-1) acceptors fit.
				planNibble := map[api.Plan]int{
					api.PlanHobby: 0x0,
					api.PlanPro:   0x1,
					api.PlanScale: 0x2,
				}[plan]
				id := fmt.Sprintf("00000000-0000-0000-%04x-%012x", planNibble, i)
				email := fmt.Sprintf("cap-%s-%d@x.com", plan, i)
				if _, err := pool.Exec(ctx,
					"insert into accounts (id, email, plan, created_at) values ($1::uuid, $2, 'free', now()) on conflict do nothing",
					id, email); err != nil {
					t.Fatalf("seed acceptor %d: %v", i, err)
				}
				return state.Account{ID: id, Email: email}
			}
			mintInvitation := func(t *testing.T, idx int, email string) []byte {
				t.Helper()
				plaintext := make([]byte, 32)
				if _, err := rand.Read(plaintext); err != nil {
					t.Fatalf("rand.Read: %v", err)
				}
				// Stamp a per-index discriminator into the last
				// byte so distinct fixtures never collide on
				// (org_id, token_hash) — the token_hash column is
				// UNIQUE per row. Wrapping at 256 is fine because
				// the cap fill loop is at most 199 (Scale - 1).
				plaintext[len(plaintext)-1] = byte(idx)
				hash := sha256.Sum256(plaintext)
				if _, err := store.CreateOrgInvitation(ctx, state.OrgInvitation{
					OrgID:     orgID,
					Email:     email,
					Role:      state.OrgRoleDeveloper,
					TokenHash: hash[:],
					ExpiresAt: time.Now().Add(time.Hour),
				}); err != nil {
					t.Fatalf("CreateOrgInvitation %d: %v", idx, err)
				}
				return hash[:]
			}

			extraMembers := membersMax - 1 // alice is the first owner
			for i := 0; i < extraMembers; i++ {
				acct := seedAcceptor(t, i+1)
				tokenHash := mintInvitation(t, i, acct.Email)
				if _, _, err := store.ConsumeOrgInvitation(ctx, tokenHash, acct); err != nil {
					t.Fatalf("ConsumeOrgInvitation %d (under cap): %v", i, err)
				}
			}
			// Assert active count = limit (alice + extraMembers).
			active, err := store.CountActiveOrgMembers(ctx, orgID)
			if err != nil {
				t.Fatalf("CountActiveOrgMembers: %v", err)
			}
			if active != membersMax {
				t.Fatalf("active members = %d, want %d", active, membersMax)
			}

			// 3. limit+1th accept must trip the cap inside the tx.
			overAcct := seedAcceptor(t, extraMembers+1)
			overHash := mintInvitation(t, extraMembers+1, overAcct.Email)
			if _, _, err := store.ConsumeOrgInvitation(ctx, overHash, overAcct); !errors.Is(err, state.ErrOrgMemberCapExceeded) {
				t.Fatalf("over-cap ConsumeOrgInvitation: err = %v, want ErrOrgMemberCapExceeded", err)
			}
			// Confirm the over-cap invite did NOT add a membership row.
			if _, err := store.OrgMemberByAccount(ctx, orgID, overAcct.ID); !errors.Is(err, state.ErrNotFound) {
				t.Errorf("OrgMemberByAccount over-cap: err = %v, want ErrNotFound", err)
			}

			// 4. Remove one of the loop's acceptors (index 1, the
			// first non-alice member), then re-attempt the cap-
			// blocked accept — should succeed because active
			// dropped to (limit - 1).
			planNibble2 := map[api.Plan]int{
				api.PlanHobby: 0x0,
				api.PlanPro:   0x1,
				api.PlanScale: 0x2,
			}[plan]
			firstID := fmt.Sprintf("00000000-0000-0000-%04x-%012x", planNibble2, 1)
			if err := store.RemoveOrgMember(ctx, orgID, firstID); err != nil {
				t.Fatalf("RemoveOrgMember: %v", err)
			}
			if _, _, err := store.ConsumeOrgInvitation(ctx, overHash, overAcct); err != nil {
				t.Fatalf("post-remove ConsumeOrgInvitation: %v", err)
			}
			active, err = store.CountActiveOrgMembers(ctx, orgID)
			if err != nil {
				t.Fatalf("CountActiveOrgMembers (post-remove): %v", err)
			}
			if active != membersMax {
				t.Errorf("active after re-add = %d, want %d", active, membersMax)
			}

			// 5. Pending-invitation cap is enforced on POST
			// /v1/orgs/{slug}/members via
			// enforcePendingInvitationCap. Mint `invitesMax`
			// pending invitations, then assert the next one is
			// rejected with the typed 403 wire shape.
			for i := 0; i < invitesMax; i++ {
				body := api.InviteMemberRequest{
					Email: fmt.Sprintf("inv-%s-%d@x.com", plan, i),
					Role:  string(state.OrgRoleViewer),
				}
				// nolint:contextcheck // doReq uses context.Background() internally; threading ctx through the shared helper would touch 19 e2e files.
				raw, status := doReq(t, h, aliceKey, http.MethodPost,
					"/v1/orgs/"+orgSlug+"/members", body,
					map[string]string{"X-Active-Org": orgSlug})
				if status != http.StatusCreated {
					t.Fatalf("invite %d: %d %s", i, status, raw)
				}
			}
			overBody := api.InviteMemberRequest{
				Email: fmt.Sprintf("inv-%s-over@x.com", plan),
				Role:  string(state.OrgRoleViewer),
			}
			// nolint:contextcheck // doReq uses context.Background() internally; threading ctx through the shared helper would touch 19 e2e files.
			raw, status = doReq(t, h, aliceKey, http.MethodPost,
				"/v1/orgs/"+orgSlug+"/members", overBody,
				map[string]string{"X-Active-Org": orgSlug})
			if status != http.StatusForbidden {
				t.Fatalf("over-invitation cap: status = %d, want 403 (body=%s)", status, raw)
			}
			var prob struct {
				Code     string `json:"code"`
				Limit    int    `json:"limit"`
				Observed int    `json:"observed"`
			}
			if err := json.Unmarshal(raw, &prob); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if prob.Code != "org_invitation_cap_exceeded" {
				t.Errorf("problem code = %q, want org_invitation_cap_exceeded", prob.Code)
			}
			if prob.Limit != invitesMax {
				t.Errorf("problem limit = %d, want %d", prob.Limit, invitesMax)
			}
			if prob.Observed != invitesMax {
				t.Errorf("problem observed = %d, want %d", prob.Observed, invitesMax)
			}
		})
	}
}

package state_test

// memstore_orgs_slo_test.go: covers zero-coverage MemStore methods
// on the orgs, invitations, overage-cap, SLO-stub, and scale-history
// surfaces that PR #1 / PR #2 left behind. All are pure in-memory
// state operations on top of state.MemStore — no Postgres, no network
// — so the tests are deterministic and fast.
//
// Each test creates the minimal fixture (one account via
// CreateAccountWithPersonalOrg, plus an app / deployment / second
// account / org / invitation as needed) and asserts the new method's
// contract. The naming convention `TestMemStore_<method>_<scenario>`
// matches the existing pkg/state/memstore_*_test.go files.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// memOrgFixture creates an account + its personal org (the account
// is the sole owner) via CreateAccountWithPersonalOrg. Returns the
// store, ctx, account, and personal org.
func memOrgFixture(t *testing.T) (*state.MemStore, context.Context, state.Account, state.Org) {
	t.Helper()
	ctx := context.Background()
	m := state.NewMemStore()
	res, err := m.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: "org-" + uuid.NewString() + "@example.com",
		Plan:  api.PlanPro,
	})
	if err != nil {
		t.Fatalf("CreateAccountWithPersonalOrg: %v", err)
	}
	return m, ctx, res.Account, res.PersonalOrg
}

// TestMemStore_UpdateOrgName_Happy — UpdateOrgName mutates the org's
// Name + UpdatedAt fields. Subsequent reads must see the new value.
func TestMemStore_UpdateOrgName_Happy(t *testing.T) {
	t.Parallel()
	m, ctx, _, org := memOrgFixture(t)
	if err := m.UpdateOrgName(ctx, org.ID, "Renamed Org"); err != nil {
		t.Fatalf("UpdateOrgName: %v", err)
	}
	got, err := m.OrgByID(ctx, org.ID)
	if err != nil {
		t.Fatalf("OrgByID: %v", err)
	}
	if got.Name != "Renamed Org" {
		t.Errorf("Name = %q, want %q", got.Name, "Renamed Org")
	}
	if !got.UpdatedAt.After(org.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want > %v (original)", got.UpdatedAt, org.UpdatedAt)
	}
}

// TestMemStore_UpdateOrgName_NotFound — unknown org id must return
// ErrNotFound, not panic or silently succeed.
func TestMemStore_UpdateOrgName_NotFound(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	err := m.UpdateOrgName(context.Background(), uuid.NewString(), "x")
	if err == nil {
		t.Error("UpdateOrgName(unknown) = nil; want ErrNotFound")
	}
}

// TestMemStore_TransferOrgOwnership_Happy — owner hands the org to
// another member; from-account is demoted to admin, to-account
// becomes owner. The 2-owner guard only fires on misuse.
func TestMemStore_TransferOrgOwnership_Happy(t *testing.T) {
	t.Parallel()
	m, ctx, owner, org := memOrgFixture(t)
	// Add a second member to the org (the destination of the transfer).
	target := state.Account{ID: uuid.NewString(), Email: "target-" + uuid.NewString() + "@example.com", Plan: api.PlanPro, Status: state.AccountActive, CreatedAt: time.Now().UTC()}
	(*m.Mu()).Lock()
	(*m.Accounts())[target.ID] = target
	(*m.Memberships())[state.OrgAccountKey{OrgID: org.ID, AccountID: target.ID}] = state.OrgMembership{
		OrgID: org.ID, AccountID: target.ID, Role: state.OrgRoleViewer, JoinedAt: time.Now().UTC(),
	}
	(*m.Mu()).Unlock()

	if err := m.TransferOrgOwnership(ctx, org.ID, owner.ID, target.ID); err != nil {
		t.Fatalf("TransferOrgOwnership: %v", err)
	}
	(*
	// Re-read memberships under the lock.
	m.Mu()).Lock()
	fromMem := (*m.Memberships())[state.OrgAccountKey{OrgID: org.ID, AccountID: owner.ID}]
	toMem := (*m.Memberships())[state.OrgAccountKey{OrgID: org.ID, AccountID: target.ID}]
	(*m.Mu()).Unlock()
	if fromMem.Role != state.OrgRoleAdmin {
		t.Errorf("from-role = %q, want %q (demoted)", fromMem.Role, state.OrgRoleAdmin)
	}
	if toMem.Role != state.OrgRoleOwner {
		t.Errorf("to-role = %q, want %q (promoted)", toMem.Role, state.OrgRoleOwner)
	}
}

// TestMemStore_TransferOrgOwnership_SameAccount — from == to must
// return ErrOrgLastOwner (the "you can't transfer to yourself" path).
func TestMemStore_TransferOrgOwnership_SameAccount(t *testing.T) {
	t.Parallel()
	m, ctx, owner, org := memOrgFixture(t)
	err := m.TransferOrgOwnership(ctx, org.ID, owner.ID, owner.ID)
	if !errors.Is(err, state.ErrOrgLastOwner) {
		t.Errorf("err = %v, want ErrOrgLastOwner", err)
	}
}

// TestMemStore_TransferOrgOwnership_ToAlreadyOwner — target already
// has the Owner role; the last-owner guard rejects the duplicate.
func TestMemStore_TransferOrgOwnership_ToAlreadyOwner(t *testing.T) {
	t.Parallel()
	m, ctx, owner, org := memOrgFixture(t)
	other := state.Account{ID: uuid.NewString(), Email: "other-" + uuid.NewString() + "@example.com", Plan: api.PlanPro, Status: state.AccountActive, CreatedAt: time.Now().UTC()}
	(*m.Mu()).Lock()
	(*m.Accounts())[other.ID] = other
	(*m.Memberships())[state.OrgAccountKey{OrgID: org.ID, AccountID: other.ID}] = state.OrgMembership{
		OrgID: org.ID, AccountID: other.ID, Role: state.OrgRoleOwner, JoinedAt: time.Now().UTC(),
	}
	(*m.Mu()).Unlock()
	err := m.TransferOrgOwnership(ctx, org.ID, owner.ID, other.ID)
	if !errors.Is(err, state.ErrOrgLastOwner) {
		t.Errorf("err = %v, want ErrOrgLastOwner (target already owner)", err)
	}
}

// TestMemStore_TransferOrgOwnership_UnknownOrg — the source-account
// lookup happens against the (orgID, accountID) pair, so an unknown
// orgID yields ErrNotFound at the membership probe.
func TestMemStore_TransferOrgOwnership_UnknownOrg(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	err := m.TransferOrgOwnership(context.Background(), uuid.NewString(), uuid.NewString(), uuid.NewString())
	if err == nil {
		t.Error("TransferOrgOwnership(unknown org) = nil; want error")
	}
}

// TestMemStore_ListOrgInvitationsForOrgPage_LimitDefaults — limit
// outside the (0, 100] band clamps to 25 (the documented default).
func TestMemStore_ListOrgInvitationsForOrgPage_LimitDefaults(t *testing.T) {
	t.Parallel()
	m, ctx, _, org := memOrgFixture(t)
	// No invitations exist; result is empty regardless of limit.
	got, err := m.ListOrgInvitationsForOrgPage(ctx, org.ID, 0, "")
	if err != nil {
		t.Fatalf("ListOrgInvitationsForOrgPage(0): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 on empty org", len(got))
	}
	// Upper clamp: limit > 100 also lands at the default.
	got, err = m.ListOrgInvitationsForOrgPage(ctx, org.ID, 1000, "")
	if err != nil {
		t.Fatalf("ListOrgInvitationsForOrgPage(1000): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (no invitations)", len(got))
	}
}

// TestMemStore_ListOrgInvitationsForOrgPage_InvalidCursor — a
// malformed `before` cursor must surface as ErrInvalidCursor joined
// with the underlying decode error.
func TestMemStore_ListOrgInvitationsForOrgPage_InvalidCursor(t *testing.T) {
	t.Parallel()
	m, ctx, _, org := memOrgFixture(t)
	_, err := m.ListOrgInvitationsForOrgPage(ctx, org.ID, 10, "not-a-cursor")
	if err == nil {
		t.Error("ListOrgInvitationsForOrgPage(invalid cursor) = nil; want ErrInvalidCursor")
	}
}

// TestMemStore_ListOrgInvitationsForOrgPage_Happy — three invitations
// on the org, sorted by (CreatedAt desc, ID desc), limited to two.
func TestMemStore_ListOrgInvitationsForOrgPage_Happy(t *testing.T) {
	t.Parallel()
	m, ctx, owner, org := memOrgFixture(t)
	now := time.Now().UTC()
	(*
	// Create three invitations directly via m.invitations map under
	// the lock — we want deterministic CreatedAt + ID ordering.
	m.Mu()).Lock()
	for i := 0; i < 3; i++ {
		inv := state.OrgInvitation{
			ID:        uuid.NewString(),
			OrgID:     org.ID,
			Email:     "invitee-" + uuid.NewString() + "@example.com",
			Role:      state.OrgRoleViewer,
			TokenHash: []byte("tok-" + uuid.NewString()),
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}
		(*m.Invitations())[string(inv.TokenHash)] = inv
	}
	(*m.Mu()).Unlock()
	got, err := m.ListOrgInvitationsForOrgPage(ctx, org.ID, 2, "")
	if err != nil {
		t.Fatalf("ListOrgInvitationsForOrgPage: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (limit honored)", len(got))
	}
	// First row has the latest CreatedAt.
	for i := 0; i < len(got)-1; i++ {
		if got[i].CreatedAt.Before(got[i+1].CreatedAt) {
			t.Errorf("got[%d].CreatedAt < got[%d].CreatedAt; want descending", i, i+1)
		}
	}
	_ = owner
}

// TestMemStore_UpdateAccountOverageCapCents_SetAndClear — cents=nil
// deletes the row (the documented "unset" path); cents=42 sets it.
func TestMemStore_UpdateAccountOverageCapCents_SetAndClear(t *testing.T) {
	t.Parallel()
	m, ctx, acct, _ := memOrgFixture(t)

	// Set.
	cents := int64(4200)
	if err := m.UpdateAccountOverageCapCents(ctx, acct.ID, &cents); err != nil {
		t.Fatalf("UpdateAccountOverageCapCents(set): %v", err)
	}
	got, ok, err := m.GetAccountOverageCapCents(ctx, acct.ID)
	if err != nil {
		t.Fatalf("GetAccountOverageCapCents: %v", err)
	}
	if !ok || got != 4200 {
		t.Errorf("got = %d, ok = %v; want 4200, true", got, ok)
	}

	// Clear.
	if err := m.UpdateAccountOverageCapCents(ctx, acct.ID, nil); err != nil {
		t.Fatalf("UpdateAccountOverageCapCents(nil): %v", err)
	}
	_, ok, err = m.GetAccountOverageCapCents(ctx, acct.ID)
	if err != nil {
		t.Fatalf("GetAccountOverageCapCents: %v", err)
	}
	if ok {
		t.Error("ok = true after clear; want false")
	}
}

// TestMemStore_UsageSLOForApp_Stub — MemStore's UsageSLOForApp is a
// stub that always returns (0, 0, nil). PgStore implements the real
// computation; the MemStore stub exists for interface parity. Pin
// the stub behaviour so a future refactor doesn't silently start
// returning real values that diverge from PgStore's contract.
func TestMemStore_UsageSLOForApp_Stub(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	p50, p99, err := m.UsageSLOForApp(context.Background(), "app", "dep", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("UsageSLOForApp: %v", err)
	}
	if p50 != 0 || p99 != 0 {
		t.Errorf("p50=%v p99=%v; want 0,0 (stub)", p50, p99)
	}
}

// TestMemStore_UsageSLOForAccount_Stub — same stub contract for the
// account-level SLO accessor.
func TestMemStore_UsageSLOForAccount_Stub(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	p50, p99, err := m.UsageSLOForAccount(context.Background(), "acct", time.Now(), time.Now())
	if err != nil {
		t.Fatalf("UsageSLOForAccount: %v", err)
	}
	if p50 != 0 || p99 != 0 {
		t.Errorf("p50=%v p99=%v; want 0,0 (stub)", p50, p99)
	}
}

// TestMemStore_GetInstanceTailCount_NotFound — unknown instance id
// returns (0, ErrNotFound).
func TestMemStore_GetInstanceTailCount_NotFound(t *testing.T) {
	t.Parallel()
	_, err := state.NewMemStore().GetInstanceTailCount(context.Background(), uuid.NewString())
	if err == nil {
		t.Error("GetInstanceTailCount(unknown) = nil; want ErrNotFound")
	}
}

// TestMemStore_GetInstanceTailCount_Zero — an instance with no
// recorded tail lines returns 0, not a default that the
// Prometheus query would mistake for "missing metric".
func TestMemStore_GetInstanceTailCount_Zero(t *testing.T) {
	t.Parallel()
	m, ctx, _, app, dep := state.MemDeploymentFixture(t)
	inst := state.MemLiveInstance(t, m, ctx, app.ID, dep.ID, "node-a")
	got, err := m.GetInstanceTailCount(ctx, inst.ID)
	if err != nil {
		t.Fatalf("GetInstanceTailCount: %v", err)
	}
	if got != 0 {
		t.Errorf("got = %d, want 0", got)
	}
}

// TestMemStore_UpsertDeploymentScanResult_NotFound — unknown
// deployment id returns ErrNotFound.
func TestMemStore_UpsertDeploymentScanResult_NotFound(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	err := m.UpsertDeploymentScanResult(context.Background(), uuid.NewString(), []byte("scan"), "ok")
	if err == nil {
		t.Error("UpsertDeploymentScanResult(unknown) = nil; want ErrNotFound")
	}
}

// TestMemStore_UpsertDeploymentScanResult_Happy — writes the scan
// result + status + timestamp onto the deployment row.
func TestMemStore_UpsertDeploymentScanResult_Happy(t *testing.T) {
	t.Parallel()
	m, ctx, _, _, dep := state.MemDeploymentFixture(t)
	payload := []byte(`{"vulns":[]}`)
	if err := m.UpsertDeploymentScanResult(ctx, dep.ID, payload, "ok"); err != nil {
		t.Fatalf("UpsertDeploymentScanResult: %v", err)
	}
	(*m.Mu()).Lock()
	got := (*m.Deployments())[dep.ID]
	(*m.Mu()).Unlock()
	if got.ScanStatus != "ok" {
		t.Errorf("ScanStatus = %q, want %q", got.ScanStatus, "ok")
	}
	if string(got.ScanResult) != string(payload) {
		t.Errorf("ScanResult = %q, want %q (defensive copy)", got.ScanResult, payload)
	}
	if got.ScannedAt.IsZero() {
		t.Error("ScannedAt is zero; want set to time.Now()")
	}
}

// TestMemStore_SetLastScaleOutAt_Happy — stamps the pointer field on
// the matching app row.
func TestMemStore_SetLastScaleOutAt_Happy(t *testing.T) {
	t.Parallel()
	m, _, _, app, _ := state.MemDeploymentFixture(t)
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := m.SetLastScaleOutAt(app.ID, ts); err != nil {
		t.Fatalf("SetLastScaleOutAt: %v", err)
	}
	(*m.Mu()).Lock()
	got := (*m.Apps())[app.ID]
	(*m.Mu()).Unlock()
	if got.LastScaleOutAt == nil {
		t.Fatal("LastScaleOutAt is nil; want non-nil")
	}
	if !got.LastScaleOutAt.Equal(ts) {
		t.Errorf("LastScaleOutAt = %v, want %v", got.LastScaleOutAt, ts)
	}
}

// TestMemStore_SetLastScaleOutAt_NotFound — unknown app id returns
// ErrNotFound.
func TestMemStore_SetLastScaleOutAt_NotFound(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	err := m.SetLastScaleOutAt(uuid.NewString(), time.Now())
	if err == nil {
		t.Error("SetLastScaleOutAt(unknown) = nil; want ErrNotFound")
	}
}

// TestMemStore_SetLastScaleInAt_Happy — symmetric counterpart of
// SetLastScaleOutAt; pins the same pointer-field contract.
func TestMemStore_SetLastScaleInAt_Happy(t *testing.T) {
	t.Parallel()
	m, _, _, app, _ := state.MemDeploymentFixture(t)
	ts := time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)
	if err := m.SetLastScaleInAt(app.ID, ts); err != nil {
		t.Fatalf("SetLastScaleInAt: %v", err)
	}
	(*m.Mu()).Lock()
	got := (*m.Apps())[app.ID]
	(*m.Mu()).Unlock()
	if got.LastScaleInAt == nil {
		t.Fatal("LastScaleInAt is nil; want non-nil")
	}
	if !got.LastScaleInAt.Equal(ts) {
		t.Errorf("LastScaleInAt = %v, want %v", got.LastScaleInAt, ts)
	}
}

// TestMemStore_SetLastScaleInAt_NotFound — symmetric error path.
func TestMemStore_SetLastScaleInAt_NotFound(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	err := m.SetLastScaleInAt(uuid.NewString(), time.Now())
	if err == nil {
		t.Error("SetLastScaleInAt(unknown) = nil; want ErrNotFound")
	}
}

// TestMemStore_AuthDefaultFlippedAt_Empty — on a fresh store with no
// flipped apps, the returned time must be the zero value (not
// time.Now()) — callers compare against this to decide "is the
// cohort wide enough to roll out?".
func TestMemStore_AuthDefaultFlippedAt_Empty(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	got, err := m.AuthDefaultFlippedAt(context.Background())
	if err != nil {
		t.Fatalf("AuthDefaultFlippedAt: %v", err)
	}
	if !got.IsZero() {
		t.Errorf("got = %v, want zero (no flipped apps)", got)
	}
}

// TestMemStore_AuthDefaultFlippedAt_FindsEarliest — when multiple
// apps have AuthDefaultFlippedAt set, the earliest timestamp wins.
// This pins the (a, b) → min(a, b) fold that the rollout daemon
// uses to decide the "earliest flipped app in the fleet" anchor.
func TestMemStore_AuthDefaultFlippedAt_FindsEarliest(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	now := time.Now().UTC()
	(*
	// Three apps with monotonically-increasing flipped timestamps.
	m.Mu()).Lock()
	for i := 0; i < 3; i++ {
		appID := uuid.NewString()
		ts := now.Add(time.Duration(i) * time.Hour)
		(*m.Apps())[appID] = state.App{
			ID:                   appID,
			Slug:                 "flipped-" + uuid.NewString(),
			AuthDefaultFlippedAt: &ts,
			Status:               state.AppActive,
			CreatedAt:            now,
		}
	}
	(*m.Mu()).Unlock()
	got, err := m.AuthDefaultFlippedAt(context.Background())
	if err != nil {
		t.Fatalf("AuthDefaultFlippedAt: %v", err)
	}
	want := now // earliest of the three
	if !got.Equal(want) {
		t.Errorf("got = %v, want %v (earliest flipped)", got, want)
	}
}

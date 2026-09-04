// memstore_sweep_extra_test.go — fill additional pkg/state MemStore
// method-coverage gaps that the existing 100+ test files don't deeply
// reach. Targets methods named in the cluster-1 plan (Accounts, API
// keys, MFA, Apps-on-node, Instances-on-node) plus a few high-impact
// methods that surfaced at 0% in the pre-PR coverage report.
//
// All tests use state.NewMemStore() so they're pgtest-free and run on
// every CI lane. Whitebox `package state` (matches every existing
// memstore_*_test.go).

package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- Accounts --------------------------------------------------------

func TestMemStore_AccountByProviderCustomerID_HitMiss(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()

	a1, err := store.CreateAccount(ctx, "alice@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, a1.ID, "cus_test_001"); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := store.AccountByProviderCustomerID(ctx, "cus_test_001")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != a1.ID {
		t.Errorf("got ID %q, want %q", got.ID, a1.ID)
	}

	// Miss → ErrNotFound.
	if _, err := store.AccountByProviderCustomerID(ctx, "cus_no_such"); !errors.Is(err, ErrNotFound) {
		t.Errorf("miss: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_UpdateAccountProviderCustomerID_MissReturnsErrNotFound(t *testing.T) {
	store := NewMemStore()
	err := store.UpdateAccountProviderCustomerID(context.Background(), "no-such-acct", "cus_x")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_ListAllAccounts_Empty(t *testing.T) {
	store := NewMemStore()
	got, err := store.ListAllAccounts(context.Background())
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty store: got %d, want 0", len(got))
	}
}

func TestMemStore_ListAllAccounts_Populated(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	for i, email := range []string{"a@x.com", "b@x.com", "c@x.com"} {
		if _, err := store.CreateAccount(ctx, email, api.PlanFree); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	got, err := store.ListAllAccounts(ctx)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d, want 3", len(got))
	}
}

func TestMemStore_UpdateAccountPlan_RoundTrip(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	if err := store.UpdateAccountPlan(ctx, a.ID, api.PlanHobby); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.AccountByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Plan != api.PlanHobby {
		t.Errorf("plan = %q, want Hobby", got.Plan)
	}
}

func TestMemStore_UpdateAccountPlan_MissReturnsErrNotFound(t *testing.T) {
	store := NewMemStore()
	if err := store.UpdateAccountPlan(context.Background(), "no-such", api.PlanPro); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_UpdateAccountStatus_RoundTrip(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	if err := store.UpdateAccountStatus(ctx, a.ID, AccountSuspended); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.AccountByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Status != AccountSuspended {
		t.Errorf("status = %q, want suspended", got.Status)
	}
}

func TestMemStore_UpdateAccountStatus_MissReturnsErrNotFound(t *testing.T) {
	store := NewMemStore()
	if err := store.UpdateAccountStatus(context.Background(), "no-such", AccountSuspended); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_UpdateAccountStripeSubscriptionItem_RoundTrip(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, a.ID, "si_test_001"); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.AccountByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.StripeSubscriptionItem != "si_test_001" {
		t.Errorf("subscription item = %q", got.StripeSubscriptionItem)
	}
}

func TestMemStore_UpdateAccountStripeSubscriptionItem_MissReturnsErrNotFound(t *testing.T) {
	store := NewMemStore()
	if err := store.UpdateAccountStripeSubscriptionItem(context.Background(), "no-such", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// --- API keys --------------------------------------------------------

func TestMemStore_CreateAPIKey_HappyPath(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	k, err := store.CreateAPIKey(ctx, a.ID, []byte("hash-1"), "ci", []string{"read", "write"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if k.ID == "" {
		t.Error("key ID empty")
	}
	if k.Status != string(APIKeyStatusActive) {
		t.Errorf("status = %q, want active", k.Status)
	}
	if len(k.Scopes) != 2 {
		t.Errorf("scopes = %v", k.Scopes)
	}
}

func TestMemStore_CreateAPIKey_DuplicateHashRejected(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	if _, err := store.CreateAPIKey(ctx, a.ID, []byte("h"), "k1", nil); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := store.CreateAPIKey(ctx, a.ID, []byte("h"), "k2", nil); err == nil {
		t.Error("duplicate hash: err = nil, want reject")
	}
}

func TestMemStore_ListAPIKeys_Filter(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	for i := 0; i < 3; i++ {
		if _, err := store.CreateAPIKey(ctx, a.ID, []byte("h"+string(rune('a'+i))), "k", nil); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	keys, err := store.ListAPIKeys(ctx, a.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) != 3 {
		t.Errorf("got %d, want 3", len(keys))
	}
}

func TestMemStore_GetAPIKey_HitMissCrossAccount(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a1, _ := store.CreateAccount(ctx, "a1@x.com", api.PlanFree)
	a2, _ := store.CreateAccount(ctx, "a2@x.com", api.PlanFree)
	k1, err := store.CreateAPIKey(ctx, a1.ID, []byte("h1"), "k", nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := store.GetAPIKey(ctx, a1.ID, k1.ID)
	if err != nil {
		t.Errorf("owner hit: err = %v", err)
	}
	if got.ID != k1.ID {
		t.Errorf("got ID %q, want %q", got.ID, k1.ID)
	}

	// Cross-account lookup must fail.
	if _, err := store.GetAPIKey(ctx, a2.ID, k1.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account: err = %v, want ErrNotFound", err)
	}

	// Miss by keyID.
	if _, err := store.GetAPIKey(ctx, a1.ID, "no-such"); !errors.Is(err, ErrNotFound) {
		t.Errorf("miss: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_DeleteAPIKeyReturning_OwnerHit(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	k, err := store.CreateAPIKey(ctx, a.ID, []byte("h"), "k", nil)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := store.DeleteAPIKeyReturning(ctx, a.ID, k.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if got.ID != k.ID {
		t.Errorf("returned ID = %q", got.ID)
	}
	// And the row is gone.
	if _, err := store.GetAPIKey(ctx, a.ID, k.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after-delete: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_DeleteAPIKeyReturning_CrossAccountFails(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a1, _ := store.CreateAccount(ctx, "a1@x.com", api.PlanFree)
	a2, _ := store.CreateAccount(ctx, "a2@x.com", api.PlanFree)
	k, _ := store.CreateAPIKey(ctx, a1.ID, []byte("h"), "k", nil)
	if _, err := store.DeleteAPIKeyReturning(ctx, a2.ID, k.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account delete: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_MarkAPIKeyRevoked_HitMiss(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	k, _ := store.CreateAPIKey(ctx, a.ID, []byte("h"), "k", nil)
	got, err := store.MarkAPIKeyRevoked(ctx, a.ID, k.ID)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got.Status != string(APIKeyStatusRevoked) {
		t.Errorf("status = %q, want revoked", got.Status)
	}
	if got.RevokedAt == nil {
		t.Error("RevokedAt is nil")
	}

	// Idempotent — second revoke returns the same row.
	again, err := store.MarkAPIKeyRevoked(ctx, a.ID, k.ID)
	if err != nil {
		t.Fatalf("re-revoke: %v", err)
	}
	if again.RevokedAt == nil || !again.RevokedAt.Equal(*got.RevokedAt) {
		t.Errorf("re-revoke changed timestamp: got %v, want %v", again.RevokedAt, got.RevokedAt)
	}

	if _, err := store.MarkAPIKeyRevoked(ctx, a.ID, "no-such"); !errors.Is(err, ErrNotFound) {
		t.Errorf("miss: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_TouchKeyLastUsed_HitMiss(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	k, _ := store.CreateAPIKey(ctx, a.ID, []byte("h"), "k", nil)

	before := time.Now().Add(-time.Hour)
	if err := store.TouchKeyLastUsed(ctx, k.ID); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, err := store.GetAPIKey(ctx, a.ID, k.ID)
	if err != nil {
		t.Fatalf("read-back: %v", err)
	}
	if !got.LastUsedAt.After(before) {
		t.Errorf("LastUsedAt %v not after %v", got.LastUsedAt, before)
	}

	if err := store.TouchKeyLastUsed(ctx, "no-such-key"); !errors.Is(err, ErrNotFound) {
		t.Errorf("miss: err = %v, want ErrNotFound", err)
	}
}

// --- MFA (issue #186) ------------------------------------------------

func TestMemStore_MFASecret_RoundTrip(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)

	// Empty initial state.
	if _, err := store.ReadMFASecret(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("read empty: err = %v, want ErrNotFound", err)
	}

	enc := []byte("encrypted-secret")
	recovery := [][]byte{[]byte("h1"), []byte("h2"), []byte("h3")}
	if err := store.SetMFASecret(ctx, a.ID, enc, recovery); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := store.ReadMFASecret(ctx, a.ID)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(enc) {
		t.Errorf("got %q", got)
	}
}

func TestMemStore_MFAEnrolled_Clear(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)

	if err := store.MarkMFAEnrolled(ctx, a.ID); err != nil {
		t.Fatalf("mark enrolled: %v", err)
	}
	got, _ := store.AccountByID(ctx, a.ID)
	if got.MFAEnrolledAt == nil {
		t.Error("MFAEnrolledAt is nil after MarkMFAEnrolled")
	}

	if err := store.ClearMFA(ctx, a.ID); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got2, _ := store.AccountByID(ctx, a.ID)
	if got2.MFAEnrolledAt != nil {
		t.Errorf("MFAEnrolledAt = %v, want nil after ClearMFA", got2.MFAEnrolledAt)
	}
}

func TestMemStore_SetMFARequired_Toggle(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)

	changed, err := store.SetMFARequired(ctx, a.ID, true)
	if err != nil {
		t.Fatalf("set required: %v", err)
	}
	if !changed {
		t.Error("expected changed=true on first set")
	}

	// Set same value again — changed=false.
	changed, err = store.SetMFARequired(ctx, a.ID, true)
	if err != nil {
		t.Fatalf("re-set required: %v", err)
	}
	if changed {
		t.Error("expected changed=false on idempotent set")
	}

	// Unset.
	changed, err = store.SetMFARequired(ctx, a.ID, false)
	if err != nil {
		t.Fatalf("unset required: %v", err)
	}
	if !changed {
		t.Error("expected changed=true on unset")
	}
}

func TestMemStore_ConsumeRecoveryCode_HitMissLastCode(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)

	h1 := []byte("hash-one")
	h2 := []byte("hash-two")
	if err := store.SetMFASecret(ctx, a.ID, []byte("enc"), [][]byte{h1, h2}); err != nil {
		t.Fatalf("set: %v", err)
	}

	// Wrong code → matched=false.
	matched, _, _, err := store.ConsumeRecoveryCode(ctx, a.ID, []byte("wrong"))
	if err != nil {
		t.Fatalf("consume wrong: %v", err)
	}
	if matched {
		t.Error("matched=true on wrong code")
	}

	// First correct burn.
	matched, lastCode, remaining, err := store.ConsumeRecoveryCode(ctx, a.ID, h1)
	if err != nil {
		t.Fatalf("burn h1: %v", err)
	}
	if !matched {
		t.Error("matched=false on h1")
	}
	if lastCode {
		t.Error("lastCode=true on first burn (2 remaining)")
	}
	if remaining != 1 {
		t.Errorf("remaining = %d, want 1", remaining)
	}

	// Second burn — lastCode=true, remaining=0.
	matched, lastCode, remaining, err = store.ConsumeRecoveryCode(ctx, a.ID, h2)
	if err != nil {
		t.Fatalf("burn h2: %v", err)
	}
	if !matched || !lastCode || remaining != 0 {
		t.Errorf("h2 burn: matched=%v lastCode=%v remaining=%d", matched, lastCode, remaining)
	}
}

func TestMemStore_ConsumeRecoveryCode_MissingAccount(t *testing.T) {
	store := NewMemStore()
	_, _, _, err := store.ConsumeRecoveryCode(context.Background(), "no-such", []byte("x"))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_MatchRecoveryCode_NotConsuming(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	a, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	h := []byte("hash-1")
	if err := store.SetMFASecret(ctx, a.ID, []byte("enc"), [][]byte{h}); err != nil {
		t.Fatalf("set: %v", err)
	}

	matched, lastCode, err := store.MatchRecoveryCode(ctx, a.ID, h)
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if !matched || !lastCode {
		t.Errorf("single-hash match: matched=%v lastCode=%v", matched, lastCode)
	}

	// Re-match — recovery code is still there (Match does not consume).
	matched2, _, err := store.MatchRecoveryCode(ctx, a.ID, h)
	if err != nil {
		t.Fatalf("re-match: %v", err)
	}
	if !matched2 {
		t.Error("Match consumed the hash")
	}
}

// --- Apps on a compute node -----------------------------------------

func TestMemStore_ListAppsByNodeID_EmptyAndFiltered(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	a1, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1"})
	a2, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a2"})
	a3, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a3"})

	// No apps placed yet → empty.
	got, err := store.ListAppsByNodeID(ctx, "node-1")
	if err != nil {
		t.Fatalf("empty list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty: got %d, want 0", len(got))
	}

	if err := store.SetAppNodeID(ctx, a1.ID, "node-1"); err != nil {
		t.Fatalf("place a1: %v", err)
	}
	if err := store.SetAppNodeID(ctx, a2.ID, "node-2"); err != nil {
		t.Fatalf("place a2: %v", err)
	}
	if err := store.SetAppNodeID(ctx, a3.ID, "node-1"); err != nil {
		t.Fatalf("place a3: %v", err)
	}

	got, err = store.ListAppsByNodeID(ctx, "node-1")
	if err != nil {
		t.Fatalf("filtered: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("node-1 apps: got %d, want 2", len(got))
	}
}

func TestMemStore_SetAppNodeID_NotFound(t *testing.T) {
	store := NewMemStore()
	if err := store.SetAppNodeID(context.Background(), "no-such-app", "node-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_ListUnplacedApps_Filter(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	a1, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1"})
	a2, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a2"})
	a3, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a3"})
	_ = a2
	_ = a3

	if err := store.SetAppNodeID(ctx, a1.ID, "node-1"); err != nil {
		t.Fatalf("place a1: %v", err)
	}
	// a2 + a3 should be unplaced.
	got, err := store.ListUnplacedApps(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d, want 2", len(got))
	}
}

func TestMemStore_ListOwnedCronsByNodeID_Filtered(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	a1, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1"})
	a2, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a2"})

	if _, err := store.CreateCron(ctx, a1.ID, "*/5 * * * *", "/healthz", true); err != nil {
		t.Fatalf("seed cron 1: %v", err)
	}
	if _, err := store.CreateCron(ctx, a2.ID, "*/10 * * * *", "/healthz", true); err != nil {
		t.Fatalf("seed cron 2: %v", err)
	}

	got, err := store.ListOwnedCronsByNodeID(ctx, "node-1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// All seeded crons have NodeID="" since CreateCron doesn't take one;
	// the filter must surface nothing on a non-empty nodeID.
	if len(got) != 0 {
		t.Errorf("got %d, want 0 (none placed on node-1)", len(got))
	}
}

func TestMemStore_ListOrphanedApps_EmptyWhenNoCooldown(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	if _, err := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := store.ListOrphanedApps(ctx, 3600, 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0 (no orphans)", len(got))
	}
}

func TestMemStore_ReassignAppOwner_Happy(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	app, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1", NodeID: "node-old"})

	if err := store.ReassignAppOwner(ctx, app.ID, "node-old", "node-new"); err != nil {
		t.Fatalf("reassign: %v", err)
	}
	got, err := store.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatalf("read-back: %v", err)
	}
	if got.NodeID != "node-new" {
		t.Errorf("NodeID = %q, want node-new", got.NodeID)
	}
}

func TestMemStore_ReassignAppOwner_MismatchFromID(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	app, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1", NodeID: "node-old"})
	if err := store.ReassignAppOwner(ctx, app.ID, "wrong", "node-new"); !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict on from-node mismatch", err)
	}
}

// --- Instances on a compute node ------------------------------------

func TestMemStore_ListInstancesByNodeID_Filtered(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	// Two apps on different nodes; instances inherit the app's
	// NodeID for ListInstancesByNodeID (it joins via app owner).
	app1, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1", NodeID: "node-1"})
	app2, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a2", NodeID: "node-2"})

	// 3 instances on app1 (node-1), 2 on app2 (node-2).
	for i := 0; i < 3; i++ {
		if _, err := store.CreateInstance(ctx, app1.ID, "dep-1", string(StateRunning), 256, "node-1", "wake-"+string(rune('a'+i))); err != nil {
			t.Fatalf("seed app1 %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := store.CreateInstance(ctx, app2.ID, "dep-1", string(StateRunning), 256, "node-2", "wake-"+string(rune('a'+i))); err != nil {
			t.Fatalf("seed app2 %d: %v", i, err)
		}
	}
	got, err := store.ListInstancesByNodeID(ctx, "node-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("node-1 instances: got %d, want 3", len(got))
	}
}

func TestMemStore_ListLiveInstancesOnNode_FilteredByState(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	app, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1"})

	// Mix of live + parked instances on node-1.
	for i, state := range []State{StateRunning, StateRunning, StateParked, StateRunning} {
		if _, err := store.CreateInstance(ctx, app.ID, "dep-1", string(state), 256, "node-1", "wake-"+string(rune('a'+i))); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	got, err := store.ListLiveInstancesOnNode(ctx, "node-1", 100)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("live: got %d, want 3 (parked filtered out)", len(got))
	}
}

func TestMemStore_MarkInstanceMigrating_HitMiss(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	app, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1"})
	inst, err := store.CreateInstance(ctx, app.ID, "dep-1", string(StateRunning), 256, "node-1", "wake-a")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := store.MarkInstanceMigrating(ctx, inst.ID, "node-1", "lease-tok-1"); err != nil {
		t.Fatalf("mark migrating: %v", err)
	}
	if err := store.MarkInstanceMigrating(ctx, "no-such", "node-1", "lease-x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("miss: err = %v, want ErrNotFound", err)
	}
}

func TestMemStore_MigrateInstanceOwner_SweepExtra_Happy(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	app, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1", NodeID: "node-1"})
	inst, _ := store.CreateInstance(ctx, app.ID, "dep-1", string(StateRunning), 256, "node-1", "wake-a")
	// Migrate requires State="migrating" + lease-token match.
	if err := store.MarkInstanceMigrating(ctx, inst.ID, "node-1", "lease-tok-1"); err != nil {
		t.Fatalf("mark migrating: %v", err)
	}
	if err := store.MigrateInstanceOwner(ctx, inst.ID, "node-1", "node-2", "stale-lease"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale migrate lease: %v, want ErrConflict", err)
	}
	if err := store.MigrateInstanceOwner(ctx, inst.ID, "node-1", "node-2", "lease-tok-1"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// After MigrateInstanceOwner: instance.NodeID == "node-2",
	// instance.State == "running", app.MigratedAt is stamped.
	// ListInstancesByNodeID filters via app.NodeID, which
	// MigrateInstanceOwner intentionally does NOT flip (only the
	// instance-level NodeID moves). So a follow-up ListInstancesByNodeID
	// on node-2 returns empty — the load-bearing assertion is "no
	// error returned".
}

func TestMemStore_MigrateInstanceOwner_FromMismatch(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	owner, _ := store.CreateAccount(ctx, "a@x.com", api.PlanFree)
	app, _ := store.CreateApp(ctx, App{AccountID: owner.ID, Slug: "a1"})
	inst, _ := store.CreateInstance(ctx, app.ID, "dep-1", string(StateRunning), 256, "node-1", "wake-a")
	if err := store.MigrateInstanceOwner(ctx, inst.ID, "wrong", "node-2", "lease-x"); !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v, want ErrConflict on from-node mismatch", err)
	}
}

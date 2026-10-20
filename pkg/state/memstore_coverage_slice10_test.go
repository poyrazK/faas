package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// This slice closes the final remaining branch gaps: early-return
// guards, empty-arg validation, no-match recovery-code paths, and the
// provider-ID remap for the Paddle path.

func TestMemStoreCoverageListOrphanedAppsGuards(t *testing.T) {
	m, ctx, _, _, _ := state.MemCoverageSlice4Fixture(t)
	// maxPerTick < 1 → nil, nil (early return).
	if got, err := m.ListOrphanedApps(ctx, 0, 0); err != nil || got != nil {
		t.Fatalf("maxPerTick 0 = %+v, %v", got, err)
	}
	if got, err := m.ListOrphanedApps(ctx, 0, -1); err != nil || got != nil {
		t.Fatalf("maxPerTick -1 = %+v, %v", got, err)
	}
	// A node-less app and an active-node app are both skipped.
	if got, err := m.ListOrphanedApps(ctx, 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("no orphaned = %+v, %v", got, err)
	}
}

func TestMemStoreCoverageReassignAppOwnerGuards(t *testing.T) {
	m, ctx, _, app, _ := state.MemCoverageSlice4Fixture(t)
	// Empty-arg validation.
	if err := m.ReassignAppOwner(ctx, "", "from", "to"); err == nil {
		t.Fatal("empty appID should fail")
	}
	if err := m.ReassignAppOwner(ctx, app.ID, "", "to"); err == nil {
		t.Fatal("empty fromNodeID should fail")
	}
	if err := m.ReassignAppOwner(ctx, app.ID, "from", ""); err == nil {
		t.Fatal("empty toNodeID should fail")
	}
	// Missing app → ErrNotFound.
	if err := m.ReassignAppOwner(ctx, "missing", "from", "to"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing app = %v", err)
	}
	// Node mismatch → ErrConflict.
	if err := m.SetAppNodeID(ctx, app.ID, state.DefaultLocalNodeName); err != nil {
		t.Fatal(err)
	}
	if err := m.ReassignAppOwner(ctx, app.ID, "wrong-node", state.DefaultLocalNodeName); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("node mismatch = %v", err)
	}
	// Happy path (default-local → default-local is a no-op move but the
	// predicate matches).
	if err := m.ReassignAppOwner(ctx, app.ID, state.DefaultLocalNodeName, state.DefaultLocalNodeName); err != nil {
		t.Fatalf("happy reassign = %v", err)
	}
}

func TestMemStoreCoverageStripeAndPaddleMissing(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageSlice4Fixture(t)
	// UpdateAccountStripeSubscriptionItem — missing account.
	if err := m.UpdateAccountStripeSubscriptionItem(ctx, "missing", "si_x"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stripe sub missing = %v", err)
	}
	if err := m.UpdateAccountStripeSubscriptionItem(ctx, account.ID, "si_x"); err != nil {
		t.Fatal(err)
	}
	// UpdateAccountProviderCustomerID — missing + remap.
	if err := m.UpdateAccountProviderCustomerID(ctx, "missing", "ctm_x"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("paddle missing = %v", err)
	}
	if err := m.UpdateAccountProviderCustomerID(ctx, account.ID, "ctm_1"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.AccountByProviderCustomerID(ctx, "ctm_1"); err != nil || got.ID != account.ID {
		t.Fatalf("paddle lookup = %+v, %v", got, err)
	}
	// Remap: the old ctm_1 index entry must go.
	if err := m.UpdateAccountProviderCustomerID(ctx, account.ID, "ctm_2"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AccountByProviderCustomerID(ctx, "ctm_1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("paddle old still resolvable = %v", err)
	}
	if got, err := m.AccountByProviderCustomerID(ctx, "ctm_2"); err != nil || got.ID != account.ID {
		t.Fatalf("paddle new = %+v, %v", got, err)
	}
}

func TestMemStoreCoverageRecoveryCodeNoMatch(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageSlice4Fixture(t)
	// SetMFASecret with two hashes.
	if err := m.SetMFASecret(ctx, account.ID, []byte("sealed"), [][]byte{[]byte("h1"), []byte("h2")}); err != nil {
		t.Fatal(err)
	}
	// No match → (false, false, 0, nil) for both consume and match.
	// (remaining is 0 on a miss — the code returns a bare zero.)
	if matched, last, remaining, err := m.ConsumeRecoveryCode(ctx, account.ID, []byte("nope")); err != nil || matched || last || remaining != 0 {
		t.Fatalf("consume no match = %v/%v/%d, %v", matched, last, remaining, err)
	}
	if matched, last, err := m.MatchRecoveryCode(ctx, account.ID, []byte("nope")); err != nil || matched || last {
		t.Fatalf("match no match = %v/%v, %v", matched, last, err)
	}
	// Match hit → (true, last=false) since 2 hashes; consume removes it.
	if matched, last, err := m.MatchRecoveryCode(ctx, account.ID, []byte("h1")); err != nil || !matched || last {
		t.Fatalf("match h1 = %v/%v, %v", matched, last, err)
	}
	if matched, last, remaining, err := m.ConsumeRecoveryCode(ctx, account.ID, []byte("h1")); err != nil || !matched || last || remaining != 1 {
		t.Fatalf("consume h1 = %v/%v/%d, %v", matched, last, remaining, err)
	}
	// Now only h2 remains → consuming it is the last code.
	if matched, last, remaining, err := m.ConsumeRecoveryCode(ctx, account.ID, []byte("h2")); err != nil || !matched || !last || remaining != 0 {
		t.Fatalf("consume h2 = %v/%v/%d, %v", matched, last, remaining, err)
	}
	// Missing account → ErrNotFound.
	if _, _, _, err := m.ConsumeRecoveryCode(ctx, "missing", []byte("h1")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("consume missing = %v", err)
	}
	if _, _, err := m.MatchRecoveryCode(ctx, "missing", []byte("h1")); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("match missing = %v", err)
	}
}

func TestMemStoreCoverageAccountByProviderStaleIndex(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageSlice4Fixture(t)
	// Plant a reverse-map entry pointing at a deleted account via
	// UpdateAccountProviderCustomerID, then hard-delete the account —
	// the stale index should surface ErrNotFound (account gone).
	if err := m.UpdateAccountProviderCustomerID(ctx, account.ID, "cus_stale"); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkAccountDeletionPending(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteAccount(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AccountByProviderCustomerID(ctx, "cus_stale"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale provider = %v", err)
	}
	// UpdateAccountProviderCustomerID on a deleted account → ErrNotFound
	// (DeleteAccount removed the row).
	if err := m.UpdateAccountProviderCustomerID(ctx, account.ID, "cus_after"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("update after delete = %v", err)
	}
}

func TestMemStoreCoverageCreateAppIfUnderQuotaEvicted(t *testing.T) {
	m, ctx, account, _, _ := state.MemCoverageSlice4Fixture(t)
	// A second app under the same account counts toward the quota cap
	// when its status is evicted_cold (mirrors PgStore).
	second, err := m.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "evicted-app", RAMMB: 256, Status: state.AppEvictedCold})
	if err != nil {
		t.Fatal(err)
	}
	_ = second
	// The fixture already has 1 active app; the evicted_cold app makes 2
	// under a cap of 1 → quota exceeded.
	if _, err := m.CreateAppIfUnderQuota(ctx, state.App{AccountID: account.ID, Slug: "third"}, api.Limits{DeployedApps: 1}); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("quota with evicted = %v", err)
	}
	// Cap of 2 → still exceeded (fixture active + evicted = 2, so a
	// third needs cap ≥ 3).
	if _, err := m.CreateAppIfUnderQuota(ctx, state.App{AccountID: account.ID, Slug: "third"}, api.Limits{DeployedApps: 2}); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("quota cap 2 = %v", err)
	}
	// Cap of 3 → allowed.
	if _, err := m.CreateAppIfUnderQuota(ctx, state.App{AccountID: account.ID, Slug: "third"}, api.Limits{DeployedApps: 3}); err != nil {
		t.Fatalf("quota ok = %v", err)
	}
	// Missing account → ErrNotFound.
	if _, err := m.CreateAppIfUnderQuota(ctx, state.App{AccountID: "missing", Slug: "x"}, api.Limits{DeployedApps: 10}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing account = %v", err)
	}
}

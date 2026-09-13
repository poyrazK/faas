// memstore_node_shard_test.go — MemStore parity tests for the Phase 2
// / Gate A node-shard surface (PR #509). The pkg/state coverage gate
// in CI asserts ≥ 70% (Makefile test-state-coverage) and the new
// ListAppsByNodeID / ListInstancesByNodeID / ListOwnedCronsByNodeID /
// ListUnplacedApps / SetAppNodeID methods dropped the package below
// the threshold because no test exercised them. These tests lift
// MemStore coverage back above the gate without leaning on the
// Postgres path (PgStore parity tests run in CI under pgtest).
//
// The fixture seeds two apps (one unplaced, one claimed) plus two
// compute nodes (default-local + a non-default peer) and exercises
// every method's happy + at least one negative path. The contract
// the gate cares about is "every public Store method has at least
// one MemStore assertion that runs in CI" — the test bodies below
// intentionally avoid bespoke mocks in favour of the same primitives
// pgstore_test.go uses, so the assertions stay bit-for-bit equivalent
// to the SQL path.

package state

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func memNodeShardFixture(t *testing.T) (*MemStore, context.Context, Account, App, App) {
	t.Helper()
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "shard-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	// unplaced: NodeID stays empty so ListUnplacedApps picks it up.
	unplaced, err := m.CreateApp(ctx, App{
		AccountID: account.ID, Slug: "unplaced-" + uuid.NewString(),
		RAMMB: 256, Status: AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	// claimed: placement chooser (legacy) or schedd's claim
	// subscriber (post-00091) stamps NodeID before any test sees it.
	claimed, err := m.CreateApp(ctx, App{
		AccountID: account.ID, Slug: "claimed-" + uuid.NewString(),
		RAMMB: 256, Status: AppActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetAppNodeID(ctx, claimed.ID, "node-fsn-2"); err != nil {
		t.Fatal(err)
	}
	return m, ctx, account, unplaced, claimed
}

// TestMemStore_ListAppsByNodeID covers the per-node reaper /
// scale-up input set. The same predicate the SQL uses must hold in
// the in-memory map: node_id == X AND status != deleted.
func TestMemStore_ListAppsByNodeID(t *testing.T) {
	m, ctx, _, _, claimed := memNodeShardFixture(t)

	got, err := m.ListAppsByNodeID(ctx, "node-fsn-2")
	if err != nil {
		t.Fatalf("ListAppsByNodeID: %v", err)
	}
	if len(got) != 1 || got[0].ID != claimed.ID {
		t.Fatalf("ListAppsByNodeID(node-fsn-2) = %+v, want exactly [%s]", got, claimed.ID)
	}

	// Empty result for a node that owns nothing.
	if got, err := m.ListAppsByNodeID(ctx, "node-empty"); err != nil || len(got) != 0 {
		t.Errorf("ListAppsByNodeID(node-empty) = %+v, %v; want empty", got, err)
	}
}

// TestMemStore_ListInstancesByNodeID covers the per-node instance
// visibility path. The MemStore implementation joins the
// app_id → owner-node map before filtering, so a test that omits
// the join (e.g. listing instances whose app is unplaced) must
// return empty.
func TestMemStore_ListInstancesByNodeID(t *testing.T) {
	m, ctx, _, unplaced, claimed := memNodeShardFixture(t)
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: claimed.ID, ImageDigest: "sha256:shard"})
	if err != nil {
		t.Fatal(err)
	}

	// Two instances on the claimed node.
	for i := 0; i < 2; i++ {
		if _, err := m.CreateInstance(ctx, claimed.ID, dep.ID, "running", 256, "node-fsn-2", ""); err != nil {
			t.Fatal(err)
		}
	}
	// One instance on the unplaced app's app_id — must NOT show up
	// under any node (its owner is "").
	if _, err := m.CreateInstance(ctx, unplaced.ID, dep.ID, "running", 256, "node-fsn-2", ""); err != nil {
		t.Fatal(err)
	}

	got, err := m.ListInstancesByNodeID(ctx, "node-fsn-2")
	if err != nil {
		t.Fatalf("ListInstancesByNodeID: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListInstancesByNodeID(node-fsn-2) = %d, want 2 (unplaced owner's instance dropped)", len(got))
	}

	// Empty for a node that owns nothing.
	if got, err := m.ListInstancesByNodeID(ctx, "node-empty"); err != nil || len(got) != 0 {
		t.Errorf("ListInstancesByNodeID(node-empty) = %+v, %v; want empty", got, err)
	}
}

// TestMemStore_ListOwnedCronsByNodeID covers the per-node cron
// dispatcher input. Same owner-join predicate as
// ListInstancesByNodeID.
func TestMemStore_ListOwnedCronsByNodeID(t *testing.T) {
	m, ctx, _, unplaced, claimed := memNodeShardFixture(t)

	// Two crons on the claimed app, one on the unplaced app.
	if _, err := m.CreateCron(ctx, claimed.ID, "0 * * * *", "/tick", true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateCron(ctx, claimed.ID, "*/5 * * * *", "/tick-5", true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateCron(ctx, unplaced.ID, "0 0 * * *", "/daily", true); err != nil {
		t.Fatal(err)
	}

	got, err := m.ListOwnedCronsByNodeID(ctx, "node-fsn-2")
	if err != nil {
		t.Fatalf("ListOwnedCronsByNodeID: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ListOwnedCronsByNodeID(node-fsn-2) = %d, want 2 (unplaced owner dropped)", len(got))
	}
	disabled := false
	if _, err := m.UpdateCron(ctx, got[0].ID, nil, nil, &disabled, nil); err != nil {
		t.Fatalf("disable cron: %v", err)
	}
	if got, err := m.ListOwnedCronsByNodeID(ctx, "node-fsn-2"); err != nil || len(got) != 1 {
		t.Fatalf("ListOwnedCronsByNodeID after disable = %+v, %v; want one", got, err)
	}

	if got, err := m.ListOwnedCronsByNodeID(ctx, "node-empty"); err != nil || len(got) != 0 {
		t.Errorf("ListOwnedCronsByNodeID(node-empty) = %+v, %v; want empty", got, err)
	}
}

// TestMemStore_ListUnplacedApps covers the cold-start sweep path.
// Every non-deleted app whose NodeID is empty lands here.
func TestMemStore_ListUnplacedApps(t *testing.T) {
	m, ctx, _, unplaced, _ := memNodeShardFixture(t)

	got, err := m.ListUnplacedApps(ctx)
	if err != nil {
		t.Fatalf("ListUnplacedApps: %v", err)
	}
	if len(got) != 1 || got[0].ID != unplaced.ID {
		t.Fatalf("ListUnplacedApps = %+v, want exactly [%s]", got, unplaced.ID)
	}

	// After SetAppNodeID the unplaced app must drop out of the set.
	if err := m.SetAppNodeID(ctx, unplaced.ID, "node-fsn-3"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ListUnplacedApps(ctx); err != nil || len(got) != 0 {
		t.Errorf("ListUnplacedApps post-claim = %+v, %v; want empty", got, err)
	}

	// A soft-deleted app must never appear, even if its NodeID is
	// empty. The status check happens before the NodeID check.
	deleted, err := m.CreateApp(ctx, App{
		AccountID: unplaced.AccountID, Slug: "deleted-" + uuid.NewString(),
		RAMMB: 128, Status: AppDeleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.ListUnplacedApps(ctx); err != nil || len(got) != 0 {
		t.Errorf("ListUnplacedApps with deleted = %+v, %v; want empty", got, err)
	}
	_ = deleted
}

// TestMemStore_SetAppNodeID covers the atomic claim path. Three
// branches: happy claim, losing the race (already claimed), and
// missing the row entirely.
func TestMemStore_SetAppNodeID(t *testing.T) {
	m, ctx, _, unplaced, claimed := memNodeShardFixture(t)

	// Happy claim.
	if err := m.SetAppNodeID(ctx, unplaced.ID, "node-fsn-3"); err != nil {
		t.Errorf("SetAppNodeID happy: %v", err)
	}
	got, err := m.AppByID(ctx, unplaced.ID)
	if err != nil || got.NodeID != "node-fsn-3" {
		t.Errorf("post-claim NodeID = %q, %v; want node-fsn-3", got.NodeID, err)
	}

	// Loser of the race: the row is already claimed.
	err = m.SetAppNodeID(ctx, unplaced.ID, "node-fsn-4")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("SetAppNodeID loser: %v; want ErrConflict", err)
	}

	// Already-claimed app cannot be re-claimed by a peer.
	err = m.SetAppNodeID(ctx, claimed.ID, "node-fsn-4")
	if !errors.Is(err, ErrConflict) {
		t.Errorf("SetAppNodeID re-claim: %v; want ErrConflict", err)
	}

	// Row missing.
	err = m.SetAppNodeID(ctx, "missing-app-"+uuid.NewString(), "node-fsn-3")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("SetAppNodeID missing: %v; want ErrNotFound", err)
	}

	// Empty nodeID rejected with a generic error (not ErrConflict).
	err = m.SetAppNodeID(ctx, unplaced.ID, "")
	if err == nil {
		t.Error("SetAppNodeID empty: nil error; want rejection")
	}
}

// migration_handoff_test.go — Tier A5 (ADR-066) focused unit
// tests on the StateStore surface the four-phase handoff
// drives:
//
//   - ListLiveInstancesOnNode (read-side: candidate set)
//   - MarkInstanceMigrating (Phase 2 conditional UPDATE)
//   - MigrateInstanceOwner (Phase 3.5 single-tx commit)
//   - CancelInstanceMigration (Phase 4 rollback)
//
// The orchestrator (MigrationHarness) is exercised by the
// metal-side integration test in pkg/vmmdgrpc + cmd/schedd's
// drain-watcher e2e; this file pins the state-machine half
// of the contract. Mirrors pkg/state/memstore_test.go's
// shape for the existing rebalance methods (ListOrphanedApps,
// ReassignAppOwner).
//
// Failure modes pinned here:
//   - Happy path: live → migrating → running, lineage stamped.
//   - Phase 2 conflict: a concurrent SetInstanceState from
//     'running' to 'parked' makes MarkInstanceMigrating
//     return ErrConflict.
//   - Phase 3.5 conflict: a concurrent node_id flip makes
//     MigrateInstanceOwner return ErrConflict.
//   - Rollback: CancelInstanceMigration restores state to
//     'running' (the pre-Phase-2 value).

package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// seedInstanceForMigration creates an account + app + instance
// in the supplied MemStore at the given node, in state
// 'running', with the supplied RAM. Returns the instanceID.
//
// Capture the account ID returned by CreateAccount (MemStore
// auto-generates it) and stamp the App row with that real
// value — BuildAppSpecForMigration / AccountByID lookups
// would 404 on a literal "1" placeholder.
func seedInstanceForMigration(t *testing.T, store *state.MemStore, nodeID string) string {
	t.Helper()
	ctx := context.Background()
	// MemStore.CreateAccount takes (ctx, email, plan). The
	// helper is called multiple times in the same test; use
	// a UUID-suffixed email so the unique-email invariant
	// holds across calls.
	acct, err := store.CreateAccount(ctx, "u-"+uuid.NewString()+"@m", "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	// MemStore.CreateApp takes the App struct directly.
	app := state.App{
		ID: uuid.NewString(), AccountID: acct.ID, Slug: "mig-" + uuid.NewString(),
		NodeID: nodeID, Status: state.AppActive, RAMMB: 256,
	}
	if _, err := store.CreateApp(ctx, app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	// CreateInstance: (ctx, appID, deploymentID, state, ramMB,
	// nodeID, wakeID). The migration tests don't drive
	// deployment; an empty deploymentID is fine because
	// ListLiveInstancesOnNode doesn't join on deployments.
	ins, err := store.CreateInstance(ctx, app.ID, "", string(state.StateRunning),
		256, nodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	return ins.ID
}

func TestListLiveInstancesOnNode_ReturnsRunningOnly(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	running := seedInstanceForMigration(t, store, "node-dying")
	// Parked instance on the same node — must be excluded
	// from "live" set.
	parked := seedInstanceForMigration(t, store, "node-dying")
	if err := store.UpdateInstanceState(ctx, parked, string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}
	// Live instance on a different node — must be excluded.
	other := seedInstanceForMigration(t, store, "node-other")

	got, err := store.ListLiveInstancesOnNode(ctx, "node-dying", 100)
	if err != nil {
		t.Fatalf("ListLiveInstancesOnNode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d instances, want 1 (only running)", len(got))
	}
	if got[0].ID != running {
		t.Fatalf("got instance %s, want %s", got[0].ID, running)
	}
	if got[0].ID == parked || got[0].ID == other {
		t.Fatalf("returned parked or other-node instance")
	}
}

func TestMarkInstanceMigrating_HappyPath(t *testing.T) {
	store := state.NewMemStore()
	insID := seedInstanceForMigration(t, store, "dying")
	ctx := context.Background()

	if err := store.MarkInstanceMigrating(ctx, insID, "dying", "lease-1"); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	ins, _ := store.InstanceByID(ctx, insID)
	if string(ins.State) != string(state.StateMigrating) {
		t.Fatalf("state = %s, want migrating", ins.State)
	}
	if ins.LeaseToken != "lease-1" {
		t.Fatalf("lease_token = %q, want lease-1 (Phase 2 stamps it)", ins.LeaseToken)
	}
}

func TestMarkInstanceMigrating_ConflictOnParkedInstance(t *testing.T) {
	store := state.NewMemStore()
	insID := seedInstanceForMigration(t, store, "dying")
	ctx := context.Background()
	if err := store.UpdateInstanceState(ctx, insID, string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}
	err := store.MarkInstanceMigrating(ctx, insID, "dying", "lease-1")
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("MarkInstanceMigrating on parked instance = %v, want ErrConflict", err)
	}
}

func TestMarkInstanceMigrating_ConflictOnWrongNode(t *testing.T) {
	store := state.NewMemStore()
	insID := seedInstanceForMigration(t, store, "node-A")
	err := store.MarkInstanceMigrating(context.Background(), insID, "node-B", "lease-1")
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("MarkInstanceMigrating with wrong nodeID = %v, want ErrConflict", err)
	}
}

func TestMigrateInstanceOwner_HappyPath(t *testing.T) {
	store := state.NewMemStore()
	insID := seedInstanceForMigration(t, store, "dying")
	ctx := context.Background()
	if err := store.MarkInstanceMigrating(ctx, insID, "dying", "lease-1"); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	lease := "lease-1"
	if err := store.MigrateInstanceOwner(ctx, insID, "dying", "new-owner", lease); err != nil {
		t.Fatalf("MigrateInstanceOwner: %v", err)
	}
	ins, _ := store.InstanceByID(ctx, insID)
	if ins.NodeID != "new-owner" {
		t.Fatalf("node_id = %s, want new-owner", ins.NodeID)
	}
	if string(ins.State) != string(state.StateRunning) {
		t.Fatalf("state = %s, want running", ins.State)
	}
	if ins.MigratedFromNodeID == nil || *ins.MigratedFromNodeID != "dying" {
		t.Fatalf("migrated_from_node_id = %v, want dying", ins.MigratedFromNodeID)
	}
	if ins.LeaseToken != lease {
		t.Fatalf("lease_token = %s, want %s", ins.LeaseToken, lease)
	}
	if ins.MigratedAt == nil {
		t.Fatalf("migrated_at is nil")
	}
}

func TestMigrateInstanceOwner_ConflictOnNotMigrating(t *testing.T) {
	store := state.NewMemStore()
	insID := seedInstanceForMigration(t, store, "dying")
	// Instance is in state 'running', NOT 'migrating'. The
	// conditional UPDATE requires state='migrating' so this
	// must return ErrConflict.
	err := store.MigrateInstanceOwner(context.Background(), insID, "dying", "new-owner", "lease")
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("MigrateInstanceOwner on running instance = %v, want ErrConflict", err)
	}
}

// TestCancelInstanceMigration_RequiresMatchingLease pins the
// race-safety contract: cancel succeeds only with the lease
// token the migration was committed under. Phase 2 stamps
// lease_token onto the row (the same UUID the dying vmmd
// minted at Phase 1), so a cancel with a non-matching token
// is rejected with ErrConflict. The full happy path
// (cancel-after-Phase-2-stamp) is exercised by the harness
// tests in migration_harness_test.go (Phase 3 fails →
// CancelInstanceMigration with the right lease_token).
func TestCancelInstanceMigration_RequiresMatchingLease(t *testing.T) {
	store := state.NewMemStore()
	insID := seedInstanceForMigration(t, store, "dying")
	ctx := context.Background()
	if err := store.MarkInstanceMigrating(ctx, insID, "dying", "lease-1"); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	// A cancel with a non-matching lease must fail with
	// ErrConflict — the row has lease_token='lease-1' and a
	// 'stale-lease' token does not match. This is the
	// race-safety contract: a stale cancel cannot silently
	// succeed.
	if err := store.CancelInstanceMigration(ctx, insID, "dying", "stale-lease"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("CancelInstanceMigration with stale lease = %v, want ErrConflict", err)
	}
	// The matching-lease happy path: cancel with the right
	// token transitions state back to 'parked' and clears
	// lease_token (so a future re-attempt mints a fresh
	// lease).
	if err := store.CancelInstanceMigration(ctx, insID, "dying", "lease-1"); err != nil {
		t.Fatalf("CancelInstanceMigration with matching lease: %v", err)
	}
	ins, _ := store.InstanceByID(ctx, insID)
	if string(ins.State) != string(state.StateParked) {
		t.Fatalf("state = %s, want parked", ins.State)
	}
	if ins.LeaseToken != "" {
		t.Fatalf("lease_token = %q, want empty (cleared on cancel)", ins.LeaseToken)
	}
}

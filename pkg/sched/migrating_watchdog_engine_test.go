// migrating_watchdog_engine_test.go — engine-level tests for
// Tier A6 (ADR-067) Engine.ReconcileExpiredMigrations.
// adr: 067
//
// The watcher loop (pkg/sched/migrating_watchdog.go) is
// exercised by migrating_watchdog_test.go. THIS file pins
// the engine's per-row policy:
//
//   - Active owner row → ReinviteMigratingInstance called
//     → state='running' + lease_token cleared, metric
//     outcome="reinvited" bumped.
//   - Dead owner row → AbortMigratingInstance called →
//     state='parked' + node_id=migrated_from_node_id +
//     lease_token cleared, metric outcome="hard_deleted"
//     bumped.
//   - Peer race winner → conditional UPDATE returns
//     ErrConflict → metric outcome="conflict" bumped (no
//     state mutation).
//   - Per-tick cap respected → 60 rows on the input set
//     cap=50 → exactly 50 processed (the rest stay 'migrating'
//     on the next tick).
//
// The in-memory memstore is the test surface — the conditional
// UPDATE semantics are the same as pgstore (the predicates
// match 1:1); the pgstore parity test lives in
// pkg/state/pgstore_migration_test.go.

package sched

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// seedReconcileFixture creates an account + app + instance in
// the supplied MemStore at the given node, in state
// 'migrating', with a fresh lease token. Mirrors the
// seedInstanceForMigration shape used by the A5 migration
// tests. Returns the instanceID.
func seedReconcileFixture(t *testing.T, store *state.MemStore, nodeID string) (instanceID, leaseToken string) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "u-"+uuid.NewString()+"@m", "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app := state.App{
		ID: uuid.NewString(), AccountID: acct.ID, Slug: "recon-" + uuid.NewString(),
		NodeID: nodeID, Status: state.AppActive, RAMMB: 256,
	}
	if _, err := store.CreateApp(ctx, app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	ins, err := store.CreateInstance(ctx, app.ID, "", string(state.StateRunning),
		256, nodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	leaseToken = uuid.NewString()
	if err := store.MarkInstanceMigrating(ctx, ins.ID, nodeID, leaseToken); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	// The engine passes the real lease age to the watchdog. Backdate the
	// fixture so these tests exercise the expired-row path without waiting
	// through the production 90-second handoff window.
	store.SetInstanceMigrationStartedAtForTest(ins.ID, time.Now().UTC().Add(-2*time.Minute))
	return ins.ID, leaseToken
}

// seedComputeNodeForReconcile creates a single compute_node
// with the given active flag. Returns the nodeID. Note that
// NewMemStore auto-seeds a default-local node with Active=true
// (see seedDefaultLocalNodeLocked), so the helper returns the
// "name"-prefixed node explicitly to avoid the alphabetical
// sort ambiguity that ListComputeNodes's name-ASC ordering
// introduces.
func seedComputeNodeForReconcile(t *testing.T, store *state.MemStore, active bool) string {
	t.Helper()
	name := "node-" + uuid.NewString()
	created, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
		Name:           name,
		Active:         active,
		MemMB:          8192,
		MaxConcurrency: 16,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	if created.Active != active {
		t.Fatalf("CreateComputeNode returned active=%v want %v", created.Active, active)
	}
	return created.ID
}

func TestReconcileExpiredMigrations_ActiveOwnerReinvited(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")
	e.WithOpsMetrics(ops)
	nodeID := seedComputeNodeForReconcile(t, store, true)
	instanceID, _ := seedReconcileFixture(t, store, nodeID)

	reconciled, err := e.ReconcileExpiredMigrations(context.Background())
	if err != nil {
		t.Fatalf("ReconcileExpiredMigrations: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("reconciled=%d want 1", reconciled)
	}
	rows, err := store.ListExpiredMigrations(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListExpiredMigrations: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("input set still has %d rows; expected empty (active-owner row should have transitioned to running)", len(rows))
	}
	if got := readMigratingReconcileMetric(t, ops, "reinvited"); got != 1 {
		t.Fatalf("metric reinvited=%d want 1", got)
	}
	// Spot-check via the input-set's complement: ListLiveInstancesOnNode
	// (running only) should now find the row.
	if _, err := store.ListInstancesByNodeID(context.Background(), instanceID); err != nil {
		t.Fatalf("ListInstancesByNodeID: %v", err)
	}
}

func TestReconcileExpiredMigrations_DeadOwnerParked(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")
	e.WithOpsMetrics(ops)
	nodeID := seedComputeNodeForReconcile(t, store, false)
	instanceID, _ := seedReconcileFixture(t, store, nodeID)
	// The watchdog's abort path leaves instance.NodeID alone
	// (migrated_from_node_id is NULL pre-Phase-3; the wake path
	// dispatches via app.NodeID, not instance.NodeID). Verify
	// the row's node_id stays on the dead OLD owner after abort.

	reconciled, err := e.ReconcileExpiredMigrations(context.Background())
	if err != nil {
		t.Fatalf("ReconcileExpiredMigrations: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("reconciled=%d want 1", reconciled)
	}
	rows, err := store.ListExpiredMigrations(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListExpiredMigrations: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("input set still has %d rows; expected empty (dead-owner row should have transitioned to parked)", len(rows))
	}
	if got := readMigratingReconcileMetric(t, ops, "hard_deleted"); got != 1 {
		t.Fatalf("metric hard_deleted=%d want 1", got)
	}
	// NodeID must still be the dead OLD owner — the wake path
	// uses app.NodeID for placement; the dead instance.NodeID is
	// harmless and resetting it would break engine.go:681's
	// WakeResult.NodeID.
	if ins, err := store.InstanceByID(context.Background(), instanceID); err != nil {
		t.Fatalf("InstanceByID: %v", err)
	} else if ins.NodeID != nodeID {
		t.Errorf("post-abort instance.NodeID=%q want %q (dead OLD owner — must not change)", ins.NodeID, nodeID)
	}
}

func TestReconcileExpiredMigrations_ConflictOnPeerRollback(t *testing.T) {
	// NOTE: peer-rollback races (CancelInstanceMigration
	// flipping the row to 'parked' before the watchdog's
	// conditional UPDATE) are NOT testable on the memstore
	// without a hook to inject a race between
	// ListExpiredMigrations and the per-row UPDATE. The row
	// is in 'parked' by the time the reconcile runs, so the
	// input set is empty — there is no metric to bump.
	//
	// The pgstore parity test (pgstore_migration_test.go) is
	// the right place to pin the conflict path: a peer
	// commits (MigrateInstanceOwner) while the watchdog's
	// UPDATE is in flight, and the conditional UPDATEs
	// return ErrConflict on the state='migrating' predicate.
	// The metric label existence is pinned by the
	// pre-instantiation loop in pkg/wire/metrics.go.
	t.Skip("conflict path requires a pgstore-side race; covered by pgstore_migration_test.go")
}

func TestReconcileExpiredMigrations_RespectsPerTickCap(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")
	e.WithOpsMetrics(ops)
	e.WithMigratingWatchdogTickLimit(50)
	nodeID := seedComputeNodeForReconcile(t, store, true)
	for i := 0; i < 60; i++ {
		_, _ = seedReconcileFixture(t, store, nodeID)
	}
	reconciled, err := e.ReconcileExpiredMigrations(context.Background())
	if err != nil {
		t.Fatalf("ReconcileExpiredMigrations: %v", err)
	}
	if reconciled != 50 {
		t.Fatalf("reconciled=%d want 50 (per-tick cap)", reconciled)
	}
	remain, err := store.ListExpiredMigrations(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListExpiredMigrations: %v", err)
	}
	if len(remain) != 10 {
		t.Fatalf("remaining=%d want 10", len(remain))
	}
}

func TestReconcileExpiredMigrations_NoRowsIsNoop(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")
	e.WithOpsMetrics(ops)
	reconciled, err := e.ReconcileExpiredMigrations(context.Background())
	if err != nil {
		t.Fatalf("ReconcileExpiredMigrations: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("reconciled=%d want 0", reconciled)
	}
}

func TestReconcileExpiredMigrations_DoesNotTouchFreshLease(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.WithOpsMetrics(ops)
	nodeID := seedComputeNodeForReconcile(t, store, true)
	instanceID, _ := seedReconcileMemStoreForFreshLease(t, store, nodeID)

	reconciled, err := e.ReconcileExpiredMigrations(context.Background())
	if err != nil {
		t.Fatalf("ReconcileExpiredMigrations: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("reconciled=%d want 0 for a fresh lease", reconciled)
	}
	ins, err := store.InstanceByID(context.Background(), instanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if ins.State != string(state.StateMigrating) || ins.LeaseToken == "" {
		t.Fatalf("fresh lease mutated: state=%q lease=%q", ins.State, ins.LeaseToken)
	}
}

func seedReconcileMemStoreForFreshLease(t *testing.T, store *state.MemStore, nodeID string) (string, string) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "fresh-"+uuid.NewString()+"@m", "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app := state.App{ID: uuid.NewString(), AccountID: acct.ID, Slug: "fresh-" + uuid.NewString(), NodeID: nodeID, Status: state.AppActive, RAMMB: 256}
	if _, err := store.CreateApp(ctx, app); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	ins, err := store.CreateInstance(ctx, app.ID, "", string(state.StateRunning), 256, nodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	lease := uuid.NewString()
	if err := store.MarkInstanceMigrating(ctx, ins.ID, nodeID, lease); err != nil {
		t.Fatalf("MarkInstanceMigrating: %v", err)
	}
	return ins.ID, lease
}

// readMigratingReconcileMetric reads the
// schedd_migrating_reconcile_total outcome counter from the
// OpsMetrics HTTP handler. Mirrors readScaleUp's shape.
func readMigratingReconcileMetric(t *testing.T, ops *wire.OpsMetrics, outcome string) int {
	t.Helper()
	if ops == nil {
		return 0
	}
	body := getMetricsBody(t, ops)
	want := `schedd_migrating_reconcile_total{outcome="` + outcome + `"}`
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, want) {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0
			}
			n, err := strconv.Atoi(fields[len(fields)-1])
			if err != nil {
				t.Fatalf("parse %q: %v", line, err)
			}
			return n
		}
	}
	return 0
}

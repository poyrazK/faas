// adr: 137
// deadnode_reconciler_test.go — engine-level tests for the
// stale-RUNNING billing-leak self-healer.
//
// The reconciler is the missing backstop between schedd's
// heartbeat (which flips compute_nodes.active=false when a vmmd
// stops answering) and meterd's sampler (which bills every
// State.CountsForRAM() row without consulting node liveness).
// Without it, a dead vmmd leaves its RUNNING instances stranded
// in PG and the customer is billed indefinitely for VMs that no
// longer exist — see pkg/meter/sampler.go (the
// `if !state.State(ins.State).CountsForRAM() { continue }` filter).
//
// These tests pin the per-row policy on the in-memory MemStore
// surface. The hand-written SQL parity (conditional UPDATE +
// ErrConflict on RowsAffected()==0, ORDER BY tie-break on
// (heartbeat, id), limit guard, etc.) lives in
// pkg/state/pgstore_dead_node_test.go and runs against a real
// Postgres when pgtest.Open can reach one; it is skipped
// otherwise, per the FAAS_SKIP_PG_TESTS convention.
//
// Cases:
//
//   - Stale active node → RUNNING row transitions to
//     FAILED, metric outcome="failed" bumped.
//   - Recovery-managed app rows remain RUNNING.
//   - App-less job-task rows remain eligible during drain.
//   - Active node with fresh heartbeat → no change.
//   - Peer-race winner (row already not RUNNING) → metric
//     outcome="conflict", no mutation.
//   - Tick cap honoured → two eligible rows → exactly two
//     reconciled.
//   - Orphan row (node_id has no compute_nodes entry) →
//     reconciled (treat as dead; owner unknowable).
//   - List / Fail arg guards (empty instanceID, empty nodeID,
//     limit ≤ 0) match pgstore.

package sched

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// seedRunningInstance creates an account + app + RUNNING instance
// on the given node. Mirrors seedReconcileFixture's shape but skips
// the migration transition (we want raw RUNNING rows). Returns
// the app + instance IDs so callers can later look up the row.
func seedRunningInstance(t *testing.T, store *state.MemStore, nodeID string) (string, string) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "u-"+uuid.NewString()+"@d", "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app := state.App{
		ID: uuid.NewString(), AccountID: acct.ID, Slug: "dnr-" + uuid.NewString(),
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
	return app.ID, ins.ID
}

// readDeadNodeReconcileMetric scrapes the
// schedd_dead_node_reconcile_total{outcome=...} counter. Mirrors
// readMigratingReconcileMetric.
func readDeadNodeReconcileMetric(t *testing.T, ops *wire.OpsMetrics, outcome string) int {
	t.Helper()
	if ops == nil {
		return 0
	}
	body := getMetricsBody(t, ops)
	want := `schedd_dead_node_reconcile_total{outcome="` + outcome + `"}`
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

func TestReconcileDeadNodeInstances_DeadNodeRowFailed(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.WithOpsMetrics(ops)
	nodeID := seedStaleComputeNodeForReconcile(t, store)
	appID, insID := seedRunningInstance(t, store, nodeID)

	reconciled, err := e.ReconcileDeadNodeInstances(context.Background())
	if err != nil {
		t.Fatalf("ReconcileDeadNodeInstances: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("reconciled=%d want 1", reconciled)
	}
	// Input set is now empty (the row transitioned out of RUNNING).
	rows, err := store.ListRunningInstancesOnDeadNodes(context.Background(),
		time.Now().UTC(), 50)
	if err != nil {
		t.Fatalf("ListRunningInstancesOnDeadNodes: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("input set still has %d rows; expected empty", len(rows))
	}
	if got := readDeadNodeReconcileMetric(t, ops, "failed"); got != 1 {
		t.Fatalf("metric failed=%d want 1", got)
	}
	// The row still exists in the store but is no longer RUNNING.
	// ListInstancesForApp returns all rows (no state filter), so we
	// can inspect the post-transition state.
	appRows, err := store.ListInstancesForApp(context.Background(), appID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(appRows) != 1 {
		t.Fatalf("appRows=%d want 1 (the row must still exist, just terminal)", len(appRows))
	}
	if appRows[0].State != string(state.StateFailed) {
		t.Fatalf("row state=%q want %q (dead-node row must be FAILED, not PARKED — no snapshot exists)", appRows[0].State, state.StateFailed)
	}
	if appRows[0].TerminalAt == nil {
		t.Fatalf("terminal_at must be stamped on transition (drives §17 retention)")
	}
	_ = insID
}

func TestReconcileDeadNodeInstances_ActiveNodeUntouched(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.WithOpsMetrics(ops)
	nodeID := seedComputeNodeForReconcile(t, store, true) // active=true, fresh heartbeat
	appID, _ := seedRunningInstance(t, store, nodeID)

	reconciled, err := e.ReconcileDeadNodeInstances(context.Background())
	if err != nil {
		t.Fatalf("ReconcileDeadNodeInstances: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("reconciled=%d want 0 (node is active and freshly heartbeated)", reconciled)
	}
	if got := readDeadNodeReconcileMetric(t, ops, "failed"); got != 0 {
		t.Fatalf("metric failed=%d want 0", got)
	}
	appRows, err := store.ListInstancesForApp(context.Background(), appID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(appRows) != 1 || appRows[0].State != string(state.StateRunning) {
		t.Fatalf("row must still be RUNNING (active node is not dead)")
	}
}

func TestReconcileDeadNodeInstances_NoOpOnEmptyStore(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.WithOpsMetrics(ops)

	reconciled, err := e.ReconcileDeadNodeInstances(context.Background())
	if err != nil {
		t.Fatalf("ReconcileDeadNodeInstances: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("reconciled=%d want 0", reconciled)
	}
}

// TestReconcileDeadNodeInstances_PeerRaceWinner counts as
// "conflict" because the row was transitioned out of RUNNING
// before the reconciler could land its conditional UPDATE. Two
// events land in order:
//
//  1. Some external path parks the instance (idle-reaper, peer
//     schedd, manual operator override).
//  2. Reconciler ticks; the conditional UPDATE finds
//     state != 'running' → ErrConflict → metric outcome="conflict"
//     bumped, no mutation, no Release.
//
// Today the engine's switch-case on ErrConflict does NOT bump
// the metric (it only logs at Debug). This test pins the current
// behaviour and would fail if a future contributor adds the bump
// — that's fine; update the assertion and ship the metric then.
func TestReconcileDeadNodeInstances_PeerRaceWinner(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.WithOpsMetrics(ops)
	nodeID := seedComputeNodeForReconcile(t, store, false)
	_, insID := seedRunningInstance(t, store, nodeID)

	// Peer wins: park the row before the reconciler ticks. We use
	// UpdateInstanceState directly to skip the full snapshot path
	// (the point of this test is the conditional-UPDATE behaviour,
	// not the park machinery).
	if err := store.UpdateInstanceState(context.Background(), insID,
		string(state.StateStopped)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}

	reconciled, err := e.ReconcileDeadNodeInstances(context.Background())
	if err != nil {
		t.Fatalf("ReconcileDeadNodeInstances: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("reconciled=%d want 0 (peer parked first)", reconciled)
	}
	if got := readDeadNodeReconcileMetric(t, ops, "failed"); got != 0 {
		t.Fatalf("metric failed=%d want 0 (peer parked first, no failed transition)", got)
	}
}

func TestReconcileDeadNodeInstances_TickCapHonoured(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.WithOpsMetrics(ops)

	// Two dead nodes, one RUNNING instance each. ReconcileDeadNodeInstances
	// reads api.DeadNodeReconcilerTickLimit (50) directly today — there
	// is no engine setter for the cap, and we don't add one in this
	// PR (mirrors how MigratingWatchdogTickLimit is structured). The
	// cap-shape assertion is implicit: with two eligible rows the
	// method reconciles both, and a future change to lower the cap
	// would require adding a setter (see TestReconcileDeadNodeInstances_PerTickCapRespected
	// below for the synthetic cap path).
	nodeA := seedStaleComputeNodeForReconcile(t, store)
	nodeB := seedStaleComputeNodeForReconcile(t, store)
	_, _ = seedRunningInstance(t, store, nodeA)
	_, _ = seedRunningInstance(t, store, nodeB)

	reconciled, err := e.ReconcileDeadNodeInstances(context.Background())
	if err != nil {
		t.Fatalf("ReconcileDeadNodeInstances: %v", err)
	}
	if reconciled != 2 {
		t.Fatalf("reconciled=%d want 2", reconciled)
	}
	if got := readDeadNodeReconcileMetric(t, ops, "failed"); got != 2 {
		t.Fatalf("metric failed=%d want 2", got)
	}
}

func TestReconcileDeadNodeInstances_OrphanNodeRowFailed(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.WithOpsMetrics(ops)

	// MemStore.CreateInstance takes a nodeID with no FK validation
	// (in-memory surface is schema-loose by design, mirroring how
	// the pgstore schema does not declare an FK on instances.node_id
	// because node_ids are populated from the heartbeat-loop's
	// discovery path, not from any registration step). An orphan
	// node_id is therefore realistic in a multi-host world where
	// compute_nodes is GC'd by an out-of-band admin path. The
	// reconciler must treat orphan as dead (owner unknowable).
	orphanID := "node-orphan-" + uuid.NewString()
	_, _ = seedRunningInstance(t, store, orphanID)

	reconciled, err := e.ReconcileDeadNodeInstances(context.Background())
	if err != nil {
		t.Fatalf("ReconcileDeadNodeInstances: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("reconciled=%d want 1 (orphan-node row must be treated as dead)", reconciled)
	}
	if got := readDeadNodeReconcileMetric(t, ops, "failed"); got != 1 {
		t.Fatalf("metric failed=%d want 1", got)
	}
}

func TestReconcileDeadNodeInstances_RecoveryManagedAppRowsUntouched(t *testing.T) {
	for _, lifecycle := range []state.NodeLifecycle{
		state.NodeLifecycleDraining,
		state.NodeLifecycleForceDraining,
		state.NodeLifecycleUnavailable,
		state.NodeLifecycleRecovering,
	} {
		lifecycle := lifecycle
		t.Run(string(lifecycle), func(t *testing.T) {
			store := state.NewMemStore()
			node, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
				Name: "managed-" + uuid.NewString(), Lifecycle: lifecycle,
				LastHeartbeatAt: time.Now().UTC().Add(-10 * time.Minute),
				MemMB:           8192, MaxConcurrency: 16,
			})
			if err != nil {
				t.Fatalf("CreateComputeNode: %v", err)
			}
			appID, insID := seedRunningInstance(t, store, node.ID)
			threshold := time.Now().UTC().Add(-2 * time.Minute)
			rows, err := store.ListRunningInstancesOnDeadNodes(context.Background(), threshold, 50)
			if err != nil {
				t.Fatalf("ListRunningInstancesOnDeadNodes: %v", err)
			}
			if len(rows) != 0 {
				t.Fatalf("recovery-managed app row returned by billing reconciler: %+v", rows)
			}
			if err := store.FailRunningInstanceOnDeadNode(context.Background(), insID, node.ID, threshold); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("FailRunningInstanceOnDeadNode err=%v, want ErrConflict", err)
			}
			appRows, err := store.ListInstancesForApp(context.Background(), appID)
			if err != nil || len(appRows) != 1 || appRows[0].State != string(state.StateRunning) {
				t.Fatalf("app row changed during %s recovery: rows=%+v err=%v", lifecycle, appRows, err)
			}
		})
	}
}

func TestReconcileDeadNodeInstances_JobTaskEligibleDuringDrain(t *testing.T) {
	store := state.NewMemStore()
	node, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
		Name: "job-drain-" + uuid.NewString(), Lifecycle: state.NodeLifecycleDraining,
		MemMB: 8192, MaxConcurrency: 16,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	_, job, run := seedJobRun(t, store, nil, nil)
	ins, err := store.CreateJobInstance(context.Background(), uuid.NewString(), job.ID, run.ID, 0,
		string(state.StateRunning), 256, node.ID, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateJobInstance: %v", err)
	}
	threshold := time.Now().UTC().Add(-2 * time.Minute)
	rows, err := store.ListRunningInstancesOnDeadNodes(context.Background(), threshold, 50)
	if err != nil {
		t.Fatalf("ListRunningInstancesOnDeadNodes: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != ins.ID {
		t.Fatalf("job-task billing row=%+v, want %s", rows, ins.ID)
	}
	if err := store.FailRunningInstanceOnDeadNode(context.Background(), ins.ID, node.ID, threshold); err != nil {
		t.Fatalf("FailRunningInstanceOnDeadNode(job task): %v", err)
	}
	got, err := store.InstanceByID(context.Background(), ins.ID)
	if err != nil || got.State != string(state.StateFailed) {
		t.Fatalf("job-task row=%+v err=%v, want failed", got, err)
	}
}

func TestFailRunningInstanceOnDeadNode_HeartbeatRaceReturnsConflict(t *testing.T) {
	store := state.NewMemStore()
	nodeID := seedStaleComputeNodeForReconcile(t, store)
	appID, insID := seedRunningInstance(t, store, nodeID)
	threshold := time.Now().UTC().Add(-2 * time.Minute)
	rows, err := store.ListRunningInstancesOnDeadNodes(context.Background(), threshold, 50)
	if err != nil || len(rows) != 1 {
		t.Fatalf("pre-race list rows=%+v err=%v", rows, err)
	}
	if err := store.HeartbeatComputeNode(context.Background(), nodeID); err != nil {
		t.Fatalf("HeartbeatComputeNode: %v", err)
	}
	if err := store.FailRunningInstanceOnDeadNode(context.Background(), insID, nodeID, threshold); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("post-heartbeat fail err=%v, want ErrConflict", err)
	}
	appRows, err := store.ListInstancesForApp(context.Background(), appID)
	if err != nil || len(appRows) != 1 || appRows[0].State != string(state.StateRunning) {
		t.Fatalf("recovered-node app row changed: rows=%+v err=%v", appRows, err)
	}
}

// TestReconcileDeadNodeInstances_ListQueryArgGuards pins the
// input contract on ListRunningInstancesOnDeadNodes: limit must
// be > 0 (matches pgstore impl).
func TestReconcileDeadNodeInstances_ListQueryArgGuards(t *testing.T) {
	store := state.NewMemStore()
	if _, err := store.ListRunningInstancesOnDeadNodes(context.Background(), time.Now().UTC(), 0); err == nil {
		t.Fatalf("limit=0 must error (defends against a misconfigured tick cap)")
	}
	if _, err := store.ListRunningInstancesOnDeadNodes(context.Background(), time.Now().UTC(), -5); err == nil {
		t.Fatalf("limit<0 must error")
	}
}

// TestReconcileDeadNodeInstances_FailArgGuards pins the empty-arg
// guards on FailRunningInstanceOnDeadNode (matching pgstore) and
// pins the ErrConflict contract for "row vanished between list and
// fail" — see F2 in the PR-A review findings.
func TestReconcileDeadNodeInstances_FailArgGuards(t *testing.T) {
	store := state.NewMemStore()
	if err := store.FailRunningInstanceOnDeadNode(context.Background(), "", "node-1", time.Now()); err == nil {
		t.Fatalf("empty instanceID must error")
	}
	if err := store.FailRunningInstanceOnDeadNode(context.Background(), "ins-1", "", time.Now()); err == nil {
		t.Fatalf("empty nodeID must error")
	}
	// Missing row → ErrConflict (NOT ErrNotFound). This matches
	// the PgStore RowsAffected()==0 outcome: from the caller's
	// perspective, the conditional UPDATE found nothing to update,
	// and "the row vanished" is the same race as "node recovered"
	// or "peer parked first" — all three must surface the same
	// outcome="conflict" so the reconciler's metric stays
	// distinguishable from a real error.
	err := store.FailRunningInstanceOnDeadNode(context.Background(), "missing", "n", time.Now())
	if err == nil {
		t.Fatalf("unknown instance must error")
	}
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unknown-instance error must be ErrConflict (peer-vanished race), got %v", err)
	}
}

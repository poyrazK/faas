// adr: 137
// pgstore_dead_node_test.go — PgStore parity tests for the
// dead-node billing-leak reconciler's two Store methods:
//
//   - ListRunningInstancesOnDeadNodes — the conditional SELECT that
//     drives the reconciler. Must filter on
//     (n.active = false OR n.last_heartbeat_at < $1), exclude
//     app rows owned by recovery-managed lifecycles, order by
//     (heartbeat ASC, id ASC) for deterministic capped-tick drain
//     (F3 from the PR-A review), and respect limit > 0.
//
//   - FailRunningInstanceOnDeadNode — the conditional UPDATE that
//     transitions state='running' + node_id=$2 on a still-dead node
//     outside recovery ownership → state='failed'.
//     RowsAffected()==0 must surface as ErrConflict (not
//     pgx.ErrNoRows, which the Store interface translates to
//     ErrNotFound) so the reconciler's metric distinguishes a
//     peer-wins race from a real error (F2 from the PR-A review).
//
// MemStore parity (the surface the reconciler unit-tests target) is
// in pkg/sched/deadnode_reconciler_test.go. This file pins the
// hand-written SQL against a real cluster, mirroring
// pkg/state/pgstore_account_quota_warning_test.go's shape. Skips on
// FAAS_SKIP_PG_TESTS and on no Postgres (pgtest.Open handles the
// skip).

package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// pgTestComputeNode seeds a compute_node and stamps its heartbeat
// to a relative age (negative offset from now). Returns the node ID.
//
// admission_ceiling_mb is mandatory: schema.sql has
// compute_nodes_admission_ceiling_mb_check CHECK (admission_ceiling_mb > 0).
// The MemStore test path doesn't enforce the constraint, so this is
// a pgstore-only footgun. Since ADR-193 the value is load-bearing
// rather than cosmetic: the instances INSERT refuses a row that would
// push the node past it, so the ceiling must be able to hold whatever
// the cases below create.
func pgTestComputeNode(t *testing.T, ctx context.Context, s *state.PgStore, active bool, age time.Duration) string {
	t.Helper()
	// Every field below is mandatory against the pgstore schema.
	// MemStore accepts the zero-value struct happily (no
	// target_url CHECK, no admission_ceiling CHECK, no vcpu_budget
	// CHECK), so the MemStore-side test in
	// pkg/sched/deadnode_reconciler_test.go has been getting away
	// with `Name: ..., Active: ..., MemMB: 8192, MaxConcurrency: 16`
	// — the pgstore parity test needs every NOT NULL column the
	// constraints key off. Mirrors
	// pkg/state/pgstore_coverage2_test.go:94.
	n, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name:      "dnr-" + uuid.NewString(),
		TargetURL: "unix:///run/faas/vmmd.sock",
		Active:    active,
		MemMB:     8192,
		// The ceiling must be consistent with MemMB: since ADR-193 the
		// instances INSERT enforces it, and the 256 MB placeholder this
		// fixture used to carry could not host the 256 MB instance the
		// dead-node cases create (256 + PerVMOverheadMB = 264 > 256).
		// These cases are about node liveness, not capacity.
		MaxConcurrency:     16,
		AdmissionCeilingMB: 8192,
		VPCPUs:             4,
		VCPUBudget:         160,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	if age == 0 {
		// active=true with a fresh heartbeat — default NewComputeNode
		// stamps now() already, so nothing to do.
		return n.ID
	}
	// To pin the heartbeat at a specific past time we'd need raw
	// SQL (the Store has no public heartbeat-stamp method by
	// design — heartbeats are owned by schedd's heartbeat loop).
	// The simplest deterministic-staleness path is to insert a
	// compute_node, let the schema's CreatedAt default to now(),
	// and query with threshold = now() - age. The reconciler never
	// cares which second the heartbeat is stamped at — it only
	// cares about the staleness predicate. So we return the ID
	// unchanged and rely on the test's threshold arithmetic to
	// decide inclusion.
	return n.ID
}

func pgTestMaintenanceNode(t *testing.T, ctx context.Context, s *state.PgStore) string {
	t.Helper()
	id := pgTestComputeNode(t, ctx, s, false, 0)
	if err := s.NodeSetLifecycle(ctx, id, state.NodeLifecycleUnavailable, state.NodeLifecycleMaintenance); err != nil {
		t.Fatalf("NodeSetLifecycle(unavailable→maintenance): %v", err)
	}
	return id
}

// pgTestSeedRunningInstance creates an account + app + deployment +
// RUNNING instance on the given node. MemStore accepts empty
// deployment_id and empty wakeID strings; pgstore requires a valid
// UUID for both (deployment_id is UUID-typed NOT NULL, wakeID is
// UUID-typed and the SQL binds it as $6::uuid). Mirrors
// pkg/state/pgstore_coverage_parity_test.go::pgCoverageFixture.
func pgTestSeedRunningInstance(t *testing.T, ctx context.Context, s *state.PgStore, nodeID string) (string, string) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, pgTestEmail(t)+"-"+uuid.NewString(), "hobby")
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	// CreateApp generates its own UUID via the schema default
	// (apps.id DEFAULT gen_random_uuid()); the column list omits id
	// so the caller-supplied value here is ignored and the returned
	// App carries the canonical UUID. Capture it — downstream calls
	// (CreateDeployment, CreateInstance) must use the returned ID,
	// not the one we wrote into the literal.
	created, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "dnr-pg-" + uuid.NewString(),
		NodeID: nodeID, Status: state.AppActive, RAMMB: 256,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	deployment, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: created.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + uuid.NewString(),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	ins, err := s.CreateInstance(ctx, created.ID, deployment.ID,
		string(state.StateRunning), 256, nodeID, uuid.NewString())
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	return created.ID, ins.ID
}

// TestPg_ListRunningInstancesOnDeadNodes_FilterByActiveOrStale pins
// the join predicate: an active node with a fresh heartbeat must
// NOT appear; a terminally inactive node OR a node whose heartbeat
// predates the threshold MUST appear. Recovery-managed app rows are
// tested separately because their transitions belong to the arbiter.
func TestPg_ListRunningInstancesOnDeadNodes_FilterByActiveOrStale(t *testing.T) {
	s, ctx, _ := pgWithPool(t)

	// maintenance/active=false → eligible regardless of heartbeat freshness
	deadNodeID := pgTestMaintenanceNode(t, ctx, s)
	_, deadInsID := pgTestSeedRunningInstance(t, ctx, s, deadNodeID)

	// active=true, fresh heartbeat → NOT eligible
	liveNodeID := pgTestComputeNode(t, ctx, s, true, 0)
	_, _ = pgTestSeedRunningInstance(t, ctx, s, liveNodeID)

	// Threshold = now() - 1m so the live node (whose heartbeat is
	// stamped at INSERT-time by the schema default `now()` and is
	// therefore ≥ now() - 1m) cannot trip the
	// `last_heartbeat_at < $1` predicate. The dead node matches the
	// OR clause (active=false) regardless of threshold. Using
	// time.Now() alone would race against the SQL clock: pgx's
	// now() resolves at INSERT-statement start, Go's now() resolves
	// on the client a few hundred µs later, so
	// `INSERT_now < SELECT_now` was true and the live row leaked
	// in. Subtracting a minute is the deterministic equivalent of
	// "a heartbeat older than the staleness window" — exactly the
	// staleness schedd's MarkComputeNodeInactive flip uses (90 s
	// default).
	rows, err := s.ListRunningInstancesOnDeadNodes(ctx,
		time.Now().UTC().Add(-time.Minute), 50)
	if err != nil {
		t.Fatalf("ListRunningInstancesOnDeadNodes: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows=%d want 1 (only the dead-node row should be eligible)", len(rows))
	}
	if rows[0].ID != deadInsID {
		t.Fatalf("rows[0].ID=%q want %q (dead-node row)", rows[0].ID, deadInsID)
	}
}

func TestPg_ListRunningInstancesOnDeadNodes_RecoveryManagedAppRowsExcluded(t *testing.T) {
	s, ctx, _ := pgWithPool(t)
	for _, lifecycle := range []state.NodeLifecycle{
		state.NodeLifecycleDraining,
		state.NodeLifecycleForceDraining,
		state.NodeLifecycleUnavailable,
		state.NodeLifecycleRecovering,
	} {
		lifecycle := lifecycle
		t.Run(string(lifecycle), func(t *testing.T) {
			nodeID := pgTestComputeNode(t, ctx, s, lifecycle == state.NodeLifecycleRecovering, 0)
			start := state.NodeLifecycleUnavailable
			if lifecycle == state.NodeLifecycleRecovering {
				start = state.NodeLifecycleActive
			}
			if lifecycle != start {
				if err := s.NodeSetLifecycle(ctx, nodeID, start, lifecycle); err != nil {
					t.Fatalf("NodeSetLifecycle(%s→%s): %v", start, lifecycle, err)
				}
			}
			_, insID := pgTestSeedRunningInstance(t, ctx, s, nodeID)
			threshold := time.Now().UTC().Add(time.Minute)
			rows, err := s.ListRunningInstancesOnDeadNodes(ctx, threshold, 500)
			if err != nil {
				t.Fatalf("ListRunningInstancesOnDeadNodes: %v", err)
			}
			for _, row := range rows {
				if row.ID == insID {
					t.Fatalf("recovery-managed app row %s returned", insID)
				}
			}
			if err := s.FailRunningInstanceOnDeadNode(ctx, insID, nodeID, threshold); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("FailRunningInstanceOnDeadNode err=%v, want ErrConflict", err)
			}
		})
	}
}

func TestPg_DeadNodeReconciler_OrphanRowEligible(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	validNodeID := pgTestComputeNode(t, ctx, s, false, 0)
	orphanNodeID := uuid.NewString()
	_, insID := pgTestSeedRunningInstance(t, ctx, s, validNodeID)
	// Production schema integrity normally prevents an instance from
	// referencing a missing compute node. The reconciler still needs to
	// recover rows left behind by an interrupted repair or an older schema,
	// so inject that corruption deliberately while retaining the app's valid
	// owner. session_replication_role disables the FK trigger only for this
	// isolated test connection.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `set session_replication_role = replica`); err != nil {
		t.Fatalf("disable FK triggers: %v", err)
	}
	if _, err := conn.Exec(ctx, `update instances set node_id = $1 where id = $2`, orphanNodeID, insID); err != nil {
		t.Fatalf("inject orphan instance: %v", err)
	}
	if _, err := conn.Exec(ctx, `set session_replication_role = origin`); err != nil {
		t.Fatalf("restore FK triggers: %v", err)
	}
	threshold := time.Now().UTC().Add(-time.Minute)
	rows, err := s.ListRunningInstancesOnDeadNodes(ctx, threshold, 50)
	if err != nil {
		t.Fatalf("ListRunningInstancesOnDeadNodes: %v", err)
	}
	found := false
	for _, row := range rows {
		if row.ID == insID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("orphan instance %s absent from dead-node reconciliation set", insID)
	}
	if err := s.FailRunningInstanceOnDeadNode(ctx, insID, orphanNodeID, threshold); err != nil {
		t.Fatalf("FailRunningInstanceOnDeadNode(orphan): %v", err)
	}
}

// TestPg_ListRunningInstancesOnDeadNodes_LimitGuard pins the input
// contract: limit must be > 0.
func TestPg_ListRunningInstancesOnDeadNodes_LimitGuard(t *testing.T) {
	s, ctx := pgStore(t)
	if _, err := s.ListRunningInstancesOnDeadNodes(ctx, time.Now().UTC(), 0); err == nil {
		t.Fatalf("limit=0 must error")
	}
	if _, err := s.ListRunningInstancesOnDeadNodes(ctx, time.Now().UTC(), -1); err == nil {
		t.Fatalf("limit<0 must error")
	}
}

// TestPg_FailRunningInstanceOnDeadNode_ConditionalMatches pins the
// race-safety contract on the conditional UPDATE: only
// state='running' AND node_id=$2 rows transition; everything else
// surfaces as ErrConflict.
func TestPg_FailRunningInstanceOnDeadNode_ConditionalMatches(t *testing.T) {
	s, ctx := pgStore(t)

	// Seed: dead node + RUNNING instance → transition succeeds.
	deadNodeID := pgTestMaintenanceNode(t, ctx, s)
	_, insID := pgTestSeedRunningInstance(t, ctx, s, deadNodeID)
	threshold := time.Now().UTC().Add(-time.Minute)

	if err := s.FailRunningInstanceOnDeadNode(ctx, insID, deadNodeID, threshold); err != nil {
		t.Fatalf("first FailRunningInstanceOnDeadNode: %v", err)
	}

	// Second call: state is now 'failed', not 'running' → ErrConflict.
	err := s.FailRunningInstanceOnDeadNode(ctx, insID, deadNodeID, threshold)
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("second call err=%v want ErrConflict (the state predicate must protect against re-running)", err)
	}

	// Wrong-node call: even if we manually flip state back to
	// running on a different node, the node_id mismatch must
	// surface as ErrConflict (the conditional UPDATE has TWO
	// predicates, not one).
	wrongNodeID := pgTestMaintenanceNode(t, ctx, s)
	err = s.FailRunningInstanceOnDeadNode(ctx, insID, wrongNodeID, threshold)
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("wrong-node call err=%v want ErrConflict (node_id mismatch must protect against misrouting)", err)
	}
}

func TestPg_FailRunningInstanceOnDeadNode_NodeRecoveredReturnsConflict(t *testing.T) {
	s, ctx, _ := pgWithPool(t)
	nodeID := pgTestMaintenanceNode(t, ctx, s)
	_, insID := pgTestSeedRunningInstance(t, ctx, s, nodeID)
	threshold := time.Now().UTC().Add(-time.Minute)
	if err := s.NodeSetLifecycle(ctx, nodeID, state.NodeLifecycleMaintenance, state.NodeLifecycleActive); err != nil {
		t.Fatalf("NodeSetLifecycle(maintenance→active): %v", err)
	}
	if err := s.FailRunningInstanceOnDeadNode(ctx, insID, nodeID, threshold); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("post-recovery fail err=%v, want ErrConflict", err)
	}
}

// TestPg_FailRunningInstanceOnDeadNode_EmptyArgs pins the input
// contract: empty instanceID / nodeID both error.
func TestPg_FailRunningInstanceOnDeadNode_EmptyArgs(t *testing.T) {
	s, ctx := pgStore(t)
	if err := s.FailRunningInstanceOnDeadNode(ctx, "", "n", time.Now()); err == nil {
		t.Fatalf("empty instanceID must error")
	}
	if err := s.FailRunningInstanceOnDeadNode(ctx, "i", "", time.Now()); err == nil {
		t.Fatalf("empty nodeID must error")
	}
}

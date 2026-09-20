// node_reservation.go — transactional per-node RAM reservation (ADR-193).
//
// Invariant §6.2-2 — Σ(ram_mb + PerVMOverheadMB) over the live instances on a
// compute node must stay at or below that node's admission_ceiling_mb — was
// enforced only by sched.NodeLedger, whose package doc states its premise
// plainly: "schedd is the single writer to the instances table and a single
// process, so this in-memory accounting needs no distributed locking".
//
// ADR-062 retired that premise. Every schedd owns a shard of apps
// (apps.node_id) and may place an instance on ANY active node, so schedd A's
// instances on node C are invisible to schedd B's ledger — SeedLedger
// rebuilds only the apps the local schedd owns. The remaining cross-schedd
// view was ComputeNodeUsedMB, and the chooser reads it through a one-second
// cache (sched.NodeUsageFreshness). That is a read-then-insert race with no
// backstop: two schedds reading the same cached headroom inside one second
// each admit against the same free MB.
//
// This file closes the race at the only point where it is closable. The
// INSERT into `instances` is already what makes a reservation visible
// fleet-wide — ComputeNodeUsedMB sums exactly those rows — so the headroom
// check and the insert become one transaction holding a per-node advisory
// lock. Admissions to the same node serialize; admissions to different nodes
// do not contend.
//
// What this tier is NOT:
//
//   - It does not replace sched.NodeLedger. The ledger stays the fast local
//     path and remains the ONLY enforcement for per-app concurrency
//     (§6.2-1), vCPU budget, and CPU millicores. This is a durable floor
//     under the RAM ceiling alone.
//   - It does not cover ownership transfer. A live migration moves an
//     instance by UPDATEing node_id (MigrateInstanceOwner), not by inserting
//     a row, so it can still push a destination node past its ceiling. That
//     path needs the same treatment and is recorded as follow-up work in
//     ADR-193.
package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrNodeCapacity reports that admitting an instance would push a compute
// node past its admission_ceiling_mb. schedd maps it to api.ErrCapacity
// (CodeCapacity / HTTP 503), the same customer-visible outcome the ledger's
// RAM refusal already produces, so the wire contract is unchanged.
//
// Callers must treat this as a retryable placement failure, not a wake
// failure: the chosen node is full, another node may not be. It is
// deliberately distinct from ErrConcurrentWake (a wake_id collision, which
// is recoverable by reading the winner's row) because there is no winning
// row to read here.
var ErrNodeCapacity = errors.New("state: compute node at admission ceiling")

// nodeReservationLockClass namespaces this file's advisory locks.
//
// PostgreSQL keeps two disjoint advisory-lock spaces: the single-bigint form
// and the (int4, int4) pair form. Everything else in this package uses the
// single-bigint form keyed on an app id (pgstore.go's edge-rule mutation
// lock, pgstore_workflows.go, the managed-secret target lock). Using the
// pair form here means a node-id hash can never collide with an app-id hash
// no matter what either one hashes to — the two spaces simply do not
// interact. The class value is "node" in ASCII, so an operator staring at
// pg_locks can tell what took it.
const nodeReservationLockClass = 0x6E6F6465

// nodeUsageCounts reports whether a row in this state contributes to the
// per-node RAM sum.
//
// This deliberately does NOT call State.CountsForRAM(). That predicate also
// returns true for 'snapshotting' and 'migrating', while the SQL in
// ComputeNodeUsedMB / ComputeNodeUsedMBByNode sums only
// ('waking','cold_booting','running','warm'). The two have disagreed since
// the states were added, and the SQL set is the one the placement chooser
// actually reads.
//
// The guard MUST agree with the chooser: if it counted a state the chooser
// ignores, a node the chooser believes has headroom would start refusing
// admissions, and every wake routed there would fail with no way for the
// chooser to learn why. Reconciling CountsForRAM with the SQL is a real
// question, but it changes placement behaviour and belongs in its own ADR —
// not smuggled in under a correctness fix.
func nodeUsageCounts(s State) bool {
	switch s {
	case StateWaking, StateColdBooting, StateRunning, StateWarm:
		return true
	default:
		return false
	}
}

// instanceInserter is the narrow slice of pgxpool.Pool / pgx.Tx that the
// instance INSERT statements need. Both satisfy it, which lets
// insertInstanceWithNodeReservation hand the caller either the pool (when no
// reservation is required) or the reserving transaction without the caller
// knowing which it got.
type instanceInserter interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// insertInstanceWithNodeReservation runs insert under the per-node headroom
// guard described in this file's doc comment.
//
// newState decides whether a guard is needed at all. A row that does not
// count toward the node sum ('parked', 'pending', terminal states) holds no
// resident RAM, so it runs straight against the pool exactly as it did
// before this file existed — no transaction, no lock, no behaviour change.
//
// When the node has no compute_nodes row, the ceiling lookup finds nothing
// and the insert is allowed to proceed so it fails on its own NOT NULL / FK
// constraint. Reporting ErrNodeCapacity there would rename a schema error
// into a capacity error and send schedd looking for a node with headroom
// that does not exist.
func (s *PgStore) insertInstanceWithNodeReservation(
	ctx context.Context,
	nodeID, newState string,
	ramMB int,
	insert func(instanceInserter) (Instance, error),
) (Instance, error) {
	if !nodeUsageCounts(State(newState)) {
		return insert(s.pool)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Instance{}, fmt.Errorf("state: node reservation: begin (node=%s): %w", nodeID, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Taken before the sum so a peer admitting to the same node blocks here
	// rather than reading the same pre-insert total we are about to act on.
	// pg_advisory_xact_lock releases on COMMIT or ROLLBACK, so every return
	// path below — including the deferred Rollback — drops it.
	if _, err := tx.Exec(ctx,
		`select pg_advisory_xact_lock($1, hashtext($2))`,
		nodeReservationLockClass, nodeID,
	); err != nil {
		return Instance{}, fmt.Errorf("state: node reservation: lock node %s: %w", nodeID, err)
	}

	// One round trip for both halves: the ceiling is on compute_nodes and the
	// sum is an instances aggregate over instances_live_node_id_idx, whose
	// partial predicate is the same four states listed here.
	var ceilingMB, usedMB int64
	err = tx.QueryRow(ctx, `
		select n.admission_ceiling_mb::bigint,
		       coalesce((select sum(i.ram_mb + $2)
		                   from instances i
		                  where i.node_id = n.id
		                    and i.state in ('waking','cold_booting','running','warm')), 0)::bigint
		  from compute_nodes n
		 where n.id = $1`,
		nodeID, api.PerVMOverheadMB,
	).Scan(&ceilingMB, &usedMB)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No such node. Fall through to the insert and let the FK speak.
	case err != nil:
		return Instance{}, fmt.Errorf("state: node reservation: read headroom (node=%s): %w", nodeID, err)
	default:
		admitMB := int64(ramMB) + int64(api.PerVMOverheadMB)
		if ceilingMB > 0 && usedMB+admitMB > ceilingMB {
			return Instance{}, fmt.Errorf(
				"state: %w: node=%s used_mb=%d admit_mb=%d ceiling_mb=%d",
				ErrNodeCapacity, nodeID, usedMB, admitMB, ceilingMB,
			)
		}
	}

	inst, err := insert(tx)
	if err != nil {
		return Instance{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Instance{}, fmt.Errorf("state: node reservation: commit (node=%s): %w", nodeID, err)
	}
	return inst, nil
}

// checkNodeReservationLocked is the MemStore mirror of the PgStore guard. The
// caller must already hold m.mu, which is what makes the check-then-insert
// atomic in the same way the advisory lock does for PgStore.
//
// An unknown node or a non-positive ceiling is a pass, matching the PgStore
// branch that lets the insert fail on its own constraint: MemStore fixtures
// routinely create instances against node ids that were never seeded, and
// turning those into capacity errors would be a test-only behaviour
// divergence — exactly the MemStore/PgStore drift that the uppercase-state
// literal bug was made of.
func (m *MemStore) checkNodeReservationLocked(nodeID, newState string, ramMB int) error {
	if !nodeUsageCounts(State(newState)) {
		return nil
	}
	node, ok := m.computeNodes[nodeID]
	if !ok || node.AdmissionCeilingMB <= 0 {
		return nil
	}
	var usedMB int64
	for _, ins := range m.instances {
		if ins.NodeID != nodeID {
			continue
		}
		if nodeUsageCounts(State(ins.State)) {
			usedMB += int64(ins.RAMMB + api.PerVMOverheadMB)
		}
	}
	admitMB := int64(ramMB) + int64(api.PerVMOverheadMB)
	if usedMB+admitMB > int64(node.AdmissionCeilingMB) {
		return fmt.Errorf(
			"state: %w: node=%s used_mb=%d admit_mb=%d ceiling_mb=%d",
			ErrNodeCapacity, nodeID, usedMB, admitMB, node.AdmissionCeilingMB,
		)
	}
	return nil
}

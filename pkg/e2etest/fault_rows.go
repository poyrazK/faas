// fault_rows.go — the fault surface that needs nothing but Postgres.
//
// These used to live in fault.go behind `//go:build e2e || metal`, alongside
// the daemon-level faults (SIGKILL, SIGSTOP, iptables) that genuinely require
// a live process or a second host. `-tags e2e` is set by no Makefile target
// and no workflow, so the whole file compiled only under `metal` — a gate that
// has never passed. The entire fault-injection surface was therefore dead
// code, and with it the failure-recovery behaviour it was written to cover.
//
// Splitting by what a fault actually NEEDS, rather than by which harness first
// wanted it, puts the row-level half where ordinary CI can reach it. A stale
// heartbeat, a drain, and a reactivate are three UPDATEs; they do not need a
// VM, a second box, or root.
//
// CmdFaultInjector embeds RowFaults, so the metal harness keeps one object
// with the full surface and its callers are unchanged.

package e2etest

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RowFaults injects node-lifecycle faults by writing the rows a daemon reads.
//
// Every method is idempotent and safe to call from a test that has already
// failed, so a t.Fatal mid-scenario cannot wedge the next one.
type RowFaults struct {
	pool *pgxpool.Pool
}

// NewRowFaults returns a row-level fault injector bound to pool.
func NewRowFaults(pool *pgxpool.Pool) *RowFaults { return &RowFaults{pool: pool} }

// StaleHeartbeat backdates a node's heartbeat, which is how a node that has
// stopped reporting looks to schedd: the row is present and plausible, and
// only its age says anything is wrong.
func (f *RowFaults) StaleHeartbeat(node string, age time.Duration) error {
	tag, err := f.pool.Exec(context.Background(),
		`UPDATE compute_nodes SET last_heartbeat_at = now() - $2::interval
		 WHERE name = $1`, node, age.String())
	if err != nil {
		return fmt.Errorf("e2etest: stale heartbeat for %s: %w", node, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("e2etest: stale heartbeat: no compute_node named %q", node)
	}
	return nil
}

// Drain flips lifecycle to 'draining' (operator-initiated; the recovery
// arbiter then orchestrates the live-migrations to completion).
func (f *RowFaults) Drain(node string) error {
	_, err := f.pool.Exec(context.Background(),
		`UPDATE compute_nodes SET lifecycle = 'draining'
		 WHERE name = $1 AND lifecycle = 'active'`, node)
	if err != nil {
		return fmt.Errorf("e2etest: drain %s: %w", node, err)
	}
	return nil
}

// Reactivate flips lifecycle back to 'active' (operator-initiated recovery
// shortcut; the recovery arbiter is the canonical path for failure-driven
// recovery, but ops gets a manual override).
func (f *RowFaults) Reactivate(node string) error {
	_, err := f.pool.Exec(context.Background(),
		`UPDATE compute_nodes SET lifecycle = 'active',
		 last_recovery_outcome = NULL,
		 recovery_initiated_at = NULL
		 WHERE name = $1`, node)
	if err != nil {
		return fmt.Errorf("e2etest: reactivate %s: %w", node, err)
	}
	return nil
}

// Deactivate takes a node out of rotation, which is the state a crashed vmmd
// leaves behind.
//
// It sets lifecycle, not `active`. compute_nodes.active is
// GENERATED ALWAYS AS (lifecycle IN ('active','recovering')) STORED, so writing
// it directly is rejected outright — this helper could never have worked as
// first written, and nothing had called it yet to find out.
//
// This is the shape of a real outage: UpsertComputeNodeFromVmmd preserved a
// non-rotating node on conflict, so a node that had crashed could never
// re-enter rotation no matter how healthily it came back. Being able to put a
// node into that state from a test is the point.
func (f *RowFaults) Deactivate(node string) error {
	tag, err := f.pool.Exec(context.Background(),
		`UPDATE compute_nodes SET lifecycle = 'unavailable' WHERE name = $1`, node)
	if err != nil {
		return fmt.Errorf("e2etest: deactivate %s: %w", node, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("e2etest: deactivate: no compute_node named %q", node)
	}
	return nil
}

// SetNodeAdmissionCeiling lowers (or raises) a node's RAM admission ceiling.
//
// Invariant §6.2-2 caps live RAM at 85% of the tenant budget — 47,600 MB on
// the reference box. A test cannot fill that on a CI runner, and should not
// try: the interesting behaviour is what happens AT the ceiling, not what the
// number is. Shrinking the node's own ceiling reaches the same code path
// (NodeLedger.Admit against compute_nodes.admission_ceiling_mb) in a few
// hundred megabytes.
func (f *RowFaults) SetNodeAdmissionCeiling(node string, mb int) error {
	tag, err := f.pool.Exec(context.Background(),
		`UPDATE compute_nodes SET admission_ceiling_mb = $2 WHERE name = $1`, node, mb)
	if err != nil {
		return fmt.Errorf("e2etest: set admission ceiling for %s: %w", node, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("e2etest: set admission ceiling: no compute_node named %q", node)
	}
	return nil
}

// AddPeerNode inserts a second compute_node that points at the SAME vmmd
// target as an existing one, and returns its id.
//
// Two rows, one fake. schedd sees two logical nodes and makes real placement,
// migration and drain decisions between them; both sets of RPCs land on the
// same FakeVMMD, which is exactly what the test wants to observe. The VM never
// actually moves — that part needs metal and is metal's job — but every
// control-plane decision around the move is the production one.
//
// This is what makes the multi-node scenarios reachable without hardware. They
// previously lived behind the two-node harness, which requires two real hosts
// and SSH, on a gate that has never passed.
func (f *RowFaults) AddPeerNode(peer, copyTargetFrom string) (string, error) {
	var id string
	err := f.pool.QueryRow(context.Background(), `
		INSERT INTO compute_nodes
		  (name, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb,
		   schedd_target_url, region, lifecycle, last_heartbeat_at)
		SELECT $1, target_url, vpcpus, mem_mb, max_concurrency, admission_ceiling_mb,
		       schedd_target_url, region, 'active', now()
		  FROM compute_nodes WHERE name = $2
		ON CONFLICT (name) DO UPDATE SET
		   lifecycle = 'active', last_heartbeat_at = now()
		RETURNING id::text`, peer, copyTargetFrom).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("e2etest: add peer node %s from %s: %w", peer, copyTargetFrom, err)
	}
	return id, nil
}

// InstanceNodeName returns the name of the node that currently owns an
// instance, which is how a test observes that a migration actually changed
// ownership rather than merely running its protocol.
func (f *RowFaults) InstanceNodeName(instanceID string) (string, error) {
	var name string
	err := f.pool.QueryRow(context.Background(),
		`SELECT n.name FROM instances i JOIN compute_nodes n ON n.id = i.node_id
		  WHERE i.id::text = $1`, instanceID).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("e2etest: owner of instance %s: %w", instanceID, err)
	}
	return name, nil
}

// NodeLifecycle reads a node's current lifecycle and active flag, so a test
// can assert on what the daemons converged to rather than on its own writes.
func (f *RowFaults) NodeLifecycle(node string) (lifecycle string, active bool, err error) {
	err = f.pool.QueryRow(context.Background(),
		`SELECT lifecycle, active FROM compute_nodes WHERE name = $1`, node).
		Scan(&lifecycle, &active)
	if err != nil {
		return "", false, fmt.Errorf("e2etest: read lifecycle for %s: %w", node, err)
	}
	return lifecycle, active, nil
}

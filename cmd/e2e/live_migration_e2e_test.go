// live_migration_e2e_test.go — draining a node moves its work, in order.
//
// Live migration is M9's current focus and had no coverage in a gate that
// runs. Its four vmmd RPCs were all Unimplemented in the fake, so any
// control-plane path reaching them got an error back and no test reached them.
// The scenarios existed only in twonode_failure_safe_metal_test.go, which
// needs two real hosts and SSH, on a gate that has never passed.
//
// It needs neither. AddPeerNode inserts a second compute_nodes row pointing at
// the SAME fake vmmd: schedd sees two logical nodes and makes real placement,
// handoff and drain decisions between them, and both sides of the move land on
// one fake where the test can watch them. The VM never physically moves — that
// part is metal's job — but every control-plane decision around the move is
// the production one.
//
// The protocol is ordered and the order is the contract:
//
//	prepare  — the source freezes the instance and issues a lease
//	adopt    — the target restores from the source's artifacts
//	acknowledge — only now may the source release the original VM
//
// Adopting before preparing would hand the target state the source is still
// writing. Acknowledging before adopting would release the source while the
// target may still fail, and the instance would exist nowhere. Neither
// mistake is visible from the outside: the instance still ends up "somewhere",
// and only a corrupted guest or a vanished one shows it later.

package e2e_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

const migrationPeerNode = "peer-node-b"

// newLiveMigrationFixture enables the live migrator and shortens its windows.
// Only the timers move; the migrator, the arbiter and the handoff are the
// production ones.
func newLiveMigrationFixture(t *testing.T, slug string) *normalPathFixture {
	t.Helper()
	return newNormalPathFixtureWithPlanAndEnv(t, slug, api.PlanHobby,
		"FAAS_MIGRATE_LIVE_MAX_PER_TICK=4",
		"FAAS_MIGRATE_LIVE_LEASE_SECONDS=30",
		"FAAS_MIGRATING_WATCHDOG_INTERVAL_SECONDS=1",
		"FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS=1",
	)
}

// TestE2E_LiveMigration_DrainMovesInstancesToThePeer drains the node holding a
// live instance and requires the work to land on the peer, via the protocol,
// in order.
func TestE2E_LiveMigration_DrainMovesInstancesToThePeer(t *testing.T) {
	f := newLiveMigrationFixture(t, "live-migration-drain")
	if f == nil {
		return
	}
	faults := e2etest.NewRowFaults(f.h.Pool)

	// A second node to move to. Without somewhere to go, a drain has nothing
	// to prove: the instance would simply be parked or failed, and the test
	// would pass without a migration ever being attempted.
	if _, err := faults.AddPeerNode(migrationPeerNode, state.DefaultLocalNodeName); err != nil {
		t.Fatalf("add peer node: %v", err)
	}

	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})
	wakeNormalPathApp(t, f)

	instance, err := f.store.RunningInstanceForApp(f.ctx, f.app.ID)
	if err != nil {
		t.Fatalf("no running instance to migrate: %v", err)
	}
	// Ask where the instance actually landed rather than assuming.
	//
	// With two interchangeable nodes the chooser is free to pick either, and
	// it picked the peer on the first run. Asserting a particular node here
	// tested the chooser's tie-break, which is not what this is about — and
	// would keep breaking as placement heuristics change.
	source, err := faults.InstanceNodeName(instance.ID)
	if err != nil {
		t.Fatalf("read initial owner: %v", err)
	}
	target := migrationPeerNode
	if source == migrationPeerNode {
		target = state.DefaultLocalNodeName
	}

	// Drain the node the instance is actually on. This is the operator action
	// the whole mechanism exists for: take a box out of service without
	// dropping the work on it.
	if err := faults.Drain(source); err != nil {
		t.Fatalf("drain %s: %v", source, err)
	}

	// The protocol must run at all. Its absence is the failure that matters:
	// a drain that parks or fails instances instead of moving them is a drain
	// that costs every tenant on the box their warm state.
	waitForWake(t, 60*time.Second, func() bool {
		calls := f.vmmd.MigrationCalls()
		return len(calls) > 0
	}, "draining a node with a live instance never started a migration. Either the recovery "+
		"arbiter did not act on the drain, or it chose to park/fail the instance instead of "+
		"moving it — which is the tenant losing their warm state for an operator's maintenance")

	// And it must run in order.
	waitForWake(t, 60*time.Second, func() bool {
		return migrationReached(f.vmmd.MigrationCalls(), "acknowledge")
	}, "the migration started but never acknowledged; the source node is still holding an "+
		"instance the target may already own")

	calls := f.vmmd.MigrationCalls()
	assertMigrationOrder(t, calls)

	// Ownership is the outcome the protocol exists to produce. A protocol that
	// runs cleanly and leaves the instance on the draining node has done
	// nothing.
	waitForWake(t, 60*time.Second, func() bool {
		name, err := faults.InstanceNodeName(instance.ID)
		return err == nil && name == target
	}, "the migration protocol completed but the instance is still owned by the drained node; "+
		"draining it again would move nothing and the box can never be taken out of service")
}

// migrationReached reports whether step appears in the recorded protocol.
func migrationReached(calls []string, step string) bool {
	for _, c := range calls {
		if c == step {
			return true
		}
	}
	return false
}

// assertMigrationOrder pins the happy-path sequence. Each inversion below is a
// real corruption, not a style preference.
func assertMigrationOrder(t *testing.T, calls []string) {
	t.Helper()
	first := map[string]int{}
	for i, c := range calls {
		if _, seen := first[c]; !seen {
			first[c] = i
		}
	}
	prepare, hasPrepare := first["prepare"]
	adopt, hasAdopt := first["adopt"]
	ack, hasAck := first["acknowledge"]
	if !hasPrepare || !hasAdopt || !hasAck {
		t.Fatalf("incomplete migration protocol %v; want prepare, adopt and acknowledge", calls)
	}
	if adopt < prepare {
		t.Errorf("adopt preceded prepare in %v: the target restored from artifacts the source "+
			"had not finished writing", calls)
	}
	if ack < adopt {
		t.Errorf("acknowledge preceded adopt in %v: the source was released before the target "+
			"had the instance, so a failed adopt leaves it running nowhere", calls)
	}
}

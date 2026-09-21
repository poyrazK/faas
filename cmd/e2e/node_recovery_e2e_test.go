// node_recovery_e2e_test.go — a node that goes unhealthy must be able to come
// back.
//
// This is the half of failure handling that gets tested least and costs most.
// Detecting a dead node is the obvious requirement and the easy one to get
// right; RE-ADMITTING it once it recovers is neither, and getting it wrong is
// invisible until a node drops out of the fleet permanently.
//
// It has already happened here. A vmmd OOM left the node with active=false,
// UpsertComputeNodeFromVmmd preserved that flag on conflict, and the node
// could never re-enter rotation no matter how healthily it came back — the
// fleet quietly shrank by one box and stayed that way.
//
// Nothing covered this in a gate that runs. pkg/e2etest/fault.go held the
// injectors for exactly these scenarios behind `//go:build e2e || metal`, and
// `-tags e2e` is set by no Makefile target and no workflow, so the whole
// surface compiled only under `metal` — which has never passed. The row-level
// faults need nothing but Postgres, so they now live in an untagged file
// (pkg/e2etest/fault_rows.go) and this runs in ordinary CI.

package e2e_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// newNodeRecoveryFixture shortens the dead-node reconciler's windows so a
// test observes in seconds what production observes in minutes. The windows
// are the only thing shortened: the reconciler, the heartbeat loop and the
// admission path are the production ones.
func newNodeRecoveryFixture(t *testing.T, slug string) *normalPathFixture {
	t.Helper()
	return newNormalPathFixtureWithPlanAndEnv(t, slug, api.PlanHobby,
		"FAAS_DEAD_NODE_RECONCILER_STALENESS_SECONDS=2",
		"FAAS_DEAD_NODE_RECONCILER_INTERVAL_SECONDS=1",
	)
}

// TestE2E_NodeRecovery_StaleHeartbeatIsDetectedAndRecovered pins both halves
// of the contract in one pass, because each is meaningless without the other:
// a fleet that never notices a dead node loses requests, and a fleet that
// never re-admits a recovered one shrinks until it cannot serve.
func TestE2E_NodeRecovery_StaleHeartbeatIsDetectedAndRecovered(t *testing.T) {
	f := newNodeRecoveryFixture(t, "node-recovery")
	if f == nil {
		return
	}
	faults := e2etest.NewRowFaults(f.h.Pool)

	// Establish that the node serves before any fault, so a later failure
	// cannot be blamed on a fixture that never worked.
	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})
	wakeNormalPathApp(t, f)

	// The node stops answering, THEN its row is backdated.
	//
	// Both halves are needed. Backdating alone does not simulate a dead node:
	// schedd keeps probing, the fake keeps answering, and last_heartbeat_at is
	// refreshed on the next tick no matter how far back the test dated it — the
	// first version of this test failed exactly there, reporting that schedd
	// had ignored a stale heartbeat when schedd had in fact healed it.
	// Silencing the node is what makes the staleness persist; backdating just
	// spares the test from waiting out the real window.
	f.vmmd.SetUnreachable(true)
	if err := faults.StaleHeartbeat(state.DefaultLocalNodeName, 10*time.Minute); err != nil {
		t.Fatalf("stale heartbeat: %v", err)
	}

	waitForWake(t, 30*time.Second, func() bool {
		lifecycle, _, err := faults.NodeLifecycle(state.DefaultLocalNodeName)
		return err == nil && lifecycle != string(state.NodeLifecycleActive)
	}, "schedd never reacted to a heartbeat that was 10 minutes stale; a node can stop "+
		"reporting and keep receiving placements")

	// The node comes back. Nothing outside the platform intervenes: schedd's
	// own heartbeat loop should find it answering again and return it to
	// service. Whether it does is the whole question.
	f.vmmd.SetUnreachable(false)
	waitForWake(t, 60*time.Second, func() bool {
		lifecycle, active, err := faults.NodeLifecycle(state.DefaultLocalNodeName)
		return err == nil && lifecycle == string(state.NodeLifecycleActive) && active
	}, "a node whose heartbeat resumed never returned to service. Either the heartbeat "+
		"loop does not clear the unhealthy state, or the node is never re-probed once "+
		"marked down — the shape of the vmmd-OOM outage, where the fleet shrank by one "+
		"box permanently")

	// Lifecycle is bookkeeping; serving traffic is the contract. A node that
	// reads active and still cannot take a wake has recovered on paper only.
	f.vmmd.SetDefaultVersion("v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 60*time.Second)
}

// TestE2E_NodeRecovery_DrainedNodeReactivates pins the operator path beside
// the automatic one: a drain must be reversible without restarting schedd.
//
// Drain is the safe, deliberate half of node lifecycle — the thing an operator
// reaches for during maintenance — so a drain that cannot be undone turns a
// routine action into an outage.
func TestE2E_NodeRecovery_DrainedNodeReactivates(t *testing.T) {
	f := newNodeRecoveryFixture(t, "node-drain-reactivate")
	if f == nil {
		return
	}
	faults := e2etest.NewRowFaults(f.h.Pool)

	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})
	wakeNormalPathApp(t, f)

	if err := faults.Drain(state.DefaultLocalNodeName); err != nil {
		t.Fatalf("drain: %v", err)
	}
	waitForWake(t, 30*time.Second, func() bool {
		lifecycle, _, err := faults.NodeLifecycle(state.DefaultLocalNodeName)
		return err == nil && lifecycle == string(state.NodeLifecycleDraining)
	}, "drain did not take")

	if err := faults.Reactivate(state.DefaultLocalNodeName); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	waitForWake(t, 30*time.Second, func() bool {
		lifecycle, active, err := faults.NodeLifecycle(state.DefaultLocalNodeName)
		return err == nil && lifecycle == string(state.NodeLifecycleActive) && active
	}, "a reactivated node did not return to active")

	// Same reasoning as above: the row saying `active` proves nothing until a
	// request actually lands on the node.
	f.vmmd.SetDefaultVersion("v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 60*time.Second)
}

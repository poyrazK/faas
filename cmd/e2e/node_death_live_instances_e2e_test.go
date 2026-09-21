// node_death_live_instances_e2e_test.go — a node dying with work on it.
//
// The recovery tests in node_recovery_e2e_test.go cover an idle node. The
// expensive case is a node that dies while instances are RUNNING, because
// three things have to happen and each fails silently on its own:
//
//   - the instances must reach a terminal state, and the RIGHT one;
//   - their capacity must be released, or the ledger keeps charging a node for
//     VMs that no longer exist and the fleet shrinks without anyone noticing;
//   - the apps must still be servable afterwards (§6.2-3).
//
// FAILED is the correct terminal state, not PARKED, and the distinction is
// load-bearing: the VM died with its host, so no snapshot was taken. Claiming
// PARKED would assert a snapshot that does not exist, and the next wake would
// try to restore from it. FAILED is cold-bootable (ADR-005 — snapshots are
// cache, not truth), so the customer's next request still serves; it just pays
// the cold-boot path.
//
// This was previously metal-only, in twonode_failure_safe_metal_test.go, on a
// gate that has never passed. FakeVMMD.SetUnreachable makes it reachable
// without KVM: a node that stops answering is a node that stops answering,
// whether its vmmd crashed or its fake stopped replying.

package e2e_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestE2E_NodeDeath_LiveInstancesFailAndReleaseCapacity kills the node under a
// running instance and checks the whole arc: terminal state, capacity release,
// and that the app is still servable once the node returns.
func TestE2E_NodeDeath_LiveInstancesFailAndReleaseCapacity(t *testing.T) {
	f := newNodeRecoveryFixture(t, "node-death-live")
	if f == nil {
		return
	}
	faults := e2etest.NewRowFaults(f.h.Pool)

	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})
	wakeNormalPathApp(t, f)

	// From here the bridge must refuse instances it never booted.
	//
	// Without this the fake answers for ANY instance id, so the gateway
	// routing to a destroyed VM still gets a 200 — and "the app recovered"
	// passes on a stale route to the dead instance, with no recovery having
	// happened. A real vmmd has no such instance and fails the forward. That
	// is exactly how this test passed its serve assertion while no replacement
	// instance existed.
	f.vmmd.SetStrictInstances(true)

	instance, err := f.store.RunningInstanceForApp(f.ctx, f.app.ID)
	if err != nil {
		t.Fatalf("no running instance to kill under: %v", err)
	}
	node, err := f.store.ComputeNodeByName(f.ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("load node: %v", err)
	}

	// Capacity is charged while the instance is live. Establishing this before
	// the kill is what makes the release assertion below mean something: a
	// reading of zero proves nothing if it was always zero.
	usedBefore, err := f.store.ComputeNodeUsedMB(f.ctx, node.ID)
	if err != nil {
		t.Fatalf("read used mb before death: %v", err)
	}
	if usedBefore == 0 {
		t.Fatal("node shows no resident RAM while an instance is running; " +
			"the release assertion below would be vacuous")
	}

	// The node dies with the instance on it. Silencing it is what makes the
	// staleness stick — schedd keeps probing, and a node that still answers
	// gets its heartbeat refreshed no matter how far back the row is dated.
	f.vmmd.SetUnreachable(true)
	// The VMs went with the host. Anything still routed to them must fail.
	f.vmmd.ForgetInstances()
	if err := faults.StaleHeartbeat(state.DefaultLocalNodeName, 10*time.Minute); err != nil {
		t.Fatalf("stale heartbeat: %v", err)
	}

	// FAILED, specifically. PARKED would claim a snapshot that was never
	// taken, and the next wake would try to restore from it.
	waitForWake(t, 60*time.Second, func() bool {
		ins, err := f.store.InstanceByID(f.ctx, instance.ID)
		return err == nil && ins.State == string(state.StateFailed)
	}, "an instance on a dead node never reached FAILED. If it is still running, the "+
		"platform is routing to a VM that no longer exists; if it reached PARKED, it is "+
		"claiming a snapshot that was never taken and the next wake will try to restore it")

	// The quiet one. A node charged for VMs that no longer exist accepts less
	// and less work until it accepts none, and nothing reports it.
	waitForWake(t, 30*time.Second, func() bool {
		used, err := f.store.ComputeNodeUsedMB(f.ctx, node.ID)
		return err == nil && used == 0
	}, "capacity was never released after the instances on a dead node failed; the ledger "+
		"keeps charging the node for VMs that are gone, so it accepts less work after every "+
		"node failure until it accepts none")

	// §6.2-3: the app must still be servable. The snapshot seeded earlier is
	// untouched by the node's death, so this proves the app is not stranded by
	// a failure that had nothing to do with its artifacts.
	f.vmmd.SetUnreachable(false)
	waitForWake(t, 60*time.Second, func() bool {
		lifecycle, active, err := faults.NodeLifecycle(state.DefaultLocalNodeName)
		return err == nil && lifecycle == string(state.NodeLifecycleActive) && active
	}, "the node never returned to service after answering again")

	f.vmmd.SetDefaultVersion("v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 60*time.Second)

	// The replacement must be a different instance: the failed one is gone with
	// its host, and reusing its row would mean routing to a dead VM.
	//
	// Asked of the live set rather than of RunningInstanceForApp. A serving
	// instance is not necessarily in `running` at the instant you look — warm
	// is resident and routable too, and schedd moves between them on its own
	// schedule. Pinning the narrower state made this fail while the platform
	// was behaving correctly.
	var replacement string
	waitForWake(t, 30*time.Second, func() bool {
		id, ok := liveInstanceIDForApp(t, f, instance.ID)
		if ok {
			replacement = id
		}
		return ok
	}, "the app served a request but no live instance other than the failed one ever "+
		"appeared; either the failed row was resurrected or the response came from "+
		"something that is not a tracked instance. Rows: "+instanceRowsForApp(t, f))
	if replacement == instance.ID {
		t.Errorf("the app came back on the SAME instance %s that was failed on the dead node; "+
			"a failed instance must not be resurrected, its VM died with the host", instance.ID)
	}
}

// instanceRowsForApp renders every instance row for the app, so a failure says
// what the state machine actually did instead of only what it did not do.
// "No live instance" has several causes — resurrected row, wrong state, wrong
// app — and they are indistinguishable without the rows.
func instanceRowsForApp(t *testing.T, f *normalPathFixture) string {
	t.Helper()
	rows, err := f.h.Pool.Query(f.ctx,
		`SELECT id::text, state, coalesce(node_id::text,'') FROM instances
		  WHERE app_id = $1 ORDER BY started_at NULLS FIRST`, f.app.ID)
	if err != nil {
		return "<unreadable: " + err.Error() + ">"
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, st, node string
		if err := rows.Scan(&id, &st, &node); err != nil {
			return "<scan error: " + err.Error() + ">"
		}
		out = append(out, id[:8]+"="+st+"@"+firstN(node, 8))
	}
	if len(out) == 0 {
		return "<none>"
	}
	return strings.Join(out, " ")
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// liveInstanceIDForApp returns a resident instance for the app other than
// `excluding`. Live means routable-and-resident — running or warm — not the
// narrower `running`.
func liveInstanceIDForApp(t *testing.T, f *normalPathFixture, excluding string) (string, bool) {
	t.Helper()
	var id string
	err := f.h.Pool.QueryRow(f.ctx,
		`SELECT id::text FROM instances
		  WHERE app_id = $1 AND id::text <> $2 AND state IN ('running','warm')
		  ORDER BY started_at DESC NULLS LAST LIMIT 1`, f.app.ID, excluding).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false
	}
	if err != nil {
		// A malformed query here silently returns "no replacement" on every
		// poll and the test fails for the wrong reason — which is exactly what
		// `ORDER BY created_at` did, on a table that has no such column.
		t.Fatalf("query live instances for app: %v", err)
	}
	return id, true
}

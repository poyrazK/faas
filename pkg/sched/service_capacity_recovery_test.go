// adr: 422
package sched

import (
	"context"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProtectedServiceRecoveryMemStore(t *testing.T) {
	store := state.NewMemStore()
	exerciseProtectedServiceRecovery(t, store, func() state.Store { return store })
}

func TestProtectedServiceRecoveryMixedSlotsMemStore(t *testing.T) {
	exerciseProtectedServiceMixedSlots(t, state.NewMemStore())
}

// A host full of small, low-quota services can look emptier to ordinary
// placement than a peer with free recovery slots. Recovery must use the peer.
func exerciseProtectedServiceMixedSlots(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.NodeList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err := store.SetComputeNodeActive(ctx, n.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	var fleet []state.ComputeNode
	for i := range 3 {
		n, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: fmt.Sprintf("mixed-%d", i), TargetURL: "unix:///tmp/mixed.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 32, Lifecycle: state.NodeLifecycleActive})
		if err != nil {
			t.Fatal(err)
		}
		fleet = append(fleet, n)
	}
	acct, err := store.CreateAccount(ctx, "mixed-slots@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	var small state.App
	var smallDep state.Deployment
	var lost []state.Instance
	for i, shape := range []struct{ ram, cpu, desired int }{{128, 250, 6}, {1024, 1000, 2}} {
		a, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: fmt.Sprintf("mixed-app-%d", i), RAMMB: shape.ram, CPUMillicores: shape.cpu, MaxConcurrency: 8, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 0, Max: 8, Desired: shape.desired}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetAppNodeID(ctx, a.ID, fleet[0].ID); err != nil {
			t.Fatal(err)
		}
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:mixed-service", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		for replica := range shape.desired {
			node := fleet[1]
			if i == 0 {
				node = fleet[0]
				if replica >= 4 {
					node = fleet[2]
				}
			}
			ins, err := store.CreateInstanceWithMode(ctx, a.ID, d.ID, string(state.StateRunning), shape.ram, node.ID, "", string(state.InstanceModeService))
			if err != nil {
				t.Fatal(err)
			}
			if node.ID == fleet[2].ID {
				lost = append(lost, ins)
			}
		}
		if i == 0 {
			small, smallDep = a, d
		}
	}
	if r, err := store.SetServiceCapacityProtection(ctx, true); err != nil || r.FailoverSlots != 8 {
		t.Fatalf("mixed protection: %+v %v", r, err)
	}
	if err := store.SetComputeNodeActive(ctx, fleet[2].ID, false); err != nil {
		t.Fatal(err)
	}
	for _, ins := range lost {
		if err := store.UpdateInstanceState(ctx, ins.ID, string(state.StateFailed)); err != nil {
			t.Fatal(err)
		}
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOwnerNodeID(fleet[0].ID)
	if err := e.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	request := Request{AppID: small.ID, RAMMB: 128, VCPU: 4, CPUMillicores: 250}
	if p, err := e.choosePlacementLocked(ctx, request); err != nil || p.NodeID != fleet[0].ID {
		t.Fatalf("ordinary placement did not expose the full-slot host: %+v %v", p, err)
	}
	if p, err := e.choosePlacementLocked(withServiceReplicaPlacementSpread(ctx), request); err != nil || p.NodeID != fleet[1].ID {
		t.Fatalf("service placement did not use spare slots: %+v %v", p, err)
	}
	if err := e.convergeServiceReplicasToTarget(ctx, smallDep.ID, 6, true); err != nil {
		t.Fatalf("mixed service recovery: %v", err)
	}
	rows, err := listServiceReplicas(ctx, store, small.ID, smallDep.ID)
	if err != nil || classifyServiceReplicas(rows).ready != 6 {
		t.Fatalf("mixed service target not restored: %+v %v", rows, err)
	}
}

// At the protected limit a lost owner must recover without restoring
// capacity, notifications, customer traffic or accepting new service intent.
func exerciseProtectedServiceRecovery(t *testing.T, store state.Store, reopen func() state.Store) {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.NodeList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err := store.SetComputeNodeActive(ctx, n.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	var fleet []state.ComputeNode
	for i := range 2 {
		n, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: fmt.Sprintf("protected-%d", i), TargetURL: fmt.Sprintf("unix:///tmp/protected-%d.sock", i), VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 16, Lifecycle: state.NodeLifecycleActive})
		if err != nil {
			t.Fatal(err)
		}
		fleet = append(fleet, n)
	}
	if err := store.SetComputeNodeActive(ctx, fleet[1].ID, false); err != nil {
		t.Fatal(err)
	}
	acct, err := store.CreateAccount(ctx, "protected-fleet@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	origin := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOwnerNodeID(fleet[0].ID)
	origin.serviceReconcileSubmit = func(context.Context, string) {}
	var apps []state.App
	var deps []state.Deployment
	for i := range 3 {
		desired := 4
		if i == 2 {
			desired = 0
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: fmt.Sprintf("protected-app-%d", i), RAMMB: 512, MaxConcurrency: 4, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 0, Max: 4, Desired: desired}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetAppNodeID(ctx, app.ID, fleet[0].ID); err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:protected-service", Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		if err := origin.convergeServiceReplicasToTarget(ctx, dep.ID, desired, true); err != nil {
			t.Fatal(err)
		}
		apps = append(apps, app)
		deps = append(deps, dep)
	}
	if err := store.SetComputeNodeActive(ctx, fleet[1].ID, true); err != nil {
		t.Fatal(err)
	}
	if r, err := store.SetServiceCapacityProtection(ctx, true); err != nil || r.State != "protected" || r.ReservedReplicas != 8 || r.FailoverSlots != 8 {
		t.Fatalf("protect full fleet: %+v %v", r, err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "unprotected-increase", RAMMB: 512, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService}}); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("fleet accepted excess demand: %v", err)
	}
	if err := store.SetComputeNodeActive(ctx, fleet[0].ID, false); err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		rows, err := store.ListInstancesForApp(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, ins := range rows {
			if err := origin.RecreateInstance(ctx, ins.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	store = reopen()
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOwnerNodeID(fleet[1].ID)
	loop := NewLoop(nil, e, testLog())
	t.Cleanup(loop.workPool().drain)
	if err := e.RebalanceOrphanedApps(ctx, ""); err != nil {
		t.Fatal(err)
	}
	loop.workPool().drain()
	for range 3 {
		loop.runServiceRecovery(ctx)
		loop.workPool().drain()
	}
	for i, a := range apps {
		owner, err := store.AppByID(ctx, a.ID)
		if err != nil || owner.NodeID != fleet[1].ID {
			t.Fatalf("owner did not recover: %+v %v", owner, err)
		}
		rows, err := listServiceReplicas(ctx, store, a.ID, deps[i].ID)
		if err != nil {
			t.Fatal(err)
		}
		want := a.Manifest.ServiceReplicas.Desired
		status := classifyServiceReplicas(rows)
		if status.ready != want || status.managed() != want {
			t.Fatalf("service did not recover: %+v want=%d", status, want)
		}
	}
	if r, err := store.ServiceCapacityProtection(ctx); err != nil || r.State != "degraded" || r.ReservedReplicas != 8 {
		t.Fatalf("surviving capacity: %+v %v", r, err)
	}
	if origin.ledger.ResidentRAM() != 0 {
		t.Fatal("lost host reservations leaked")
	}
}

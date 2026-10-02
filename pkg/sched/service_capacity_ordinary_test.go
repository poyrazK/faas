// adr: 438
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestOrdinaryPlacementPreservesRegionalRecoveryMemStore(t *testing.T) {
	exerciseOrdinaryRegionalPlacement(t, state.NewMemStore())
}

func exerciseOrdinaryRegionalPlacement(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	existing, err := store.NodeList(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range existing {
		if err := store.SetComputeNodeActive(ctx, node.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	var fleet []state.ComputeNode
	for _, region := range []string{"eu-fsn", "eu-fsn", "us-ash"} {
		if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: region, DisplayName: region, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
		ram := 1040
		if region == "us-ash" {
			ram = 4160
		}
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: uuid.NewString(), Region: &region, TargetURL: "unix:///tmp/ordinary-capacity.sock", VPCPUs: 4, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: ram, VCPUBudget: 32, Lifecycle: state.NodeLifecycleActive})
		if err != nil {
			t.Fatal(err)
		}
		fleet = append(fleet, node)
	}
	conformance.KeepNodesHeartbeating(t, store, fleet)
	acct, err := store.CreateAccount(ctx, "ordinary-placement@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	service, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "reserved-eu", ComputeRegion: "eu-fsn", RAMMB: 512, CPUMillicores: 1000, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 0, Max: 5, Desired: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	serviceDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: service.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "ordinary", RAMMB: 512, CPUMillicores: 1000, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: ordinary.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetServiceCapacityProtection(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstance(ctx, ordinary.ID, dep.ID, string(state.StateRunning), 512, fleet[0].ID, uuid.NewString()); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("ordinary allocation consumed EU recovery: %v", err)
	}
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	request := Request{AppID: ordinary.ID, DeploymentID: dep.ID, RAMMB: 512, VCPU: 2, CPUMillicores: 1000, PreferredNodeID: fleet[0].ID, PrioritizeSnapshotLocality: true}
	placement, err := engine.choosePlacementLocked(ctx, request)
	if err != nil || placement.NodeID != fleet[2].ID {
		t.Fatalf("ordinary placement selected reserved region despite safe US capacity: %+v %v", placement, err)
	}
	if _, err := store.CreateInstance(ctx, ordinary.ID, dep.ID, string(state.StateRunning), 512, placement.NodeID, uuid.NewString()); err != nil {
		t.Fatalf("safe ordinary placement was refused: %v", err)
	}
	for _, sidecar := range []api.Sidecar{{Name: "ram", RamMB: 4096}, {Name: "cpu", CPUMillicores: 32000}} {
		raw, err := json.Marshal([]api.Sidecar{sidecar})
		if err != nil {
			t.Fatal(err)
		}
		largeDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: ordinary.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, Sidecars: raw})
		if err != nil {
			t.Fatal(err)
		}
		r := request
		r.DeploymentID = largeDep.ID
		if p, err := engine.choosePlacementLocked(ctx, r); err == nil {
			t.Fatalf("sidecar %s omitted from prospective capacity: %+v", sidecar.Name, p)
		}
		if _, err := store.CreateInstance(ctx, ordinary.ID, largeDep.ID, string(state.StateRunning), 512, fleet[2].ID, uuid.NewString()); state.ServiceCapacityProblem(err) == nil {
			t.Fatalf("sidecar %s admitted beyond recovery budget: %v", sidecar.Name, err)
		}
	}
	for _, region := range []string{"", "eu-fsn"} {
		job := Request{RAMMB: 512, VCPU: 1, CPUMillicores: 1000, Kind: KindJob, RequiredRegion: region, PreferredNodeID: fleet[0].ID, PrioritizeSnapshotLocality: true}
		p, err := engine.choosePlacementLocked(ctx, job)
		if region == "" {
			if err != nil || p.NodeID != fleet[2].ID {
				t.Fatalf("unrestricted job missed safe foreign capacity: %+v %v", p, err)
			}
		} else {
			var problem *api.Problem
			if !errors.As(err, &problem) || problem.Code != api.CodeComputeRegionCapacityExhausted {
				t.Fatalf("pinned job spilled from reserved region: %+v %v", p, err)
			}
		}
	}
	// A stale request topology must not hide the plan's durable guest bound.
	us := "us-ash"
	small, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: uuid.NewString(), Region: &us, TargetURL: "unix:///tmp/small-topology.sock", VPCPUs: 4, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 3, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	conformance.KeepNodesHeartbeating(t, store, []state.ComputeNode{small})
	if _, err := store.CreateInstance(ctx, ordinary.ID, dep.ID, string(state.StateRunning), 512, small.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	staleTopology := request
	staleTopology.VCPU, staleTopology.PreferredNodeID = 1, small.ID
	if p, err := engine.choosePlacementLocked(ctx, staleTopology); err != nil || p.NodeID != fleet[2].ID {
		t.Fatalf("request topology hid plan guest bound: %+v %v", p, err)
	}
	if _, err := store.CreateInstance(ctx, ordinary.ID, dep.ID, string(state.StateRunning), 512, small.ID, uuid.NewString()); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("plan guest bound was not atomically enforced: %v", err)
	}
	serviceRequest := Request{AppID: service.ID, DeploymentID: serviceDep.ID, RAMMB: 512, VCPU: 2, CPUMillicores: 1000, PreferredNodeID: fleet[0].ID}
	if p, err := engine.choosePlacementLocked(ctx, serviceRequest); err != nil || p.Region != "eu-fsn" {
		t.Fatalf("normal service placement lost declared capacity: %+v %v", p, err)
	}
	ordinaryService := serviceRequest
	ordinaryService.ordinaryResident = true // mirror, warm fill or private app task
	if p, err := engine.choosePlacementLocked(withServiceReplicaPlacementSpread(ctx), ordinaryService); err == nil {
		t.Fatalf("ordinary service-bound resident spent declared recovery: %+v", p)
	}
	if err := store.SetComputeNodeActive(ctx, fleet[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if p, err := engine.choosePlacementLocked(ctx, serviceRequest); err != nil || p.NodeID != fleet[1].ID {
		t.Fatalf("normal service recovery blocked during degradation: %+v %v", p, err)
	}
	if p, err := engine.choosePlacementLocked(ctx, request); err == nil {
		t.Fatalf("ordinary growth accepted while degraded: %+v", p)
	}
	if err := engine.ClaimUnplaced(ctx, ordinary.ID); err != nil {
		t.Fatalf("allocation-free owner claim blocked by recovery policy: %v", err)
	}
}

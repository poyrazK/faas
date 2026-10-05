// adr: 589
package sched

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestRegionalServiceCapacityPlacementMemStore(t *testing.T) {
	exerciseRegionalServiceCapacityPlacement(t, state.NewMemStore())
}

func exerciseRegionalServiceCapacityPlacement(t *testing.T, store state.Store) {
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
	fleet := map[string][]state.ComputeNode{}
	for _, region := range []string{"eu-fsn", "us-ash"} {
		if _, err := store.UpsertComputeRegion(ctx, state.ComputeRegion{ID: region, DisplayName: region, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
		count := 2
		if region == "eu-fsn" {
			count = 3
		}
		for i := range count {
			n, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: fmt.Sprintf("%s-%d", region, i), Region: &region, TargetURL: "unix:///tmp/regional-capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 2080, VCPUBudget: 8, Lifecycle: state.NodeLifecycleActive})
			if err != nil {
				t.Fatal(err)
			}
			fleet[region] = append(fleet[region], n)
		}
		conformance.KeepNodesHeartbeating(t, store, fleet[region])
	}
	acct, err := store.CreateAccount(ctx, "regional-placement@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	apps := map[string]state.App{}
	deps := map[string]state.Deployment{}
	for _, spec := range []struct {
		region string
		target int
	}{{"eu-fsn", 4}, {"us-ash", 4}, {"", 6}} {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "placement-" + uuid.NewString(), ComputeRegion: spec.region, RAMMB: 512, CPUMillicores: 1000, MaxConcurrency: 20, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 0, Max: 20, Desired: spec.target}}})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:regional-placement"})
		if err != nil {
			t.Fatal(err)
		}
		apps[spec.region] = app
		deps[spec.region] = dep
	}
	if _, err := store.SetServiceCapacityProtection(ctx, true); err != nil {
		t.Fatal(err)
	}
	create := func(region string, node state.ComputeNode) {
		t.Helper()
		if _, err := store.CreateInstanceWithMode(ctx, apps[region].ID, deps[region].ID, string(state.StateRunning), 512, node.ID, uuid.NewString(), string(state.InstanceModeService)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		create("eu-fsn", fleet["eu-fsn"][0])
	}
	for _, region := range []string{"eu-fsn", ""} {
		owner := fleet["eu-fsn"][0]
		if region == "" {
			owner = fleet["eu-fsn"][2]
			for range 2 {
				create("", fleet["eu-fsn"][0])
				create("", owner)
			}
		}
		if err := store.SetAppNodeID(ctx, apps[region].ID, owner.ID); err != nil {
			t.Fatal(err)
		}
		engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOwnerNodeID(owner.ID)
		if err := engine.SeedLedger(ctx); err != nil {
			t.Fatal(err)
		}
		ownerHint := Request{AppID: apps[region].ID, RAMMB: 512, VCPU: 2, placementOnly: true}
		if placement, err := engine.choosePlacementLocked(ctx, ownerHint); err != nil || placement.NodeID != owner.ID {
			t.Fatalf("placement snapshot lost owner affinity: %+v %v", placement, err)
		}
		request := Request{AppID: apps[region].ID, RAMMB: 512, VCPU: 2, CPUMillicores: 1000, PreferredNodeID: owner.ID, PrioritizeSnapshotLocality: true}
		// ADR-590: ordinary service wakes also use the safe survivor hints.
		if placement, err := engine.choosePlacementLocked(ctx, request); err != nil || placement.NodeID == owner.ID || placement.Region != "eu-fsn" {
			t.Fatalf("service placement without convergence context selected unsafe owner: %+v %v", placement, err)
		}
		placement, err := engine.choosePlacementLocked(withServiceReplicaPlacementSpread(ctx), request)
		if err != nil || placement.NodeID == owner.ID || placement.Region != "eu-fsn" {
			t.Fatalf("regional recovery selected unsafe owner/foreign region: %+v %v", placement, err)
		}
		if region == "" && placement.NodeID != fleet["eu-fsn"][1].ID {
			t.Fatalf("legacy did not use only safe pending placement: %+v", placement)
		}
	}
}

package conformance

import (
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-463: these cases run through the same public boundary in both stores.
func regionalCapacityFleet(t *testing.T, fx *Fixture, eu, us int) map[string][]state.ComputeNode {
	t.Helper()
	nodes, err := fx.Store.NodeList(fx.Ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err := fx.Store.SetComputeNodeActive(fx.Ctx, n.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	fleet := map[string][]state.ComputeNode{}
	for _, cell := range []struct {
		id    string
		count int
	}{{"eu-fsn", eu}, {"us-ash", us}} {
		if _, err := fx.Store.UpsertComputeRegion(fx.Ctx, state.ComputeRegion{ID: cell.id, DisplayName: cell.id, AcceptingNewApps: true}); err != nil {
			t.Fatal(err)
		}
		for range cell.count {
			region := cell.id
			n, err := fx.Store.CreateComputeNode(fx.Ctx, state.ComputeNode{Name: "regional-capacity-" + uuid.NewString(), Region: &region, TargetURL: "unix:///tmp/regional-capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 2080, VCPUBudget: 8, Lifecycle: state.NodeLifecycleActive})
			if err != nil {
				t.Fatal(err)
			}
			fleet[cell.id] = append(fleet[cell.id], n)
		}
		KeepNodesHeartbeating(t, fx.Store, fleet[cell.id])
	}
	return fleet
}

func regionalCapacityApp(t *testing.T, fx *Fixture, region string, desired int) state.App {
	t.Helper()
	proposal := capacityApp(fx, desired)
	proposal.ComputeRegion = region
	app, err := fx.Store.CreateApp(fx.Ctx, proposal)
	if err != nil {
		t.Fatal(err)
	}
	return app
}
func regionalCapacityDeployment(t *testing.T, fx *Fixture, app state.App) state.Deployment {
	t.Helper()
	d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:regional-capacity", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func regionalCapacityReplica(t *testing.T, fx *Fixture, app state.App, dep state.Deployment, node state.ComputeNode) state.Instance {
	t.Helper()
	ins, err := fx.Store.CreateInstanceWithMode(fx.Ctx, app.ID, dep.ID, string(state.StateRunning), 512, node.ID, uuid.NewString(), string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	return ins
}
func regionCapacity(t *testing.T, report api.ServiceCapacityProtection, id string) api.ServiceRegionCapacityProtection {
	t.Helper()
	for _, r := range report.Regions {
		if r.ComputeRegion == id {
			return r
		}
	}
	t.Fatalf("region %s missing: %+v", id, report)
	return api.ServiceRegionCapacityProtection{}
}

func testRegionalCapacityEnable(t *testing.T, fx *Fixture) {
	fleet := regionalCapacityFleet(t, fx, 1, 3)
	app := regionalCapacityApp(t, fx, "eu-fsn", 4)
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("foreign capacity covered sole EU host: %v", err)
	}
	report, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || report.Enabled || report.FailoverSlots != 12 || regionCapacity(t, report, "eu-fsn").FailoverSlots != 0 {
		t.Fatalf("enable rollback/report: %+v %v", report, err)
	}
	peer := fleet["eu-fsn"][0]
	peer.ID = ""
	peer.Name = "regional-capacity-" + uuid.NewString()
	peer, err = fx.Store.CreateComputeNode(fx.Ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	KeepNodesHeartbeating(t, fx.Store, []state.ComputeNode{peer})
	report, err = fx.Store.SetServiceCapacityProtection(fx.Ctx, true)
	if err != nil || report.State != "protected" || regionCapacity(t, report, "eu-fsn").FailoverSlots != 4 {
		t.Fatalf("same-region peer: %+v %v", report, err)
	}
	proposal := capacityApp(fx, 1)
	proposal.ComputeRegion = "eu-fsn"
	if _, err := fx.Store.CreateApp(fx.Ctx, proposal); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("foreign capacity covered increased EU intent: %v", err)
	}
	if _, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage}); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("foreign capacity covered EU scope: %v", err)
	}
	burst := capacityApp(fx, 0)
	burst.Manifest = state.AppManifest{}
	ordinary, err := fx.Store.CreateApp(fx.Ctx, burst)
	if err != nil {
		t.Fatal(err)
	}
	dep := regionalCapacityDeployment(t, fx, ordinary)
	if _, err := fx.Store.CreateInstanceWithMode(fx.Ctx, ordinary.ID, dep.ID, string(state.StateRunning), 512, peer.ID, uuid.NewString(), string(state.InstanceModeNormal)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("ordinary placement consumed EU recovery: %v", err)
	}
	if _, err := fx.Store.CreateInstanceWithMode(fx.Ctx, ordinary.ID, dep.ID, string(state.StateRunning), 512, fleet["us-ash"][0].ID, uuid.NewString(), string(state.InstanceModeNormal)); err != nil {
		t.Fatalf("unrelated US ordinary capacity refused: %v", err)
	}
}

func testRegionalCapacityLegacyIntent(t *testing.T, fx *Fixture) {
	regionalCapacityFleet(t, fx, 2, 2)
	regionalCapacityApp(t, fx, "eu-fsn", 4)
	regionalCapacityApp(t, fx, "us-ash", 4)
	report, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true)
	if err != nil || report.State != "protected" || report.FailoverSlots != 12 || report.LegacyPlacementCapacity != 0 {
		t.Fatalf("two pinned cells: %+v %v", report, err)
	}
	if _, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 1)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("admitted legacy with no safe final placement: %v", err)
	}
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, false); err != nil {
		t.Fatal(err)
	}
	legacy := regionalCapacityApp(t, fx, "", 1)
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("enabled pending legacy with no placement: %v", err)
	}
	manifest := legacy.Manifest
	replicas := *manifest.ServiceReplicas
	replicas.Desired = 0
	manifest.ServiceReplicas = &replicas
	if _, err := fx.Store.UpdateApp(fx.Ctx, legacy.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true); err != nil {
		t.Fatal(err)
	}
}

func testRegionalCapacityPlacementRecovery(t *testing.T, fx *Fixture) {
	fleet := regionalCapacityFleet(t, fx, 3, 2)
	eu := regionalCapacityApp(t, fx, "eu-fsn", 4)
	us := regionalCapacityApp(t, fx, "us-ash", 4)
	legacy := regionalCapacityApp(t, fx, "", 6)
	euDep := regionalCapacityDeployment(t, fx, eu)
	usDep := regionalCapacityDeployment(t, fx, us)
	legacyDep := regionalCapacityDeployment(t, fx, legacy)
	report, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true)
	if err != nil || report.State != "protected" || report.LegacyPlacementCapacity != 6 {
		t.Fatalf("pending placement proof: %+v %v", report, err)
	}
	var lost []state.Instance
	for range 2 {
		lost = append(lost, regionalCapacityReplica(t, fx, eu, euDep, fleet["eu-fsn"][0]))
	}
	hint, err := fx.Store.ServiceCapacityPlacement(fx.Ctx)
	if err != nil || hint.Nodes[fleet["eu-fsn"][0].ID].PinnedAllowed || !hint.Nodes[fleet["eu-fsn"][1].ID].PinnedAllowed {
		t.Fatalf("unsafe pinned survivor hint: %+v %v", hint, err)
	}
	if _, err := fx.Store.CreateInstanceWithMode(fx.Ctx, eu.ID, euDep.ID, string(state.StateRunning), 512, fleet["eu-fsn"][0].ID, uuid.NewString(), string(state.InstanceModeService)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("pinned survivor blocked pending legacy: %v", err)
	}
	for range 2 {
		regionalCapacityReplica(t, fx, eu, euDep, fleet["eu-fsn"][1])
	}
	// To preserve EU recovery, all six unrestricted replicas must use two
	// slots on each EU host. A third healthy survivor on one host is unsafe.
	for i, node := range fleet["eu-fsn"] {
		for range 2 {
			ins := regionalCapacityReplica(t, fx, legacy, legacyDep, node)
			if i == 0 {
				lost = append(lost, ins)
			}
		}
	}
	for range 2 {
		regionalCapacityReplica(t, fx, us, usDep, fleet["us-ash"][0])
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fleet["eu-fsn"][0].ID, false); err != nil {
		t.Fatal(err)
	}
	for _, ins := range lost {
		if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(state.StateFailed)); err != nil {
			t.Fatal(err)
		}
	}
	report, err = fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || report.State != "degraded" || regionCapacity(t, report, "eu-fsn").AvailableSlots != 4 {
		t.Fatalf("regional host loss: %+v %v", report, err)
	}
	// Healthy legacy survivors stay put. Only the lost legacy replicas may
	// move to US; the remaining EU slots belong to the pinned declaration.
	hint, err = fx.Store.ServiceCapacityPlacement(fx.Ctx)
	if err != nil || hint.Nodes[fleet["eu-fsn"][2].ID].LegacyAllowed || !hint.Nodes[fleet["eu-fsn"][2].ID].PinnedAllowed {
		t.Fatalf("degraded recovery hint: %+v %v", hint, err)
	}
	if _, err := fx.Store.CreateInstanceWithMode(fx.Ctx, legacy.ID, legacyDep.ID, string(state.StateRunning), 512, fleet["eu-fsn"][2].ID, uuid.NewString(), string(state.InstanceModeService)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("legacy stole EU declared recovery: %v", err)
	}
	for range 2 {
		regionalCapacityReplica(t, fx, eu, euDep, fleet["eu-fsn"][2])
		regionalCapacityReplica(t, fx, legacy, legacyDep, fleet["us-ash"][1])
		regionalCapacityReplica(t, fx, us, usDep, fleet["us-ash"][0])
	}
	// Releases and scale-down remain possible without restoring N-1 capacity.
	rows, err := fx.Store.ListInstancesForApp(fx.Ctx, eu.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range rows {
		if ins.State == string(state.StateRunning) {
			if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(state.StateFailed)); err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest := eu.Manifest
	replicas := *manifest.ServiceReplicas
	replicas.Desired = 0
	manifest.ServiceReplicas = &replicas
	if _, err := fx.Store.UpdateApp(fx.Ctx, eu.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
}

func testRegionalCapacityConcurrent(t *testing.T, fx *Fixture) {
	regionalCapacityFleet(t, fx, 2, 3)
	regionalCapacityApp(t, fx, "eu-fsn", 3)
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true); err != nil {
		t.Fatal(err)
	}
	other, err := fx.Store.CreateAccount(fx.Ctx, fmt.Sprintf("capacity-%s@example.com", uuid.NewString()), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	proposals := []state.App{capacityApp(fx, 1), capacityApp(fx, 1)}
	proposals[0].ComputeRegion = "eu-fsn"
	proposals[1].ComputeRegion = "eu-fsn"
	proposals[1].AccountID = other.ID
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, a := range proposals {
		wg.Add(1)
		go func(a state.App) { defer wg.Done(); <-start; _, err := fx.Store.CreateApp(fx.Ctx, a); results <- err }(a)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, refused := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if state.ServiceCapacityProblem(err) != nil {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatalf("regional capacity raced: accepted=%d refused=%d", accepted, refused)
	}
	report, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || report.State != "protected" || regionCapacity(t, report, "eu-fsn").ReservedReplicas != 4 {
		t.Fatalf("concurrent regional report: %+v %v", report, err)
	}
}

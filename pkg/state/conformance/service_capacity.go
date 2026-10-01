package conformance

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// KeepNodesHeartbeating models live fixture hosts while slow database tests
// exercise admission. Heartbeats refresh freshness without changing lifecycle,
// so explicitly failed or inactive hosts remain ineligible. The returned stop
// function joins the worker and is also registered before store cleanup.
func KeepNodesHeartbeating(t *testing.T, store state.Store, nodes []state.ComputeNode) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(state.DefaultHeartbeatStaleness / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, node := range nodes {
					if err := store.HeartbeatComputeNode(ctx, node.ID); err != nil {
						if ctx.Err() == nil {
							t.Errorf("fixture heartbeat for %s: %v", node.ID, err)
						}
						return
					}
				}
			}
		}
	}()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	return stop
}

func capacityFleet(t *testing.T, fx *Fixture) []state.ComputeNode {
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
	var fleet []state.ComputeNode
	for range 2 {
		n, err := fx.Store.CreateComputeNode(fx.Ctx, state.ComputeNode{Name: "capacity-" + uuid.NewString(), TargetURL: "unix:///tmp/capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4160, VCPUBudget: 16, Lifecycle: state.NodeLifecycleActive})
		if err != nil {
			t.Fatal(err)
		}
		fleet = append(fleet, n)
	}
	KeepNodesHeartbeating(t, fx.Store, fleet)
	if r, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true); err != nil || r.State != "protected" {
		t.Fatalf("enable: %+v %v", r, err)
	}
	return fleet
}

func capacityApp(fx *Fixture, desired int) state.App {
	return state.App{AccountID: fx.Account.ID, Slug: "capacity-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 512, CPUMillicores: 1000, MaxConcurrency: 20, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 0, Max: 20, Desired: desired}}}
}

// ADR-422: enabling must validate existing intent and leave the policy
// disabled on refusal, including when only one healthy host remains.
func testServiceCapacityEnable(t *testing.T, fx *Fixture) {
	fleet := capacityFleet(t, fx)
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, false); err != nil {
		t.Fatal(err)
	}
	a, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 9))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("enabled over unprotected intent: %v", err)
	}
	r, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.Enabled || r.State != "disabled" || r.ReservedReplicas != 9 {
		t.Fatalf("enable refusal changed policy: %+v %v", r, err)
	}
	manifest := a.Manifest
	target := *manifest.ServiceReplicas
	target.Desired = 8
	manifest.ServiceReplicas = &target
	if _, err := fx.Store.UpdateApp(fx.Ctx, a.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fleet[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("enabled with one healthy host: %v", err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fleet[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if r, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true); err != nil || r.State != "protected" {
		t.Fatalf("enable with protected intent: %+v %v", r, err)
	}
}

// ADR-422: intent consumes capacity before any VM exists; zero is stopped.
func testServiceCapacityIntent(t *testing.T, fx *Fixture) {
	capacityFleet(t, fx)
	a, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 8))
	if err != nil {
		t.Fatal(err)
	}
	r, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.State != "protected" || r.ReservedReplicas != 8 || r.FailoverSlots != 8 || r.ReplicaRAMMB != 520 || r.ReplicaCPUMillicores != 1000 || r.ReplicaVCPU != 2 {
		t.Fatalf("reservation: %+v %v", r, err)
	}
	if _, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 1)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("new service overcommitted: %v", err)
	}
	zero := capacityApp(fx, 0)
	zero.RAMMB = 4096
	if _, err := fx.Store.CreateApp(fx.Ctx, zero); err != nil {
		t.Fatalf("stopped app charged: %v", err)
	}
	manifest := a.Manifest
	copyTarget := *manifest.ServiceReplicas
	copyTarget.Desired = 9
	manifest.ServiceReplicas = &copyTarget
	if _, err := fx.Store.UpdateApp(fx.Ctx, a.ID, state.UpdateAppParams{Manifest: &manifest}); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("replica increase overcommitted: %v", err)
	}
	stored, err := fx.Store.AppByID(fx.Ctx, a.ID)
	if err != nil || stored.Manifest.ServiceReplicas.Desired != 8 {
		t.Fatalf("refusal changed intent: %+v %v", stored, err)
	}
	// A new scope consumes another independent desired count, even pending.
	if _, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: a.ID, Scope: "staging", Kind: state.DeploymentKindImage}); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("scope overcommitted: %v", err)
	}
	rows, err := fx.Store.ListDeploymentsForApp(fx.Ctx, a.ID, 100, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("refused deployment persisted: %+v %v", rows, err)
	}
}

// ADR-422: cross-account writers must serialize on the same fleet capacity.
func testServiceCapacityConcurrent(t *testing.T, fx *Fixture) {
	capacityFleet(t, fx)
	if _, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 7)); err != nil {
		t.Fatal(err)
	}
	other, err := fx.Store.CreateAccount(fx.Ctx, "capacity-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	proposals := []state.App{capacityApp(fx, 1), capacityApp(fx, 1)}
	proposals[1].AccountID = other.ID
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, proposal := range proposals {
		wg.Add(1)
		go func(a state.App) { defer wg.Done(); <-start; _, err := fx.Store.CreateApp(fx.Ctx, a); results <- err }(proposal)
	}
	close(start)
	wg.Wait()
	close(results)
	success, refusal := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if state.ServiceCapacityProblem(err) != nil {
			refusal++
		} else {
			t.Fatalf("unexpected result: %v", err)
		}
	}
	if success != 1 || refusal != 1 {
		t.Fatalf("success=%d refusal=%d", success, refusal)
	}
	r, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.DesiredReplicas != 8 || r.State != "protected" {
		t.Fatalf("concurrent reservation: %+v %v", r, err)
	}
}

// ADR-422: functions cannot steal the reserve. Existing service declarations
// remain recoverable on a surviving host, and stopping still works degraded.
func testServiceCapacityRecovery(t *testing.T, fx *Fixture) {
	fleet := capacityFleet(t, fx)
	a, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 8))
	if err != nil {
		t.Fatal(err)
	}
	d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.MarkDeploymentLive(fx.Ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateRunning), 4096, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("oversized replica consumed unreserved resources: %v", err)
	}
	if _, err := fx.Store.CreateInstance(fx.Ctx, fx.App.ID, fx.Deployment.ID, string(state.StateRunning), 512, fleet[0].ID, uuid.NewString()); !errors.Is(err, state.ErrNodeCapacity) {
		t.Fatalf("burst stole reserve: %v", err)
	}
	if _, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateWarm), 512, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("warm pool stole reserve: %v", err)
	}
	if _, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateRunning), 512, fleet[0].ID, uuid.NewString(), string(state.InstanceModeMirror)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("mirror stole reserve: %v", err)
	}
	parked, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateParked), 512, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, parked.ID, string(state.StateWarm)); !errors.Is(err, state.ErrNodeCapacity) {
		t.Fatalf("warm transition did not return retryable capacity: %v", err)
	}
	stored, err := fx.Store.InstanceByID(fx.Ctx, parked.ID)
	if err != nil || stored.State != string(state.StateParked) {
		t.Fatalf("refusal changed residency: %+v %v", stored, err)
	}
	var replicas []state.Instance
	for i := range 8 {
		ins, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateRunning), 512, fleet[i%2].ID, uuid.NewString(), string(state.InstanceModeService))
		if err != nil {
			t.Fatal(err)
		}
		replicas = append(replicas, ins)
	}
	if _, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateRunning), 512, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("excess replica bypassed reservation: %v", err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fleet[0].ID, false); err != nil {
		t.Fatal(err)
	}
	r, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.State != "degraded" || r.HealthyNodes != 1 {
		t.Fatalf("host loss: %+v %v", r, err)
	}
	for _, ins := range replicas {
		if ins.NodeID == fleet[0].ID {
			if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(state.StateFailed)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 4 {
		if _, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateRunning), 512, fleet[1].ID, uuid.NewString(), string(state.InstanceModeService)); err != nil {
			report, reportErr := fx.Store.ServiceCapacityProtection(fx.Ctx)
			t.Fatalf("existing reservation could not recover: %v; capacity: %+v (%v)", err, report, reportErr)
		}
	}
	p, err := fx.Store.ServiceCapacityPlacement(fx.Ctx)
	if err != nil || !p.Enabled || len(p.Nodes) != 1 || p.Nodes[fleet[1].ID].Slots != 8 || p.Nodes[fleet[1].ID].Used != 8 {
		t.Fatalf("surviving placement projection: %+v %v", p, err)
	}
	if _, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 1)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("degraded fleet accepted new intent: %v", err)
	}
	manifest := a.Manifest
	target := *manifest.ServiceReplicas
	target.Desired = 0
	manifest.ServiceReplicas = &target
	if _, err := fx.Store.UpdateApp(fx.Ctx, a.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatalf("stop blocked during degradation: %v", err)
	}
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, false); err != nil {
		t.Fatal(err)
	}
}

// ADR-422: a smaller survivor sets the protected limit; total RAM alone is
// insufficient. Guest CPU and startup CPU are independent placement bounds.
func testServiceCapacityHeterogeneous(t *testing.T, fx *Fixture) {
	nodes, err := fx.Store.NodeList(fx.Ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err := fx.Store.SetComputeNodeActive(fx.Ctx, n.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	var fleet []state.ComputeNode
	for _, ceiling := range []int{4160, 1040} {
		n, err := fx.Store.CreateComputeNode(fx.Ctx, state.ComputeNode{Name: "capacity-" + uuid.NewString(), TargetURL: "unix:///tmp/capacity.sock", VPCPUs: 1, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: ceiling, VCPUBudget: 64, Lifecycle: state.NodeLifecycleActive})
		if err != nil {
			t.Fatal(err)
		}
		fleet = append(fleet, n)
	}
	KeepNodesHeartbeating(t, fx.Store, fleet)
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 3)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("approved aggregate-only fit: %v", err)
	}
	a, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 2))
	if err != nil {
		t.Fatal(err)
	}
	r, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.FailoverSlots != 2 || r.FleetSlots != 10 {
		t.Fatalf("heterogeneous certificate: %+v %v", r, err)
	}
	// A sidecar changes the declared replica shape before it has a VM.
	if _, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage, Sidecars: []byte(`[{"name":"helper","type":"sidecar","ram_mb":512}]`)}); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("sidecar bypassed admission: %v", err)
	}
}

// ADR-422: explicit zero sidecar CPU inherits the startup quota, just like an
// omitted value. CPU alone can exhaust the protected capacity.
func testServiceCapacitySidecarCPU(t *testing.T, fx *Fixture) {
	capacityFleet(t, fx)
	a, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 4))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage, Sidecars: []byte(`[{"name":"helper","type":"sidecar","cpu_millicores":0}]`)}); err != nil {
		t.Fatal(err)
	}
	r, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.State != "protected" || r.ReplicaCPUMillicores != 2000 || r.FailoverSlots != 4 {
		t.Fatalf("sidecar CPU certificate: %+v %v", r, err)
	}
	if _, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 1)); state.ServiceCapacityProblem(err) == nil {
		t.Fatalf("sidecar CPU overcommitted: %v", err)
	}
}

// ADR-422: a plan downgrade does not resize an already-running guest. Its
// admitted topology remains reserved until the instance releases resources.
func testServiceCapacityAdmittedShape(t *testing.T, fx *Fixture) {
	fleet := capacityFleet(t, fx)
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	a, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 2))
	if err != nil {
		t.Fatal(err)
	}
	d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateRunning), 512, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	r, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.ReplicaVCPU != 4 || r.FailoverSlots != 4 {
		t.Fatalf("running guest shrank on plan update: %+v %v", r, err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(state.StateFailed)); err != nil {
		t.Fatal(err)
	}
	r, err = fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.ReplicaVCPU != 2 || r.FailoverSlots != 8 {
		t.Fatalf("released guest retained capacity: %+v %v", r, err)
	}
}

func refuseCapacityTransition(t *testing.T, fx *Fixture, ins state.Instance, next state.State) {
	t.Helper()
	before, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	transitions := []struct {
		name string
		run  func() error
	}{
		{"state", func() error { return fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(next)) }},
		{"conditional", func() error {
			return fx.Store.UpdateInstanceStateIf(fx.Ctx, ins.ID, ins.State, string(next))
		}},
		{"timestamp", func() error {
			return fx.Store.UpdateInstanceStateWithTimestamp(fx.Ctx, ins.ID, string(next), time.Now().UTC())
		}},
	}
	for _, transition := range transitions {
		t.Run(transition.name, func(t *testing.T) {
			if err := transition.run(); !errors.Is(err, state.ErrNodeCapacity) || state.ServiceCapacityProblem(err) == nil {
				t.Fatalf("%s -> %s did not refuse with retryable capacity: %v", ins.State, next, err)
			}
			stored, err := fx.Store.InstanceByID(fx.Ctx, ins.ID)
			if err != nil || stored.State != ins.State || !stored.ParkedAt.Equal(ins.ParkedAt) {
				t.Fatalf("refusal changed instance: %+v %v", stored, err)
			}
			after, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("refusal changed capacity: before=%+v after=%+v err=%v", before, after, err)
			}
		})
	}
}

// ADR-422: promoting a warm guest after a plan downgrade retains its original
// topology. Reclassification must validate the larger recovery slot even when
// the host's physical resource totals do not change.
func testServiceCapacityWarmPromotionShape(t *testing.T, fx *Fixture) {
	fleet := capacityFleet(t, fx)
	if _, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanScale); err != nil {
		t.Fatal(err)
	}
	a, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 5))
	if err != nil {
		t.Fatal(err)
	}
	d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateWarm), 512, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	r, err := fx.Store.SetServiceCapacityProtection(fx.Ctx, true)
	if err != nil || r.State != "protected" || r.ReservedReplicas != 5 || r.ReplicaVCPU != 2 || r.FailoverSlots != 6 {
		t.Fatalf("warm guest certificate: %+v %v", r, err)
	}
	refuseCapacityTransition(t, fx, ins, state.StateRunning)
	manifest := a.Manifest
	target := *manifest.ServiceReplicas
	target.Desired = 4
	manifest.ServiceReplicas = &target
	if _, err := fx.Store.UpdateApp(fx.Ctx, a.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(state.StateRunning)); err != nil {
		t.Fatalf("protected promotion refused: %v", err)
	}
	r, err = fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.State != "protected" || r.ReplicaVCPU != 4 || r.FailoverSlots != 4 {
		t.Fatalf("promoted guest lost original topology: %+v %v", r, err)
	}
}

// ADR-422: unchanged slot shape and physical usage do not authorize another
// service on a host whose conservative slots are already occupied.
func testServiceCapacityWarmPromotionSlots(t *testing.T, fx *Fixture) {
	capacityFleet(t, fx)
	nodes, err := fx.Store.NodeList(fx.Ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if err := fx.Store.SetComputeNodeActive(fx.Ctx, n.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	var fleet []state.ComputeNode
	for range 3 {
		n, err := fx.Store.CreateComputeNode(fx.Ctx, state.ComputeNode{Name: "capacity-" + uuid.NewString(), TargetURL: "unix:///tmp/capacity.sock", VPCPUs: 2, MemMB: 8192, MaxConcurrency: 20, AdmissionCeilingMB: 4640, VCPUBudget: 64, Lifecycle: state.NodeLifecycleActive})
		if err != nil {
			t.Fatal(err)
		}
		fleet = append(fleet, n)
	}
	KeepNodesHeartbeating(t, fx.Store, fleet)
	large := capacityApp(fx, 1)
	large.RAMMB = 1024
	if _, err := fx.Store.CreateApp(fx.Ctx, large); err != nil {
		t.Fatal(err)
	}
	small := capacityApp(fx, 5)
	small.RAMMB = 128
	a, err := fx.Store.CreateApp(fx.Ctx, small)
	if err != nil {
		t.Fatal(err)
	}
	d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	var running state.Instance
	for range 4 {
		running, err = fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateRunning), 128, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService))
		if err != nil {
			t.Fatal(err)
		}
	}
	warm, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateWarm), 128, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	p, err := fx.Store.ServiceCapacityPlacement(fx.Ctx)
	if err != nil || p.Nodes[fleet[0].ID].Slots != 4 || p.Nodes[fleet[0].ID].Used != 4 {
		t.Fatalf("full host slots: %+v %v", p, err)
	}
	refuseCapacityTransition(t, fx, warm, state.StateRunning)
	if err := fx.Store.UpdateInstanceState(fx.Ctx, running.ID, string(state.StateFailed)); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, warm.ID, string(state.StateRunning)); err != nil {
		t.Fatalf("promotion into released slot refused: %v", err)
	}
	p, err = fx.Store.ServiceCapacityPlacement(fx.Ctx)
	if err != nil || p.Nodes[fleet[0].ID].Slots != 4 || p.Nodes[fleet[0].ID].Used != 4 {
		t.Fatalf("promoted host slots: %+v %v", p, err)
	}
}

// ADR-422: service membership can grow only on an eligible host, but promotion
// into a declared slot on the surviving host must still work while degraded.
func testServiceCapacityWarmRecovery(t *testing.T, fx *Fixture) {
	fleet := capacityFleet(t, fx)
	a, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 7))
	if err != nil {
		t.Fatal(err)
	}
	d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateWarm), 512, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fleet[0].ID, false); err != nil {
		t.Fatal(err)
	}
	refuseCapacityTransition(t, fx, ins, state.StateRunning)
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fleet[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fleet[1].ID, false); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateInstanceStateIf(fx.Ctx, ins.ID, string(state.StateWarm), string(state.StateRunning)); err != nil {
		t.Fatalf("declared warm recovery refused: %v", err)
	}
	r, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.State != "degraded" || r.HealthyNodes != 1 || !r.PlacementsFit || r.ReservedReplicas != 7 || r.FleetSlots != 8 {
		t.Fatalf("warm recovery certificate: %+v %v", r, err)
	}
}

// ADR-422: turning a declared service VM into an ordinary warm guest consumes
// recovery headroom despite unchanged physical totals. Removing service intent
// remains allowed during degradation, even while its guest is resident.
func testServiceCapacityWarmDemotion(t *testing.T, fx *Fixture) {
	fleet := capacityFleet(t, fx)
	a, err := fx.Store.CreateApp(fx.Ctx, capacityApp(fx, 8))
	if err != nil {
		t.Fatal(err)
	}
	d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: a.ID, Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := fx.Store.CreateInstanceWithMode(fx.Ctx, a.ID, d.ID, string(state.StateRunning), 512, fleet[0].ID, uuid.NewString(), string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	refuseCapacityTransition(t, fx, ins, state.StateWarm)
	manifest := a.Manifest
	target := *manifest.ServiceReplicas
	target.Desired = 7
	manifest.ServiceReplicas = &target
	if _, err := fx.Store.UpdateApp(fx.Ctx, a.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(state.StateWarm)); err != nil {
		t.Fatalf("protected warm demotion refused: %v", err)
	}
	r, err := fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.State != "protected" || r.ReservedReplicas != 7 || r.FailoverSlots != 7 {
		t.Fatalf("warm demotion certificate: %+v %v", r, err)
	}
	if err := fx.Store.UpdateInstanceState(fx.Ctx, ins.ID, string(state.StateRunning)); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.SetComputeNodeActive(fx.Ctx, fleet[1].ID, false); err != nil {
		t.Fatal(err)
	}
	manifest.ExecutionMode = api.ExecutionModeRequest
	manifest.ServiceReplicas = nil
	if _, err := fx.Store.UpdateApp(fx.Ctx, a.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatalf("removing service declaration refused: %v", err)
	}
	r, err = fx.Store.ServiceCapacityProtection(fx.Ctx)
	if err != nil || r.ReservedReplicas != 0 || r.HealthyNodes != 1 {
		t.Fatalf("service declaration not released: %+v %v", r, err)
	}
}

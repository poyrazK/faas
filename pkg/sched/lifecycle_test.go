package sched

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type routeAckNotifier struct {
	*fakeNotifier
	events chan db.Notification
	nodes  []string
}

func newRouteAckNotifier(nodes ...string) *routeAckNotifier {
	return &routeAckNotifier{
		fakeNotifier: &fakeNotifier{},
		events:       make(chan db.Notification, len(nodes)),
		nodes:        append([]string(nil), nodes...),
	}
}

func (n *routeAckNotifier) Subscribe(_ context.Context, _ []string) (<-chan db.Notification, error) {
	return n.events, nil
}

func (n *routeAckNotifier) Notify(ctx context.Context, channel, payload string) error {
	if err := n.fakeNotifier.Notify(ctx, channel, payload); err != nil {
		return err
	}
	if channel != db.NotifyDeploymentRouteChanged {
		return nil
	}
	changed, err := db.ParseDeploymentRouteChangedPayload(payload)
	if err != nil {
		return err
	}
	for _, node := range n.nodes {
		body, err := json.Marshal(db.DeploymentRouteAckPayload{Generation: changed.Generation, Node: node})
		if err != nil {
			return err
		}
		n.events <- db.Notification{Channel: db.NotifyDeploymentRouteAck, Payload: string(body)}
	}
	return nil
}

func TestInstanceModeForApp(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want state.InstanceMode
	}{
		{name: "empty", want: state.InstanceModeNormal},
		{name: "request", mode: api.ExecutionModeRequest, want: state.InstanceModeNormal},
		{name: "service", mode: api.ExecutionModeService, want: state.InstanceModeService},
		{name: "worker", mode: api.ExecutionModeWorker, want: state.InstanceModeWorker},
		{name: "job", mode: api.ExecutionModeJob, want: state.InstanceModeJob},
		{name: "unknown", mode: "future", want: state.InstanceModeNormal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := state.App{Manifest: state.AppManifest{ExecutionMode: tt.mode}}
			if got := instanceModeForApp(app); got != string(tt.want) {
				t.Fatalf("mode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyServiceReplicasSeparatesReadiness(t *testing.T) {
	replicas := []state.Instance{
		{Mode: string(state.InstanceModeService), State: string(state.StateRunning)},
		{Mode: string(state.InstanceModeService), State: string(state.StateWaking)},
		{Mode: string(state.InstanceModeService), State: string(state.StateColdBooting)},
		{Mode: string(state.InstanceModeService), State: string(state.StateSnapshotting)},
		{Mode: string(state.InstanceModeService), State: string(state.StateMigrating)},
		{Mode: string(state.InstanceModeService), State: string(state.StateParked)},
		{Mode: string(state.InstanceModeService), State: string(state.StateFailed)},
	}

	got := classifyServiceReplicas(replicas)
	if got.ready != 1 || got.starting != 2 || got.draining != 2 || got.unavailable != 2 {
		t.Fatalf("service replica status = %+v, want ready:1 starting:2 draining:2 unavailable:2", got)
	}
	if got.inFlight() != 4 || got.managed() != 5 {
		t.Fatalf("service replica capacity = in_flight:%d managed:%d, want in_flight:4 managed:5", got.inFlight(), got.managed())
	}
}

// adr: 208 — only active, named compute nodes with a gateway endpoint
// participate in the service-route acknowledgement barrier.
func TestServingGatewayNamesFiltersNonServingNodes(t *testing.T) {
	servingRole := "compute-only"
	controlRole := "control-plane"
	target := "https://10.0.0.2:8443"
	nodes := []state.ComputeNode{
		{Name: "gw-a", Active: true, Role: &servingRole, GatewayTargetURL: &target},
		{Name: "", Active: true, Role: &servingRole, GatewayTargetURL: &target},
		{Name: "inactive", Active: false, Role: &servingRole, GatewayTargetURL: &target},
		{Name: "control", Active: true, Role: &controlRole, GatewayTargetURL: &target},
		{Name: "no-target", Active: true, Role: &servingRole},
	}
	got := servingGatewayNames(nodes)
	if len(got) != 1 {
		t.Fatalf("serving gateways = %v, want only gw-a", got)
	}
	if _, ok := got["gw-a"]; !ok {
		t.Fatalf("serving gateways = %v, want gw-a", got)
	}
}

// adr: 208 — a cutover may proceed only after every serving gateway
// acknowledges the published route generation.
func TestWaitForServiceRouteConvergenceRequiresEveryServingGateway(t *testing.T) {
	store := state.NewMemStore()
	role := "compute-only"
	target := "https://10.0.0.2:8443"
	for _, name := range []string{"gw-a", "gw-b"} {
		if _, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
			Name: name, TargetURL: "tcp://" + name + ":50051", Active: true,
			Role: &role, GatewayTargetURL: &target,
		}); err != nil {
			t.Fatalf("CreateComputeNode(%s): %v", name, err)
		}
	}
	notifier := newRouteAckNotifier("gw-a", "gw-b")
	e := newEngine(t, store, &fakeVMM{}, notifier, "1.10.0")

	acknowledgedAt, fleetBarrier, ok := e.waitForServiceRouteConvergence(
		context.Background(), "app-1", "dep-2")
	if !ok || !fleetBarrier || acknowledgedAt.IsZero() {
		t.Fatalf("route convergence = (%v, %v, %v), want timestamp/true/true", acknowledgedAt, fleetBarrier, ok)
	}
	if notifier.count(db.NotifyDeploymentRouteChanged) != 1 {
		t.Fatalf("route-change notifications = %d, want 1", notifier.count(db.NotifyDeploymentRouteChanged))
	}
}

// adr: 208 — a configured fleet without a registered serving gateway fails
// closed instead of silently using the single-box compatibility path.
func TestWaitForServiceRouteConvergenceFailsClosedWithoutFleetGateway(t *testing.T) {
	store := state.NewMemStore()
	role := "compute-only"
	if _, err := store.CreateComputeNode(context.Background(), state.ComputeNode{
		Name: "compute-without-gateway", TargetURL: "tcp://10.0.0.3:50051",
		Active: true, Role: &role,
	}); err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, store, &fakeVMM{}, newRouteAckNotifier(), "1.10.0")

	acknowledgedAt, fleetBarrier, ok := e.waitForServiceRouteConvergence(
		context.Background(), "app-1", "dep-2")
	if ok || !fleetBarrier || !acknowledgedAt.IsZero() {
		t.Fatalf("route convergence = (%v, %v, %v), want zero/true/false", acknowledgedAt, fleetBarrier, ok)
	}
}

// adr: 208 — an operator abort is executed as a reverse handoff. Even on the
// single-box compatibility path, the predecessor is restored before the
// candidate becomes terminal; the generic API request itself never performs
// that terminal transition.
func TestReverseServiceRolloutCompletesRequestedAbort(t *testing.T) {
	store := state.NewMemStore()
	_, app, stable := seedApp(t, store, api.PlanPro, 128, 5)
	if err := store.SetDeploymentCanaryState(context.Background(), stable.ID, "none", 0, 0, time.Time{}, "complete"); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-time.Minute)
	rollout, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:            app.ID,
		Kind:             state.DeploymentKindImage,
		ImageDigest:      "sha256:service-next",
		Status:           state.DeployLive,
		Scope:            stable.Scope,
		TrafficPercent:   0,
		RolloutState:     "rolling_out",
		RolloutStartedAt: &started,
		CreatedAt:        stable.CreatedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	requested, _, err := store.RecoverRollout(context.Background(), app.ID, "abort", "operator stop")
	if err != nil {
		t.Fatalf("request abort: %v", err)
	}
	if requested.RolloutState != "rolling_out" || !requested.ServiceRolloutHandoff.ActiveAbort() {
		t.Fatalf("requested rollout = %+v; want active abort intent", requested)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if !e.reverseServiceRollout(context.Background(), app, requested) {
		t.Fatal("reverseServiceRollout returned false")
	}
	got, err := store.DeploymentByID(context.Background(), rollout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeploySuperseded || got.RolloutState != "aborted" || got.ServiceRolloutHandoff.Phase != state.ServiceRolloutPhaseComplete {
		t.Fatalf("candidate after reverse handoff = %+v; want superseded/aborted/complete", got)
	}
	old, err := store.DeploymentByID(context.Background(), stable.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != state.DeployLive || old.TrafficPercent != 100 {
		t.Fatalf("predecessor after reverse handoff = status:%s traffic:%d; want live/100", old.Status, old.TrafficPercent)
	}
}

// adr: 137 — service replica readiness and desired-capacity projection.
func TestObserveServiceReplicaStatusProjectsCapacity(t *testing.T) {
	store := state.NewMemStore()
	_, app, deployment := seedApp(t, store, api.PlanPro, 128, 5)
	app.Manifest = state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 4, Desired: 4},
	}
	for i, replicaState := range []state.State{state.StateRunning, state.StateColdBooting, state.StateFailed} {
		if _, err := store.CreateInstanceWithMode(context.Background(), app.ID, deployment.ID,
			string(replicaState), app.RAMMB, "node-1", "status-wake-"+string(rune('1'+i)), string(state.InstanceModeService)); err != nil {
			t.Fatal(err)
		}
	}
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	e.observeServiceReplicaStatus(context.Background(), app, []state.Deployment{deployment})

	body := getMetricsBody(t, ops)
	for _, want := range []string{
		`schedd_service_replicas{app="` + app.ID + `",state="desired"} 4`,
		`schedd_service_replicas{app="` + app.ID + `",state="ready"} 1`,
		`schedd_service_replicas{app="` + app.ID + `",state="starting"} 1`,
		`schedd_service_replicas{app="` + app.ID + `",state="draining"} 0`,
		`schedd_service_replicas{app="` + app.ID + `",state="unavailable"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in /metrics:\n%s", want, body)
		}
	}
}

func TestAllocateServiceReplicaTargets(t *testing.T) {
	tests := []struct {
		name    string
		deploys []state.Deployment
		desired int
		want    map[string]int
	}{
		{
			name: "single generation keeps full target",
			deploys: []state.Deployment{
				{ID: "stable", TrafficPercent: 100},
			},
			desired: 3,
			want:    map[string]int{"stable": 3},
		},
		{
			name: "weighted canary",
			deploys: []state.Deployment{
				{ID: "canary", TrafficPercent: 25},
				{ID: "stable", TrafficPercent: 75},
			},
			desired: 4,
			want:    map[string]int{"canary": 1, "stable": 3},
		},
		{
			name: "positive generations get a warm floor",
			deploys: []state.Deployment{
				{ID: "canary", TrafficPercent: 1},
				{ID: "stable", TrafficPercent: 99},
			},
			desired: 3,
			want:    map[string]int{"canary": 1, "stable": 2},
		},
		{
			name: "small target favors higher traffic",
			deploys: []state.Deployment{
				{ID: "canary", TrafficPercent: 10},
				{ID: "stable", TrafficPercent: 90},
			},
			desired: 1,
			want:    map[string]int{"canary": 0, "stable": 1},
		},
		{
			name: "invalid split prefers newest generation",
			deploys: []state.Deployment{
				{ID: "newest", TrafficPercent: 0},
				{ID: "older", TrafficPercent: 0},
			},
			desired: 2,
			want:    map[string]int{"newest": 2, "older": 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allocateServiceReplicaTargets(tt.deploys, tt.desired)
			for id, want := range tt.want {
				if got[id] != want {
					t.Errorf("target[%q] = %d, want %d", id, got[id], want)
				}
			}
			if len(got) != len(tt.want) {
				t.Fatalf("target map has %d entries, want %d: %+v", len(got), len(tt.want), got)
			}
			sum := 0
			for _, target := range got {
				sum += target
			}
			if sum != tt.desired {
				t.Fatalf("allocated replicas = %d, want %d: %+v", sum, tt.desired, got)
			}
		})
	}
}

// TestDrainDeploymentInstances_ReleasesHotSupersededRevision covers the
// request-mode cutover path. A hot old revision must be parked before a new
// request can consume a one-instance plan's only slot.
func TestDrainDeploymentInstances_ReleasesHotSupersededRevision(t *testing.T) {
	store := state.NewMemStore()
	_, app, oldDep := seedApp(t, store, api.PlanFree, 128, 1)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake old revision: %v", err)
	}
	newDep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:new", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.MarkDeploymentLive(context.Background(), newDep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	e.drainDeploymentInstances(context.Background(), oldDep.ID, true)
	old, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if old.State != string(state.StateParked) {
		t.Fatalf("old instance state = %q, want parked", old.State)
	}
	if vmm.snapshots != 1 {
		t.Fatalf("old revision snapshots = %d, want 1", vmm.snapshots)
	}
	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake new revision after drain: %v", err)
	}
}

func TestReconcileServiceApp_AllocatesAcrossLiveGenerations(t *testing.T) {
	store := state.NewMemStore()
	_, app, stable := seedApp(t, store, api.PlanPro, 128, 5)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 3},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.CreateInstanceWithMode(context.Background(), app.ID, stable.ID,
			string(state.StateRunning), app.RAMMB, "node-1", "stable-wake-"+string(rune('1'+i)), string(state.InstanceModeService)); err != nil {
			t.Fatal(err)
		}
	}

	canary, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:            app.ID,
		Kind:             state.DeploymentKindImage,
		ImageDigest:      "sha256:canary",
		Status:           state.DeployPending,
		TrafficPercent:   10,
		CanaryTotalSteps: 4,
		Scope:            stable.Scope,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(context.Background(), canary.ID); err != nil {
		t.Fatal(err)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.ReconcileServiceApp(context.Background(), app.ID)

	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	runningByDeployment := map[string]int{}
	parkedByDeployment := map[string]int{}
	for _, ins := range instances {
		if ins.Mode != string(state.InstanceModeService) {
			continue
		}
		switch state.State(ins.State) {
		case state.StateRunning:
			runningByDeployment[ins.DeploymentID]++
		case state.StateParked:
			parkedByDeployment[ins.DeploymentID]++
		}
	}
	if runningByDeployment[stable.ID] != 2 || runningByDeployment[canary.ID] != 1 {
		t.Fatalf("running replicas = stable:%d canary:%d, want stable:2 canary:1; all=%+v",
			runningByDeployment[stable.ID], runningByDeployment[canary.ID], runningByDeployment)
	}
	if parkedByDeployment[stable.ID] != 1 {
		t.Fatalf("parked stable replicas = %d, want 1; all=%+v", parkedByDeployment[stable.ID], parkedByDeployment)
	}
}

func TestReconcileServiceApp_ReadinessGatedRolloutPromotesAfterBoot(t *testing.T) {
	store := state.NewMemStore()
	_, app, stable := seedApp(t, store, api.PlanPro, 128, 5)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 3},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := store.CreateInstanceWithMode(context.Background(), app.ID, stable.ID,
			string(state.StateRunning), app.RAMMB, "node-1", "stable-rollout-"+string(rune('1'+i)), string(state.InstanceModeService)); err != nil {
			t.Fatal(err)
		}
	}
	rollout, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:service-next",
		Status: state.DeployPending, Scope: stable.Scope, TrafficPercent: 0,
		RolloutState: "rolling_out",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(context.Background(), rollout.ID); err != nil {
		t.Fatal(err)
	}

	// A zero-weight rollout must not become the request wake target while the
	// predecessor is still serving.
	if got, err := store.LiveDeployment(context.Background(), app.ID); err != nil || got.ID != stable.ID {
		t.Fatalf("LiveDeployment before readiness = %q, %v; want stable %q", got.ID, err, stable.ID)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	deadline := time.Now().Add(2 * time.Second)
	for {
		e.ReconcileServiceApp(context.Background(), app.ID)
		got, readErr := store.DeploymentByID(context.Background(), rollout.ID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if got.RolloutState == "complete" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service rollout did not complete: %+v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}

	got, err := store.DeploymentByID(context.Background(), rollout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeployLive || got.TrafficPercent != 100 || got.RolloutState != "complete" {
		t.Fatalf("promoted rollout = status:%q traffic:%d state:%q; want live/100/complete", got.Status, got.TrafficPercent, got.RolloutState)
	}
	old, err := store.DeploymentByID(context.Background(), stable.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != state.DeploySuperseded || old.TrafficPercent != 0 {
		t.Fatalf("old rollout = status:%q traffic:%d; want superseded/0", old.Status, old.TrafficPercent)
	}
}

func TestReconcileServiceApp_ReadinessTimeoutRestoresPrevious(t *testing.T) {
	store := state.NewMemStore()
	_, app, stable := seedApp(t, store, api.PlanPro, 128, 5)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstanceWithMode(context.Background(), app.ID, stable.ID,
		string(state.StateRunning), app.RAMMB, "node-1", "stable-timeout", string(state.InstanceModeService)); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-serviceRolloutTimeout - time.Minute)
	rollout, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:service-bad",
		Status: state.DeployPending, Scope: stable.Scope, TrafficPercent: 0,
		RolloutState: "rolling_out", RolloutStartedAt: &started,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(context.Background(), rollout.ID); err != nil {
		t.Fatal(err)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.ReconcileServiceApp(context.Background(), app.ID)

	failed, err := store.DeploymentByID(context.Background(), rollout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != state.DeploySuperseded || failed.RolloutState != "aborted" || failed.RolloutAbortedReason != "readiness timeout" {
		t.Fatalf("timed-out rollout = status:%q state:%q reason:%q; want superseded/aborted/readiness timeout", failed.Status, failed.RolloutState, failed.RolloutAbortedReason)
	}
	restored, err := store.DeploymentByID(context.Background(), stable.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != state.DeployLive || restored.TrafficPercent != 100 {
		t.Fatalf("restored deployment = status:%q traffic:%d; want live/100", restored.Status, restored.TrafficPercent)
	}
}

// adr: 208 — a rollout consumes its bounded surge before retiring the healthy
// predecessor, even when the steady-state app ceiling is an exact fit.
func TestReconcileServiceApp_ExactFitRolloutUsesSurgeBeforeRetiringPredecessor(t *testing.T) {
	store := state.NewMemStore()
	_, app, stable := seedApp(t, store, api.PlanPro, 128, 1)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 1, Desired: 1},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	old, err := store.CreateInstanceWithMode(context.Background(), app.ID, stable.ID,
		string(state.StateRunning), app.RAMMB, "node-1", "stable-exact-fit", string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	limits := api.MustLimitsFor(api.PlanPro)
	if err := e.ledger.Admit(Request{
		Instance: old.ID, AppID: app.ID, DeploymentID: stable.ID, Plan: api.PlanPro,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
		NodeID: "node-1",
	}); err != nil {
		t.Fatalf("seed predecessor ledger reservation: %v", err)
	}

	rollout, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:service-exact-fit",
		Status: state.DeployPending, Scope: stable.Scope, TrafficPercent: 0,
		RolloutState: "rolling_out",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(context.Background(), rollout.ID); err != nil {
		t.Fatal(err)
	}

	e.ReconcileServiceApp(context.Background(), app.ID)
	got, err := store.DeploymentByID(context.Background(), rollout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeployLive || got.TrafficPercent != 100 || got.RolloutState != "complete" {
		t.Fatalf("exact-fit rollout = status:%q traffic:%d state:%q; want live/100/complete", got.Status, got.TrafficPercent, got.RolloutState)
	}
	if got, err := store.InstanceByID(context.Background(), old.ID); err != nil {
		t.Fatal(err)
	} else if got.State != string(state.StateParked) {
		t.Fatalf("predecessor after exact-fit promotion = %q; want parked", got.State)
	}
}

// adr: 208 — exhausted physical capacity holds the rollout without parking
// the last healthy predecessor.
func TestReconcileServiceApp_PhysicalCapacityHoldsWithoutParkingPredecessor(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, stable := seedApp(t, store, api.PlanPro, 128, 1)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 1, Desired: 1},
	}
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	node.AdmissionCeilingMB = app.RAMMB + api.PerVMOverheadMB
	node, err = store.UpsertComputeNodeFromOperator(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.CreateInstanceWithMode(ctx, app.ID, stable.ID,
		string(state.StateRunning), app.RAMMB, node.ID, "stable-capacity-full", string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	limits := api.MustLimitsFor(api.PlanPro)
	if err := e.ledger.Admit(Request{
		Instance: old.ID, AppID: app.ID, DeploymentID: stable.ID, Plan: api.PlanPro,
		RAMMB: app.RAMMB, VCPU: limits.VCPU, MaxConcurrency: app.MaxConcurrency,
		NodeID: node.ID, NodeCeilingMB: node.AdmissionCeilingMB, VCPUBudget: node.VCPUBudget,
	}); err != nil {
		t.Fatalf("seed predecessor ledger reservation: %v", err)
	}
	rollout, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:service-capacity-hold",
		Status: state.DeployPending, Scope: stable.Scope, TrafficPercent: 0,
		RolloutState: "rolling_out",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, rollout.ID); err != nil {
		t.Fatal(err)
	}

	e.ReconcileServiceApp(ctx, app.ID)
	predecessor, err := store.InstanceByID(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if predecessor.State != string(state.StateRunning) {
		t.Fatalf("predecessor state = %q, want running while candidate lacks physical capacity", predecessor.State)
	}
	pending, err := store.DeploymentByID(ctx, rollout.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != state.DeployLive || pending.TrafficPercent != 0 || pending.RolloutState != "rolling_out" {
		t.Fatalf("capacity-held rollout = status:%q traffic:%d state:%q; want live/0/rolling_out",
			pending.Status, pending.TrafficPercent, pending.RolloutState)
	}
	if count, err := store.CountLiveInstancesByDeployment(ctx, rollout.ID); err != nil {
		t.Fatal(err)
	} else if count != 0 {
		t.Fatalf("candidate live instances = %d, want 0", count)
	}
}

func TestConvergeServiceReplicas_AdmitsDeficit(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 2},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.convergeServiceReplicas(context.Background(), dep.ID)

	count, err := store.CountLiveInstancesByDeployment(context.Background(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("live service replicas = %d, want 2", count)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ins := range instances {
		if ins.Mode != string(state.InstanceModeService) {
			t.Fatalf("instance mode = %q, want service", ins.Mode)
		}
	}
}

func TestConvergeServiceReplicasSpreadsAcrossComputeNodes(t *testing.T) {
	store := state.NewMemStore()
	localID, remoteID := seedTwoNodes(t, store)
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 2},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}

	// A normal request wake would follow this valid sticky-warm hint. Service
	// reconciliation must ignore it so a two-node fleet provides failure
	// isolation for the desired replica set.
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithWarmAffinity(NewWarmAffinity(time.Minute))
	e.warmAffinity.RecordWake(app.ID, localID)
	e.convergeServiceReplicas(context.Background(), dep.ID)

	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	byNode := map[string]int{}
	for _, ins := range instances {
		if ins.Mode == string(state.InstanceModeService) && state.State(ins.State).CountsForConcurrency() {
			byNode[ins.NodeID]++
		}
	}
	if byNode[localID] != 1 || byNode[remoteID] != 1 {
		t.Fatalf("service replicas by node = %+v, want one on each compute node (%s, %s)", byNode, localID, remoteID)
	}
}

func TestConvergeServiceReplicas_IgnoresNonServiceInstances(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	normal, err := store.CreateInstance(context.Background(), app.ID, dep.ID,
		string(state.StateRunning), app.RAMMB, "node-1", "wake-normal")
	if err != nil {
		t.Fatal(err)
	}
	if normal.Mode != "" {
		t.Fatalf("legacy instance mode = %q, want empty normal-mode fixture", normal.Mode)
	}
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.convergeServiceReplicas(context.Background(), dep.ID)

	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	serviceCount := 0
	for _, ins := range instances {
		if ins.Mode == string(state.InstanceModeService) && state.State(ins.State).CountsForConcurrency() {
			serviceCount++
		}
	}
	if serviceCount != 1 {
		t.Fatalf("service replicas = %d, want 1 (normal-mode row must not satisfy the target)", serviceCount)
	}
	gotNormal, err := store.InstanceByID(context.Background(), normal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotNormal.State != string(state.StateParked) {
		t.Fatalf("incompatible normal-mode state = %q, want PARKED", gotNormal.State)
	}
}

func TestConvergeServiceReplicas_StopsInFlightNonServiceWakeAfterModeSwitch(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	requestWake, err := store.CreateInstance(context.Background(), app.ID, dep.ID,
		string(state.StateColdBooting), app.RAMMB, "node-1", "wake-request")
	if err != nil {
		t.Fatal(err)
	}
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.convergeServiceReplicas(context.Background(), dep.ID)

	gotRequest, err := store.InstanceByID(context.Background(), requestWake.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotRequest.State != string(state.StateStopped) {
		t.Fatalf("in-flight request wake state = %q, want STOPPED after mode switch", gotRequest.State)
	}
	count, err := store.CountLiveInstancesByDeployment(context.Background(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("live instances after mode switch = %d, want one service replica", count)
	}
}

func TestConvergeServiceReplicas_NoOpForNonService(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeRequest,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 2},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.convergeServiceReplicas(context.Background(), dep.ID)
	count, err := store.CountLiveInstancesByDeployment(context.Background(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("non-service convergence admitted %d instances", count)
	}
}

func TestConvergeServiceReplicas_ParksSurplus(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 3, Desired: 1},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
			string(state.StateRunning), app.RAMMB, "node-1", "wake-service-"+string(rune('1'+i)), string(state.InstanceModeService)); err != nil {
			t.Fatal(err)
		}
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.convergeServiceReplicas(context.Background(), dep.ID)

	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatal(err)
	}
	running, parked := 0, 0
	for _, ins := range instances {
		if ins.DeploymentID != dep.ID || ins.Mode != string(state.InstanceModeService) {
			continue
		}
		switch state.State(ins.State) {
		case state.StateRunning:
			running++
		case state.StateParked:
			parked++
		}
	}
	if running != 1 || parked != 1 {
		t.Fatalf("service states = running:%d parked:%d, want running:1 parked:1", running, parked)
	}
}

func TestConvergeServiceReplicas_PreservesReadyWhileReplacementStarts(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	ready, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
		string(state.StateRunning), app.RAMMB, "node-1", "wake-ready", string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	starting, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
		string(state.StateColdBooting), app.RAMMB, "node-1", "wake-starting", string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.convergeServiceReplicas(context.Background(), dep.ID)

	gotReady, err := store.InstanceByID(context.Background(), ready.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotReady.State != string(state.StateRunning) {
		t.Fatalf("ready replica state = %q, want RUNNING", gotReady.State)
	}
	gotStarting, err := store.InstanceByID(context.Background(), starting.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotStarting.State != string(state.StateColdBooting) {
		t.Fatalf("starting replica state = %q, want COLD_BOOTING", gotStarting.State)
	}
}

func TestConvergeServiceReplicas_ReplacesTerminalReplica(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 2},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
		string(state.StateFailed), app.RAMMB, "node-1", "wake-failed", string(state.InstanceModeService)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
		string(state.StateRunning), app.RAMMB, "node-1", "wake-ready", string(state.InstanceModeService)); err != nil {
		t.Fatal(err)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.convergeServiceReplicas(context.Background(), dep.ID)

	count, err := store.CountLiveInstancesByDeployment(context.Background(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("live service replicas = %d, want 2 after terminal replacement", count)
	}
}

func TestConvergeServiceReplicas_DrainsAfterModeSwitch(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	service := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &service}); err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID,
		string(state.StateRunning), app.RAMMB, "node-1", "wake-service", string(state.InstanceModeService))
	if err != nil {
		t.Fatal(err)
	}
	request := state.AppManifest{ExecutionMode: api.ExecutionModeRequest}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &request}); err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.convergeServiceReplicas(context.Background(), dep.ID)

	got, err := store.InstanceByID(context.Background(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != string(state.StateParked) {
		t.Fatalf("mode-switched service state = %q, want PARKED", got.State)
	}
}

func TestWakeDoesNotReturnMismatchedRunningInstance(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	manifest := state.AppManifest{
		ExecutionMode:   api.ExecutionModeService,
		ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 2, Desired: 1},
	}
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	requestInstance, err := store.CreateInstance(context.Background(), app.ID, dep.ID,
		string(state.StateRunning), app.RAMMB, "node-1", "wake-request")
	if err != nil {
		t.Fatal(err)
	}
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	res, err := e.Wake(context.Background(), app.ID, dep.ID, "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if res.InstanceID == requestInstance.ID {
		t.Fatalf("Wake returned mismatched request instance %q", requestInstance.ID)
	}
	got, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != string(state.InstanceModeService) {
		t.Fatalf("woken instance mode = %q, want service", got.Mode)
	}
}

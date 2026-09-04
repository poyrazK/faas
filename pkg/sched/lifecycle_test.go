package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

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

// adr: 137

package sched_test

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStartupDeadlineForApp(t *testing.T) {
	tests := []struct {
		name     string
		manifest state.AppManifest
		plan     api.Plan
		want     int32
	}{
		// Pro's 60 s default is capped at the 30 s cold-boot window (Hunt 12 H12-10).
		{name: "plan default", plan: api.PlanPro, want: api.ColdBootReadyWindowS},
		{name: "manifest override", manifest: state.AppManifest{StartupDeadlineS: 17}, plan: api.PlanPro, want: 17},
		{name: "unknown plan preserves vmmd default", plan: api.Plan("unknown"), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := state.App{Manifest: tt.manifest}
			if got := sched.StartupDeadlineForApp(app, tt.plan); got != tt.want {
				t.Fatalf("startup deadline = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestExecutionModeForApp(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{name: "empty defaults request", want: api.ExecutionModeRequest},
		{name: "request", mode: api.ExecutionModeRequest, want: api.ExecutionModeRequest},
		{name: "service", mode: api.ExecutionModeService, want: api.ExecutionModeService},
		{name: "worker", mode: api.ExecutionModeWorker, want: api.ExecutionModeWorker},
		{name: "job", mode: api.ExecutionModeJob, want: api.ExecutionModeJob},
		{name: "unknown fails safe", mode: "magic", want: api.ExecutionModeRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := state.App{Manifest: state.AppManifest{ExecutionMode: tt.mode}}
			if got := sched.ExecutionModeForApp(app); got != tt.want {
				t.Fatalf("execution mode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppSpecToProtoCarriesExecutionMode(t *testing.T) {
	got := (sched.AppSpec{ExecutionMode: api.ExecutionModeWorker}).ToProto().GetExecutionMode()
	if got != api.ExecutionModeWorker {
		t.Fatalf("execution mode = %q, want %q", got, api.ExecutionModeWorker)
	}
}

func TestAppSpecToProtoCarriesWorkerPushDelivery(t *testing.T) {
	if !(sched.AppSpec{WorkerPushDelivery: true}).ToProto().GetWorkerPushDelivery() {
		t.Fatal("worker_push_delivery was not forwarded to vmmd")
	}
}

// ADR 20261021153953: only a worker-mode app with a non-retired push binding
// may boot with its HTTP delivery listener bound.
func TestWorkerPushDeliveryFollowsPushBindings(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "worker-push-delivery@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := sched.NewEngine(ctx, store, sched.NewNodeLedger(), nil, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug, mode string) state.App {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp,
			WorkloadClass: state.WorkloadClassWorker, Manifest: state.AppManifest{ExecutionMode: mode}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	bind := func(app state.App, mode string, enabled bool) {
		if _, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders",
			QueueName: "orders", Mode: mode, WorkloadClass: state.WorkloadClassWorker, Enabled: enabled, MaxConcurrency: 1}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(app state.App, want bool) {
		t.Helper()
		got, err := sched.WorkerPushDelivery(engine, ctx, app)
		if err != nil || got != want {
			t.Fatalf("%s push delivery = (%v, %v), want %v", app.Slug, got, err, want)
		}
	}

	push := newApp("push-worker", api.ExecutionModeWorker)
	bind(push, "push", true)
	check(push, true)

	paused := newApp("paused-push-worker", api.ExecutionModeWorker)
	bind(paused, "push", false)
	check(paused, true)

	pull := newApp("pull-worker", api.ExecutionModeWorker)
	bind(pull, "pull", true)
	check(pull, false)

	check(newApp("bare-worker", api.ExecutionModeWorker), false)
	// Request mode never needs the flag, whatever its bindings.
	request := newApp("push-request", api.ExecutionModeRequest)
	check(request, false)
}

// A qualification candidate's frozen review names its push consumers.
func TestQualificationWorkerPushDeliveryFollowsFrozenBindings(t *testing.T) {
	candidate := func(mode string, queueModes ...string) state.EnvironmentWorkloadQualificationRequest {
		request := state.EnvironmentWorkloadQualificationRequest{ExecutionMode: mode}
		request.FrozenInputs.QueueBindings = map[string]state.EnvironmentScopedQueueBinding{}
		for i, queueMode := range queueModes {
			binding := state.EnvironmentScopedQueueBinding{}
			binding.Contract.Mode = queueMode
			request.FrozenInputs.QueueBindings[fmt.Sprintf("q%d", i)] = binding
		}
		return request
	}
	for _, tt := range []struct {
		name    string
		request state.EnvironmentWorkloadQualificationRequest
		want    bool
	}{
		{"push worker", candidate(api.ExecutionModeWorker, "pull", "push"), true},
		{"pull worker", candidate(api.ExecutionModeWorker, "pull"), false},
		{"bare worker", candidate(api.ExecutionModeWorker), false},
		{"push function", candidate(api.ExecutionModeRequest, "push"), false},
	} {
		if got := sched.QualificationWorkerPushDelivery(tt.request); got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestAppSpecToProtoCarriesStartupCPUBoostOptOut(t *testing.T) {
	got := (sched.AppSpec{DisableStartupCPUBoost: true}).ToProto().GetDisableStartupCpuBoost()
	if !got {
		t.Fatal("disable_startup_cpu_boost was not forwarded to vmmd")
	}
}

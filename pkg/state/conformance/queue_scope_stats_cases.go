package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func testQueueDemandScope(t *testing.T, fx *Fixture) {
	project, app := invocationScopeApp(t, fx)
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	for _, item := range []struct {
		scope string
		queue string
		state state.InvocationState
		lease *time.Time
	}{
		{"production", "orders", state.InvocationPending, nil},
		{"production", "orders", state.InvocationDispatching, &future},
		{"production", "orders", state.InvocationDispatching, &past},
		{"production", "orders", state.InvocationDispatching, nil},
		{"production", "orders", state.InvocationDeadLetter, nil},
		{"production", "payments", state.InvocationPending, nil},
		{"production", "", state.InvocationPending, nil},
		{"staging", "orders", state.InvocationPending, nil},
		{"staging", "orders", state.InvocationDeadLetter, nil},
		{"staging", "orders", state.InvocationCompleted, nil},
		{"a", "orders", state.InvocationPending, nil},
		{"1", "orders", state.InvocationPending, nil},
	} {
		_, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: app.ID, AccountID: fx.Account.ID,
			Source: state.InvocationQueue, DeploymentScope: item.scope, QueueName: item.queue,
			State: item.state, LeaseExpiresAt: item.lease, DueAt: past})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Project removal must not move the already accepted backlog.
	if err := fx.Store.DeleteProject(fx.Ctx, project.ID); err != nil {
		t.Fatal(err)
	}
	_, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: app.ID, AccountID: fx.Account.ID,
		Source: state.InvocationQueue, QueueName: "orders", DueAt: past})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		scope           string
		depth, inflight int
		dead            int
	}{
		{"production", 6, 1, 1}, {"staging", 1, 0, 1}, {"default", 1, 0, 0}, {"missing", 0, 0, 0},
		{"a", 1, 0, 0}, {"1", 1, 0, 0},
	} {
		stats, err := fx.Store.QueueStateInScope(fx.Ctx, app.ID, item.scope)
		if err != nil || stats.Depth != item.depth || stats.InFlight != item.inflight || stats.DeadLetter != item.dead ||
			(item.depth > 0) == stats.OldestPendingAt.IsZero() {
			t.Fatalf("scope %q stats = %+v, %v", item.scope, stats, err)
		}
	}
	for _, item := range []struct {
		scope, queue    string
		depth, inflight int
		dead            int
	}{
		{"production", "orders", 4, 1, 1}, {"production", "payments", 1, 0, 0},
		{"production", "", 1, 0, 0}, {"staging", "orders", 1, 0, 1}, {"default", "orders", 1, 0, 0},
		{"a", "orders", 1, 0, 0}, {"1", "orders", 1, 0, 0},
	} {
		stats, err := fx.Store.QueueStateForQueueInScope(fx.Ctx, app.ID, item.queue, item.scope)
		if err != nil || stats.Depth != item.depth || stats.InFlight != item.inflight || stats.DeadLetter != item.dead {
			t.Fatalf("scope/name %q/%q stats = %+v, %v", item.scope, item.queue, stats, err)
		}
	}
	for _, scope := range []string{"", "__all__", "UPPER", "-", "staging/other"} {
		if _, err := fx.Store.QueueStateInScope(fx.Ctx, app.ID, scope); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid scope %q accepted: %v", scope, err)
		}
		if _, err := fx.Store.QueueStateForQueueInScope(fx.Ctx, app.ID, "orders", scope); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid named scope %q accepted: %v", scope, err)
		}
	}
}

func testWorkerPoolHistory(t *testing.T, fx *Fixture) {
	neighbor, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: fx.App.ID,
		Scope: "staging", Kind: state.DeploymentKindImage, ImageDigest: "sha256:neighbor"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(dep state.Deployment, mode state.InstanceMode) state.Instance {
		t.Helper()
		ins, err := fx.Store.CreateInstanceWithMode(fx.Ctx, fx.App.ID, dep.ID,
			string(state.StateRunning), 128, fx.Node.ID, uuid.NewString(), string(mode))
		if err != nil {
			t.Fatal(err)
		}
		return ins
	}
	worker := create(fx.Deployment, state.InstanceModeWorker)
	other := create(neighbor, state.InstanceModeWorker)
	ordinary := create(fx.Deployment, state.InstanceModeNormal)
	terminated := time.Now().UTC().Truncate(time.Microsecond)
	if err := fx.Store.UpdateInstanceStateToTerminal(fx.Ctx, worker.ID, string(state.StateStopped), terminated); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.UpdateInstanceStateToTerminal(fx.Ctx, ordinary.ID, string(state.StateStopped), terminated.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		app, deployment string
		admitted, ended time.Time
	}{
		{fx.App.ID, fx.Deployment.ID, worker.StartedAt, terminated},
		{fx.App.ID, neighbor.ID, other.StartedAt, time.Time{}},
		{uuid.NewString(), fx.Deployment.ID, time.Time{}, time.Time{}},
		{fx.App.ID, uuid.NewString(), time.Time{}, time.Time{}},
	} {
		history, err := fx.Store.WorkerPoolHistory(fx.Ctx, item.app, item.deployment)
		if err != nil || !history.LastAdmissionAt.Equal(item.admitted) || !history.LastTerminationAt.Equal(item.ended) {
			t.Fatalf("generation %s history = %+v, want admission=%v termination=%v, err=%v",
				item.deployment, history, item.admitted, item.ended, err)
		}
	}
}

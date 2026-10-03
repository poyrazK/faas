package sched

// adr: 117 — snapshot_prepare timeout failures retain a typed deployment
// error and an operator-visible phase instead of a raw deadline string.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMarkPrimeFailedClassifiesSchedulerDeadline(t *testing.T) {
	store := state.NewMemStore()
	_, _, dep := seedApp(t, store, api.PlanScale, 1024, 10)
	if err := store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatalf("mark deployment snapshotting: %v", err)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.markPrimeFailed(context.Background(), dep.ID, errors.Join(errors.New("sched: prime: cold boot"), context.DeadlineExceeded))

	got, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("load deployment: %v", err)
	}
	if got.Status != state.DeployFailed {
		t.Fatalf("status = %q, want %q", got.Status, state.DeployFailed)
	}
	if got.ErrorCode != api.CodeStageSnapshotPrepareTimeout {
		t.Fatalf("error code = %q, want %q", got.ErrorCode, api.CodeStageSnapshotPrepareTimeout)
	}
	if !strings.Contains(got.Error, "startup_phase=scheduler_timeout") {
		t.Fatalf("error = %q, want scheduler startup phase", got.Error)
	}
}

func TestMarkPrimeFailedExplainsBeforeCheckpointFailure(t *testing.T) {
	store := state.NewMemStore()
	_, _, dep := seedApp(t, store, api.PlanScale, 1024, 10)
	if err := store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	cause := api.NewProblem(422, api.CodeBeforeCheckpointFailed, "Before checkpoint callback failed", "guest callback rejected capture")
	e.markPrimeFailed(context.Background(), dep.ID, errors.Join(errors.New("private vmmd detail"), cause))

	got, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeployFailed || got.ErrorCode != api.CodeBeforeCheckpointFailed {
		t.Fatalf("deployment status/code = %s/%s", got.Status, got.ErrorCode)
	}
	if got.ErrorHint == "" || got.ErrorWhy == "" || got.ErrorFix == "" {
		t.Fatalf("missing callback guidance: hint=%q why=%q fix=%q", got.ErrorHint, got.ErrorWhy, got.ErrorFix)
	}
	if strings.Contains(got.Error, "private vmmd detail") || !strings.Contains(got.Error, "before_checkpoint") {
		t.Fatalf("deployment error = %q, want safe hook-specific message", got.Error)
	}
}

func TestMarkPrimeFailedPersistsStartupPhaseGuidance(t *testing.T) {
	for _, tc := range []struct{ phase, want string }{
		{"guest_startup", "guest did not answer"},
		{"handler_healthcheck", "answered readiness probes"},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, _, dep := seedApp(t, store, api.PlanScale, 1024, 10)
			if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
				t.Fatal(err)
			}
			e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			problem := api.NewProblem(422, api.CodeAppStartupTimeout, "startup timeout", "startup_phase="+tc.phase+": deadline elapsed")
			e.markPrimeFailed(ctx, dep.ID, problem)
			got, err := store.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != state.DeployFailed || got.ErrorCode != api.CodeAppStartupTimeout || !strings.Contains(got.ErrorWhy, tc.want) || got.ErrorFix == "" {
				t.Fatalf("status=%s code=%s why=%q fix=%q", got.Status, got.ErrorCode, got.ErrorWhy, got.ErrorFix)
			}
		})
	}
}

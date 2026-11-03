package sched_test

// adr: 117 — snapshot_prepare timeout failures retain a typed deployment
// error and an operator-visible phase instead of a raw deadline string.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMarkPrimeFailedClassifiesSchedulerDeadline(t *testing.T) {
	store := state.NewMemStore()
	_, _, dep := sched.SeedApp(t, store, api.PlanScale, 1024, 10)
	if err := store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatalf("mark deployment snapshotting: %v", err)
	}

	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	e.MarkPrimeFailed(context.Background(), dep.ID, errors.Join(errors.New("sched: prime: cold boot"), context.DeadlineExceeded))

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
	_, _, dep := sched.SeedApp(t, store, api.PlanScale, 1024, 10)
	if err := store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}

	e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	cause := api.NewProblem(422, api.CodeBeforeCheckpointFailed, "Before checkpoint callback failed", "guest callback rejected capture")
	e.MarkPrimeFailed(context.Background(), dep.ID, errors.Join(errors.New("private vmmd detail"), cause))

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
		{"image_healthcheck", "fresh successful result"},
	} {
		t.Run(tc.phase, func(t *testing.T) {
			ctx := context.Background()
			store := state.NewMemStore()
			_, _, dep := sched.SeedApp(t, store, api.PlanScale, 1024, 10)
			if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
				t.Fatal(err)
			}
			e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			problem := api.NewProblem(422, api.CodeAppStartupTimeout, "startup timeout", "startup_phase="+tc.phase+": deadline elapsed")
			e.MarkPrimeFailed(ctx, dep.ID, problem)
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

// #5709: a release whose /healthz answered 503 was refused as
// "cold_boot_timeout: instance=…". The watchdog stops the priming guest and
// fails the deployment first; vmmd's reason for that guest arrives after it
// and now replaces the generic timeout. Any other terminal reason stays.
func TestMarkPrimeFailedRefinesTheWatchdogTimeout(t *testing.T) {
	healthcheck := api.NewProblem(422, api.CodeAppStartupTimeout, "app healthcheck did not become ready in time",
		"startup_phase=handler_healthcheck: guest i-1 answered 9 readiness probes at /healthz without a 2xx response (last status 503) before 24s")
	for _, tc := range []struct {
		name      string
		code      string
		message   string
		cause     error
		wantError string
	}{
		{"watchdog timeout, then vmmd's reason", api.CodeAppStartupTimeout, "cold_boot_timeout: instance=i-1", healthcheck, "last status 503"},
		{"watchdog timeout, then an untyped error", api.CodeAppStartupTimeout, "cold_boot_timeout: instance=i-1", errors.New("vmm: readiness: context canceled"), "cold_boot_timeout: instance=i-1"},
		{"another failure is kept", api.CodeDeployFailed, "image scan refused", healthcheck, "image scan refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			_, _, dep := sched.SeedApp(t, store, api.PlanScale, 1024, 10)
			if _, err := store.SetDeploymentFailedEx(context.Background(), dep.ID, tc.code, tc.message, "", "", "", nil); err != nil {
				t.Fatal(err)
			}
			e := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			e.MarkPrimeFailed(context.Background(), dep.ID, tc.cause)
			got, err := store.DeploymentByID(context.Background(), dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != state.DeployFailed || !strings.Contains(got.Error, tc.wantError) {
				t.Fatalf("status=%s error=%q, want failed with %q", got.Status, got.Error, tc.wantError)
			}
		})
	}
}

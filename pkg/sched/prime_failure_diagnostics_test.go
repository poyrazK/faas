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

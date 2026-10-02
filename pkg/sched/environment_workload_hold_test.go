// adr: 435 — environment intent and runtime ownership contracts.
package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type heldEnvironmentPrimeStore struct{ state.Store }

func (s heldEnvironmentPrimeStore) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	dep, err := s.Store.DeploymentByID(ctx, id)
	// Even corrupt internal metadata must hold execution, rather than fall
	// back to a normal deployment path and run an unqualified process.
	dep.EnvironmentWorkloadRuntime = "invalid-but-held"
	return dep, err
}

func TestEnvironmentWorkloadPrimeAndRecoveryHoldBeforeVMAdmission(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 2)
	engine := newEngine(t, heldEnvironmentPrimeStore{store}, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := engine.Prime(t.Context(), app.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	loop := NewLoop(nil, engine, testLog())
	if err := loop.recoverPrimeCandidate(t.Context(), dep.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	loop.waitPrimes()
	instances, err := store.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(instances) != 0 {
		t.Fatalf("held graph admitted a VM: %+v %v", instances, err)
	}
}

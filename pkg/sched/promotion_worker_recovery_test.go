package sched

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// The state suites verify promotion hold admission. Here the underlying durable
// job pin supplies admission while this adapter isolates the candidate pool path.
type heldWorkerRecoveryStore struct {
	MemStore *state.MemStore
	held     string
	readErr  error
}

func (s *heldWorkerRecoveryStore) PinnedProjectReleaseWorkers(context.Context, string) ([]string, error) {
	return nil, nil
}
func (s *heldWorkerRecoveryStore) HeldProjectReleaseWorkers(context.Context, string) ([]string, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return []string{s.held}, nil
}

func TestHeldWorkerCandidateRecoversAfterSchedulerRestart(t *testing.T) {
	base, app, old, current, _ := seedPinnedWorkerProject(t)
	store := &heldWorkerRecoveryStore{MemStore: base, held: old.ID}
	ctx := t.Context()
	check := func() state.Instance {
		t.Helper()
		rows, err := base.ListInstancesForApp(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		var held state.Instance
		for _, r := range rows {
			if state.State(r.State).CountsForConcurrency() {
				counts[r.DeploymentID]++
				if r.DeploymentID == old.ID {
					held = r
				}
			}
		}
		if counts[old.ID] != 1 || counts[current.ID] != 1 {
			t.Fatalf("candidate and serving pools: %v", counts)
		}
		return held
	}
	vmm := &recordingStopVMM{fakeVMM: &fakeVMM{}}
	engine := newEngine(t, statetest.NewMem(store), vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	resident := check()
	if err := base.UpdateInstanceState(ctx, resident.ID, string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	engine = newEngine(t, statetest.NewMem(store), vmm, &fakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	check()
	store.readErr = errors.New("promotion inventory unavailable")
	boots := vmm.coldBoots
	if err := engine.ReconcileWorkerPools(ctx, app.ID, TriggerWorkerPool); err == nil {
		t.Fatal("missing hold inventory allowed mutation")
	}
	if vmm.coldBoots != boots {
		t.Fatal("inventory failure started a worker")
	}
}

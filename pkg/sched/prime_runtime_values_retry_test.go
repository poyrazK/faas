package sched_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// valuesChangedStore fails runtime publication as if the app's config changed
// while the prime booted, a set number of times.
type valuesChangedStore struct {
	MemStore *state.MemStore
	mu       sync.Mutex
	failures int
	calls    int
}

func (s *valuesChangedStore) PublishOwnedInstanceRuntime(ctx context.Context, p state.RuntimeInstancePublication) (state.Instance, error) {
	s.mu.Lock()
	s.calls++
	fail := s.failures > 0
	if fail {
		s.failures--
	}
	s.mu.Unlock()
	if fail {
		return state.Instance{}, fmt.Errorf("runtime publication: app config fence: %w",
			fmt.Errorf("%w: app config changed since the wake read it", state.ErrRuntimeValuesChanged))
	}
	return s.MemStore.PublishOwnedInstanceRuntime(ctx, p)
}

// Deploy-path review P3 (production h34-zone-http-1113, 2026-11-13): a config
// change while a deploy's snapshot prime booted failed the whole deployment.
// The prime now boots again with the new values, within its own budget.
func TestPrimeRetriesWhenRuntimeValuesChangeDuringBoot(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failures   int
		wantFailed bool
	}{
		{name: "changes during two boots", failures: 2, wantFailed: false},
		{name: "keeps changing past the budget", failures: 10, wantFailed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			mem := state.NewMemStore()
			app, dep := primeHandoffFixture(t, mem, api.ExecutionModeRequest)
			store := &valuesChangedStore{MemStore: mem, failures: tc.failures}
			e := sched.XNewEngine(t, statetest.NewMem(store), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			loop := sched.NewLoop(nil, e, primeHandoffLog())
			loop.HandleNotification(ctx, db.Notification{
				Channel: db.NotifySnapshotPrime,
				Payload: `{"app_id":"` + app.ID + `","deployment_id":"` + dep.ID + `"}`,
			})
			loop.WaitPrimes()

			got, err := mem.DeploymentByID(ctx, dep.ID)
			if err != nil {
				t.Fatal(err)
			}
			if failed := got.Status == state.DeployFailed; failed != tc.wantFailed {
				t.Fatalf("deployment status = %s (%s) after %d publication attempts, want failed=%v", got.Status, got.Error, store.calls, tc.wantFailed)
			}
			if !tc.wantFailed && store.calls != tc.failures+1 {
				t.Fatalf("publication attempts = %d, want %d", store.calls, tc.failures+1)
			}
		})
	}
}

package sched_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type busyPublicationStore struct {
	MemStore *state.MemStore
	busy     atomic.Int32
	calls    atomic.Int32
}

func (s *busyPublicationStore) PublishOwnedInstanceRuntime(ctx context.Context, p state.RuntimeInstancePublication) (state.Instance, error) {
	s.calls.Add(1)
	if s.busy.Add(-1) >= 0 {
		return state.Instance{}, state.ErrApplicationStandardRuntimeBusy
	}
	return s.MemStore.PublishOwnedInstanceRuntime(ctx, p)
}

// Production-us rc.294: h14-py's deploy failed with "snapshot prime failed:
// sched: prime: record runtime: state: runtime admission inputs are busy"
// after its VM had booted; the fence was held past the store's own ~1 s
// replay (Hunt 14 H14-31).
func TestPrimeWaitsOutBusyRuntimeFences(t *testing.T) {
	old := *sched.PrimeRuntimeBusyBackoff
	*sched.PrimeRuntimeBusyBackoff = time.Millisecond
	t.Cleanup(func() { *sched.PrimeRuntimeBusyBackoff = old })
	for _, tc := range []struct {
		name   string
		busy   int32
		wantOK bool
	}{
		{"fence frees up", 2, true},
		{"fence stays busy", 100, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &busyPublicationStore{MemStore: state.NewMemStore()}
			_, app, dep := sched.SeedApp(t, statetest.NewMem(store), api.PlanHobby, 256, 2)
			store.busy.Store(tc.busy)
			e := sched.XNewEngine(t, statetest.NewMem(store), &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
			err := e.Prime(context.Background(), app.ID, dep.ID)
			if tc.wantOK && err != nil {
				t.Fatalf("Prime after %d busy publications: %v", tc.busy, err)
			}
			if !tc.wantOK && !errors.Is(err, state.ErrApplicationStandardRuntimeBusy) {
				t.Fatalf("Prime with a busy fence = %v, want ErrApplicationStandardRuntimeBusy", err)
			}
			if calls := store.calls.Load(); !tc.wantOK && calls != int32(api.PrimeRuntimePublicationBusyAttempts) {
				t.Fatalf("publication attempts = %d, want %d", calls, api.PrimeRuntimePublicationBusyAttempts)
			}
		})
	}
}

// The customer saw "Application standards changed during startup" for a busy
// fence, where no standard had changed (Hunt 14 H14-32).
func TestBusyRuntimeFenceIsNotReportedAsAStandardsChange(t *testing.T) {
	var problem *api.Problem
	err := sched.ApplicationStandardRuntimeProblem(state.ErrApplicationStandardRuntimeBusy)
	if !errors.As(err, &problem) || problem.Detail == "" || strings.Contains(problem.Detail, "standards changed") {
		t.Fatalf("busy fence problem = %+v", problem)
	}
	err = sched.ApplicationStandardRuntimeProblem(state.ErrApplicationStandardRuntimeStale)
	if !errors.As(err, &problem) || !strings.Contains(problem.Detail, "standards changed") {
		t.Fatalf("stale standards problem = %+v", problem)
	}
}

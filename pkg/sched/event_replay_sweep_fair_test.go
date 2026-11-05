package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type blockingReplaySweepStore struct {
	state.Store
	state.EventReplayBackfillStore
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
	done     chan struct{}
}

func (s *blockingReplaySweepStore) ProcessNextEventReplayBackfill(ctx context.Context, _ time.Time) (bool, error) {
	defer close(s.done)
	close(s.started)
	select {
	case <-s.release:
		return false, nil
	case <-ctx.Done():
		close(s.canceled)
		return false, ctx.Err()
	}
}

func TestReplaySweepRunsOutsideEventFanout(t *testing.T) {
	store := &blockingReplaySweepStore{Store: state.NewMemStore(), started: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{}), done: make(chan struct{})}
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	loop := NewLoop(nil, engine, quietLog())
	loop.dispatchEventReplayBackfillSweep(t.Context())
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("replay worker did not start")
	}
	select {
	case <-store.canceled:
		t.Fatal("slow replay page was canceled by the sweep budget")
	case <-time.After(eventReplayBackfillSweepTimeout + 100*time.Millisecond):
	}
	close(store.release)
	<-store.done
}

type fairReplaySweepStore struct {
	state.Store
	state.EventReplayBackfillStore
	seen []string
}

func (s *fairReplaySweepStore) ProcessNextEventReplayBackfill(context.Context, time.Time) (bool, error) {
	panic("fair replay path was not used")
}

func (s *fairReplaySweepStore) ProcessNextEventReplayBackfillAfter(_ context.Context, _ time.Time, after string) (string, bool, error) {
	s.seen = append(s.seen, after)
	if len(s.seen) == 4 {
		return "", false, nil
	}
	return []string{"first", "second", "third"}[len(s.seen)-1], true, nil
}

func TestReplaySweepAdvancesBetweenJobs(t *testing.T) {
	store := &fairReplaySweepStore{Store: state.NewMemStore()}
	engine, _ := makeEngine(t, store, &fakeWakeVMM{})
	loop := NewLoop(nil, engine, quietLog())
	loop.runEventReplayBackfillSweep(t.Context(), time.Now())
	want := []string{"", "first", "second", "third"}
	if len(store.seen) != len(want) {
		t.Fatalf("job cursors: %v", store.seen)
	}
	for i := range want {
		if store.seen[i] != want[i] {
			t.Fatalf("job cursors: %v", store.seen)
		}
	}
}

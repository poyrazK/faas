package sched_test

// adr: 263

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type blockedWorkflowRetention struct {
	state.WorkflowStore
	started chan struct{}
	release chan struct{}
	done    chan struct{}
}

type batchedWorkflowRetention struct {
	state.WorkflowStore
	runCalls, eventCalls int
	done                 chan struct{}
}

func (s *batchedWorkflowRetention) SweepExpiredWorkflowRuns(context.Context, time.Duration) (int, error) {
	s.runCalls++
	if s.runCalls == 1 {
		return api.WorkflowRetentionPruneBatch, nil
	}
	return 1, nil
}

func (s *batchedWorkflowRetention) SweepExpiredWorkflowEvents(context.Context, time.Duration) (int, error) {
	s.eventCalls++
	if s.eventCalls == 1 {
		return api.WorkflowRetentionPruneBatch, nil
	}
	if s.done != nil {
		close(s.done)
	}
	return 0, nil
}

func TestWorkflowRetentionDrainsBoundedBatches(t *testing.T) {
	store := &batchedWorkflowRetention{WorkflowStore: state.NewMemStore()}
	if err := sched.NewWorkflowRetention(store, nil).SweepOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.runCalls != 1 || store.eventCalls != 1 {
		t.Fatalf("one pass must visit each phase once: runs=%d events=%d", store.runCalls, store.eventCalls)
	}
	if err := sched.NewWorkflowRetention(store, nil).SweepOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.runCalls != 2 || store.eventCalls != 2 {
		t.Fatalf("second retention pass: runs=%d events=%d", store.runCalls, store.eventCalls)
	}
}

func (s *blockedWorkflowRetention) SweepExpiredWorkflowRuns(ctx context.Context, _ time.Duration) (int, error) {
	close(s.started)
	select {
	case <-s.release:
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (s *blockedWorkflowRetention) SweepExpiredWorkflowEvents(context.Context, time.Duration) (int, error) {
	close(s.done)
	return 0, nil
}

func TestWorkflowRetentionDispatchDoesNotBlockLoop(t *testing.T) {
	store := &blockedWorkflowRetention{WorkflowStore: state.NewMemStore(), started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	loop := sched.NewLoop(nil, nil, nil).WithWorkflowRetention(sched.NewWorkflowRetention(store, nil))
	returned := make(chan struct{})
	go func() {
		loop.DispatchWorkflowRetention(context.Background())
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("retention dispatch blocked the loop")
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("retention worker did not start")
	}
	close(store.release)
	select {
	case <-store.done:
	case <-time.After(time.Second):
		t.Fatal("retention worker did not finish")
	}
}

func TestWorkflowRetentionContinuesFullBatches(t *testing.T) {
	store := &batchedWorkflowRetention{WorkflowStore: state.NewMemStore(), done: make(chan struct{})}
	loop := sched.NewLoop(nil, nil, nil).WithWorkflowRetention(sched.NewWorkflowRetention(store, nil))
	loop.DispatchWorkflowRetention(context.Background())
	select {
	case <-store.done:
	case <-time.After(3 * time.Second):
		t.Fatal("full retention batch did not schedule a continuation")
	}
	if store.runCalls != 2 || store.eventCalls != 2 {
		t.Fatalf("continuation passes: runs=%d events=%d", store.runCalls, store.eventCalls)
	}
}

var errRetentionPhase = errors.New("retention phase failed")

type failingWorkflowRetention struct {
	state.WorkflowStore
	failed string
	calls  []string
}

func (s *failingWorkflowRetention) SweepExpiredWorkflowRuns(context.Context, time.Duration) (int, error) {
	s.calls = append(s.calls, "runs")
	if s.failed == "runs" {
		return 0, errRetentionPhase
	}
	return 0, nil
}
func (s *failingWorkflowRetention) PruneWorkflowScheduleOccurrences(context.Context, time.Time, int) (int, error) {
	s.calls = append(s.calls, "history")
	if s.failed == "history" {
		return 0, errRetentionPhase
	}
	return 0, nil
}
func (s *failingWorkflowRetention) ListWorkflowScheduleOccurrences(context.Context, string, string, string, int) ([]state.WorkflowScheduleOccurrence, error) {
	return nil, nil
}
func (s *failingWorkflowRetention) SweepExpiredWorkflowEvents(context.Context, time.Duration) (int, error) {
	s.calls = append(s.calls, "events")
	if s.failed == "events" {
		return 0, errRetentionPhase
	}
	return 0, nil
}
func TestWorkflowRetentionContinuesAfterPhaseFailure(t *testing.T) {
	for _, phase := range []string{"runs", "history", "events"} {
		t.Run(phase, func(t *testing.T) {
			store := &failingWorkflowRetention{WorkflowStore: state.NewMemStore(), failed: phase}
			err := sched.NewWorkflowRetention(store, nil).SweepOnce(context.Background())
			if !errors.Is(err, errRetentionPhase) {
				t.Fatalf("missing phase error: %v", err)
			}
			if len(store.calls) != 3 || store.calls[0] != "runs" || store.calls[1] != "history" || store.calls[2] != "events" {
				t.Fatalf("phases visited: %v", store.calls)
			}
		})
	}
}

type pacedWorkflowRetention struct {
	state.WorkflowStore
	calls  chan time.Time
	events int
}

func (s *pacedWorkflowRetention) SweepExpiredWorkflowRuns(context.Context, time.Duration) (int, error) {
	return 0, errRetentionPhase
}
func (s *pacedWorkflowRetention) SweepExpiredWorkflowEvents(context.Context, time.Duration) (int, error) {
	s.events++
	s.calls <- time.Now()
	if s.events == 1 {
		return api.WorkflowRetentionPruneBatch, nil
	}
	return 0, nil
}
func TestWorkflowRetentionBacksOffAfterPhaseFailure(t *testing.T) {
	store := &pacedWorkflowRetention{WorkflowStore: state.NewMemStore(), calls: make(chan time.Time, 2)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop := sched.NewLoop(nil, nil, nil).WithWorkflowRetention(sched.NewWorkflowRetention(store, nil))
	loop.DispatchWorkflowRetention(ctx)
	var first, second time.Time
	select {
	case first = <-store.calls:
	case <-time.After(3 * time.Second):
		t.Fatal("first pass did not run")
	}
	select {
	case second = <-store.calls:
	case <-time.After(3 * time.Second):
		t.Fatal("continuation did not run")
	}
	if second.Sub(first) < 900*time.Millisecond {
		t.Fatalf("retry was not paced: %s", second.Sub(first))
	}
}

type partialEventRetention struct {
	state.WorkflowStore
	calls chan int
	count int
}

type archivingWorkflowRetention struct {
	state.WorkflowStore
	calls int
	done  chan struct{}
}

func (s *archivingWorkflowRetention) SweepExpiredWorkflowRuns(context.Context, time.Duration) (int, error) {
	return 0, nil
}
func (s *archivingWorkflowRetention) SweepExpiredWorkflowEventsWithProgress(context.Context, time.Duration) (int, bool, error) {
	s.calls++
	if s.calls == 1 {
		return 0, true, nil
	}
	close(s.done)
	return 0, false, nil
}

func TestWorkflowRetentionContinuesArchivingWithoutExpirations(t *testing.T) {
	store := &archivingWorkflowRetention{WorkflowStore: state.NewMemStore(), done: make(chan struct{})}
	loop := sched.NewLoop(nil, nil, nil).WithWorkflowRetention(sched.NewWorkflowRetention(store, nil))
	loop.DispatchWorkflowRetention(context.Background())
	select {
	case <-store.done:
	case <-time.After(3 * time.Second):
		t.Fatal("bounded archive work did not continue")
	}
	if store.calls != 2 {
		t.Fatalf("archive passes = %d", store.calls)
	}
}

func (s *partialEventRetention) SweepExpiredWorkflowRuns(context.Context, time.Duration) (int, error) {
	return 0, nil
}
func (s *partialEventRetention) SweepExpiredWorkflowEvents(context.Context, time.Duration) (int, error) {
	s.count++
	s.calls <- s.count
	if s.count == 1 {
		return api.WorkflowRetentionPruneBatch, errRetentionPhase
	}
	return 0, errRetentionPhase
}
func TestWorkflowRetentionContinuesAfterPartialEventFailure(t *testing.T) {
	store := &partialEventRetention{WorkflowStore: state.NewMemStore(), calls: make(chan int, 2)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop := sched.NewLoop(nil, nil, nil).WithWorkflowRetention(sched.NewWorkflowRetention(store, nil))
	loop.DispatchWorkflowRetention(ctx)
	for want := 1; want <= 2; want++ {
		select {
		case got := <-store.calls:
			if got != want {
				t.Fatalf("pass = %d, want %d", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("event cleanup did not reach pass %d", want)
		}
	}
}

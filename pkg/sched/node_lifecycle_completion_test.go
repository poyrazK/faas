// adr: 1527
package sched_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type lifecyclePersistenceStore struct {
	MemStore                *state.MemStore
	calls, failures         int
	loseReply, failDelivery bool
	entered, release        chan struct{}
	once                    sync.Once
}

func (s *lifecyclePersistenceStore) CompleteNodeLifecycle(ctx context.Context, in state.NodeLifecycleCompletion) (state.NodeLifecycleCompletionResult, error) {
	s.calls++
	if s.entered != nil {
		s.once.Do(func() { close(s.entered); <-s.release })
	}
	if s.failures > 0 {
		s.failures--
		return state.NodeLifecycleCompletionResult{}, errors.New("database unavailable")
	}
	r, err := s.MemStore.CompleteNodeLifecycle(ctx, in)
	if err == nil && s.loseReply {
		s.loseReply = false
		if r.ResultLifecycle == state.NodeLifecycleMaintenance {
			if err := s.MemStore.NodeSetLifecycle(ctx, in.NodeID, r.ResultLifecycle, state.NodeLifecycleActive); err != nil {
				return r, err
			}
		} else {
			if err := s.MemStore.NodeSetLifecycle(ctx, in.NodeID, r.ResultLifecycle, state.NodeLifecycleUnavailable); err != nil {
				return r, err
			}
		}
		current, err := s.MemStore.NodeGet(ctx, in.NodeID)
		if err != nil {
			return r, err
		}
		if err := s.MemStore.NodeSetLifecycle(ctx, in.NodeID, current.Lifecycle, in.Lifecycle); err != nil {
			return r, err
		}
		return state.NodeLifecycleCompletionResult{}, errors.New("commit reply lost")
	}
	return r, err
}

func (s *lifecyclePersistenceStore) DeliverAuditEvent(ctx context.Context, id int64) (bool, error) {
	if s.failDelivery {
		return false, errors.New("audit delivery unavailable")
	}
	return s.MemStore.DeliverAuditEvent(ctx, id)
}

func lifecycleRunnerFixture(t *testing.T, lifecycle state.NodeLifecycle) (*sched.RecoveryRunner, *lifecyclePersistenceStore, state.ComputeNode) {
	t.Helper()
	fake := &lifecyclePersistenceStore{MemStore: state.NewMemStore()}
	store := statetest.NewMem(fake)
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "fenced-completion", Lifecycle: lifecycle})
	if err != nil {
		t.Fatal(err)
	}
	return sched.NewRecoveryRunner(store, sched.NewArbiter(nil, nil), nil, sched.TestLog()), fake, node
}

func TestNodeLifecycleCompletionRunnerRejectsPausedOldSweep(t *testing.T) {
	for _, lifecycle := range []state.NodeLifecycle{state.NodeLifecycleDraining, state.NodeLifecycleForceDraining, state.NodeLifecycleRecovering} {
		t.Run(string(lifecycle), func(t *testing.T) {
			runner, fake, node := lifecycleRunnerFixture(t, lifecycle)
			fake.entered, fake.release = make(chan struct{}), make(chan struct{})
			finished := make(chan error, 1)
			go func() { finished <- runner.Tick(t.Context()) }()
			<-fake.entered
			if err := fake.MemStore.NodeSetLifecycle(t.Context(), node.ID, lifecycle, state.NodeLifecycleUnavailable); err != nil {
				t.Fatal(err)
			}
			if err := fake.MemStore.NodeSetLifecycle(t.Context(), node.ID, state.NodeLifecycleUnavailable, lifecycle); err != nil {
				t.Fatal(err)
			}
			close(fake.release)
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			got, err := fake.MemStore.NodeGet(t.Context(), node.ID)
			if err != nil || got.Lifecycle != lifecycle || got.LifecycleGeneration != node.LifecycleGeneration+2 {
				t.Fatalf("old sweep completed later cycle: %+v %v", got, err)
			}
			rows, err := fake.MemStore.ListEvents(t.Context(), "", 100)
			if err != nil || len(rows) != 0 || fake.calls != 1 {
				t.Fatalf("obsolete sweep retried/emitted: calls=%d events=%d %v", fake.calls, len(rows), err)
			}
		})
	}
}

func TestNodeLifecycleCompletionRunnerReplaysLostCommitReply(t *testing.T) {
	for _, lifecycle := range []state.NodeLifecycle{state.NodeLifecycleDraining, state.NodeLifecycleRecovering} {
		t.Run(string(lifecycle), func(t *testing.T) {
			runner, fake, node := lifecycleRunnerFixture(t, lifecycle)
			fake.loseReply = true
			if err := runner.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := fake.MemStore.NodeGet(t.Context(), node.ID)
			if err != nil || got.Lifecycle != lifecycle || got.LifecycleGeneration != node.LifecycleGeneration+3 || fake.calls != 2 {
				t.Fatalf("retry mutated later cycle: %+v calls=%d %v", got, fake.calls, err)
			}
			rows, err := fake.MemStore.ListEvents(t.Context(), "", 100)
			if err != nil || len(rows) != 1 {
				t.Fatalf("completion audits=%d %v", len(rows), err)
			}
			if rows[0].Kind != "node.drained" && rows[0].Kind != "node.recovered" {
				t.Fatalf("unexpected completion audit: %+v", rows[0])
			}
		})
	}
}

func TestNodeLifecycleCompletionRunnerRetriesOnlyPersistence(t *testing.T) {
	runner, fake, node := lifecycleRunnerFixture(t, state.NodeLifecycleDraining)
	fake.failures = api.NodeLifecycleCompletionAttempts
	if err := runner.Tick(t.Context()); err == nil {
		t.Fatal("database failure hidden")
	}
	got, err := fake.MemStore.NodeGet(t.Context(), node.ID)
	if err != nil || got.LifecycleGeneration != node.LifecycleGeneration || got.Lifecycle != node.Lifecycle || fake.calls != api.NodeLifecycleCompletionAttempts {
		t.Fatalf("partial failed completion: %+v calls=%d %v", got, fake.calls, err)
	}
	// A fresh process/sweep resumes the same database-only completion.
	if err := sched.NewRecoveryRunner((*runner.Store()), sched.NewArbiter(nil, nil), nil, sched.TestLog()).Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err = fake.MemStore.NodeGet(t.Context(), node.ID)
	if err != nil || got.Lifecycle != state.NodeLifecycleMaintenance || fake.calls != api.NodeLifecycleCompletionAttempts+1 {
		t.Fatalf("completion not resumed: %+v %v", got, err)
	}
}

func TestNodeLifecycleCompletionRunnerDefersAuditFailure(t *testing.T) {
	runner, fake, node := lifecycleRunnerFixture(t, state.NodeLifecycleDraining)
	fake.failDelivery = true
	if err := runner.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := fake.MemStore.NodeGet(t.Context(), node.ID)
	if err != nil || got.Lifecycle != state.NodeLifecycleMaintenance || fake.calls != 1 {
		t.Fatalf("audit failure retried mutation: %+v %v", got, err)
	}
	item, err := fake.MemStore.ClaimAuditEvent(t.Context(), "background-delivery", api.NodeLifecycleCompletionTimeout)
	if err != nil {
		t.Fatal("completion lost durable audit handoff", err)
	}
	fake.failDelivery = false
	if delivered, err := fake.DeliverAuditEvent(t.Context(), item.ID); err != nil || !delivered {
		t.Fatalf("background delivery: %v %v", delivered, err)
	}
	if err := runner.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := fake.MemStore.ListEvents(t.Context(), "", 100)
	if err != nil || len(rows) != 1 || fake.calls != 1 {
		t.Fatalf("duplicate completion after audit retry: events=%d calls=%d %v", len(rows), fake.calls, err)
	}
}

func TestNodeLifecycleCompletionRunnerIncludesWarmAndFrozen(t *testing.T) {
	for _, current := range []state.State{state.StateWarm, state.StateFrozen} {
		t.Run(string(current), func(t *testing.T) {
			runner, fake, node := lifecycleRunnerFixture(t, state.NodeLifecycleActive)
			if _, err := fake.MemStore.CreateInstance(t.Context(), "app", "dep", string(current), 128, node.ID, "wake"); err != nil {
				t.Fatal(err)
			}
			if err := fake.MemStore.NodeSetLifecycle(t.Context(), node.ID, state.NodeLifecycleActive, state.NodeLifecycleDraining); err != nil {
				t.Fatal(err)
			}
			if err := runner.Tick(t.Context()); err != nil {
				t.Fatal(err)
			}
			got, err := fake.MemStore.NodeGet(t.Context(), node.ID)
			if err != nil || got.Lifecycle != state.NodeLifecycleDraining {
				t.Fatalf("drain omitted resident %s: %+v %v", current, got, err)
			}
		})
	}
}

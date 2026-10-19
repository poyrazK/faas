// adr: 1526
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type nodePersistenceStore struct {
	MemStore        *state.MemStore
	failures, calls int
	loseReply       bool
}

func (s *nodePersistenceStore) ExecuteOperatorNode(ctx context.Context, id, token string) (state.OperatorNodeResult, error) {
	s.calls++
	if s.failures > 0 {
		s.failures--
		return state.OperatorNodeResult{}, errors.New("database unavailable")
	}
	r, err := s.MemStore.ExecuteOperatorNode(ctx, id, token)
	if err == nil && s.loseReply {
		s.loseReply = false
		// The recovery controller completes the drain and a new cycle starts
		// before the scheduler can retry its lost transaction response.
		current, err := s.MemStore.NodeGet(ctx, r.NodeID)
		if err != nil {
			return r, err
		}
		if _, err := s.MemStore.CompleteNodeLifecycle(ctx, state.NodeLifecycleCompletion{NodeID: r.NodeID, Generation: current.LifecycleGeneration, Lifecycle: current.Lifecycle}); err != nil {
			return r, err
		}
		if err := s.MemStore.NodeSetLifecycle(ctx, r.NodeID, state.NodeLifecycleMaintenance, state.NodeLifecycleActive); err != nil {
			return r, err
		}
		if err := s.MemStore.NodeSetLifecycle(ctx, r.NodeID, state.NodeLifecycleActive, state.NodeLifecycleForceDraining); err != nil {
			return r, err
		}
		return state.OperatorNodeResult{}, errors.New("commit response lost")
	}
	return r, err
}

func nodeLoopFixture(t *testing.T) (*Loop, *nodePersistenceStore, state.OperatorIntent, state.ComputeNode) {
	t.Helper()
	fake := &nodePersistenceStore{MemStore: state.NewMemStore()}
	store := statetest.NewMem(fake)
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "maintenance-node", TargetURL: "unix:///run/faas/vmmd.sock", Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertOperatorIntent(t.Context(), state.OperatorIntentKindNodeDrain, node.ID, nil, "actor-id", "maintenance", nil, nil); err != nil {
		t.Fatal(err)
	}
	i, err := store.ClaimPendingOperatorIntent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	loop := &Loop{engine: newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0"), log: silenceLog()}
	return loop, fake, i, node
}

func TestOperatorNodeDispatcherRecoversLostCommitResponse(t *testing.T) {
	loop, store, i, node := nodeLoopFixture(t)
	store.loseReply = true
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	loop.processOperatorIntent(ctx, i)
	row, err := store.MemStore.GetOperatorIntent(t.Context(), i.ID)
	if err != nil || row.Status != state.OperatorIntentSucceeded || store.calls != 2 {
		t.Fatalf("lost reply: %+v calls=%d %v", row, store.calls, err)
	}
	current, err := store.MemStore.NodeGet(t.Context(), node.ID)
	if err != nil || current.Lifecycle != state.NodeLifecycleForceDraining {
		t.Fatalf("retry changed a later maintenance cycle: %+v %v", current, err)
	}
	events, err := store.MemStore.ListEvents(t.Context(), "", 100)
	if err != nil || len(events) != 1 || events[0].Kind != "operator.action.node_drain.outcome" {
		t.Fatalf("outcomes: %+v %v", events, err)
	}
	fresh := &Loop{engine: newEngine(t, statetest.NewMem(store), &fakeVMM{}, &fakeNotifier{}, "1.10.0"), log: silenceLog()}
	fresh.drainPendingOperatorIntents(t.Context())
	if store.calls != 2 {
		t.Fatal("new scheduler redispatched completed node action")
	}
}

func TestOperatorNodeDispatcherRetriesAfterWorkerReplacement(t *testing.T) {
	loop, store, i, node := nodeLoopFixture(t)
	store.failures = api.OperatorNodePersistenceAttempts
	loop.processOperatorIntent(t.Context(), i)
	row, err := store.MemStore.GetOperatorIntent(t.Context(), i.ID)
	if err != nil || row.Status != state.OperatorIntentRunning || row.CompletionAuditOutboxID != 0 {
		t.Fatalf("persistence failure completed intent: %+v %v", row, err)
	}
	current, err := store.MemStore.NodeGet(t.Context(), node.ID)
	if err != nil || current.Lifecycle != state.NodeLifecycleActive {
		t.Fatalf("failed transaction changed lifecycle: %+v %v", current, err)
	}
	if _, err := store.MemStore.ReclaimStuckRunningOperatorIntents(t.Context(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	fresh := &Loop{engine: newEngine(t, statetest.NewMem(store), &fakeVMM{}, &fakeNotifier{}, "1.10.0"), log: silenceLog()}
	fresh.drainPendingOperatorIntents(t.Context())
	row, err = store.MemStore.GetOperatorIntent(t.Context(), i.ID)
	if err != nil || row.Status != state.OperatorIntentSucceeded {
		t.Fatalf("replacement failed: %+v %v", row, err)
	}
	calls := store.calls
	if err := loop.processOperatorNode(t.Context(), i); !errors.Is(err, state.ErrOperatorNodeClaimLost) || store.calls != calls+1 {
		t.Fatalf("obsolete worker retried: calls=%d err=%v", store.calls, err)
	}
}

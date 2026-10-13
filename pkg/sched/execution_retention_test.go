package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type retentionRecordingStore struct {
	state.ExecutionStore
	cutoffs []time.Time
	batches []int
}

func (s *retentionRecordingStore) PurgeExpiredExecutions(_ context.Context, finishedBefore time.Time, limit int) (int, error) {
	s.cutoffs = append(s.cutoffs, finishedBefore)
	if len(s.batches) == 0 {
		return 0, nil
	}
	deleted := min(s.batches[0], limit)
	s.batches = s.batches[1:]
	return deleted, nil
}

func TestExecutionCoordinatorPurgeUsesReceiptRetention(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	store := &retentionRecordingStore{ExecutionStore: state.NewMemStore()}
	coordinator := NewExecutionCoordinator(store, nil, ExecutionCoordinatorConfig{}, nil)
	coordinator.now = func() time.Time { return now }

	if _, err := coordinator.PurgeExpiredOnce(context.Background()); err != nil {
		t.Fatalf("PurgeExpiredOnce: %v", err)
	}
	if want := now.Add(-api.ExecutionReceiptRetention); len(store.cutoffs) != 1 || !store.cutoffs[0].Equal(want) {
		t.Fatalf("cutoffs = %v, want [%v]", store.cutoffs, want)
	}
}

func TestExecutionCoordinatorPurgeDrainsFullBatches(t *testing.T) {
	store := &retentionRecordingStore{ExecutionStore: state.NewMemStore(), batches: []int{2, 2, 1}}
	coordinator := NewExecutionCoordinator(store, nil, ExecutionCoordinatorConfig{SweepLimit: 2}, nil)

	coordinator.purgeExpired(context.Background())
	if len(store.cutoffs) != 3 {
		t.Fatalf("purge calls = %d, want 3 (drain until a short batch)", len(store.cutoffs))
	}
}

func TestExecutionCoordinatorRunRetentionNeedsStore(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewExecutionCoordinator(nil, nil, ExecutionCoordinatorConfig{}, nil).RunRetention(ctx); err == nil {
		t.Fatal("RunRetention without a store returned nil")
	}
	store := &retentionRecordingStore{ExecutionStore: state.NewMemStore()}
	if err := NewExecutionCoordinator(store, nil, ExecutionCoordinatorConfig{}, nil).RunRetention(ctx); err != nil {
		t.Fatalf("RunRetention after cancel: %v", err)
	}
}

// adr: 368
package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type fencedDeadLetterStore struct {
	fakeDeadLetterStore
	current map[string]int64
	routed  []string
}

func (f *fencedDeadLetterStore) check(id string, generation int64) error {
	if f.current[id] != generation {
		return state.ErrNotFound
	}
	return nil
}

func (f *fencedDeadLetterStore) CompleteClaimedTriggerRecord(_ context.Context, id string, generation int64) error {
	return f.check(id, generation)
}

func (f *fencedDeadLetterStore) RetryClaimedTriggerRecord(_ context.Context, id string, generation int64, _ string, _ time.Time) error {
	return f.check(id, generation)
}

func (f *fencedDeadLetterStore) DeadLetterClaimedTriggerRecord(_ context.Context, id string, generation int64, _ string) error {
	return f.check(id, generation)
}

func (f *fencedDeadLetterStore) RouteClaimedTriggerDeadLetter(_ context.Context, id string, generation int64, _, _ string, _ []byte) error {
	if err := f.check(id, generation); err != nil {
		return err
	}
	f.routed = append(f.routed, id)
	return nil
}

func TestClaimedTriggerDLQSkipsStaleBrokerHandles(t *testing.T) {
	const triggerID = "11111111-1111-1111-1111-111111111111"
	const staleID = "22222222-2222-2222-2222-222222222222"
	const currentID = "33333333-3333-3333-3333-333333333333"
	stale := sqlc.TriggerRecord{ID: pgtypeUUIDFromString(t, staleID), ItemIdentifier: "stale", ClaimGeneration: 1}
	current := sqlc.TriggerRecord{ID: pgtypeUUIDFromString(t, currentID), ItemIdentifier: "current", ClaimGeneration: 2}
	store := &fencedDeadLetterStore{current: map[string]int64{staleID: 2, currentID: 2}}
	finalized := makeLoopForDLQ().deadLetterAllForApp(context.Background(), "", "", triggerID,
		[]string{"stale", "current"}, triggerReasonPoisonRecord, "bad result", store, stale, current)
	if len(finalized) != 1 || finalized[0] != "current" {
		t.Fatalf("finalized handles = %v, want only current", finalized)
	}
	if len(store.routed) != 1 || store.routed[0] != currentID || len(store.inserts) != 0 || len(store.marks) != 0 {
		t.Fatalf("route=%v insert=%v mark=%v", store.routed, store.inserts, store.marks)
	}
	if err := completeTriggerClaim(context.Background(), store, stale); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale completion = %v, want ErrNotFound", err)
	}
	if err := retryTriggerClaim(context.Background(), store, stale, "retry", time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale retry = %v, want ErrNotFound", err)
	}
	if len(store.retries) != 0 {
		t.Fatalf("unfenced retry calls = %d", len(store.retries))
	}
}

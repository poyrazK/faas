package outbound

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// memoryBatch is a batch's reservation in the memory backend (ADR-1431).
type memoryBatch struct {
	batch     AIBatch
	open      bool
	settled   bool
	nextCheck time.Time
}

// stateLocked returns an integration's state, creating it. b.mu is held.
func (b *MemoryBackend) stateLocked(integrationID string) *memoryState {
	state := b.states[integrationID]
	if state == nil {
		state = &memoryState{leases: make(map[string]memoryLease), bindingDailyCounts: make(map[string]int64)}
		b.states[integrationID] = state
	}
	if state.batches == nil {
		state.batches = make(map[string]*memoryBatch)
	}
	return state
}

func (b *MemoryBackend) ReserveAIBatch(_ context.Context, batch AIBatch) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if batch.CreatedAt.IsZero() {
		batch.CreatedAt = b.now()
	}
	b.stateLocked(batch.IntegrationID).batches[batch.ID] = &memoryBatch{batch: batch}
	return nil
}

func (b *MemoryBackend) ConfirmAIBatch(_ context.Context, id, providerBatchID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, state := range b.states {
		if batch, ok := state.batches[id]; ok && !batch.open {
			// As in Postgres, the first check waits one poll interval.
			batch.open, batch.batch.ProviderBatchID = true, providerBatchID
			batch.nextCheck = b.now().Add(api.OutboundAIBatchPollIntervalSeconds * time.Second)
		}
	}
	return nil
}

func (b *MemoryBackend) DropAIBatch(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, state := range b.states {
		if batch, ok := state.batches[id]; ok && !batch.open {
			delete(state.batches, id)
		}
	}
	return nil
}

func (b *MemoryBackend) ClaimAIBatches(_ context.Context, limit int) ([]AIBatch, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	var due []*memoryBatch
	for _, state := range b.states {
		for id, batch := range state.batches {
			switch {
			case !batch.open && now.Sub(batch.batch.CreatedAt) > api.OutboundAIBatchPendingExpiryMinutes*time.Minute:
				delete(state.batches, id)
			case batch.open && !batch.settled && !batch.nextCheck.After(now):
				due = append(due, batch)
			}
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].nextCheck.Before(due[j].nextCheck) })
	var out []AIBatch
	for _, batch := range due[:min(len(due), limit)] {
		batch.nextCheck = now.Add(api.OutboundAIBatchPollIntervalSeconds * time.Second)
		out = append(out, batch.batch)
	}
	return out, nil
}

func (b *MemoryBackend) SettleAIBatch(_ context.Context, id string, _ AIUsageOutcome, _, _ int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, state := range b.states {
		if batch, ok := state.batches[id]; ok && batch.open {
			batch.settled = true
		}
	}
	return nil
}

// AIBatchOpen reports whether a batch is reserved and not yet settled, for
// tests.
func (b *MemoryBackend) AIBatchOpen(integrationID, id string) (AIBatch, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil || state.batches[id] == nil || state.batches[id].settled {
		return AIBatch{}, false
	}
	return state.batches[id].batch, true
}

// AIBatches lists an integration's unsettled batches, for tests.
func (b *MemoryBackend) AIBatches(integrationID string) []AIBatch {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[integrationID]
	if state == nil {
		return nil
	}
	var out []AIBatch
	for _, batch := range state.batches {
		if !batch.settled {
			out = append(out, batch.batch)
		}
	}
	return out
}

var _ AIBatchBackend = (*MemoryBackend)(nil)

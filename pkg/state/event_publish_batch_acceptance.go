package state

import (
	"context"
	"fmt"
)

// PublishedEventBatchAcceptance is one event's outcome in
// AcceptPublishedEventBatch: its acceptance, or the error that refused it.
type PublishedEventBatchAcceptance struct {
	Acceptance PublishedEventAcceptance
	Err        error
}

// PublishedEventBatchAcceptanceStore accepts a publish batch in one
// transaction (2026-11-12 event throughput test: one transaction, account
// lock and commit per event held a batch to ~15–30 events/s).
type PublishedEventBatchAcceptanceStore interface {
	AcceptPublishedEventBatch(ctx context.Context, actor, accountID string, payloads [][]byte) ([]PublishedEventBatchAcceptance, error)
}

// AcceptPublishedEventBatch accepts each payload as AcceptPublishedEvent
// does, in one transaction. Each event runs under its own savepoint, so an
// event refused (identity conflict, storage cap) leaves the others in the
// batch. The returned error is for the batch as a whole (begin or commit):
// no event of it can then be reported accepted.
func (s *PgStore) AcceptPublishedEventBatch(ctx context.Context, actor, accountID string, payloads [][]byte) ([]PublishedEventBatchAcceptance, error) {
	out := make([]PublishedEventBatchAcceptance, len(payloads))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i, payload := range payloads {
		item, err := tx.Begin(ctx)
		if err != nil {
			return nil, fmt.Errorf("state: publish batch savepoint: %w", err)
		}
		var accepted PublishedEventAcceptance
		if err := appendCustomerPublishedEventTx(ctx, item, actor, accountID, "", "", payload, nil, nil, &accepted); err != nil {
			if rbErr := item.Rollback(ctx); rbErr != nil {
				return nil, fmt.Errorf("state: publish batch rollback to savepoint: %w", rbErr)
			}
			out[i].Err = err
			continue
		}
		if err := item.Commit(ctx); err != nil {
			return nil, fmt.Errorf("state: publish batch release savepoint: %w", err)
		}
		out[i].Acceptance = accepted
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// AcceptPublishedEventBatch mirrors the PgStore batch one event at a time;
// MemStore has no transaction to share.
func (m *MemStore) AcceptPublishedEventBatch(ctx context.Context, actor, accountID string, payloads [][]byte) ([]PublishedEventBatchAcceptance, error) {
	out := make([]PublishedEventBatchAcceptance, len(payloads))
	for i, payload := range payloads {
		out[i].Acceptance, out[i].Err = m.AcceptPublishedEvent(ctx, actor, accountID, payload)
	}
	return out, nil
}

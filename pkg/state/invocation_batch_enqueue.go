// adr: 20261022105723
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// InvocationBatchEnqueuer admits several invocations that differ only in
// payload in one transaction under one app lock (queue send-batch). Messages
// are admitted in order against the same caps a single enqueue checks; the
// first refusal stops the batch.
//
// It returns the admitted rows. A nil error means every payload was admitted;
// otherwise payloads[len(rows)] failed with the error and the rest were not
// attempted. On PgStore a failure that is not a cap refusal rolls back, so it
// returns no rows.
//
// A 2026-11-12 production test measured send-batch at ~34 ms per message:
// each message resolved, locked, counted and committed on its own, so a
// batch of 100 took 3.4 s and concurrent batches ran out of time part-way.
type InvocationBatchEnqueuer interface {
	EnqueueInvocationBatch(ctx context.Context, template Invocation, payloads []json.RawMessage) ([]Invocation, error)
}

var (
	_ InvocationBatchEnqueuer = (*PgStore)(nil)
	_ InvocationBatchEnqueuer = (*MemStore)(nil)
)

// ErrInvocationBatchUnsupported reports a template the batch path does not
// take (keyed work, or an environment-owned invocation); the caller enqueues
// those one at a time.
var ErrInvocationBatchUnsupported = errors.New("state: invocation batch unsupported for this template")

func (s *PgStore) EnqueueInvocationBatch(ctx context.Context, template Invocation, payloads []json.RawMessage) ([]Invocation, error) {
	if len(payloads) == 0 {
		return nil, nil
	}
	if template.WorkPolicyName != "" || template.ID != "" {
		return nil, ErrInvocationBatchUnsupported
	}
	if err := validateInvocationWorkEnvironment(ctx, s, template, false); err != nil {
		return nil, err
	}
	prepared, info, err := resolveInvocationEnvironmentAdmission(ctx, s, template)
	if err != nil {
		return nil, err
	}
	if info.environment.ID != "" {
		return nil, ErrInvocationBatchUnsupported
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockInvocationAdmissionAppDB(ctx, tx, prepared); err != nil {
		return nil, err
	}
	out := make([]Invocation, 0, len(payloads))
	var refused error
	for _, payload := range payloads {
		inv := prepared
		inv.Payload = payload
		// The backlog counters move with each insert inside this
		// transaction, so every message meets the caps the rows before it
		// left, as consecutive single sends would.
		if err := checkInvocationAdmissionDepthDB(ctx, tx, inv); err != nil {
			if !errors.Is(err, ErrQuotaExceeded) {
				return nil, err
			}
			refused = err
			break
		}
		row, err := enqueueInvocationRow(ctx, tx, inv)
		if err != nil {
			return nil, fmt.Errorf("state: invocation batch row %d: %w", len(out), err)
		}
		out = append(out, row)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, refused
}

// EnqueueInvocationBatch on MemStore admits each payload in turn through
// EnqueueInvocation, which already holds the store lock per row.
func (m *MemStore) EnqueueInvocationBatch(ctx context.Context, template Invocation, payloads []json.RawMessage) ([]Invocation, error) {
	if template.WorkPolicyName != "" || template.ID != "" {
		return nil, ErrInvocationBatchUnsupported
	}
	out := make([]Invocation, 0, len(payloads))
	for _, payload := range payloads {
		inv := template
		inv.Payload = payload
		row, err := m.EnqueueInvocation(ctx, inv)
		if err != nil {
			return out, err
		}
		out = append(out, row)
	}
	return out, nil
}

package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TriggerDeliveryStore switches a trigger between one invocation per record
// and one invocation per dispatch batch (ADR-1321).
type TriggerDeliveryStore interface {
	SetTriggerDelivery(ctx context.Context, id, delivery string) (sqlc.Trigger, error)
}

// SetTriggerDelivery stores a validated delivery mode and returns the
// trigger. Binding-owned queue projections are never changed.
func (s *PgStore) SetTriggerDelivery(ctx context.Context, id, delivery string) (sqlc.Trigger, error) {
	current, err := s.TriggerByID(ctx, id)
	if err != nil {
		return sqlc.Trigger{}, err
	}
	if err := api.ValidateTriggerDelivery(api.TriggerKind(current.Kind), delivery); err != nil || (current.Agent.Valid && delivery != api.TriggerDeliveryRecord) {
		return sqlc.Trigger{}, ErrInvalidArgument
	}
	n, err := s.triggerQueries().SetTriggerDelivery(ctx, s.pool, sqlc.SetTriggerDeliveryParams{ID: mustPgUUID(id), Delivery: delivery})
	if err != nil {
		return sqlc.Trigger{}, fmt.Errorf("state: set trigger delivery: %w", mapErr(err))
	}
	if n == 0 {
		return sqlc.Trigger{}, ErrConflict
	}
	return s.TriggerByID(ctx, id)
}

func (m *MemStore) SetTriggerDelivery(_ context.Context, id, delivery string) (sqlc.Trigger, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.triggers[id]
	if !ok {
		return sqlc.Trigger{}, ErrNotFound
	}
	// An agent trigger starts one run per record (ADR-20261027090010 §9).
	if err := api.ValidateTriggerDelivery(api.TriggerKind(t.Kind), delivery); err != nil || (t.Agent.Valid && delivery != api.TriggerDeliveryRecord) {
		return sqlc.Trigger{}, ErrInvalidArgument
	}
	if t.QueueBindingID.Valid {
		return sqlc.Trigger{}, ErrConflict
	}
	t.Delivery = delivery
	m.triggers[id] = t
	return t, nil
}

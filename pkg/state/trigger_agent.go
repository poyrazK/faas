package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TriggerAgentStore points a broker trigger at an agent (ADR-20261027090010 §9). apid
// sets the agent on a just-created, disabled trigger, then enables it, so no
// record ever reaches the app's function instead.
type TriggerAgentStore interface {
	SetTriggerAgent(ctx context.Context, id, agent string) (sqlc.Trigger, error)
}

// SetTriggerAgent stores a validated agent target and returns the trigger.
// Binding-owned queue projections are never changed.
func (s *PgStore) SetTriggerAgent(ctx context.Context, id, agent string) (sqlc.Trigger, error) {
	current, err := s.TriggerByID(ctx, id)
	if err != nil {
		return sqlc.Trigger{}, err
	}
	if err := api.ValidateTriggerAgent(api.TriggerKind(current.Kind), current.Delivery, agent); err != nil {
		return sqlc.Trigger{}, ErrInvalidArgument
	}
	n, err := s.triggerQueries().SetTriggerAgent(ctx, s.pool, sqlc.SetTriggerAgentParams{ID: mustPgUUID(id), Agent: pgtype.Text{String: agent, Valid: true}})
	if err != nil {
		return sqlc.Trigger{}, fmt.Errorf("state: set trigger agent: %w", mapErr(err))
	}
	if n == 0 {
		return sqlc.Trigger{}, ErrConflict
	}
	return s.TriggerByID(ctx, id)
}

func (m *MemStore) SetTriggerAgent(_ context.Context, id, agent string) (sqlc.Trigger, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.triggers[id]
	if !ok {
		return sqlc.Trigger{}, ErrNotFound
	}
	if err := api.ValidateTriggerAgent(api.TriggerKind(t.Kind), t.Delivery, agent); err != nil {
		return sqlc.Trigger{}, ErrInvalidArgument
	}
	if t.QueueBindingID.Valid {
		return sqlc.Trigger{}, ErrConflict
	}
	t.Agent = pgtype.Text{String: agent, Valid: true}
	m.triggers[id] = t
	return t, nil
}

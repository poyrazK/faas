package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

// PlanChangeReader reports when a billing subject's plan last changed
// (accounts.plan_changed_at or a company organization's orgs.plan_changed_at,
// stamped by a trigger). meterd dates a newly observed plan from it so a plan
// change made while meterd was down or catching up is not dated late
// (ADR-20261103121407). ok is false when the change time is unknown (rows
// from before the column existed, or a subject that never changed plan).
type PlanChangeReader interface {
	PlanChangedAt(ctx context.Context, subjectID string) (at time.Time, ok bool, err error)
}

// PlanChangedAt implements PlanChangeReader.
func (s *PgStore) PlanChangedAt(ctx context.Context, subjectID string) (time.Time, bool, error) {
	if uuid.Validate(subjectID) != nil {
		return time.Time{}, false, ErrInvalidArgument
	}
	var at pgtype.Timestamptz
	err := s.pool.QueryRow(ctx,
		`select coalesce((select plan_changed_at from accounts where id = $1::uuid),
		                 (select plan_changed_at from orgs where id = $1::uuid and not personal_org))`,
		subjectID).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !at.Valid {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return at.Time.UTC(), true, nil
}

// PlanChangedAt implements PlanChangeReader.
func (m *MemStore) PlanChangedAt(_ context.Context, subjectID string) (time.Time, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	at, ok := m.planChangedAt[subjectID]
	return at, ok, nil
}

// notePlanChangeLocked stamps a plan change the way the Postgres trigger
// does. Callers hold m.mu.
func (m *MemStore) notePlanChangeLocked(id string, from, to api.Plan) {
	if from == to {
		return
	}
	if m.planChangedAt == nil {
		m.planChangedAt = map[string]time.Time{}
	}
	m.planChangedAt[id] = time.Now().UTC()
}

// SetPlanChangedAt overrides when subjectID's plan last changed; tests use it
// to model a change meterd observes late.
func (m *MemStore) SetPlanChangedAt(subjectID string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.planChangedAt == nil {
		m.planChangedAt = map[string]time.Time{}
	}
	m.planChangedAt[subjectID] = at.UTC()
}

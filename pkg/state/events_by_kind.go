package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// EventsByKindReader is an optional read capability for the operator
// wake-latency report (ADR-20261121200409); Store stays unchanged. It returns
// one kind's events at or after since, newest first, at most limit rows.
type EventsByKindReader interface {
	ListEventsByKindSince(ctx context.Context, kind string, since time.Time, limit int) ([]Event, error)
}

func (s *PgStore) ListEventsByKindSince(ctx context.Context, kind string, since time.Time, limit int) ([]Event, error) {
	if kind == "" || limit < 1 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListEventsByKindSince(ctx, s.pool, sqlc.ListEventsByKindSinceParams{
		Kind: kind, Since: pgtype.Timestamptz{Time: since.UTC(), Valid: true}, RowLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list events by kind since: %w", err)
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		e := Event{ID: r.ID, At: r.At.Time, Actor: r.Actor, Kind: r.Kind, Data: r.Data}
		if r.Subject.Valid {
			id := uuid.UUID(r.Subject.Bytes)
			e.Subject = &id
		}
		out = append(out, e)
	}
	return out, nil
}

func (m *MemStore) ListEventsByKindSince(_ context.Context, kind string, since time.Time, limit int) ([]Event, error) {
	if kind == "" || limit < 1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Event
	for _, e := range m.events {
		if e.Kind == kind && !e.At.Before(since) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

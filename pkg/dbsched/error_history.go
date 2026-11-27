package dbsched

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/dbqueryerrors"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ExchangeErrorHistory accepts only bounded, structured proxy observations.
func (s *Scheduler) ExchangeErrorHistory(ctx context.Context, runtimeIDs []string, events []dbqueryerrors.Event) ([]dbqueryerrors.Policy, error) {
	store, ok := s.store.(*PgStore)
	if !ok {
		return nil, ErrConflict
	}
	if len(events) > dbqueryerrors.MaxBatch || len(runtimeIDs) > dbqueryerrors.MaxDatabaseIDs {
		return nil, ErrInvalid
	}
	for _, id := range runtimeIDs {
		if !dbqueryerrors.ValidID(id) {
			return nil, ErrInvalid
		}
	}
	if err := store.RecordPooledErrors(ctx, events); err != nil {
		return nil, err
	}
	if len(runtimeIDs) == 0 {
		return nil, nil
	}
	rows, err := sqlc.New().GetNativePostgresErrorHistoryPolicies(ctx, store.pool, runtimeIDs)
	if err != nil {
		return nil, err
	}
	policies := make([]dbqueryerrors.Policy, 0, len(rows))
	for _, row := range rows {
		policies = append(policies, dbqueryerrors.Policy{RuntimeID: row.RuntimeID, Epoch: row.Epoch.String()})
	}
	return policies, nil
}

// RecordPooledErrors rechecks opt-in and placement when persisting each event.
// A policy change fences buffered events and the event ID deduplicates retries.
func (p *PgStore) RecordPooledErrors(ctx context.Context, events []dbqueryerrors.Event) error {
	if len(events) == 0 {
		return nil
	}
	if len(events) > dbqueryerrors.MaxBatch {
		return ErrInvalid
	}
	epochs := make([]pgtype.UUID, 0, len(events))
	for _, event := range events {
		if !event.Valid() {
			return ErrInvalid
		}
		epochs = append(epochs, mustUUID(event.Epoch))
	}
	batch, err := json.Marshal(events)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		q := sqlc.New()
		// Serialize concurrent writes to the same history so the row cap
		// holds even when several proxies report after a failover.
		databases, err := q.LockNativePostgresErrorHistoryPolicies(ctx, tx, epochs)
		if err != nil || len(databases) == 0 {
			return err
		}
		if err := q.AppendNativePostgresErrorEvents(ctx, tx, batch); err != nil {
			return err
		}
		return q.TrimNativePostgresErrorEvents(ctx, tx, sqlc.TrimNativePostgresErrorEventsParams{
			DatabaseIds: databases, RetainedCount: dbqueryerrors.MaxRetained,
		})
	})
}

func (p *PgStore) pruneErrorHistory(ctx context.Context) error {
	return sqlc.New().ExpireNativePostgresErrorEvents(ctx, p.pool)
}

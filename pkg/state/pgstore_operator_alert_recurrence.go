package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) OperatorAlertRecurrence(ctx context.Context, input OperatorAlertRecurrenceFilter) (OperatorAlertRecurrenceSnapshot, error) {
	out := OperatorAlertRecurrenceSnapshot{Episodes: []OperatorAlertRecurrenceEpisode{}}
	f, err := normalizeAlertRecurrenceFilter(input)
	if err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("recurrence snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	collection, err := q.OperatorAlertCollection(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("recurrence collection: %w", err)
	}
	out.Collection = pgAlertCollection(collection)
	retention, err := q.OperatorAlertRetentionStatus(ctx, tx, pgtype.Timestamptz{Time: f.Now.Add(-api.ObsAlertHistoryRetention), Valid: true})
	if err != nil {
		return out, fmt.Errorf("recurrence retention: %w", err)
	}
	out.Retention = pgAlertRetention(retention)
	out.CollectionOutcomes, err = pgAlertCollectionOutcomes(ctx, q, tx, f.Since, f.Now)
	if err != nil {
		return out, err
	}
	if out.Collection.LastSuccessAt.IsZero() {
		return out, tx.Commit(ctx)
	}
	rows, err := q.OperatorAlertRecurrenceEpisodes(ctx, tx, sqlc.OperatorAlertRecurrenceEpisodesParams{Since: pgtype.Timestamptz{Time: f.Since, Valid: true}, AsOf: pgtype.Timestamptz{Time: f.Now, Valid: true}, FreshSince: pgtype.Timestamptz{Time: f.Now.Add(-api.ObsHealthFreshnessMaxAge), Valid: true}, FutureBefore: pgtype.Timestamptz{Time: f.Now.Add(api.ObsHealthClockSkewTolerance), Valid: true}, CollectionEnabled: f.CollectionEnabled, RowLimit: api.ObsAlertRecurrenceEpisodesMax + 1})
	if err != nil {
		return out, fmt.Errorf("recurrence cohort: %w", err)
	}
	if len(rows) > api.ObsAlertRecurrenceEpisodesMax {
		return out, &OperatorAlertRecurrenceLimitError{Limit: api.ObsAlertRecurrenceEpisodesMax}
	}
	for _, r := range rows {
		var resolved *time.Time
		if r.ResolvedAt.Valid {
			at := r.ResolvedAt.Time.UTC()
			resolved = &at
		}
		out.Episodes = append(out.Episodes, OperatorAlertRecurrenceEpisode{ID: r.ID, RuleKey: r.RuleKey, SeriesID: r.SeriesID, Name: r.Name, Severity: r.Severity, Component: r.Component, AccountID: r.AccountID, AppID: r.AppID, NodeID: r.NodeID, Status: r.Status, StartedAt: r.StartedAt.Time.UTC(), ResolvedAt: resolved})
	}
	return out, tx.Commit(ctx)
}

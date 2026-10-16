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

func (s *PgStore) OperatorAlertCollectionHistory(ctx context.Context, input OperatorAlertCollectionHistoryFilter) (OperatorAlertCollectionHistorySnapshot, error) {
	out := OperatorAlertCollectionHistorySnapshot{Outcomes: []OperatorAlertCollectionOutcome{}}
	f, err := normalizeCollectionHistoryFilter(input)
	if err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("collection history snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.OperatorAlertCollection(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("collection history status: %w", err)
	}
	out.Collection = pgAlertCollection(row)
	out.Outcomes, err = pgAlertCollectionOutcomes(ctx, q, tx, f.Since, f.Now)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func pgAlertCollectionOutcomes(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, since, now time.Time) ([]OperatorAlertCollectionOutcome, error) {
	rows, err := q.OperatorAlertCollectionOutcomes(ctx, tx, sqlc.OperatorAlertCollectionOutcomesParams{Since: pgtype.Timestamptz{Time: collectionOutcomeReadSince(since, now), Valid: true}, AsOf: pgtype.Timestamptz{Time: now, Valid: true}, RowLimit: api.ObsAlertCollectionHistoryOutcomesMax + 1})
	if err != nil {
		return nil, fmt.Errorf("collection outcomes: %w", err)
	}
	if len(rows) > api.ObsAlertCollectionHistoryOutcomesMax {
		return nil, &OperatorAlertCollectionHistoryLimitError{Limit: api.ObsAlertCollectionHistoryOutcomesMax}
	}
	out := []OperatorAlertCollectionOutcome{}
	for _, r := range rows {
		out = append(out, OperatorAlertCollectionOutcome{AttemptedAt: r.AttemptedAt.Time.UTC(), Outcome: r.Outcome, Reason: r.Reason})
	}
	return out, nil
}

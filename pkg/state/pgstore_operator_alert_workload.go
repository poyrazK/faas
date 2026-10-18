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

func (s *PgStore) OperatorAlertWorkload(ctx context.Context, f OperatorAlertWorkloadFilter) (OperatorAlertWorkloadSnapshot, error) {
	out := OperatorAlertWorkloadSnapshot{Buckets: []OperatorAlertWorkloadBucket{}}
	if f.Now.IsZero() {
		return out, ErrInvalidArgument
	}
	f.Now = f.Now.UTC().Truncate(time.Microsecond)
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("alert workload snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	collection, err := q.OperatorAlertCollection(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("alert workload collection: %w", err)
	}
	out.Collection = pgAlertCollection(collection)
	retention, err := q.OperatorAlertRetentionStatus(ctx, tx, pgtype.Timestamptz{Time: f.Now.Add(-api.ObsAlertHistoryRetention), Valid: true})
	if err != nil {
		return out, fmt.Errorf("alert workload retention: %w", err)
	}
	out.Retention = pgAlertRetention(retention)
	if !out.Collection.LastSuccessAt.IsZero() {
		rows, err := q.OperatorAlertWorkloadBuckets(ctx, tx, sqlc.OperatorAlertWorkloadBucketsParams{NowAt: pgtype.Timestamptz{Time: f.Now, Valid: true}, CollectionEnabled: f.CollectionEnabled, FreshSince: pgtype.Timestamptz{Time: f.Now.Add(-api.ObsHealthFreshnessMaxAge), Valid: true}, FutureBefore: pgtype.Timestamptz{Time: f.Now.Add(api.ObsHealthClockSkewTolerance), Valid: true}})
		if err != nil {
			return out, fmt.Errorf("alert workload buckets: %w", err)
		}
		for _, b := range rows {
			out.Buckets = append(out.Buckets, OperatorAlertWorkloadBucket{Status: b.Status, Severity: b.Severity, Acknowledged: b.Acknowledged, Owned: b.Owned, Snoozed: b.Snoozed, Count: b.Count, OldestID: b.OldestID, OldestStartedAt: b.OldestStartedAt.Time.UTC()})
		}
	}
	return out, tx.Commit(ctx)
}

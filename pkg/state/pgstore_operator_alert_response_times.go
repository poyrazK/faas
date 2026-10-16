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

func (s *PgStore) OperatorAlertResponseTimes(ctx context.Context, input OperatorAlertResponseTimesFilter) (OperatorAlertResponseTimesSnapshot, error) {
	out := OperatorAlertResponseTimesSnapshot{Episodes: []OperatorAlertResponseTimesEpisode{}}
	f, err := normalizeAlertResponseTimesFilter(input)
	if err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("response timing snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	collection, err := q.OperatorAlertCollection(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("response timing collection: %w", err)
	}
	out.Collection = pgAlertCollection(collection)
	retention, err := q.OperatorAlertRetentionStatus(ctx, tx, pgtype.Timestamptz{Time: f.Now.Add(-api.ObsAlertHistoryRetention), Valid: true})
	if err != nil {
		return out, fmt.Errorf("response timing retention: %w", err)
	}
	out.Retention = pgAlertRetention(retention)
	out.CollectionOutcomes, err = pgAlertCollectionOutcomes(ctx, q, tx, f.Since, f.Now)
	if err != nil {
		return out, err
	}
	if out.Collection.LastSuccessAt.IsZero() {
		return out, tx.Commit(ctx)
	}
	rows, err := q.OperatorAlertResponseTimesEpisodes(ctx, tx, sqlc.OperatorAlertResponseTimesEpisodesParams{Since: pgtype.Timestamptz{Time: f.Since, Valid: true}, AsOf: pgtype.Timestamptz{Time: f.Now, Valid: true}, FreshSince: pgtype.Timestamptz{Time: f.Now.Add(-api.ObsHealthFreshnessMaxAge), Valid: true}, FutureBefore: pgtype.Timestamptz{Time: f.Now.Add(api.ObsHealthClockSkewTolerance), Valid: true}, CollectionEnabled: f.CollectionEnabled, RowLimit: api.ObsAlertResponseTimesEpisodesMax + 1})
	if err != nil {
		return out, fmt.Errorf("response timing cohort: %w", err)
	}
	if len(rows) > api.ObsAlertResponseTimesEpisodesMax {
		return out, &OperatorAlertResponseTimesLimitError{Resource: "episodes", Limit: api.ObsAlertResponseTimesEpisodesMax}
	}
	ids := []string{}
	for _, r := range rows {
		var resolved *time.Time
		if r.ResolvedAt.Valid {
			at := r.ResolvedAt.Time.UTC()
			resolved = &at
		}
		out.Episodes = append(out.Episodes, OperatorAlertResponseTimesEpisode{ID: r.ID, Severity: r.Severity, Component: r.Component, Status: r.Status, StartedAt: r.StartedAt.Time.UTC(), ResolvedAt: resolved, Revision: r.TriageRevision})
		ids = append(ids, r.ID)
	}
	receipts, err := q.OperatorAlertResponseTimesReceipts(ctx, tx, sqlc.OperatorAlertResponseTimesReceiptsParams{EpisodeIds: ids, RowLimit: api.ObsAlertResponseTimesReceiptsMax + 1})
	if err != nil {
		return out, fmt.Errorf("response timing receipts: %w", err)
	}
	if len(receipts) > api.ObsAlertResponseTimesReceiptsMax {
		return out, &OperatorAlertResponseTimesLimitError{Resource: "receipts", Limit: api.ObsAlertResponseTimesReceiptsMax}
	}
	projected := []alertTimingReceipt{}
	for _, r := range receipts {
		projected = append(projected, alertTimingReceipt{EpisodeID: r.EpisodeID, ID: r.ID, At: r.ReceivedAt.Time.UTC(), Payload: r.Payload})
	}
	if err := applyAlertTimingReceipts(out.Episodes, projected, f.Now); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

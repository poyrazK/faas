package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) OperatorAlertTriageHistory(ctx context.Context, f OperatorAlertTriageHistoryFilter) (OperatorAlertTriageHistorySnapshot, error) {
	out := OperatorAlertTriageHistorySnapshot{Items: []api.ObsAlertTriageHistoryEntry{}}
	if err := validateOperatorAlertTriageHistoryFilter(f); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, fmt.Errorf("triage history snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	collection, err := q.OperatorAlertCollection(ctx, tx)
	if err != nil {
		return out, fmt.Errorf("triage history collection: %w", err)
	}
	out.Collection = pgAlertCollection(collection)
	episode, err := q.OperatorAlertEpisode(ctx, tx, f.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("triage history episode: %w", err)
	}
	out.Episode = pgAlertEpisode(episode)
	out.SnapshotRevision, err = triageHistoryRevision(out.Episode, f)
	if err != nil {
		return out, err
	}
	stats, err := q.OperatorAlertTriageHistoryCoverage(ctx, tx, sqlc.OperatorAlertTriageHistoryCoverageParams{EpisodeID: out.Episode.ID, SnapshotRevision: out.SnapshotRevision})
	if err != nil {
		return out, fmt.Errorf("triage receipt coverage: %w", err)
	}
	var oldest, newest *int64
	if stats.DistinctEntries > 0 {
		oldest = &stats.OldestRevision
		newest = &stats.NewestRevision
	}
	out.Coverage, err = triageHistoryCoverage(out.SnapshotRevision, stats.TotalEntries, stats.DistinctEntries, oldest, newest)
	if err != nil {
		return out, err
	}
	rows, err := q.OperatorAlertTriageHistoryEntries(ctx, tx, sqlc.OperatorAlertTriageHistoryEntriesParams{EpisodeID: out.Episode.ID, SnapshotRevision: out.SnapshotRevision, BeforeRevision: f.BeforeRevision, PageLimit: int32(f.Limit + 1)})
	if err != nil {
		return out, fmt.Errorf("triage receipt page: %w", err)
	}
	if len(rows) > f.Limit {
		rows = rows[:f.Limit]
		out.Truncated = true
	}
	for _, row := range rows {
		entry, err := decodeAlertTriageReceipt(row.ID, out.Episode.ID, row.ReceivedAt.Time, row.Payload)
		if err != nil || entry.Revision != row.Revision {
			return out, errAlertTriageReceipt
		}
		out.Items = append(out.Items, entry)
	}
	return out, tx.Commit(ctx)
}

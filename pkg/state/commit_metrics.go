package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CommitRelayObservation is an aggregate of the shared durable source health.
// Unknown sources are excluded from backlog totals, not counted as empty.
type CommitRelayObservation struct {
	EnabledSources         int64
	UnknownSources         int64
	FailingSources         int64
	PendingEvents          int64
	BlockedEvents          int64
	OldestPendingTimestamp float64
}

func (s *PgStore) CommitRelayObservationSummary(ctx context.Context, freshAfter time.Time) (CommitRelayObservation, error) {
	row, err := sqlc.New().CommitRelayObservationSummary(ctx, s.pool, pgtype.Timestamptz{Time: freshAfter, Valid: true})
	return CommitRelayObservation{
		EnabledSources: row.EnabledSources, UnknownSources: row.UnknownSources, FailingSources: row.FailingSources,
		PendingEvents: row.PendingEvents, BlockedEvents: row.BlockedEvents, OldestPendingTimestamp: row.OldestPendingTimestamp,
	}, err
}

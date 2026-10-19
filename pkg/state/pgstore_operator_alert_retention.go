package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) RecordOperatorAlertRetentionFailure(ctx context.Context, at time.Time) error {
	if at.IsZero() {
		return ErrInvalidArgument
	}
	if err := sqlc.New().OperatorAlertRetentionFailed(ctx, s.pool, pgtype.Timestamptz{Time: at.UTC().Truncate(time.Microsecond), Valid: true}); err != nil {
		return fmt.Errorf("alert retention failure: %w", err)
	}
	return nil
}

func (s *PgStore) OperatorAlertRetentionStatus(ctx context.Context, now time.Time) (OperatorAlertRetention, error) {
	if now.IsZero() {
		return OperatorAlertRetention{}, ErrInvalidArgument
	}
	now = now.UTC().Truncate(time.Microsecond)
	row, err := sqlc.New().OperatorAlertRetentionStatus(ctx, s.pool, pgtype.Timestamptz{Time: now.Add(-api.ObsAlertHistoryRetention), Valid: true})
	if err != nil {
		return OperatorAlertRetention{}, fmt.Errorf("alert retention status: %w", err)
	}
	return pgAlertRetention(row), nil
}

func pgAlertRetention(row sqlc.OperatorAlertRetentionStatusRow) OperatorAlertRetention {
	out := OperatorAlertRetention{LastPrunedEpisodes: row.LastPrunedEpisodes, ExpiredEpisodes: row.ExpiredEpisodes}
	if row.LastAttemptAt.Valid {
		out.LastAttemptAt = row.LastAttemptAt.Time.UTC()
	}
	if row.LastSuccessAt.Valid {
		out.LastSuccessAt = row.LastSuccessAt.Time.UTC()
	}
	if row.OldestExpiredAt.Valid {
		out.OldestExpiredAt = row.OldestExpiredAt.Time.UTC()
	}
	return out
}

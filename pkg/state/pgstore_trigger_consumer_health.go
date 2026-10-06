package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RecordTriggerConsumerHealth upserts the scheduler's last poll observation.
// A successful poll advances last_success_at and preserves the most recent
// error for diagnosis; an error advances last_error_at and records its detail.
func (s *PgStore) RecordTriggerConsumerHealth(ctx context.Context, triggerID string, observation TriggerConsumerHealthObservation) error {
	params := sqlc.RecordTriggerConsumerHealthParams{
		TriggerID:   mustPgUUID(triggerID),
		PolledAt:    pgtype.Timestamptz{Time: observation.LastPollAt, Valid: true},
		Success:     observation.Success,
		ErrorDetail: observation.Error,
	}
	if observation.LagMessages != nil {
		params.LagMessages = pgtype.Int8{Int64: *observation.LagMessages, Valid: true}
	}
	if observation.LagAgeSeconds != nil {
		params.LagAgeSeconds = pgtype.Float8{Float64: *observation.LagAgeSeconds, Valid: true}
	}
	return sqlc.New().RecordTriggerConsumerHealth(ctx, s.pool, params)
}

// TriggerConsumerHealth returns the last-known scheduler observation.
func (s *PgStore) TriggerConsumerHealth(ctx context.Context, triggerID string) (TriggerConsumerHealth, error) {
	row, err := sqlc.New().TriggerConsumerHealth(ctx, s.pool, mustPgUUID(triggerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return TriggerConsumerHealth{}, ErrNotFound
	}
	if err != nil {
		return TriggerConsumerHealth{}, err
	}
	return TriggerConsumerHealth{
		LastPollAt:    optionalHealthTime(row.LastPollAt),
		LastSuccessAt: optionalHealthTime(row.LastSuccessAt),
		LastErrorAt:   optionalHealthTime(row.LastErrorAt),
		LastError:     optionalHealthText(row.LastError),
		LagMessages:   optionalHealthInt64(row.LagMessages),
		LagAgeSeconds: optionalHealthFloat64(row.LagAgeSeconds),
	}, nil
}

func optionalHealthTime(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time.UTC()
	return &t
}

func optionalHealthText(v pgtype.Text) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func optionalHealthInt64(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

func optionalHealthFloat64(v pgtype.Float8) *float64 {
	if !v.Valid {
		return nil
	}
	n := v.Float64
	return &n
}

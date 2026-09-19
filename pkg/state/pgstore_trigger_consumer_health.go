package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// RecordTriggerConsumerHealth upserts the scheduler's last poll observation.
// A successful poll advances last_success_at and preserves the most recent
// error for diagnosis; an error advances last_error_at and records its detail.
func (s *PgStore) RecordTriggerConsumerHealth(ctx context.Context, triggerID string, observation TriggerConsumerHealthObservation) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO trigger_consumer_health (
			trigger_id, last_poll_at, last_success_at, last_error_at, last_error,
			lag_messages, lag_age_seconds
		) VALUES (
			$1, $2, CASE WHEN $3 THEN $2 ELSE NULL END,
			CASE WHEN $3 THEN NULL ELSE $2 END,
			CASE WHEN $3 THEN NULL ELSE NULLIF($4, '') END,
			$5, $6
		)
		ON CONFLICT (trigger_id) DO UPDATE SET
			last_poll_at = EXCLUDED.last_poll_at,
			last_success_at = CASE WHEN $3 THEN EXCLUDED.last_poll_at ELSE trigger_consumer_health.last_success_at END,
			last_error_at = CASE WHEN $3 THEN trigger_consumer_health.last_error_at ELSE EXCLUDED.last_poll_at END,
			last_error = CASE WHEN $3 THEN trigger_consumer_health.last_error ELSE NULLIF($4, '') END,
			lag_messages = EXCLUDED.lag_messages,
			lag_age_seconds = EXCLUDED.lag_age_seconds,
			updated_at = NOW()
	`, mustPgUUID(triggerID), observation.LastPollAt, observation.Success, observation.Error, observation.LagMessages, observation.LagAgeSeconds)
	return err
}

// TriggerConsumerHealth returns the last-known scheduler observation.
func (s *PgStore) TriggerConsumerHealth(ctx context.Context, triggerID string) (TriggerConsumerHealth, error) {
	var (
		lastPoll, lastSuccess, lastError pgtype.Timestamptz
		lastErr                          pgtype.Text
		lagMessages                      pgtype.Int8
		lagAgeSeconds                    pgtype.Float8
	)
	err := s.pool.QueryRow(ctx, `
		SELECT last_poll_at, last_success_at, last_error_at, last_error,
		       lag_messages, lag_age_seconds
		FROM trigger_consumer_health
		WHERE trigger_id = $1
	`, mustPgUUID(triggerID)).Scan(&lastPoll, &lastSuccess, &lastError, &lastErr, &lagMessages, &lagAgeSeconds)
	if errors.Is(err, pgx.ErrNoRows) {
		return TriggerConsumerHealth{}, ErrNotFound
	}
	if err != nil {
		return TriggerConsumerHealth{}, err
	}
	return TriggerConsumerHealth{
		LastPollAt:    optionalHealthTime(lastPoll),
		LastSuccessAt: optionalHealthTime(lastSuccess),
		LastErrorAt:   optionalHealthTime(lastError),
		LastError:     optionalHealthText(lastErr),
		LagMessages:   optionalHealthInt64(lagMessages),
		LagAgeSeconds: optionalHealthFloat64(lagAgeSeconds),
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

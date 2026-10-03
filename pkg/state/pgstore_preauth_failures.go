package state

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
)

// CheckPreAuthFailure checks a shared response-driven budget without spending
// a token. A missing row has its full burst available. RecordPreAuthFailure
// serializes later failures against this same row, including in-flight debt.
func (b *PGRateLimitBackend) CheckPreAuthFailure(ctx context.Context, subjectID, plan string, rps float64, burst int) (bool, int, error) {
	if b == nil || b.pool == nil || rps <= 0 || burst <= 0 {
		return false, 0, errors.New("pre-auth failure counter unavailable")
	}
	const q = `
		SELECT LEAST($3::bigint, tokens + FLOOR(GREATEST(0,
			EXTRACT(EPOCH FROM (now() - last_refill))) * $4)::bigint)
		FROM pg_preauth_failure_counters
		WHERE subject_id = $1 AND plan = $2`
	var available int64
	if err := b.pool.QueryRow(ctx, q, subjectID, plan, burst, rps).Scan(&available); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, 0, nil
		}
		return false, 0, fmt.Errorf("check pre-auth failure counter: %w", err)
	}
	if available >= 1 {
		return true, 0, nil
	}
	return false, max(1, int(math.Ceil((1-float64(available))/rps))), nil
}

// RecordPreAuthFailure spends a token only after a selected application
// failure. Balances can go negative when concurrent requests were admitted
// before their responses arrived, but are capped at -burst to bound debt and
// guarantee that seven idle days fully refill any row at the minimum 1/minute.
func (b *PGRateLimitBackend) RecordPreAuthFailure(ctx context.Context, subjectID, plan string, rps float64, burst int) error {
	if b == nil || b.pool == nil || rps <= 0 || burst <= 0 {
		return errors.New("pre-auth failure counter unavailable")
	}
	const q = `
		INSERT INTO pg_preauth_failure_counters (subject_id, plan, tokens, last_refill)
		VALUES ($1, $2, $3::bigint - 1, now())
		ON CONFLICT (subject_id, plan) DO UPDATE SET
			tokens = GREATEST(-$3::bigint,
				LEAST($3::bigint, pg_preauth_failure_counters.tokens
					+ FLOOR(GREATEST(0, EXTRACT(EPOCH FROM
						(now() - pg_preauth_failure_counters.last_refill))) * $4)::bigint) - 1),
			last_refill = CASE
				WHEN pg_preauth_failure_counters.tokens
					+ FLOOR(GREATEST(0, EXTRACT(EPOCH FROM
						(now() - pg_preauth_failure_counters.last_refill))) * $4)::bigint >= $3::bigint
				THEN now()
				ELSE pg_preauth_failure_counters.last_refill
					+ (FLOOR(GREATEST(0, EXTRACT(EPOCH FROM
						(now() - pg_preauth_failure_counters.last_refill))) * $4) / $4)
					  * interval '1 second'
			END`
	if _, err := b.pool.Exec(ctx, q, subjectID, plan, burst, rps); err != nil {
		return fmt.Errorf("record pre-auth failure counter: %w", err)
	}
	return nil
}

// PrunePreAuthFailureCounters removes idle rows only after every possible
// capped debt has refilled at the minimum configured failure rate (1/minute).
func (b *PGRateLimitBackend) PrunePreAuthFailureCounters(ctx context.Context) (int64, error) {
	const q = `
		WITH stale AS (
			SELECT ctid FROM pg_preauth_failure_counters
			WHERE last_refill < now() - interval '7 days'
			ORDER BY last_refill
			LIMIT 1000 FOR UPDATE SKIP LOCKED
		)
		DELETE FROM pg_preauth_failure_counters
		WHERE ctid IN (SELECT ctid FROM stale)`
	result, err := b.pool.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("prune pre-auth failure counters: %w", err)
	}
	return result.RowsAffected(), nil
}

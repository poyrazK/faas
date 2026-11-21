package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ConsumeEventPublishRate takes n events from the account's shared publish
// counter (scope event_publish in pg_ratelimit_counters), all or nothing.
// Every apid replica and vmmd node charges the same row, so the plan's
// api.EventPublishLimits hold across processes (event-driven hunt #16). A
// zero rate admits everything; n above the burst never fits and is refused.
func (s *PgStore) ConsumeEventPublishRate(ctx context.Context, accountID string, plan api.Plan, limits api.EventPublishLimits, n int) (bool, error) {
	if limits.PerSecond <= 0 || n <= 0 {
		return true, nil
	}
	burst := float64(max(limits.Burst, 1))
	if float64(n) > burst {
		return false, nil
	}
	id, err := uuid.Parse(accountID)
	if err != nil {
		return false, fmt.Errorf("state: event publish rate subject: %w", err)
	}
	subject := pgtype.UUID{Bytes: id, Valid: true}
	q := sqlc.New()
	// Two attempts: a concurrent first publish can create the row between
	// this statement's read and its insert.
	for range 2 {
		row, err := q.ConsumeEventPublishTokens(ctx, s.pool, sqlc.ConsumeEventPublishTokensParams{
			SubjectID: subject, Plan: string(plan), Burst: burst, Rps: float64(limits.PerSecond), N: int64(n),
		})
		if err != nil {
			return false, fmt.Errorf("state: consume event publish rate: %w", err)
		}
		if row.Found {
			return row.Granted, nil
		}
		_, err = q.CreateEventPublishCounter(ctx, s.pool, sqlc.CreateEventPublishCounterParams{
			SubjectID: subject, Plan: string(plan), Burst: burst, N: int64(n),
		})
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, fmt.Errorf("state: create event publish rate counter: %w", err)
		}
	}
	return false, fmt.Errorf("state: event publish rate counter for %s raced its creation twice", accountID)
}

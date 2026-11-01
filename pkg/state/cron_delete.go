package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// cronPendingCancelBatch bounds the not-yet-started runs one pass cancels.
// A cron has a handful at most: its single-attempt runs and up to
// api.CronFireNowActivePerCron manual runs.
const cronPendingCancelBatch = 100

// cancelPendingCronRuns cancels a cron's runs that have not started, queued
// or waiting to retry, so a deleted cron never calls the app again. A run
// already dispatched finishes. An id that is not a UUID has no runs.
func (s *PgStore) cancelPendingCronRuns(ctx context.Context, cronID, appID string) error {
	cron, app, ok := pgUUIDPair(cronID, appID)
	if !ok {
		return nil
	}
	params := sqlc.ListPendingCronInvocationIDsParams{CronID: cron, AppID: app, RowLimit: cronPendingCancelBatch}
	for {
		ids, err := sqlc.New().ListPendingCronInvocationIDs(ctx, s.pool, params)
		if err != nil {
			return fmt.Errorf("state: list a deleted cron's pending runs: %w", err)
		}
		for _, id := range ids {
			if _, err := s.CancelPendingInvocation(ctx, id); err != nil && !errors.Is(err, ErrNotFound) {
				return fmt.Errorf("state: cancel a deleted cron's run: %w", err)
			}
		}
		if len(ids) < cronPendingCancelBatch {
			return nil
		}
	}
}

// pgUUIDPair parses two ids, or reports false when either is not a UUID.
func pgUUIDPair(a, b string) (pgtype.UUID, pgtype.UUID, bool) {
	x, errA := uuid.Parse(a)
	y, errB := uuid.Parse(b)
	ok := errA == nil && errB == nil
	return pgtype.UUID{Bytes: x, Valid: ok}, pgtype.UUID{Bytes: y, Valid: ok}, ok
}

// cancelPendingCronRunsLocked is the MemStore mirror of cancelPendingCronRuns.
func (m *MemStore) cancelPendingCronRunsLocked(cronID string, now time.Time) error {
	for id, inv := range m.invocations {
		if inv.CronID == nil || *inv.CronID != cronID || inv.State != InvocationPending {
			continue
		}
		if err := m.cancelPendingInvocationLocked(id, inv, now); err != nil {
			return err
		}
	}
	return nil
}

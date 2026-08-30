package sched

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TriggersRetention is the ADR-134 PR-E retention sweep for
// trigger_records rows.
//
// Mirrors pkg/sched/retention_invocations.go: DELETEs rows whose
// result_retention_until is in the past. The dispatcher's
// hot path (ClaimTriggerRecords) only reads rows in
// (pending|claimed|retry) — the reaper's DELETE on terminal
// (succeeded|dead_letter) rows cannot race the dispatcher.
//
// Pattern mirrors pkg/sched/retention.go:
//
//   - injected Now() for test-time freezing,
//   - per-row errors logged + continued (one stuck row never
//     stalls the rest),
//   - ErrNotFound swallowed for redelivery safety.
type TriggersRetention struct {
	store state.Store
	now   func() time.Time
	log   *slog.Logger
}

// NewTriggersRetention returns the sweep ready for the Loop
// ticker. log defaults to slog.Default.
func NewTriggersRetention(store state.Store, log *slog.Logger) *TriggersRetention {
	if store == nil {
		panic("sched: TriggersRetention.store is required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &TriggersRetention{
		store: store,
		now:   time.Now,
		log:   log,
	}
}

// WithClock injects a frozen time source for tests. Same shape as
// InvocationsRetention.WithClock.
func (r *TriggersRetention) WithClock(now func() time.Time) *TriggersRetention {
	if now != nil {
		r.now = now
	}
	return r
}

// SweepOnce deletes trigger_records rows whose
// result_retention_until is in the past. Returns the count of
// rows deleted (0 is a normal outcome — every tick after the
// first sweep is a no-op).
//
// Errors:
//   - listing failure: returned (caller logs).
//   - per-row delete failure other than ErrNotFound: logged,
//     counted, and the loop continues.
func (r *TriggersRetention) SweepOnce(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 1000
	}
	now := r.now()
	ids, err := r.store.ListExpiredTriggerRecordsForReaper(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, id := range ids {
		if _, err := r.store.DeleteTriggerRecordsByIDs(ctx, []string{id}); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			r.log.Warn("trigger_records retention: delete failed", "id", id, "err", err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		r.log.Info("trigger_records retention sweep", "deleted", deleted, "cutoff", now.Format(time.RFC3339))
	}
	return deleted, nil
}

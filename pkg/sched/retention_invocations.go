package sched

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// InvocationsRetention is the §17 / ADR-134 PR-B retention +
// deadline-breach sweep for invocations rows.
//
// Two sweeps, one ticker:
//
//   - Retention sweep: DELETEs invocations rows whose
//     result_retention_until is in the past. The drain's hot
//     path (ListDueInvocations, ClaimInvocation) ignores terminal
//     rows entirely; deleting them after the retention horizon
//     keeps the table bounded for the dashboard's account-scoped
//     history read.
//   - Deadline-breach sweep: transitions (pending|dispatching)
//     rows whose deadline_at is in the past to dead_letter with
//     outcome='deadline'. Decrements the per-account counter so
//     the cap reflects the abandoned work.
//
// Both sweeps are idempotent and tolerate the missing-cap-row
// condition (DecrementAccountAsyncInflight swallows zero-row).
//
// Pattern mirrors pkg/sched/retention.go:
//
//   - injected Now() for test-time freezing,
//   - per-row errors logged + continued (one stuck row never
//     stalls the rest),
//   - ErrNotFound swallowed for redelivery safety.
type InvocationsRetention struct {
	store state.Store
	now   func() time.Time
	log   *slog.Logger
}

// NewInvocationsRetention returns the sweep ready for the Loop
// ticker. log defaults to slog.Default.
func NewInvocationsRetention(store state.Store, log *slog.Logger) *InvocationsRetention {
	if store == nil {
		panic("sched: InvocationsRetention.store is required")
	}
	if log == nil {
		log = slog.Default()
	}
	return &InvocationsRetention{
		store: store,
		now:   time.Now,
		log:   log,
	}
}

// WithClock injects a frozen time source for tests. Same shape as
// Retention.WithClock.
func (r *InvocationsRetention) WithClock(now func() time.Time) *InvocationsRetention {
	if now != nil {
		r.now = now
	}
	return r
}

// SweepRetention deletes rows whose result_retention_until is in
// the past. Returns the count of rows deleted (0 is a normal
// outcome — every tick after the first sweep is a no-op).
//
// Errors:
//   - listing failure: returned (caller logs).
//   - per-row delete failure other than ErrNotFound: logged,
//     counted, and the loop continues.
func (r *InvocationsRetention) SweepRetention(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	now := r.now()
	ids, err := r.store.ListExpiredInvocationsForReaper(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, id := range ids {
		if _, err := r.store.DeleteInvocationsByIDs(ctx, []string{id}); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			r.log.Warn("invocations retention: delete failed", "id", id, "err", err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		r.log.Info("invocations retention sweep", "deleted", deleted, "cutoff", now.Format(time.RFC3339))
	}
	return deleted, nil
}

// SweepDeadlineBreached transitions (pending|dispatching) rows
// whose deadline_at is in the past to dead_letter. Decrements the
// per-account counter inside the same tx (atomic with the state
// transition in PgStore.ForceDeadlineBreachedInvocations).
// Returns the count of rows transitioned.
func (r *InvocationsRetention) SweepDeadlineBreached(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	now := r.now()
	ids, err := r.store.ListDeadlineBreachedInvocations(ctx, now, limit)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	n, err := r.store.ForceDeadlineBreachedInvocations(ctx, ids)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		r.log.Info("invocations deadline breach", "forced", n, "cutoff", now.Format(time.RFC3339))
	}
	return n, nil
}

// SweepOnce runs both sweeps back-to-back. The retention sweep is
// cheap when there are no overdue rows; the deadline sweep is
// always cheap (the partial index is bounded by the rows that
// carry a deadline, which is rare). Wired into the loop ticker at
// 60s cadence (same as Retention.SweepOnce today) so the budget
// for one tick is two cheap SELECTs.
func (r *InvocationsRetention) SweepOnce(ctx context.Context) (int, int, error) {
	retentionDeleted, err := r.SweepRetention(ctx, 500)
	if err != nil {
		return 0, 0, err
	}
	deadlineForced, err := r.SweepDeadlineBreached(ctx, 500)
	if err != nil {
		return retentionDeleted, 0, err
	}
	return retentionDeleted, deadlineForced, nil
}

package sched

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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
//     outcome='timeout'. Decrements the per-account counter so
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

	operationTelemetry *OperationTelemetry // ADR-20261016123221; nil reads nothing
}

// deadlineBreachDetailsStore is implemented by the production stores. The
// existing Store method keeps its count-only contract for older callers;
// this optional seam lets the reaper report the exact rows changed by its
// atomic update. The store also enqueues any selected destination in that
// same transition transaction.
type deadlineBreachDetailsStore interface {
	ForceDeadlineBreachedInvocationsWithDetails(context.Context, []string) ([]state.Invocation, error)
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

// WithOperationTelemetry refreshes the Customer Operations gauges after each
// sweep, so they read the state the sweep left (ADR-20261016123221).
func (r *InvocationsRetention) WithOperationTelemetry(t *OperationTelemetry) *InvocationsRetention {
	r.operationTelemetry = t
	return r
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
	// One statement per batch keeps up with the stamped backlog
	// (ADR 20261019104819); a failed batch falls back to per-row deletes
	// so one stuck row never stalls the rest.
	deleted, err := r.store.DeleteInvocationsByIDs(ctx, ids)
	if err != nil {
		deleted = 0
		for _, id := range ids {
			n, err := r.store.DeleteInvocationsByIDs(ctx, []string{id})
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					continue
				}
				r.log.Warn("invocations retention: delete failed", "id", id, "err", err)
				continue
			}
			deleted += n
		}
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
	var forced []state.Invocation
	if detailed, ok := r.store.(deadlineBreachDetailsStore); ok {
		var err error
		forced, err = detailed.ForceDeadlineBreachedInvocationsWithDetails(ctx, ids)
		if err != nil {
			return 0, err
		}
	} else {
		n, err := r.store.ForceDeadlineBreachedInvocations(ctx, ids)
		if err != nil {
			return 0, err
		}
		// Test doubles that only implement the original Store contract still
		// get destination coverage. The concrete MemStore/PgStore path above
		// is preferred because it identifies the rows changed atomically.
		for _, id := range ids {
			inv, lookupErr := r.store.InvocationByID(ctx, id)
			if lookupErr != nil || inv.State != state.InvocationDeadLetter || inv.Outcome == nil || *inv.Outcome != state.OutcomeTimeout {
				continue
			}
			forced = append(forced, inv)
		}
		if n == 0 {
			forced = nil
		}
	}
	if len(forced) > 0 {
		r.log.Info("invocations deadline breach", "forced", len(forced), "cutoff", now.Format(time.RFC3339))
	}
	return len(forced), nil
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
	// ADR-20261022105723: pending queue messages past their plan's
	// retention move to the dead letter before stamping, so the stamp
	// below gives them their result retention in the same pass.
	if expiry, ok := r.store.(state.QueueMessageExpiryStore); ok {
		r.expireQueueMessages(ctx, expiry)
	}
	// Stamp after reaping: rows stamped with an already-past horizon are
	// pruned on the next tick, and a stamping failure cannot hold up the
	// reaper or the deadline sweep below.
	if stamper, ok := r.store.(state.InvocationDefaultRetentionStore); ok {
		stamped, err := stamper.StampDefaultInvocationRetention(ctx, 500)
		if err != nil {
			r.log.Warn("invocations retention: default stamp failed", "err", err)
		} else if stamped > 0 {
			r.log.Info("invocations retention: stamped plan default", "rows", stamped)
		}
	}
	if attempts, ok := r.store.(state.InvocationAttemptRetentionStore); ok {
		if _, err := attempts.PruneInvocationAttemptHistory(ctx, r.now(), 500); err != nil {
			return retentionDeleted, 0, err
		}
	}
	if ledger, ok := r.store.(state.EventExecutionLedgerRetentionStore); ok {
		if _, err := ledger.PruneEventExecutionLedger(ctx, r.now(), api.EventExecutionLedgerPruneBatchDefault); err != nil {
			return retentionDeleted, 0, err
		}
	}
	if jobs, ok := r.store.(state.EventExecutionLineageRetryJobsStore); ok {
		if _, err := jobs.AdvanceEventExecutionLineageRetryJobs(ctx, api.EventExecutionLineageRetryJobBatch); err != nil {
			return retentionDeleted, 0, err
		}
	}
	if backfill, ok := r.store.(state.EventExecutionLineageCaseBackfillStore); ok {
		if _, err := backfill.AdvanceEventExecutionLineageCaseBackfills(ctx, api.EventExecutionLineageCaseBackfillBatch); err != nil {
			return retentionDeleted, 0, err
		}
	}
	if reconciliation, ok := r.store.(state.EventExecutionReconciliationStore); ok {
		if _, err := reconciliation.AdvanceEventExecutionReconciliations(ctx, api.EventExecutionReconciliationBatch); err != nil {
			return retentionDeleted, 0, err
		}
	}
	deadlineForced, err := r.SweepDeadlineBreached(ctx, 500)
	if err != nil {
		return retentionDeleted, 0, err
	}
	// ADR-1196: workflow Operations carry no invocation deadline, so their
	// own deadline is swept here; HTTP executions settled just above.
	if operations, ok := r.store.(state.OperationDeadlineStore); ok {
		settled, err := operations.SettleOperationDeadlines(ctx, r.now(), api.OperationDeadlineSweepBatch)
		if err != nil {
			return retentionDeleted, deadlineForced, err
		}
		if settled > 0 {
			r.log.Info("operation deadline sweep", "settled", settled)
		}
	}
	// Telemetry is best effort: a failed read keeps the last snapshot, whose
	// refresh timestamp then shows its age.
	if err := r.operationTelemetry.Refresh(ctx, r.now()); err != nil {
		r.log.Warn("operation telemetry refresh failed", "err", err)
	}
	return retentionDeleted, deadlineForced, nil
}

// A sweep expires up to queueExpiryBatches batches of queueExpiryBatch
// messages, each its own transaction: at the 60 s cadence that clears a
// million-message backlog in under two hours without one long transaction.
const (
	queueExpiryBatch   = 1000
	queueExpiryBatches = 10
)

// expireQueueMessages moves pending queue messages past their plan's
// retention to the dead letter (ADR-20261022105723). A failure is logged
// and retried next sweep; it never holds up the reaper.
func (r *InvocationsRetention) expireQueueMessages(ctx context.Context, expiry state.QueueMessageExpiryStore) {
	expired := 0
	for range queueExpiryBatches {
		n, err := expiry.ExpirePendingQueueMessages(ctx, r.now(), queueExpiryBatch)
		expired += n
		if err != nil {
			r.log.Warn("invocations retention: queue message expiry failed", "err", err)
			break
		}
		if n < queueExpiryBatch {
			break
		}
	}
	if expired > 0 {
		r.log.Info("invocations retention: queue messages expired to the dead letter", "messages", expired)
	}
}

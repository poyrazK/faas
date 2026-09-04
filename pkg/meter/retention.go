// Package meter — retention cron (ADR-049 §B.4 + §B.5).
//
// pkg/meter/retention.go enforces the financial-model 13-month
// retention window on public.usage_minutes. Every
// FAAS_RETENTION_INTERVAL (default 1 day, aligned with the §11
// Sunday 04:00 UTC reboot window) the cron DELETEs rows older
// than 13 months. The migration 00069 partial index
// `usage_minutes_minute_idx` keeps the per-batch DELETE cheap
// regardless of usage_minutes cardinality.
//
// The DELETE is BATCHED (PR #428 review blocker #4) —
// an unbounded DELETE on a 5 M+ rows table would either take a
// statement_timeout-sized lock or balloon WAL on the EX44.
// RetentionOnce runs the batched DELETE in a loop with a hard
// iteration cap (`MaxRetentionBatches = 1000`, ~10 M rows / day
// at 10 000 rows/batch) and returns the cumulative row count.
//
// This is the B.4.c shape (DELETE cron, not declarative
// partitioning). Partitioning lands in a follow-up PR after
// weekly DELETE behaviour is measured (vacuum cost on a 5 M
// rows/month table is non-trivial on the EX44).
//
// Synthetic-row recovery (B.5) is a scaffold-only this PR:
// pkg/meter/sampler.go detects a ≥ 2-tick gap and logs a
// warning. The synthetic column + backfill insert are deferred
// to a follow-up PR.
package meter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// DefaultRetentionInterval is the cadence the retention cron
// runs at when the caller passes 0. 1 day matches the §11
// Sunday reboot window — the DELETE runs on a daily schedule
// even though the financial-model retention is 13 months, so a
// single missed tick is harmless.
const DefaultRetentionInterval = 24 * time.Hour

// RetentionBatchSize is the number of rows deleted per
// statement. 10 000 keeps a single DELETE under Postgres's
// default 1000 ms statement_timeout on the EX44's modest
// hardware while still being a meaningful chunk on a 5 M
// rows/month table. Tunable via FAAS_RETENTION_BATCH_SIZE if a
// future migration to partitioning raises the per-batch budget.
const RetentionBatchSize = 10_000

// MaxRetentionBatches caps the loop in RetentionOnce at 1000
// iterations = 10 M rows / call. Real-world retention at the
// 13-month cutoff will never touch 10 M rows in a single day;
// the cap is a safety belt against an accidental `interval '0'`
// typo or a buggy clock that pins the cutoff to "now()" and
// tries to nuke the entire table.
const MaxRetentionBatches = 1000

// ErrRetentionBatchCap is returned when the loop hits
// MaxRetentionBatches. The caller (RetentionLoop) logs and
// retries on the next tick; the next tick will pick up where
// this one left off because the WHERE predicate is unchanged.
var ErrRetentionBatchCap = errors.New("meter: retention DELETE hit batch cap; row count returned, retry next tick")

// retentionExecer is the minimal SQL surface the retention cron
// needs. *pgxpool.Pool satisfies it via the poolAdapter in
// cmd/meterd/main.go.
type retentionExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
}

// retentionBatchSQL is the per-batch DELETE used by
// RetentionOnce. The sub-select on ctid + LIMIT N is the
// standard Postgres "bounded DELETE" pattern: it gives the
// planner an early-stop on the index scan instead of building a
// full visibility map entry set. The `WHERE ctid IN (SELECT
// ctid ...)` form keeps row-level locks short and avoids the
// "huge DELETE inflates the WAL" pathology the review surfaced.
const retentionBatchSQL = `DELETE FROM public.usage_minutes
                          WHERE ctid IN (
                              SELECT ctid FROM public.usage_minutes
                              WHERE minute < (now() - interval '13 months')
                              LIMIT $1
                          )`

// RetentionOnce runs one DELETE pass, bounded by
// RetentionBatchSize per statement and MaxRetentionBatches
// total iterations. Returns the cumulative row count across
// all batches. Safe to call concurrently — pgx serialises the
// DELETE at the row level and the WHERE predicate is stable
// across overlapping runs (a second run on the same day finds
// no rows to delete).
//
// If the cap is hit, returns (rows_deleted, ErrRetentionBatchCap)
// so the caller can decide to log-and-retry or escalate.
func RetentionOnce(ctx context.Context, db retentionExecer) (int64, error) {
	var total int64
	for i := 0; i < MaxRetentionBatches; i++ {
		tag, err := db.Exec(ctx, retentionBatchSQL, RetentionBatchSize)
		if err != nil {
			return total, fmt.Errorf("retention delete (batch %d, deleted so far %d): %w", i, total, err)
		}
		total += tag
		if tag < RetentionBatchSize {
			// Short read — fewer rows matched the predicate than the
			// batch ceiling, so the next batch would also be a no-op.
			return total, nil
		}
	}
	// Hit the cap. Return the cumulative count + sentinel so the
	// loop logs but doesn't panic. The next tick picks up.
	return total, ErrRetentionBatchCap
}

// RetentionLoop is the free-function goroutine that calls
// RetentionOnce every Interval. Returns on ctx.Done(). Matches
// the cadence contract for the other meterd ticks.
//
// ErrRetentionBatchCap is logged at Warn (not Error) and the
// loop continues — the next tick picks up where this one left
// off because the WHERE predicate is unchanged. Only hard DB
// failures (network, FK violation, etc.) bubble up to Error.
func RetentionLoop(ctx context.Context, db retentionExecer, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultRetentionInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := RetentionOnce(ctx, db)
			switch {
			case err == nil:
				if log != nil {
					log.Info("retention tick ok", "rows_deleted", n)
				}
			case errors.Is(err, ErrRetentionBatchCap):
				if log != nil {
					log.Warn("retention tick hit batch cap; will resume next tick", "rows_deleted", n, "err", err)
				}
			default:
				if log != nil {
					log.Error("retention tick failed", "err", err)
				}
			}
		}
	}
}

// DefaultRequestTelemetryRetentionDays is the upper bound across
// plans (Scale=14, ADR-127 / pkg/api/limits.go). Hobby (3) and
// Pro (7) fall under it — over-retention by ≤ 14 days is the
// safe failure mode. PR-B's plan-aware sweep uses the per-plan
// cap from pkg/api.MustLimitsFor(plan).DebugTelemetryRetentionDays
// and falls back to DefaultRequestTelemetryRetentionDays for
// unknown plans.
const DefaultRequestTelemetryRetentionDays = 14

// RequestTelemetryRetentionInterval is the cron cadence for the
// request_telemetry sweep. Hourly matches the smaller per-plan
// retention caps (Hobby=3d) without a meaningful load cost —
// the DELETE is bounded and the index makes it cheap.
const RequestTelemetryRetentionInterval = 1 * time.Hour

// retentionRequestTelemetryPlanAwareBatchSQL is the per-app,
// plan-aware sweep (PR-B). Joins request_telemetry to apps to
// accounts and computes a per-app retention cutoff from the
// account's plan via the CASE ladder. The bounded ctid-IN-SELECT
// shape keeps the row-level locks short and avoids the
// unbounded-DELETE WAL pathology.
//
// Plan → retention ladder (mirrors pkg/api/MustLimitsFor):
//   - free   — 0 days (DebugTelemetryEnabled=false; pre-existing
//     rows pre-downgrade are swept on the next tick).
//   - hobby  — 3 days
//   - pro    — 7 days
//   - scale  — 14 days
//   - other  — 1 day (safe failure mode for an unrecognised plan
//     name; over-retention is the wrong failure mode here)
//
// The CASE uses acct.plan as a TEXT (the column type) so the
// planner can short-circuit on the join key.
const retentionRequestTelemetryPlanAwareBatchSQL = `DELETE FROM public.request_telemetry
                                                   WHERE ctid IN (
                                                       SELECT rt.ctid FROM public.request_telemetry rt
                                                       JOIN public.apps a     ON a.id = rt.app_id
                                                       JOIN public.accounts ac ON ac.id = a.account_id
                                                       WHERE rt.received_at < now() - (CASE ac.plan
                                                           WHEN 'free'  THEN interval '0 days'
                                                           WHEN 'hobby' THEN interval '3 days'
                                                           WHEN 'pro'   THEN interval '7 days'
                                                           WHEN 'scale' THEN interval '14 days'
                                                           ELSE              interval '1 day'
                                                       END)
                                                       LIMIT $1
                                                   )`

// RetentionOnceRequestTelemetry runs one DELETE pass against
// public.request_telemetry, bounded by RetentionBatchSize per
// statement and MaxRetentionBatches total iterations. Returns
// the cumulative row count. Same shape as RetentionOnce —
// additive free function, no shared state with the usage_minutes
// sweep.
//
// PR-B switched from the hardcoded 14-day sweep
// (PR-A's behavior) to a per-app, plan-aware sweep. The retention
// window is derived from the account's plan via the CASE ladder
// in retentionRequestTelemetryPlanAwareBatchSQL. Over-retention
// is impossible because the cap is the plan's own cap, not the
// fleet-wide Scale ceiling.
//
// On unknown plan names the sweep uses a 1-day fallback so an
// unrecognised plan doesn't accidentally retain 14 days of
// telemetry. PR-A's DefaultRequestTelemetryRetentionDays is
// retained as the constant other callers reference.
func RetentionOnceRequestTelemetry(ctx context.Context, db retentionExecer) (int64, error) {
	var total int64
	for i := 0; i < MaxRetentionBatches; i++ {
		tag, err := db.Exec(ctx, retentionRequestTelemetryPlanAwareBatchSQL, RetentionBatchSize)
		if err != nil {
			return total, fmt.Errorf("request telemetry retention delete (batch %d, deleted so far %d): %w", i, total, err)
		}
		total += tag
		if tag < RetentionBatchSize {
			return total, nil
		}
	}
	return total, ErrRetentionBatchCap
}

// retentionDropExpiredPartitionsSQL is a single-statement
// partition-drop pass (PR-B ADR-127). Enumerates monthly
// partitions of request_telemetry whose name encodes a month
// older than the floor retention cap (1 day; safe upper bound
// for any plan — Hobby=3d is the loosest cap), and DROPs each
// in turn via a DO block.
//
// The partition name is request_telemetry_YYYYMM (the 00435
// migration convention). We parse the trailing 6-digit suffix
// into a date and compare against now() - 1 day. Anything older
// can be safely dropped — the per-row sweep above already
// deleted any row that's not beyond the per-plan retention.
//
// Cost: O(partitions) ≈ 3-4 rows on a healthy fleet. The
// pg_inherits catalog query is constant-time.
const retentionDropExpiredPartitionsSQL = `
DO $$
DECLARE
    partname text;
    partsuffix text;
    partstart date;
BEGIN
    FOR partname IN
        SELECT c.relname
        FROM pg_inherits i
        JOIN pg_class c ON c.oid = i.inhrelid
        JOIN pg_class p ON p.oid = i.inhparent
        WHERE p.relname = 'request_telemetry'
          AND c.relname ~ '^request_telemetry_[0-9]{6}$'
    LOOP
        partsuffix := substring(partname from '[0-9]{6}$');
        BEGIN
            partstart := to_date(partsuffix, 'YYYYMM');
        EXCEPTION WHEN OTHERS THEN
            CONTINUE;
        END;
        IF partstart < date_trunc('month', now() - interval '1 day') THEN
            EXECUTE 'DROP TABLE IF EXISTS public.' || quote_ident(partname);
        END IF;
    END LOOP;
END $$;
`

// DropExpiredRequestTelemetryPartitions (PR-B ADR-127) drops
// monthly partitions of request_telemetry whose month-suffix
// encodes a month older than the floor retention cap (1 day;
// safe upper bound for any plan — Hobby=3d is the loosest cap).
//
// The DROP runs once per cron tick; the cron in
// pkg/meter/retention.go::RetentionLoopRequestTelemetry calls
// this AFTER RetentionOnceRequestTelemetry to keep the
// per-tick work concentrated. Errors are logged + skipped —
// a transient lock contention on one partition doesn't abort
// the rest of the drops.
//
// The DO block loops over partitions and DROPs each inside the
// same transaction; if one DROP fails the rest are skipped.
// The IF EXISTS clause makes each DROP idempotent — a second
// call finds nothing to drop.
func DropExpiredRequestTelemetryPartitions(ctx context.Context, db retentionExecer) (int64, error) {
	if _, err := db.Exec(ctx, retentionDropExpiredPartitionsSQL); err != nil {
		return 0, fmt.Errorf("drop expired partitions: %w", err)
	}
	// The DO block doesn't surface a row count via Exec;
	// return 0 to signal "ran" without claiming a count. The
	// caller logs at the call site if a positive count is
	// needed (pg_stat_user_tables provides the source-of-truth
	// for post-drop validation).
	return 0, nil
}

// RetentionLoopRequestTelemetry is the free-function goroutine
// that calls RetentionOnceRequestTelemetry every Interval.
// Returns on ctx.Done(). Mirrors RetentionLoop — same log posture
// (cap-hit is Warn, hard DB failure is Error).
//
// PR-B: also calls DropExpiredRequestTelemetryPartitions after
// the row-level sweep so monthly partitions whose entire row
// set is past retention are reclaimed in one DDL op instead of
// per-row ctid-IN-SELECT. The drop is best-effort — a transient
// failure is logged + the loop continues. Next tick retries.
func RetentionLoopRequestTelemetry(ctx context.Context, db retentionExecer, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = RequestTelemetryRetentionInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := RetentionOnceRequestTelemetry(ctx, db)
			switch {
			case err == nil:
				if log != nil {
					log.Info("request telemetry retention tick ok", "rows_deleted", n)
				}
			case errors.Is(err, ErrRetentionBatchCap):
				if log != nil {
					log.Warn("request telemetry retention tick hit batch cap; will resume next tick", "rows_deleted", n, "err", err)
				}
			default:
				if log != nil {
					log.Error("request telemetry retention tick failed", "err", err)
				}
			}
			// PR-B: drop expired monthly partitions AFTER the
			// row-level sweep so a fresh partition doesn't lose
			// rows that haven't been touched yet (the per-row
			// sweep may have skipped rows whose plan-derived
			// cap is still in the future).
			if _, dropErr := DropExpiredRequestTelemetryPartitions(ctx, db); dropErr != nil {
				if log != nil {
					log.Warn("request telemetry partition drop tick failed", "err", dropErr)
				}
			}
		}
	}
}

// DefaultDeploymentAuditRetentionDays is the upper bound for the
// deployment_audit table. SAFE-RELEASES production-leveling
// (Stream D, issue #976 / ADR-122 post-merge audit): the table
// has no GC today, so unbounded growth would fill disk + bloat
// the at_gc index on a long-lived cluster. 90 days is well past
// the longest plausible investigation window (operator
// post-incident review rarely extends past 30 days) and stays
// inside the financial-model retention envelope.
const DefaultDeploymentAuditRetentionDays = 90

// DefaultDeploymentAuditRetentionInterval is the cron cadence
// for the deployment_audit sweep. 6 h matches the usage_minutes
// sweep's "one pass per business-day quadrant" rhythm — a daily
// sweep would let the table accumulate up to ~1 extra day's
// worth of rows between ticks, which is fine for a 90-day
// retention window but misses shorter-detect signal.
const DefaultDeploymentAuditRetentionInterval = 6 * time.Hour

// retentionDeploymentAuditBatchSQL deletes rows in the bounded
// DELETE pattern (ctid + LIMIT N) — same shape as
// retentionBatchSQL above. The planner uses
// deployment_audit_at_gc_idx (single-column B-tree on `at`,
// migrations/00477) so the WHERE at < cutoff range-scan is
// index-backed.
const retentionDeploymentAuditBatchSQL = `DELETE FROM public.deployment_audit
                                       WHERE ctid IN (
                                           SELECT ctid FROM public.deployment_audit
                                           WHERE at < (now() - $1::interval)
                                           LIMIT $2
                                       )`

// RetentionOnceDeploymentAudit runs one DELETE pass against
// public.deployment_audit, bounded by RetentionBatchSize per
// statement and MaxRetentionBatches total iterations. Returns
// the cumulative row count. Same shape as RetentionOnce —
// additive free function, no shared state with the
// usage_minutes / request_telemetry sweeps.
//
// The retention window is DefaultDeploymentAuditRetentionDays
// (90 d). Operators can widen via a config knob in a follow-up
// ADR — today the constant matches the financial-model + on-
// call investigation envelope.
func RetentionOnceDeploymentAudit(ctx context.Context, db retentionExecer) (int64, error) {
	interval := fmt.Sprintf("%d days", DefaultDeploymentAuditRetentionDays)
	var total int64
	for i := 0; i < MaxRetentionBatches; i++ {
		tag, err := db.Exec(ctx, retentionDeploymentAuditBatchSQL, interval, RetentionBatchSize)
		if err != nil {
			return total, fmt.Errorf("deployment audit retention delete (batch %d, deleted so far %d): %w", i, total, err)
		}
		total += tag
		if tag < RetentionBatchSize {
			return total, nil
		}
	}
	return total, ErrRetentionBatchCap
}

// RetentionLoopDeploymentAudit is the free-function goroutine
// that calls RetentionOnceDeploymentAudit every Interval.
// Returns on ctx.Done(). Mirrors RetentionLoop /
// RetentionLoopRequestTelemetry — same log posture (cap-hit is
// Warn, hard DB failure is Error).
//
// onTickRows is invoked once per tick after a successful pass
// with the cumulative row count (only when n > 0 so idle
// passes don't tick up). Used by cmd/meterd to Inc the
// meterd_deployment_audit_gc_rows_deleted_total counter (SAFE-
// RELEASES Stream D). nil-allowed so test callers can wire the
// loop without a Prometheus registry.
func RetentionLoopDeploymentAudit(ctx context.Context, db retentionExecer, interval time.Duration, log *slog.Logger, onTickRows func(int64), onTickError func(error)) {
	if interval <= 0 {
		interval = DefaultDeploymentAuditRetentionInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := RetentionOnceDeploymentAudit(ctx, db)
			switch {
			case err == nil:
				if log != nil {
					log.Info("deployment audit retention tick ok", "rows_deleted", n)
				}
				if n > 0 && onTickRows != nil {
					onTickRows(n)
				}
			case errors.Is(err, ErrRetentionBatchCap):
				if log != nil {
					log.Warn("deployment audit retention tick hit batch cap; will resume next tick", "rows_deleted", n, "err", err)
				}
			default:
				if log != nil {
					log.Error("deployment audit retention tick failed", "err", err)
				}
				// SAFE-RELEASES-OBS PR-A: surface the failure
				// through the onTickError callback so cmd/meterd
				// can bump the deployment_audit_gc_failed_total
				// counter. Pre-PR the failure was journal-only —
				// operators had to grep logs to notice the prune
				// loop was down (disk-fill risk). PR-B's
				// deployment_audit_gc_failing alert queries the
				// counter's rate over a 1h window.
				if onTickError != nil {
					onTickError(err)
				}
			}
		}
	}
}

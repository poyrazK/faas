// Package meter — usage_daily rollup (ADR-048 §5).
//
// usage_minutes is the canonical per-(account, meter identity, instance, minute)
// ledger; rows stay forever and feed UsageByMonth, the Stripe push,
// and the reconciliation checks. But the dashboard's
// "yesterday's traffic per app" query scans minutes — every minute,
// for every app — to compute one day's worth of sums. The rollup
// table (migrations/00067_extend_metering_telemetry.sql::usage_daily)
// carries the day-grain so the hot path is one Indexed tuple read.
//
// The rollup is a point-in-time overwrite — re-running for the
// same (account, app, day) tuple REPLACES the existing row with
// the freshly summed values. This is the right contract for a
// repeating cron: the SUM over the full day window is a pure
// function of the underlying usage_minutes rows in that window,
// so re-aggregating must converge to the same answer. Additive
// merge would multiply the day total by the number of cron ticks
// (~288× per day at 5-min cadence) and silently inflate the
// dashboard. The session-isolation invariant ("a missed tick or
// a meterd restart covers the gap") is preserved because the cron
// always re-aggregates yesterday and the current partial day on every
// tick — the next tick after a gap covers the same data.
//
// The rollup never pushes to billing providers — it is informational
// only, mirroring the per-row additivity of the underlying minute
// grain. Provider push stays on the 24 h StripeInterval loop
// (pkg/meter/loop.go).
//
// NOTE — the new `usage_daily.builder_seconds` column is NOT
// populated by this rollup. builder_seconds are written to a
// separate grain (`builder_usage`, migration 00068) and rolled up
// in a follow-up PR; the column stays at 0 today.
//
// NOTE — `usage_daily.tail_seconds` (issue #667, ADR-078) IS
// populated by this rollup. It's the per-day SUM of
// `usage_minutes.tail_seconds` — also INFORMATIONAL ONLY. The same
// "no Provider push" rule that pins tail_seconds below the billing
// boundary (see pkg/meter/pusher_shadow_test.go::TestPushHour_ExcludesTailSeconds)
// applies at the daily grain too.
package meter

import (
	"context"
	"log/slog"
	"time"
)

// execer is the minimal pgxpool contract the rollup needs:
// Exec(sql, args...) for the INSERT ... ON CONFLICT statement.
// Production wires pgxpool.Pool.Exec via a thin adapter; tests
// inject a stub. Keeping the seam narrow avoids a pkg/meter →
// pgxpool import cycle and keeps the rollup unit-testable.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (rows int64, err error)
}

// rollupSQL is the half-open [start, end) INSERT ... ON CONFLICT
// statement that rolls one window of usage_minutes rows into
// usage_daily. Mirrors the column set declared in
// migrations/00067_extend_metering_telemetry.sql plus the job identity
// widening in migrations/20260917131722793_usage_daily_job_meter_kind.sql.
// The on-conflict
// clause uses OVERWRITE (col = EXCLUDED.col) — re-running for the
// same window converges to the same day total, which is the right
// contract for a repeating 5-minute cron. Additive merge would
// multiply the day total by the number of cron ticks. See the
// package doc comment for the full rationale.
const rollupSQL = `
INSERT INTO public.usage_daily (
    account_id, app_id, meter_kind, job_id, day,
    mb_seconds, requests, cpu_usec, tx_bytes,
    net_tx_bytes, net_rx_bytes, cold_boot_count, builder_seconds,
    tail_seconds, rolled_up_at
)
SELECT
    account_id,
    COALESCE(app_id, job_id) AS app_id,
    meter_kind,
    CASE WHEN meter_kind = 'job' THEN job_id ELSE NULL END AS job_id,
    date_trunc('day', minute AT TIME ZONE 'UTC')::date AS day,
    SUM(mb_seconds), SUM(requests), SUM(cpu_usec), SUM(tx_bytes),
    SUM(net_tx_bytes), SUM(net_rx_bytes), SUM(cold_boot_count),
    SUM(CASE WHEN builder_kind <> 'none' THEN builder_seconds ELSE 0 END),
    SUM(tail_seconds),
    now()
FROM public.usage_minutes
WHERE minute >= $1 AND minute < $2
GROUP BY 1, 2, 3, 4
ON CONFLICT (account_id, app_id, day) DO UPDATE SET
    meter_kind      = EXCLUDED.meter_kind,
    job_id          = EXCLUDED.job_id,
    mb_seconds      = EXCLUDED.mb_seconds,
    requests        = EXCLUDED.requests,
    cpu_usec        = EXCLUDED.cpu_usec,
    tx_bytes        = EXCLUDED.tx_bytes,
    net_tx_bytes    = EXCLUDED.net_tx_bytes,
    net_rx_bytes    = EXCLUDED.net_rx_bytes,
    cold_boot_count = EXCLUDED.cold_boot_count,
    builder_seconds = EXCLUDED.builder_seconds,
    tail_seconds    = EXCLUDED.tail_seconds,
    rolled_up_at    = EXCLUDED.rolled_up_at
`

// RollupOnce rolls one window of usage_minutes rows into
// usage_daily. windowStart and windowEnd form a half-open UTC
// window. Caller picks the window; the RollupLoop helper below
// picks `since the last successful rollup` automatically.
//
// Returns the number of usage_daily rows touched by the SQL (sum
// of inserted + updated). 0 is a valid result for an empty window.
func RollupOnce(ctx context.Context, db execer, windowStart, windowEnd time.Time) (int64, error) {
	tag, err := db.Exec(ctx, rollupSQL, windowStart, windowEnd)
	if err != nil {
		return 0, err
	}
	return tag, nil
}

// RollupLoop ticks RollupOnce on interval. Each tick rolls both the previous
// complete UTC day and the current partial UTC day. Recomputing the complete
// day repairs late-arriving rows; recomputing today makes the customer-facing
// default useful within one interval. ON CONFLICT overwrites totals, so ticks
// and process restarts cannot double count.
//
// Errors are logged Warn and retried on the next tick — a
// persistent failure shows up as a flood of WARN logs that an
// operator can alert on.
//
// Use as a free-function goroutine (mirrors pkg/builderd/reaper.go):
//
//	go meter.RollupLoop(ctx, pool, 5*time.Minute, log)
//
// The loop is single-goroutine today; a future meterd replica would
// parallelise via the (account_id, app_id, day) PK — the SQL's
// additive merge is safe under concurrent calls on disjoint day
// ranges, and the on-conflict update under concurrent calls on
// the same day range is monotonic-additive.
func RollupLoop(ctx context.Context, db execer, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	if log == nil {
		log = slog.Default()
	}
	runDailyRollups(ctx, db, time.Now().UTC(), log, "initial")
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runDailyRollups(ctx, db, time.Now().UTC(), log, "tick")
		}
	}
}

type dailyRollupWindow struct {
	start time.Time
	end   time.Time
}

func dailyRollupWindows(now time.Time) []dailyRollupWindow {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return []dailyRollupWindow{
		{start: today.Add(-24 * time.Hour), end: today},
		{start: today, end: now},
	}
}

func runDailyRollups(ctx context.Context, db execer, now time.Time, log *slog.Logger, phase string) {
	for _, window := range dailyRollupWindows(now) {
		if _, err := RollupOnce(ctx, db, window.start, window.end); err != nil {
			log.Warn("meter: usage_daily rollup",
				"phase", phase,
				"window_start", window.start.Format(time.RFC3339),
				"window_end", window.end.Format(time.RFC3339),
				"err", err)
			continue
		}
		log.Info("meter: usage_daily rollup ok",
			"phase", phase,
			"window_start", window.start.Format(time.RFC3339),
			"window_end", window.end.Format(time.RFC3339))
	}
}

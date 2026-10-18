package slo

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// MaxHoursPerSLOPerTick bounds one rollup pass so a fresh deployment or a
// long outage backfills over several ticks instead of one burst of queries.
// Oldest hours go first: they are the ones about to leave Prometheus.
const MaxHoursPerSLOPerTick = 72

// RetainHours keeps a little more than the longest window so a window that
// straddles the purge boundary is never short an hour.
const RetainHours = 31 * 24

// RollupStats summarises one pass for logs and tests.
type RollupStats struct {
	SLOs, Recorded, Failed int
	Purged                 int64
}

// Rollup records each SLO's completed hours (ADR-747). It is idempotent:
// an hour already recorded is skipped, and re-recording replaces it.
type Rollup struct {
	Store state.SLOBudgetStore
	Prom  PromQL
	Log   *slog.Logger
}

// RunOnce fills missing completed hours for every SLO, back to the later of
// its creation hour and the backfill horizon, then purges expired rows.
func (r *Rollup) RunOnce(ctx context.Context, now time.Time) (RollupStats, error) {
	var stats RollupStats
	defs, err := r.Store.ListAllSLOs(ctx)
	if err != nil {
		return stats, err
	}
	current := now.UTC().Truncate(time.Hour) // the hour in progress is never recorded
	horizon := current.Add(-time.Duration(api.SLORollupBackfillHours) * time.Hour)
	for _, def := range defs {
		stats.SLOs++
		start := def.CreatedAt.UTC().Truncate(time.Hour)
		if start.Before(horizon) {
			start = horizon
		}
		recorded, failed, err := r.fill(ctx, def, start, current)
		stats.Recorded += recorded
		stats.Failed += failed
		if err != nil {
			return stats, err
		}
	}
	stats.Purged, err = r.Store.PurgeSLOHoursBefore(ctx, current.Add(-RetainHours*time.Hour))
	return stats, err
}

func (r *Rollup) fill(ctx context.Context, def state.SLO, start, end time.Time) (recorded, failed int, err error) {
	have, err := r.Store.SLOHourStarts(ctx, def.ID, start)
	if err != nil {
		return 0, 0, err
	}
	seen := make(map[time.Time]bool, len(have))
	for _, h := range have {
		seen[h.UTC()] = true
	}
	for hour := start; hour.Before(end) && recorded+failed < MaxHoursPerSLOPerTick; hour = hour.Add(time.Hour) {
		if seen[hour] {
			continue
		}
		good, total, qerr := Counts(ctx, r.Prom, def, "1h", hour.Add(time.Hour))
		if qerr != nil {
			// Prometheus is down or slow: leave the hour missing so the
			// next tick retries it, and stop querying for this SLO.
			r.log().Warn("slo: rollup query failed", "slo", def.ID, "hour", hour, "err", qerr)
			return recorded, failed + 1, nil
		}
		if err := r.Store.UpsertSLOHour(ctx, def.ID, hour, good, total); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return recorded, failed, nil // SLO deleted mid-pass
			}
			return recorded, failed, err
		}
		recorded++
	}
	return recorded, failed, nil
}

func (r *Rollup) log() *slog.Logger {
	if r.Log == nil {
		return slog.Default()
	}
	return r.Log
}

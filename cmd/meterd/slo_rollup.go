package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/slo"
	"github.com/onebox-faas/faas/pkg/state"
)

// sloRollupInterval is how often meterd records completed SLO hours
// (ADR-747). Five minutes keeps a just-completed hour visible within minutes
// while costing a few instant queries per SLO per tick.
const sloRollupInterval = 5 * time.Minute

// runSLORollup records SLO budget hours until ctx ends. It is a no-op when
// Prometheus is unconfigured or the store predates ADR-747; budgets then
// read as "no data" instead of failing.
func runSLORollup(ctx context.Context, store state.Store, prom appmetrics.PromQL, log *slog.Logger) {
	budgets, ok := store.(state.SLOBudgetStore)
	if !ok || prom == nil {
		log.Info("meterd: SLO budget rollup disabled", "store_supported", ok, "prometheus_configured", prom != nil)
		return
	}
	r := &slo.Rollup{Store: budgets, Prom: prom, Log: log}
	tick := func() {
		stats, err := r.RunOnce(ctx, time.Now())
		if err != nil {
			log.Warn("meterd: SLO budget rollup", "err", err)
			return
		}
		if stats.Recorded > 0 || stats.Failed > 0 || stats.Purged > 0 {
			log.Info("meterd: SLO budget rollup", "slos", stats.SLOs, "recorded_hours", stats.Recorded, "failed_hours", stats.Failed, "purged_hours", stats.Purged)
		}
	}
	tick()
	ticker := time.NewTicker(sloRollupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

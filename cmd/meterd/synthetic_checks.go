package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/synthetics"
)

// syntheticCheckTick is how often meterd looks for due synthetic checks
// (ADR-748). Checks run every 5 minutes at the most, so a 30-second tick
// keeps each run within 30 s of its schedule.
const syntheticCheckTick = 30 * time.Second

// syntheticCheckConcurrency bounds simultaneous probes from the control
// plane so a burst of due checks cannot flood the public edge.
const syntheticCheckConcurrency = 8

// runSyntheticChecks probes due checks until ctx ends. A store that
// predates ADR-748 disables the runner rather than failing meterd.
func runSyntheticChecks(ctx context.Context, store state.Store, appsDomain string, log *slog.Logger) {
	runs, ok := store.(state.SyntheticRunStore)
	if !ok {
		log.Info("meterd: synthetic checks disabled; store does not support them")
		return
	}
	r := &synthetics.Runner{Store: runs, AppsDomain: appsDomain, Concurrency: syntheticCheckConcurrency, Log: log}
	ticker := time.NewTicker(syntheticCheckTick)
	defer ticker.Stop()
	for {
		stats, err := r.RunOnce(ctx)
		switch {
		case err != nil:
			log.Warn("meterd: synthetic checks", "err", err)
		case stats.Due > 0 || stats.Purged > 0:
			log.Info("meterd: synthetic checks", "due", stats.Due, "ok", stats.OK, "failed", stats.Failed,
				"record_errors", stats.RecordErrors, "purged_runs", stats.Purged)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

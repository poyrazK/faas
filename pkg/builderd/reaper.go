package builderd

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// ReaperLoop sweeps build rows stuck in 'running' past a configurable
// threshold (issue #195 B1.4). Runs as a free-function goroutine next
// to workerLoop; cadence is configurable so a CI smoke test can use
// milliseconds while production uses minutes.
//
// Why this exists:
//   - The builder VM path is a single-process claim → run → markSucceeded
//     (or markFailed). A builder VM crash, kernel panic, OOM-kill, or
//     the host losing power mid-build all bypass markSucceeded/markFailed
//     and leave the build row in status='running' forever.
//   - Without a reaper, the dashboard shows "still building" for a
//     dead build indefinitely, and the slot is held in the queue
//     until manual intervention.
//   - The reaper flips stuck rows to status='failed' with
//     failure_class='timeout' and fails the owning in-flight deployment in
//     the same store operation. This keeps the customer-visible deployment
//     state consistent with the build after a worker disappears (issue #195
//     B1.5 / ADR-031).
//
// The threshold is wider than the 10-minute VM build timeout (default
// 15 minutes) so a slow-but-finishing build isn't swept out from under
// itself. The CAS guard on UpdateBuildStatus (also issue #195 B1.4)
// closes the race window: a build that completes between the sweep
// selecting it and the late markSucceeded arriving cannot resurrect a
// 'failed(timeout)' row.
//
// Idempotent: a second sweep with the same threshold matches 0 rows.
func ReaperLoop(ctx context.Context, store state.Store, interval, threshold time.Duration, log *slog.Logger) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cutoff := time.Now().Add(-threshold)
		n, err := store.SweepStuckRunningBuilds(ctx, cutoff)
		if err != nil {
			// Sweep failures are operator-visible but not fatal —
			// the next tick retries. A persistent failure (e.g.,
			// DB outage) would show up as a flood of WARN logs.
			if errors.Is(err, state.ErrNotFound) {
				// Should not happen — SweepStuckRunningBuilds
				// returns (0, nil) on no rows, not ErrNotFound.
				// Treat defensively as a no-op.
				continue
			}
			log.Warn("builderd: stuck-running sweep", "err", err)
			continue
		}
		if n > 0 {
			log.Info("builderd: swept stuck-running builds",
				"count", n, "threshold", threshold, "cutoff", cutoff.Format(time.RFC3339))
		}
	}
}

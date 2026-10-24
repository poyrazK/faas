// Package deploywatchdog fails in-flight deployments that can no longer
// finish on their own, so every deployment reaches a terminal state
// (ADR-20261024140000). apid owns deployments; builderd's reaper keeps
// running builds, and schedd keeps instances.
package deploywatchdog

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Watchdog sweeps stuck deployments.
type Watchdog struct {
	Store state.DeploymentWatchdogStore
	Log   *slog.Logger
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// Run sweeps once at start, so a restarted apid catches up, then every
// api.DeploymentWatchdogIntervalSeconds until ctx ends.
func (w *Watchdog) Run(ctx context.Context) error {
	w.sweepAndLog(ctx)
	tick := time.NewTicker(time.Duration(api.DeploymentWatchdogIntervalSeconds) * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			w.sweepAndLog(ctx)
		}
	}
}

func (w *Watchdog) sweepAndLog(ctx context.Context) {
	if _, err := w.Sweep(ctx); err != nil && ctx.Err() == nil {
		w.log().Warn("deployment watchdog: sweep failed", "err", err)
	}
}

// Sweep fails up to api.DeploymentWatchdogBatch stuck deployments and
// returns how many it failed. Each failure re-checks its deployment under
// the row lock, so one that moved on since the listing is left alone, and
// several apid replicas can sweep at once.
func (w *Watchdog) Sweep(ctx context.Context) (int, error) {
	now := time.Now()
	if w.Now != nil {
		now = w.Now()
	}
	queuedBefore := now.Add(-time.Duration(api.DeploymentQueuedBuildTimeoutSeconds) * time.Second)
	stalledBefore := now.Add(-time.Duration(api.DeploymentStallTimeoutSeconds) * time.Second)
	stuck, err := w.Store.ListStuckDeployments(ctx, queuedBefore, stalledBefore, api.DeploymentWatchdogBatch)
	if err != nil {
		return 0, err
	}
	failed := 0
	for _, d := range stuck {
		code, message := Failure(d)
		if _, ok, err := w.Store.FailStuckDeployment(ctx, d, queuedBefore, stalledBefore, code, message); err != nil {
			w.log().Warn("deployment watchdog: fail stuck deployment", "deployment", d.ID, "app", d.AppID, "reason", d.Reason, "err", err)
			continue
		} else if !ok {
			continue
		}
		failed++
		w.log().Info("deployment watchdog: failed stuck deployment", "deployment", d.ID, "app", d.AppID,
			"status", d.Status, "reason", d.Reason, "since", d.Since.UTC().Format(time.RFC3339), "code", code)
	}
	return failed, nil
}

// Failure is the stable code and customer-readable message the watchdog
// records on a stuck deployment.
func Failure(d state.StuckDeployment) (code, message string) {
	if d.Reason == state.StuckDeploymentQueued {
		return api.CodeDeploymentQueueTimeout, fmt.Sprintf(
			"The build waited more than %s in the builder queue without starting, so Gregale stopped this deployment. Deploy again.",
			humanHours(api.DeploymentQueuedBuildTimeoutSeconds))
	}
	return api.CodeDeploymentStalled, fmt.Sprintf(
		"The deployment made no progress for more than %s while %s, so Gregale stopped it. Deploy again.",
		humanHours(api.DeploymentStallTimeoutSeconds), d.Status)
}

func humanHours(seconds int) string {
	if seconds%3600 == 0 {
		if seconds == 3600 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", seconds/3600)
	}
	return (time.Duration(seconds) * time.Second).String()
}

func (w *Watchdog) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

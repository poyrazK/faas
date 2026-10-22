package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/releasegate"
	"github.com/onebox-faas/faas/pkg/releasegate/gather"
)

var _ canary.ReleaseGateChecker = (*canaryStoreAdapter)(nil)

// ReleaseGateVerdict judges the app's release gates (ADR-20261019071734) for
// the candidate and its stable predecessor over the same window: since the
// rollout started, so evidence adds up across stages.
func (a *canaryStoreAdapter) ReleaseGateVerdict(ctx context.Context, row canary.CanaryRow, stableDeploymentID string, now time.Time) (releasegate.Verdict, error) {
	since := row.RolloutStarted
	if since.IsZero() || !since.Before(now) {
		since = row.CanaryStepStarted
	}
	if since.IsZero() || !since.Before(now) {
		since = now.Add(-5 * time.Minute)
	}
	window := gather.Window{Since: since, Until: now}
	// ADR-20261022082305: metric_change gates compare with the same length
	// of time before the rollout; synthetic gates read both revisions'
	// probes.
	prior := gather.Window{Since: since.Add(-now.Sub(since)), Until: since}
	return gather.Verdict(ctx, a.store, gather.Request{AppID: row.AppID, ReleaseID: row.ID, BaselineID: stableDeploymentID,
		ReleaseWindow: window, BaselineWindow: window, PriorWindow: prior, Canary: true})
}

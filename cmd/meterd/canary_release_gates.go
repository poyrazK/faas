package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/appmetrics"
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
	req := gather.Request{AppID: row.AppID, ReleaseID: row.ID, BaselineID: stableDeploymentID,
		ReleaseWindow: window, BaselineWindow: window}
	if history := appmetrics.NewCustomMetricHistory(a.promQL); history != nil {
		req.CustomMetrics = history
	}
	return gather.Verdict(ctx, a.store, req)
}

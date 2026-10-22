package rolloutevidence

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// An app is failing when at least failingErrorPct of at least
// failingMinRequests requests in the window ended in a server error.
const (
	failingErrorPct    = 5
	failingMinRequests = 20
	// failureScan bounds the newest rows read to find recent failures.
	failureScan = 200
)

// Status summarizes the app over [now-since, now]: serving deployments,
// server errors and p95, the rollout in flight or rolled back in the window,
// open regressions, and the newest failing requests, with a verdict.
func (r Reader) Status(ctx context.Context, app state.App, since, retention time.Duration) (api.DebugStatusResponse, error) {
	now := r.now()
	start := now.Add(-since)
	out := api.DebugStatusResponse{
		WindowStart: start.Format(time.RFC3339Nano), WindowEnd: now.Format(time.RFC3339Nano),
		Deployments: []api.DebugStatusDeployment{}, Regressions: []api.DebugRegressionItem{},
		RecentFailures: []api.DebugTelemetryRequestItem{},
		TracingEnabled: app.Manifest.Tracing != nil && app.Manifest.Tracing.Enabled,
		GeneratedAt:    now.Format(time.RFC3339Nano),
	}
	live, err := r.Store.LiveDeployments(ctx, app.ID)
	if err != nil {
		return out, fmt.Errorf("rolloutevidence: live deployments: %w", err)
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].TrafficPercent > live[j].TrafficPercent })
	for _, dep := range live {
		out.Deployments = append(out.Deployments, api.DebugStatusDeployment{
			ID: dep.ID, TrafficPercent: dep.TrafficPercent, RolloutState: dep.RolloutState,
			CommitSHA: dep.CommitSHA, CreatedAt: dep.CreatedAt.UTC().Format(time.RFC3339),
		})
		errs, err := r.Store.RequestTelemetryErrorsByRoute(ctx, sqlc.RequestTelemetryErrorsByRouteParams{
			AppID: pgUUID(app.ID), DeploymentID: pgUUID(dep.ID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(now),
		})
		if err != nil {
			return out, fmt.Errorf("rolloutevidence: deployment errors: %w", err)
		}
		for _, row := range errs {
			out.Health.Requests += row.Requests
			out.Health.ServerErrors += row.ErrorRequests
		}
	}
	summary, err := r.Store.RequestTelemetryAnalyticsSummary(ctx, sqlc.RequestTelemetryAnalyticsSummaryParams{
		AppID: pgUUID(app.ID), AccountID: pgUUID(app.AccountID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(now),
	})
	if err != nil {
		return out, fmt.Errorf("rolloutevidence: request summary: %w", err)
	}
	out.Health.P95MS = int(summary.P95Ms)

	rollout, err := r.Explain(ctx, app, "", retention)
	if err != nil {
		return out, err
	}
	if rolloutInWindow(rollout, start) {
		out.Rollout = &rollout
	}
	if r.Regressions != nil {
		items, err := r.Regressions(ctx, app.ID, since)
		if err != nil {
			return out, err
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].LastDetectedAt > items[j].LastDetectedAt })
		out.RegressionsTotal = len(items)
		out.Regressions = items[:min(len(items), api.DebugStatusMaxRegression)]
	}
	if out.Health.ServerErrors > 0 {
		if out.RecentFailures, err = r.recentFailures(ctx, app.ID, start, now); err != nil {
			return out, err
		}
	}
	out.Verdict, out.Headline = verdict(out, since)
	return out, nil
}

// rolloutInWindow reports whether the status should carry the rollout: one
// still in flight, or one rolled back inside the window.
func rolloutInWindow(rollout api.DebugRolloutExplanation, start time.Time) bool {
	switch rollout.State {
	case api.DebugRolloutHeld, api.DebugRolloutRollingOut:
		return true
	case api.DebugRolloutAborted:
		end, err := time.Parse(time.RFC3339Nano, rollout.WindowEnd)
		return err == nil && !end.Before(start)
	}
	return false
}

func (r Reader) recentFailures(ctx context.Context, appID string, start, end time.Time) ([]api.DebugTelemetryRequestItem, error) {
	rows, err := r.Store.ListRequestTelemetryByApp(ctx, sqlc.ListRequestTelemetryByAppParams{
		AppID: pgUUID(appID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(end), ColdBootFilter: -1, Limit: failureScan,
	})
	if err != nil {
		return nil, fmt.Errorf("rolloutevidence: recent failures: %w", err)
	}
	out := []api.DebugTelemetryRequestItem{}
	for _, row := range rows {
		if row.Status >= 500 && len(out) < api.DebugStatusMaxFailures {
			out = append(out, r.item(row))
		}
	}
	return out, nil
}

// verdict picks the worst condition, in order: failing, rolled back,
// degraded, releasing, healthy, idle.
func verdict(out api.DebugStatusResponse, since time.Duration) (string, string) {
	window := humanWindow(since)
	health := out.Health
	switch {
	case health.Requests >= failingMinRequests && health.ServerErrors*100 >= failingErrorPct*health.Requests:
		return api.DebugStatusFailing, fmt.Sprintf("%d of %d requests failed with a server error in the last %s.",
			health.ServerErrors, health.Requests, window)
	case out.Rollout != nil && out.Rollout.State == api.DebugRolloutAborted:
		return api.DebugStatusRollback, out.Rollout.Headline
	case out.RegressionsTotal > 0:
		return api.DebugStatusDegraded, fmt.Sprintf("%s open in the last %s.", plural(out.RegressionsTotal, "regression"), window)
	case health.ServerErrors > 0:
		return api.DebugStatusDegraded, fmt.Sprintf("%s in the last %s.", plural(int(health.ServerErrors), "server error"), window)
	case out.Rollout != nil:
		return api.DebugStatusReleasing, out.Rollout.Headline
	case health.Requests == 0:
		return api.DebugStatusIdle, fmt.Sprintf("No requests in the last %s.", window)
	}
	return api.DebugStatusHealthy, fmt.Sprintf("%s in the last %s, no server errors, p95 %d ms.",
		plural(int(health.Requests), "request"), window, health.P95MS)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// humanWindow renders a lookback such as 1h, 30m, or 2d for headlines.
func humanWindow(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	return s
}

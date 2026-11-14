package rolloutevidence

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

// An app is failing when at least failingErrorPct of at least
// failingMinRequests requests in the window ended in a server error.
const (
	failingErrorPct    = 5
	failingMinRequests = 20
	// failureScan bounds the newest rows read, per serving deployment, to
	// find recent failures. A busy app's newest 200 rows across every
	// deployment were often all successes (or the rolled-back candidate's),
	// so a degraded verdict came back with no failing request to show.
	failureScan = 1000
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
		RecentFailures: []api.DebugTelemetryRequestItem{}, OOMKills: []api.DebugStatusOOMKill{},
		TracingEnabled: app.Manifest.Tracing != nil && app.Manifest.Tracing.Enabled,
		GeneratedAt:    now.Format(time.RFC3339Nano),
	}
	live, err := r.Store.LiveDeployments(ctx, app.ID)
	if err != nil {
		return out, fmt.Errorf("rolloutevidence: live deployments: %w", err)
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].TrafficPercent > live[j].TrafficPercent })
	var latency []sqlc.RequestTelemetryLatencyWeightsByRouteRow
	var serving []string
	for _, dep := range live {
		// A rolled-back candidate stays live at 0% traffic. Its failures
		// belong to Rollout, not to the health of what serves now.
		if dep.RolloutState == api.DebugRolloutAborted {
			continue
		}
		serving = append(serving, dep.ID)
		out.Deployments = append(out.Deployments, api.DebugStatusDeployment{
			ID: dep.ID, TrafficPercent: dep.TrafficPercent, RolloutState: dep.RolloutState,
			CommitSHA: dep.CommitSHA, CreatedAt: dep.CreatedAt.UTC().Format(time.RFC3339),
		})
		// The regression view: the platform's own refusals and requests
		// pinned to a deployment are not the serving release's errors.
		errs, err := r.Store.RequestTelemetryErrorsByRoute(ctx, sqlc.RequestTelemetryErrorsByRouteParams{
			AppID: pgUUID(app.ID), DeploymentID: pgUUID(dep.ID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(now),
			Column5: true,
		})
		if err != nil {
			return out, fmt.Errorf("rolloutevidence: deployment errors: %w", err)
		}
		for _, row := range errs {
			out.Health.Requests += row.Requests
			out.Health.ServerErrors += row.ErrorRequests
		}
		weights, err := r.Store.RequestTelemetryLatencyWeightsByRoute(ctx, sqlc.RequestTelemetryLatencyWeightsByRouteParams{
			AppID: pgUUID(app.ID), DeploymentID: pgUUID(dep.ID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(now),
		})
		if err != nil {
			return out, fmt.Errorf("rolloutevidence: deployment latency: %w", err)
		}
		latency = append(latency, weights...)
	}
	// The p95 is the warm p95 of what serves now: the app-wide analytics
	// summary mixed in cold boots and a rolled-back candidate's requests.
	out.Health.P95MS = weightedP95(latency)

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
		if out.RecentFailures, err = r.recentFailures(ctx, app.ID, serving, start, now); err != nil {
			return out, err
		}
	}
	if r.OOMKills != nil {
		kills, total, err := r.OOMKills(ctx, app.ID, start, now)
		if err != nil {
			return out, err
		}
		out.OOMKills, out.OOMKillsTotal = kills, total
		if len(kills) > 0 {
			out.OOMFix = oomFix(kills[0])
		}
	}
	out.Verdict, out.Headline = verdict(out, since)
	return out, nil
}

// oomFix is the app_runtime_oom remedy for the kill's peak and plan.
func oomFix(kill api.DebugStatusOOMKill) string {
	problem := &api.Problem{}
	_ = whycopy.Decorate(problem, api.CodeAppRuntimeOOM, struct{ PeakMB, PlanMB int }{PeakMB: kill.PeakMB, PlanMB: kill.PlanMB})
	return problem.Fix
}

func oomHeadline(out api.DebugStatusResponse, window string) string {
	headline := fmt.Sprintf("%s in the last %s", plural(out.OOMKillsTotal, "out-of-memory kill"), window)
	if newest := out.OOMKills[0]; newest.PeakMB > 0 && newest.PlanMB > 0 {
		headline += fmt.Sprintf("; the newest peaked at %d MB on a %d MB plan", newest.PeakMB, newest.PlanMB)
	}
	return headline + "."
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

// recentFailures returns the newest server errors of the serving
// deployments, newest first.
func (r Reader) recentFailures(ctx context.Context, appID string, serving []string, start, end time.Time) ([]api.DebugTelemetryRequestItem, error) {
	var failed []sqlc.ListRequestTelemetryByAppRow
	for _, deploymentID := range serving {
		rows, err := r.Store.ListRequestTelemetryByApp(ctx, sqlc.ListRequestTelemetryByAppParams{
			AppID: pgUUID(appID), ReceivedAt: pgTime(start), ReceivedAt_2: pgTime(end), DeploymentID: deploymentID,
			ColdBootFilter: -1, Limit: failureScan,
		})
		if err != nil {
			return nil, fmt.Errorf("rolloutevidence: recent failures: %w", err)
		}
		for _, row := range rows {
			if row.Status >= 500 {
				failed = append(failed, row)
			}
		}
	}
	sort.SliceStable(failed, func(i, j int) bool { return failed[i].ReceivedAt.Time.After(failed[j].ReceivedAt.Time) })
	out := []api.DebugTelemetryRequestItem{}
	for _, row := range failed[:min(len(failed), api.DebugStatusMaxFailures)] {
		out = append(out, r.item(row))
	}
	return out, nil
}

// weightedP95 is the 95th percentile latency of weighted latency buckets.
func weightedP95(rows []sqlc.RequestTelemetryLatencyWeightsByRouteRow) int {
	var total int64
	for _, row := range rows {
		total += row.Requests
	}
	if total == 0 {
		return 0
	}
	sorted := slices.Clone(rows)
	slices.SortFunc(sorted, func(a, b sqlc.RequestTelemetryLatencyWeightsByRouteRow) int {
		return cmp.Compare(a.LatencyMs, b.LatencyMs)
	})
	rank := (total*95 + 99) / 100 // nearest rank
	var seen int64
	for _, row := range sorted {
		seen += row.Requests
		if seen >= rank {
			return int(row.LatencyMs)
		}
	}
	return int(sorted[len(sorted)-1].LatencyMs)
}

// verdict picks the worst condition, in order: failing, rolled back,
// degraded, releasing, healthy, idle.
func verdict(out api.DebugStatusResponse, since time.Duration) (string, string) {
	window := FormatWindow(since)
	health := out.Health
	switch {
	case health.Requests >= failingMinRequests && health.ServerErrors*100 >= failingErrorPct*health.Requests:
		return api.DebugStatusFailing, fmt.Sprintf("%d of %d requests failed with a server error in the last %s.",
			health.ServerErrors, health.Requests, window)
	case out.Rollout != nil && out.Rollout.State == api.DebugRolloutAborted:
		return api.DebugStatusRollback, out.Rollout.Headline
	case out.OOMKillsTotal > 0 && len(out.OOMKills) > 0:
		return api.DebugStatusDegraded, oomHeadline(out, window)
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

// FormatWindow renders a lookback such as 1h, 30m, or 2d for headlines and
// the status response.
func FormatWindow(d time.Duration) string {
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

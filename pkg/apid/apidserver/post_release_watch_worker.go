// adr: 1445 — apid watches completed releases and rolls back regressions.
package apidserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/releasegate"
	"github.com/onebox-faas/faas/pkg/releasegate/gather"
	"github.com/onebox-faas/faas/pkg/releasemetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// postReleaseWatchSweep judges one bounded page of watched releases. Later
// ticks continue the pass; one failing release does not stop the others.
func (s *server) postReleaseWatchSweep(ctx context.Context, now time.Time) error {
	s.postReleaseScanMu.Lock()
	defer s.postReleaseScanMu.Unlock()
	store, ok := s.store.(state.PostReleaseWatchStore)
	telemetry := s.postReleaseTelemetry
	if telemetry == nil {
		telemetry, _ = s.store.(rollbackOn5xxTelemetry)
	}
	if !ok || telemetry == nil {
		return nil
	}
	rows, err := store.ListPostReleaseWatchCandidatesPage(ctx, api.PostReleaseWatchWindow+api.RollbackOn5xxTelemetryGrace, api.PostReleaseWatchBatchSize, s.postReleaseCursor)
	if err != nil {
		if ctx.Err() == nil {
			s.releaseMetrics.Error(releasemetrics.WorkerPostReleaseWatch)
		}
		return err
	}
	s.releaseMetrics.Pass(releasemetrics.WorkerPostReleaseWatch, now)
	for _, c := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Healthy releases stay watched; resume after each attempted row.
		// Cancellation must not skip the page's unattempted candidates.
		s.postReleaseCursor = state.ReleaseScanCursor{At: c.RolloutCompletedAt, ID: c.DeploymentID}
		if err := s.evaluatePostRelease(ctx, store, telemetry, c, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.Warn("post-release watch: evaluate release", "app", c.AppID, "deployment", c.DeploymentID, "err", err)
			s.releaseMetrics.Error(releasemetrics.WorkerPostReleaseWatch)
		}
	}
	if len(rows) < api.PostReleaseWatchBatchSize {
		s.postReleaseCursor = state.ReleaseScanCursor{}
	}
	return nil
}

func postReleaseWindow(t rollbackOn5xxTelemetry, ctx context.Context, appID, deploymentID string, since, until time.Time) (canary.HealthWindow, error) {
	requests, serverErrors, p95, coldBoot, coldBootP95, _, _, err := t.RequestTelemetryCircuitBreakerSummary(ctx, appID, deploymentID, since, until)
	if err != nil {
		return canary.HealthWindow{}, err
	}
	return canary.HealthWindow{Requests: requests, ServerErrors: serverErrors, P95LatencyMS: p95,
		ColdBootRequests: coldBoot, ColdBootP95LatencyMS: coldBootP95}, nil
}

// postReleaseSharedRouteTelemetry is the optional reader for the shared-route
// latency check; Postgres request telemetry implements it.
type postReleaseSharedRouteTelemetry interface {
	RequestTelemetrySharedRouteLatency(ctx context.Context, appID, releaseID string, releaseSince, releaseUntil time.Time, baselineID string, baselineSince, baselineUntil time.Time) (state.SharedRouteLatency, error)
}

// confirmSharedRouteLatency re-judges a whole-app p95 regression over the
// routes both releases served (ADR-1445 amendment 2026-11-10, Hunt 32
// H32-33). It runs only after the whole-app p95 regressed, so the extra read
// happens on suspected regressions alone. A reader without the query keeps
// the whole-app verdict.
func confirmSharedRouteLatency(ctx context.Context, telemetry rollbackOn5xxTelemetry, c state.PostReleaseWatchCandidate, wholeAppReason string, since, until, baselineEnd time.Time) (rule, reason string, regressed bool, err error) {
	reader, ok := telemetry.(postReleaseSharedRouteTelemetry)
	if !ok {
		return api.RolloutRuleAbortPostReleaseP95, wholeAppReason, true, nil
	}
	shared, err := reader.RequestTelemetrySharedRouteLatency(ctx, c.AppID, c.DeploymentID, since, until,
		c.PredecessorID, baselineEnd.Add(-api.PostReleaseWatchBaseline), baselineEnd)
	if err != nil {
		return "", "", false, fmt.Errorf("read shared-route latency: %w", err)
	}
	rule, reason, regressed = release.ConfirmPostReleaseLatency(
		canary.HealthWindow{Requests: shared.ReleaseRequests, P95LatencyMS: shared.ReleaseP95LatencyMS},
		canary.HealthWindow{Requests: shared.BaselineRequests, P95LatencyMS: shared.BaselineP95LatencyMS})
	return rule, reason, regressed, nil
}

// evaluatePostRelease compares the release since it completed with its
// predecessor's traffic before the rollout started. Telemetry is bucketed by
// minute, so the bucket holding the window's end still counts.
func (s *server) evaluatePostRelease(ctx context.Context, store state.PostReleaseWatchStore, telemetry rollbackOn5xxTelemetry, c state.PostReleaseWatchCandidate, now time.Time) error {
	since := c.RolloutCompletedAt.Truncate(time.Minute)
	until := now
	if end := c.RolloutCompletedAt.Add(api.PostReleaseWatchWindow); end.Before(now) {
		until = end.Truncate(time.Minute).Add(time.Minute)
	}
	// Only closed minutes: an open minute holds 5xx rows but not the folded
	// 2xx yet (ADR-625 amendment 2026-11-10).
	until = canary.ClosedTelemetryUntil(until, now)
	current, err := postReleaseWindow(telemetry, ctx, c.AppID, c.DeploymentID, since, until)
	if err != nil {
		return fmt.Errorf("read release telemetry: %w", err)
	}
	baselineEnd := c.RolloutStartedAt.Truncate(time.Minute)
	previous, err := postReleaseWindow(telemetry, ctx, c.AppID, c.PredecessorID, baselineEnd.Add(-api.PostReleaseWatchBaseline), baselineEnd)
	if err != nil {
		return fmt.Errorf("read predecessor telemetry: %w", err)
	}
	rule, reason, regressed := release.JudgePostRelease(current, previous)
	if regressed && rule == api.RolloutRuleAbortPostReleaseP95 {
		if rule, reason, regressed, err = confirmSharedRouteLatency(ctx, telemetry, c, reason, since, until, baselineEnd); err != nil {
			return err
		}
	}
	var gates releasegate.Verdict
	if !regressed {
		// ADR-20261019071734: the app's own signals, judged against the
		// predecessor's traffic before the rollout.
		var gateErr error
		req := gather.Request{AppID: c.AppID, ReleaseID: c.DeploymentID, BaselineID: c.PredecessorID,
			ReleaseWindow:  gather.Window{Since: since, Until: until},
			BaselineWindow: gather.Window{Since: baselineEnd.Add(-api.PostReleaseWatchBaseline), Until: baselineEnd},
			PriorWindow:    gather.Window{Since: baselineEnd.Add(-api.PostReleaseWatchBaseline), Until: baselineEnd},
			Evidence:       s.releaseGateEvidence}
		if history := appmetrics.NewCustomMetricHistory(s.promqlClient); history != nil {
			req.CustomMetrics = history
		}
		gates, gateErr = gather.Verdict(ctx, s.store, req)
		if gateErr != nil {
			s.log.Warn("post-release watch: some release gates could not be read; judging the rest", "app", c.AppID, "deployment", c.DeploymentID, "err", gateErr)
		}
		if gates.Failed == nil {
			return nil
		}
		rule, reason = api.RolloutRuleAbortPostReleaseGate, "after the rollout completed: "+gates.Summary()
	}
	if regressed {
		// Request telemetry a gateway dropped would compare partial counts;
		// judge again once coverage holds (ADR-625 amendment 2026-11-10).
		for _, window := range [][2]time.Time{{since, until}, {baselineEnd.Add(-api.PostReleaseWatchBaseline), baselineEnd}} {
			if lost, err := s.releaseTelemetryLost(ctx, telemetry, window[0], window[1]); err != nil || lost {
				return err
			}
		}
	}
	decision := release.PostReleaseDecision(rule, reason, c.PredecessorID, current, previous, since, until, now)
	decision.Gates = gates.Results
	return s.postReleaseRollback(ctx, store, c, decision)
}

// postReleaseRollback hands the predecessor to the readiness-gated rollback
// every other surface uses, then marks the release so it is never judged
// again and records why.
func (s *server) postReleaseRollback(ctx context.Context, store state.PostReleaseWatchStore, c state.PostReleaseWatchCandidate, decision api.RolloutDecision) error {
	app, err := s.store.AppByID(ctx, c.AppID)
	if err != nil {
		return fmt.Errorf("load app: %w", err)
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return fmt.Errorf("load account: %w", err)
	}
	request, err := http.NewRequestWithContext(state.WithExpectedRollbackCurrent(ctx, c.DeploymentID), http.MethodPost, "/", nil)
	if err != nil {
		return err
	}
	target := c.PredecessorID
	if _, _, problem := s.rollbackAppCore(request, acct, app, api.RollbackRequest{TargetDeploymentID: &target}, state.RollbackTriggerPostRelease); problem != nil {
		if problem.Code == api.CodeRollbackCrossesRelease {
			// ADR-1448: automation never runs code on changes it has not
			// served with. The release keeps serving; a person decides.
			return s.postReleaseRollbackSkipped(ctx, store, c, acct, app, decision, problem)
		}
		return fmt.Errorf("roll back to %s: %s: %s", target, problem.Code, problem.Detail)
	}
	s.releaseMetrics.Decision(releasemetrics.GuardPostRelease, "rolled_back")
	if _, err := s.store.MarkAutoRollback(ctx, c.DeploymentID, string(state.AutoRollbackReasonPostReleaseRegression), time.Now().UTC()); err != nil {
		return fmt.Errorf("record post-release rollback: %w", err)
	}
	if err := decision.Validate(); err == nil {
		if raw, err := json.Marshal(decision); err == nil {
			if err := store.RecordPostReleaseRolloutDecision(ctx, c.DeploymentID, raw); err != nil {
				s.log.Warn("post-release watch: record decision", "deployment", c.DeploymentID, "err", err)
			}
		}
	}
	s.log.Warn("post-release watch: rollback requested", "app", app.ID, "deployment", c.DeploymentID,
		"target", target, "rule", decision.Rule, "summary", decision.Summary)
	s.audit.Emit(ctx, "app.post_release_rollback", &acct.ID, map[string]any{
		"app_id": app.ID, "from": c.DeploymentID, "to": target, "rule": decision.Rule, "summary": decision.Summary,
	})
	return nil
}

// postReleaseRollbackSkipped records a regression the watch found but could
// not roll back. The decision on the release carries rollback_skipped, so the
// release, its timeline and `gregale release get` show the regression, and
// the watch does not judge it again (release review 2026-10-31: the skip was
// only logged, every sweep, and the timeline then said the watch passed).
func (s *server) postReleaseRollbackSkipped(ctx context.Context, store state.PostReleaseWatchStore, c state.PostReleaseWatchCandidate,
	acct state.Account, app state.App, decision api.RolloutDecision, problem *api.Problem) error {
	decision.RollbackSkipped = problem.Code
	if err := decision.Validate(); err != nil {
		return fmt.Errorf("post-release decision: %w", err)
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		return fmt.Errorf("encode post-release decision: %w", err)
	}
	if err := store.RecordPostReleaseRolloutDecision(ctx, c.DeploymentID, raw); err != nil {
		return fmt.Errorf("record skipped post-release rollback: %w", err)
	}
	s.releaseMetrics.Decision(releasemetrics.GuardPostRelease, "kept")
	s.log.Warn("post-release watch: rollback skipped; it would cross release commands",
		"app", app.ID, "deployment", c.DeploymentID, "target", c.PredecessorID, "detail", problem.Detail)
	s.audit.Emit(ctx, "app.post_release_rollback_skipped", &acct.ID, map[string]any{
		"app_id": app.ID, "deployment": c.DeploymentID, "target": c.PredecessorID, "rule": decision.Rule,
		"summary": decision.Summary, "reason": problem.Code,
	})
	return nil
}

func (s *server) runPostReleaseWatchWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(api.PostReleaseWatchCheckIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := s.postReleaseWatchSweep(bounded, time.Now().UTC()); err != nil && ctx.Err() == nil {
			s.log.Warn("post-release watch sweep", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

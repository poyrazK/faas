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
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/releasegate"
	"github.com/onebox-faas/faas/pkg/releasegate/gather"
	"github.com/onebox-faas/faas/pkg/state"
)

// postReleaseWatchSweep judges every watched release once. One failing
// release does not stop the others.
func (s *server) postReleaseWatchSweep(ctx context.Context, now time.Time) error {
	store, ok := s.store.(state.PostReleaseWatchStore)
	telemetry := s.postReleaseTelemetry
	if telemetry == nil {
		telemetry, _ = s.store.(rollbackOn5xxTelemetry)
	}
	if !ok || telemetry == nil {
		return nil
	}
	rows, err := store.ListPostReleaseWatchCandidates(ctx, api.PostReleaseWatchWindow+api.RollbackOn5xxTelemetryGrace, api.PostReleaseWatchBatchSize)
	if err != nil {
		return err
	}
	for _, c := range rows {
		if err := s.evaluatePostRelease(ctx, store, telemetry, c, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.Warn("post-release watch: evaluate release", "app", c.AppID, "deployment", c.DeploymentID, "err", err)
		}
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

// evaluatePostRelease compares the release since it completed with its
// predecessor's traffic before the rollout started. Telemetry is bucketed by
// minute, so the bucket holding the window's end still counts.
func (s *server) evaluatePostRelease(ctx context.Context, store state.PostReleaseWatchStore, telemetry rollbackOn5xxTelemetry, c state.PostReleaseWatchCandidate, now time.Time) error {
	since := c.RolloutCompletedAt.Truncate(time.Minute)
	until := now
	if end := c.RolloutCompletedAt.Add(api.PostReleaseWatchWindow); end.Before(now) {
		until = end.Truncate(time.Minute).Add(time.Minute)
	}
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
	var gates releasegate.Verdict
	if !regressed {
		// ADR-20261019071734: the app's own signals, judged against the
		// predecessor's traffic before the rollout.
		var gateErr error
		gates, gateErr = gather.Verdict(ctx, s.store, gather.Request{AppID: c.AppID, ReleaseID: c.DeploymentID, BaselineID: c.PredecessorID,
			ReleaseWindow:  gather.Window{Since: since, Until: until},
			BaselineWindow: gather.Window{Since: baselineEnd.Add(-api.PostReleaseWatchBaseline), Until: baselineEnd},
			PriorWindow:    gather.Window{Since: baselineEnd.Add(-api.PostReleaseWatchBaseline), Until: baselineEnd},
			Evidence:       s.releaseGateEvidence})
		if gateErr != nil {
			s.log.Warn("post-release watch: some release gates could not be read; judging the rest", "app", c.AppID, "deployment", c.DeploymentID, "err", gateErr)
		}
		if gates.Failed == nil {
			return nil
		}
		rule, reason = api.RolloutRuleAbortPostReleaseGate, "after the rollout completed: "+gates.Summary()
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
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if err != nil {
		return err
	}
	target := c.PredecessorID
	if _, _, problem := s.rollbackAppCore(request, acct, app, api.RollbackRequest{TargetDeploymentID: &target}, state.RollbackTriggerPostRelease); problem != nil {
		if problem.Code == api.CodeRollbackCrossesRelease {
			// ADR-1448: automation never runs code on changes it has not
			// served with. The release keeps serving; a person decides.
			s.log.Warn("post-release watch: rollback skipped; it would cross release commands",
				"app", app.ID, "deployment", c.DeploymentID, "target", target, "detail", problem.Detail)
			return nil
		}
		return fmt.Errorf("roll back to %s: %s: %s", target, problem.Code, problem.Detail)
	}
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

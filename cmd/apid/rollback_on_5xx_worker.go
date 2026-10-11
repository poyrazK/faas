// adr: 625 — apid evaluates first-wake 5xx auto-rollback from request telemetry.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// rollbackOn5xxTelemetry is the per-deployment request summary the meterd
// canary circuit breaker already reads. Request telemetry is Postgres-only,
// so the worker is inert on a store without it.
type rollbackOn5xxTelemetry interface {
	RequestTelemetryCircuitBreakerSummary(ctx context.Context, appID, deploymentID string, since, until time.Time) (requests, serverErrors int64, p95LatencyMS float64, coldBootRequests int64, coldBootP95LatencyMS float64, cpuUsec, cpuRequests int64, err error)
}

// rollbackOn5xxBreached reports whether a release's first-wake responses
// cross the ADR-625 threshold: enough 5xx responses, and enough of them
// relative to its traffic that one failing route on a busy release does not
// revert it.
func rollbackOn5xxBreached(requests, serverErrors int64) bool {
	return serverErrors >= api.RollbackOn5xxMinServerErrors &&
		serverErrors*100 >= requests*api.RollbackOn5xxMinErrorPct
}

// rollbackOn5xxSweep evaluates every eligible release once. production-us
// hunt #4: `deploy --rollback-on-5xx` stored the opt-in and nothing read it,
// so a release answering 100% 500s stayed live. One failing candidate does
// not stop the others.
func (s *server) rollbackOn5xxSweep(ctx context.Context, now time.Time) error {
	store, ok := s.store.(state.RollbackOn5xxStore)
	telemetry, hasTelemetry := s.store.(rollbackOn5xxTelemetry)
	if !ok || !hasTelemetry {
		return nil
	}
	rows, err := store.ListRollbackOn5xxCandidates(ctx, api.RollbackOn5xxTelemetryGrace, api.RollbackOn5xxBatchSize)
	if err != nil {
		return err
	}
	for _, candidate := range rows {
		if err := s.evaluateRollbackOn5xx(ctx, telemetry, candidate, now); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.log.Warn("rollback-on-5xx: evaluate release", "app", candidate.AppID, "deployment", candidate.DeploymentID, "err", err)
		}
	}
	return nil
}

// evaluateRollbackOn5xx opens the first-wake window the first time traffic
// for the release is observed, then rolls back once the window's responses
// breach the threshold. Responses are counted from the release's creation so
// errors seen before the window was stamped still count.
func (s *server) evaluateRollbackOn5xx(ctx context.Context, telemetry rollbackOn5xxTelemetry, c state.RollbackOn5xxCandidate, now time.Time) error {
	until := now
	if c.WindowEndsAt != nil && c.WindowEndsAt.Before(now) {
		// Telemetry timestamps are minute buckets: the bucket holding the
		// window's end still belongs to the window.
		until = c.WindowEndsAt.Truncate(time.Minute).Add(time.Minute)
	}
	requests, serverErrors, _, _, _, _, _, err := telemetry.RequestTelemetryCircuitBreakerSummary(ctx, c.AppID, c.DeploymentID, c.CreatedAt.Truncate(time.Minute), until)
	if err != nil {
		return fmt.Errorf("read request telemetry: %w", err)
	}
	if requests == 0 {
		return nil
	}
	if c.WindowEndsAt == nil {
		if _, err := s.store.StampFirstWake(ctx, c.DeploymentID, api.RollbackOn5xxWindowMinutes); err != nil {
			return fmt.Errorf("open first-wake window: %w", err)
		}
	}
	if !rollbackOn5xxBreached(requests, serverErrors) {
		return nil
	}
	return s.autoRollbackOn5xx(ctx, c, requests, serverErrors)
}

// autoRollbackOn5xx hands the release's predecessor to the same
// readiness-gated rollback the REST and dashboard surfaces use, then records
// the reason so later sweeps skip the release.
func (s *server) autoRollbackOn5xx(ctx context.Context, c state.RollbackOn5xxCandidate, requests, serverErrors int64) error {
	app, err := s.store.AppByID(ctx, c.AppID)
	if err != nil {
		return fmt.Errorf("load app: %w", err)
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return fmt.Errorf("load account: %w", err)
	}
	target, err := s.rollbackOn5xxTarget(ctx, c)
	if errors.Is(err, state.ErrNotFound) {
		// The release has no predecessor in its scope (a first deploy):
		// there is nothing to revert to, so leave it serving.
		return nil
	}
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if err != nil {
		return err
	}
	targetID := target.ID
	if _, problem := s.rollbackAppCore(request, acct, app, api.RollbackRequest{TargetDeploymentID: &targetID}); problem != nil {
		return fmt.Errorf("roll back to %s: %s: %s", target.ID, problem.Code, problem.Detail)
	}
	if _, err := s.store.MarkAutoRollback(ctx, c.DeploymentID, string(state.AutoRollbackReasonThresholdExceeded), time.Now().UTC()); err != nil {
		return fmt.Errorf("record auto-rollback: %w", err)
	}
	s.log.Warn("rollback-on-5xx: release rolled back", "app", app.ID, "deployment", c.DeploymentID,
		"target", target.ID, "requests", requests, "server_errors", serverErrors)
	s.audit.Emit(ctx, "app.auto_rollback_on_5xx", &acct.ID, map[string]any{
		"app_id": app.ID, "from": c.DeploymentID, "to": target.ID,
		"requests": requests, "server_errors": serverErrors, "reason": string(state.AutoRollbackReasonThresholdExceeded),
	})
	return nil
}

// rollbackOn5xxTarget is the release the candidate replaced: the newest
// superseded deployment in the same scope created before it.
func (s *server) rollbackOn5xxTarget(ctx context.Context, c state.RollbackOn5xxCandidate) (state.Deployment, error) {
	deployments, err := s.store.ListDeploymentsForApp(ctx, c.AppID, 50, 0)
	if err != nil {
		return state.Deployment{}, fmt.Errorf("list deployments: %w", err)
	}
	for _, d := range deployments {
		if d.ID != c.DeploymentID && d.Status == state.DeploySuperseded &&
			recoveryScope(d.Scope) == recoveryScope(c.Scope) && d.CreatedAt.Before(c.CreatedAt) {
			return d, nil
		}
	}
	return state.Deployment{}, state.ErrNotFound
}

func (s *server) runRollbackOn5xxWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(api.RollbackOn5xxCheckIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		if err := s.rollbackOn5xxSweep(bounded, time.Now().UTC()); err != nil && ctx.Err() == nil {
			s.log.Warn("rollback-on-5xx sweep", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

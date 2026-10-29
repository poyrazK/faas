package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeLabelObserver reads which route labels an app served.
type routeLabelObserver interface {
	ObservedRouteLabels(ctx context.Context, accountID, appID string, since time.Time, labels []string) ([]string, error)
}

// unobservedRouteWarnings warns about saved selectors no request matched in
// the plan's telemetry lookback (ADR-20261028110044). It is advisory: an
// unreadable store or a plan without request telemetry warns nothing.
func (s *server) unobservedRouteWarnings(ctx context.Context, acct state.Account, appID string, labels []string) []string {
	limits := api.MustLimitsFor(acct.Plan)
	observer, ok := s.store.(routeLabelObserver)
	if !ok || len(labels) == 0 || !limits.DebugTelemetryEnabled {
		return nil
	}
	lookback := min(api.RouteHealthSeedLookback, time.Duration(limits.DebugTelemetryRetentionDays)*24*time.Hour)
	if lookback <= 0 {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, api.RouteSelectorObservationTimeout)
	defer cancel()
	observed, err := observer.ObservedRouteLabels(readCtx, acct.ID, appID, time.Now().Add(-lookback), labels)
	if err != nil {
		s.log.Warn("route selectors: read observed route labels", "app_id", appID, "err", err)
		return nil
	}
	return routehealth.UnobservedWarnings(labels, observed, lookback)
}

// routeHealthAbortWarning warns when an enforce guard that aborts confirmed
// regressions is saved while no Safe Deploy worker holds the rollout lease
// (ADR-20261028110044): only that worker runs the recovery, so until one does
// a confirmed regression holds the canary instead of aborting it.
func (s *server) routeHealthAbortWarning(ctx context.Context, gate api.RouteHealthGate) []string {
	if gate.Mode != "enforce" || routehealth.RegressionAction(gate.OnRegression) != "abort" {
		return nil
	}
	leases, ok := s.store.(state.SafeReleaseWorkerLeaseStore)
	if !ok {
		return nil
	}
	ready, err := leases.SafeReleaseWorkerLeaseReady(ctx)
	if err != nil {
		s.log.Warn("route health: read safe release worker lease", "app_id", gate.AppID, "err", err)
		return nil
	}
	if ready {
		return nil
	}
	return []string{"on_regression abort: no Safe Deploy worker holds the rollout lease now, so a confirmed regression holds the canary until a worker recovers it"}
}

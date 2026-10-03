package canary

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

type routeHealthRecoveryClient interface {
	RecoverCanaryRouteHealth(context.Context, string, int) (api.CanaryRouteHealthRecoveryResponse, error)
}

func (p *Progression) routeHealthRecoveryReady(ctx context.Context, row CanaryRow, stats *Stats) bool {
	client, ok := p.APID.(routeHealthRecoveryClient)
	if !ok {
		return true
	}
	result, err := client.RecoverCanaryRouteHealth(ctx, row.ID, row.CanaryStep)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) && (problem.Code == api.CodeCanaryStepConflict || problem.Code == api.CodeRolloutStateInvalid || problem.Status == 404) {
			stats.SkippedRouteGate++
			return false
		}
		p.Log.Warn("canary: route health recovery check failed; holding progression", "deployment_id", row.ID, "err", err)
		stats.Errors++
		return false
	}
	if !result.Aborted && (result.RouteHealth == nil || result.RouteHealth.Status != "aborted") {
		return true
	}
	if !result.Aborted || result.RouteHealth == nil || result.RouteHealth.Status != "aborted" || result.RouteHealth.DeploymentID != row.ID ||
		result.RouteHealth.Mode != "enforce" || result.RouteHealth.OnRegression != "abort" || result.RouteHealth.HistoryID == "" || result.AuditID == "" {
		p.Log.Error("canary: invalid route health recovery result; holding progression", "deployment_id", row.ID)
		stats.Errors++
		return false
	}
	stats.Aborted++
	p.Log.Warn("canary: critical route regression restored stable traffic", "deployment_id", row.ID, "stable_deployment_id", result.RouteHealth.StableDeploymentID,
		"decision_id", result.RouteHealth.HistoryID, "audit_id", result.AuditID)
	return false
}

package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

type routeMonitorRollbackClaimKey struct{}

// WithRouteMonitorRollbackClaim binds checked intent creation to a claimed
// incident. The store commits the operation, readiness outbox and incident
// link together, so retrying an abandoned claim cannot orphan an operation.
// Only the internal monitor worker supplies this context, never request data.
func WithRouteMonitorRollbackClaim(ctx context.Context, claim RouteMonitorRollbackClaim) context.Context {
	return context.WithValue(ctx, routeMonitorRollbackClaimKey{}, claim)
}

func routeMonitorRollbackClaim(ctx context.Context) (RouteMonitorRollbackClaim, bool) {
	claim, ok := ctx.Value(routeMonitorRollbackClaimKey{}).(RouteMonitorRollbackClaim)
	return claim, ok
}

func claimMatchesIncident(claim RouteMonitorRollbackClaim, i api.RouteMonitorIncident, app, target, current string) bool {
	return claim.IncidentID == i.ID && i.AppID == app && i.DeploymentID == current &&
		claim.DeploymentID == current && claim.TargetDeploymentID == target && i.Rollback != nil &&
		i.Rollback.TargetDeploymentID == target && i.Rollback.Route == claim.Route
}

func resumableRouteMonitorClaim(c api.RouteMonitorConfig, i api.RouteMonitorIncident) bool {
	return c.Enabled && routemonitor.OnViolation(c.OnViolation) == "rollback" &&
		i.Status == "open" && i.Revision == c.Revision && i.Rollback != nil && i.Rollback.Status == "claimed"
}

func requestedMonitorRollback(i api.RouteMonitorIncident, id string, now time.Time) (api.RouteMonitorIncident, error) {
	copy, err := cloneRouteMonitorIncident(i)
	if err != nil {
		return i, err
	}
	i = copy
	if err := recordRouteMonitorRollback(&i, api.RouteMonitorIncidentRollback{Status: "requested", OperationID: id, DecidedAt: now}); err != nil {
		return i, err
	}
	// Check/compact the bounded incident before MemStore changes deployments;
	// PostgreSQL would roll its entire transaction back on this failure.
	_, err = encodeRouteMonitorIncident(&i)
	return i, err
}

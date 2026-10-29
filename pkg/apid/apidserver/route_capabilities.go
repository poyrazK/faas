package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// Routes capabilities whose work also runs outside their API routes
// (ADR-20261028110044). The capability gate hides the routes; these decide
// the background work, so an operator's kill switch or a per-account preview
// release stops evaluation, enforcement and notifications too.
const (
	routeMonitorCapability      = "production-route-monitoring"
	routeHealthCapability       = "route-health-canary-guard"
	routeInsightsCapability     = "route-insights"
	routeRequirementsCapability = "route-requirements"
)

// routeGuardRelease decides the canary route guards for one app.
func (s *server) routeGuardRelease(acct state.Account, app state.App) state.RouteGuardRelease {
	return state.RouteGuardRelease{
		Health:       s.capabilityEnabledFor(routeHealthCapability, acct, app),
		Requirements: s.capabilityEnabledFor(routeRequirementsCapability, acct, app),
	}
}

// routeCapabilityReleased decides a routes capability for background work
// that knows only the account and app. Without a gate every capability is
// released; a failed read decides off, so unreadable owners do no work.
func (s *server) routeCapabilityReleased(ctx context.Context, capability, accountID, appID string) bool {
	if s.capGate == nil {
		return true
	}
	acct, err := s.store.AccountByID(ctx, accountID)
	if err != nil {
		s.log.DebugContext(ctx, "routes capability release: account lookup", "app_id", appID, "err", err)
		return false
	}
	app, err := s.store.AppByID(ctx, appID)
	if err != nil || app.AccountID != acct.ID {
		s.log.DebugContext(ctx, "routes capability release: app lookup", "app_id", appID, "err", err)
		return false
	}
	return s.capabilityEnabledFor(capability, acct, app)
}

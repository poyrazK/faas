package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routejobs"
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

// customerGroupCapabilities names the capability whose identities a route
// customer grouping reads (ADR-20261028110044 amendment 2).
var customerGroupCapabilities = map[string]string{"consumer": "api-consumers", "tenant": "platform-tenants"}

// routeCustomerGroupingProblem refuses per-customer route evaluation grouped
// by identities whose capability is not released for the account: there
// would be no customers to evaluate.
func (s *server) routeCustomerGroupingProblem(acct state.Account, app state.App, groupBy string) *api.Problem {
	capability, ok := customerGroupCapabilities[groupBy]
	if !ok || s.capabilityEnabledFor(capability, acct, app) {
		return nil
	}
	return api.ErrCapabilityNotReleased("customer_group_by "+groupBy, s.capGate.Name(capability))
}

// routeHealthHold reports when an enforced guard's hold on deploymentID began
// and when the hold deadline aborts it (ADR-20261028110044 amendment 2); nil
// when the canary is not held.
func (s *server) routeHealthHold(ctx context.Context, report api.RouteHealthReport, appID, deploymentID string) *api.RouteHealthHold {
	holds, ok := s.store.(state.RouteAlertStore)
	if !ok || report.Mode != "enforce" {
		return nil
	}
	now := time.Now()
	found, _, err := holds.RouteHealthHoldsSince(ctx, appID, now, routeHealthHoldScan)
	if err != nil {
		s.log.Warn("route health: read hold", "app_id", appID, "err", err)
		return nil
	}
	held, _ := routejobs.HoldPastDeadline(found, deploymentID, now)
	if held <= 0 {
		return nil
	}
	since := now.Add(-held).UTC()
	return &api.RouteHealthHold{Since: since, AbortsAt: since.Add(api.RouteHealthHoldDeadline)}
}

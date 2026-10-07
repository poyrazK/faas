package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

var _ RouteHealthInvestigationStore = (*MemStore)(nil)

func (m *MemStore) GetRouteHealthInvestigation(_ context.Context, accountID, appID, deploymentID string, opts api.RouteHealthInvestigationOptions) (api.RouteHealthInvestigation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	report, err := m.routeHealthReportLocked(accountID, appID, deploymentID)
	if err != nil {
		return api.RouteHealthInvestigation{}, err
	}
	if !m.accounts[accountID].Plan.DebugTelemetryEnabled() {
		return api.RouteHealthInvestigation{}, ErrRouteInvestigationPlan
	}
	routehealth.EvaluateClientErrors(&report, "telemetry_unavailable")
	finding, err := routehealth.InvestigationFinding(report, opts)
	if err != nil {
		return api.RouteHealthInvestigation{}, ErrInvalidArgument
	}
	selection := opts.Selection()
	if selection.CustomerID != "" {
		if selection.CustomerGroupBy == "tenant" {
			t, ok := m.platformTenants[selection.CustomerID]
			if !ok || t.AccountID != accountID {
				return api.RouteHealthInvestigation{}, ErrNotFound
			}
		} else {
			c, ok := m.apiConsumers[selection.CustomerID]
			if !ok || c.AccountID != accountID || c.AppID != appID {
				return api.RouteHealthInvestigation{}, ErrNotFound
			}
		}
	}
	return routehealth.NewInvestigation(report, finding, selection), nil
}

package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

var _ RouteCustomerHealthStore = (*MemStore)(nil)

func (m *MemStore) GetRouteHealthReportWithCustomers(_ context.Context, accountID, appID, deploymentID, groupBy string, details bool) (api.RouteHealthReport, error) {
	if groupBy != "tenant" && groupBy != "consumer" {
		return api.RouteHealthReport{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	report, err := m.routeHealthReportLocked(accountID, appID, deploymentID)
	if err != nil {
		return report, err
	}
	report.Customers = &api.RouteCustomerHealthReport{GroupBy: groupBy, DetailsIncluded: details, Routes: []api.RouteCustomerHealthRoute{}}
	for _, r := range report.Routes {
		report.Customers.Routes = append(report.Customers.Routes, api.RouteCustomerHealthRoute{Method: r.Method, Path: r.Path, Customers: []api.RouteCustomerHealthCohort{}})
	}
	routehealth.EvaluateCustomers(&report, "telemetry_unavailable")
	return report, nil
}

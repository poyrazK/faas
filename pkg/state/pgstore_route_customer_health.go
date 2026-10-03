package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RouteCustomerHealthStore = (*PgStore)(nil)

func (s *PgStore) GetRouteHealthReportWithCustomers(ctx context.Context, accountID, appID, deploymentID, groupBy string, details bool) (api.RouteHealthReport, error) {
	if groupBy != "tenant" && groupBy != "consumer" {
		return api.RouteHealthReport{}, ErrInvalidArgument
	}
	return s.getRouteHealthReport(ctx, accountID, appID, deploymentID, groupBy, details)
}

func pgRouteCustomerHealth(ctx context.Context, db sqlc.DBTX, accountID string, report *api.RouteHealthReport, groupBy string, details bool) error {
	report.Customers = &api.RouteCustomerHealthReport{GroupBy: groupBy, DetailsIncluded: details, Routes: []api.RouteCustomerHealthRoute{}}
	selectors := []api.RouteHealthRoute{}
	for _, r := range report.Routes {
		selectors = append(selectors, api.RouteHealthRoute{WatchStatuses: r.WatchStatuses, Method: r.Method, Path: r.Path, CheckLatency: r.CheckLatency, MaxP95MS: r.MaxP95MS})
		report.Customers.Routes = append(report.Customers.Routes, api.RouteCustomerHealthRoute{Method: r.Method, Path: r.Path, Customers: []api.RouteCustomerHealthCohort{}})
	}
	unavailable := ""
	if report.StableDeploymentID == "" {
		unavailable = report.Reason
	} else if len(selectors) > 0 {
		windows := routehealth.Windows(report.CheckedAt)
		routesJSON, err := json.Marshal(selectors)
		if err != nil {
			return fmt.Errorf("encode customer health routes: %w", err)
		}
		windowsJSON, err := json.Marshal(windows)
		if err != nil {
			return fmt.Errorf("encode customer health windows: %w", err)
		}
		body, err := (&sqlc.Queries{}).RouteCustomerHealthObservation(ctx, db, sqlc.RouteCustomerHealthObservationParams{AppID: report.AppID, AccountID: accountID, CandidateID: report.DeploymentID, StableID: report.StableDeploymentID, GroupBy: groupBy, Routes: routesJSON, Windows: windowsJSON, CustomerLimit: api.RouteCustomerHealthMaxCustomers, LatencyQuantile: api.RouteHealthLatencyQuantile, Since: NewPgtypeTime(windows[0].Start), Until: NewPgtypeTime(windows[len(windows)-1].End)})
		if err != nil {
			return fmt.Errorf("read customer route health: %w", err)
		}
		if err := json.Unmarshal(body, &report.Customers.Routes); err != nil {
			return fmt.Errorf("decode customer route health: %w", err)
		}
	}
	routehealth.EvaluateCustomers(report, unavailable)
	return nil
}

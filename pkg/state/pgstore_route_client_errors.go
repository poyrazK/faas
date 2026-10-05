package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Only live report reads call this helper; advance/recovery snapshots remain
// confined to the original 5xx/latency evidence and policy.
func pgRouteClientErrors(ctx context.Context, db sqlc.DBTX, accountID string, report *api.RouteHealthReport) error {
	return pgRouteClientErrorsForCustomer(ctx, db, accountID, report, api.RouteHealthInvestigationSelection{})
}

func pgRouteClientErrorsForCustomer(ctx context.Context, db sqlc.DBTX, accountID string, report *api.RouteHealthReport, selection api.RouteHealthInvestigationSelection) error {
	selectors := []api.RouteHealthRoute{}
	for _, f := range report.Routes {
		if len(f.WatchStatuses) > 0 {
			selectors = append(selectors, api.RouteHealthRoute{Method: f.Method, Path: f.Path, WatchStatuses: f.WatchStatuses})
		}
	}
	unavailable := ""
	if report.StableDeploymentID == "" {
		unavailable = report.Reason
	} else if len(selectors) > 0 {
		windows := routehealth.Windows(report.CheckedAt)
		routesJSON, err := json.Marshal(selectors)
		if err != nil {
			return fmt.Errorf("encode watched status routes: %w", err)
		}
		windowsJSON, err := json.Marshal(windows)
		if err != nil {
			return fmt.Errorf("encode watched status windows: %w", err)
		}
		body, err := (&sqlc.Queries{}).RouteHealthClientErrorObservation(ctx, db, sqlc.RouteHealthClientErrorObservationParams{AppID: report.AppID, AccountID: accountID, CandidateID: report.DeploymentID, StableID: report.StableDeploymentID, Routes: routesJSON, Windows: windowsJSON, Since: NewPgtypeTime(windows[0].Start), Until: NewPgtypeTime(windows[len(windows)-1].End), CustomerID: selection.CustomerID, CustomerGroupBy: selection.CustomerGroupBy})
		if err != nil {
			return fmt.Errorf("read watched status observations: %w", err)
		}
		var rows []struct {
			Method       string                            `json:"method"`
			Path         string                            `json:"path"`
			ClientErrors *api.RouteHealthClientErrorReport `json:"client_errors"`
		}
		if err := json.Unmarshal(body, &rows); err != nil {
			return fmt.Errorf("decode watched status observations: %w", err)
		}
		for i := range report.Routes {
			for _, row := range rows {
				if row.Method == report.Routes[i].Method && row.Path == report.Routes[i].Path {
					report.Routes[i].ClientErrors = row.ClientErrors
				}
			}
		}
	}
	routehealth.EvaluateClientErrors(report, unavailable)
	return nil
}

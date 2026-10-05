package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgActiveRouteMonitorIncident(ctx context.Context, db sqlc.DBTX, accountID, appID string) (*api.RouteMonitorIncident, error) {
	body, err := sqlc.New().ReadActiveRouteMonitorIncident(ctx, db, sqlc.ReadActiveRouteMonitorIncidentParams{AccountID: accountID, AppID: appID})
	if err != nil {
		return nil, fmt.Errorf("read active route monitor incident: %w", err)
	}
	if len(body) == 0 || string(body) == "null" {
		return nil, nil
	}
	var i api.RouteMonitorIncident
	if err := json.Unmarshal(body, &i); err != nil {
		return nil, fmt.Errorf("decode active route monitor incident: %w", err)
	}
	return &i, nil
}

type requiredMonitorCustomer struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	CustomerID string `json:"customer_id"`
}

func emptyRouteMonitorRecoveryState() routeMonitorRecoveryState {
	return routeMonitorRecoveryState{Routes: []routeMonitorRecoveryRoute{}}
}

func pgReadRouteMonitorRecoveryState(ctx context.Context, db sqlc.DBTX, accountID, appID string) (routeMonitorRecoveryState, error) {
	body, err := sqlc.New().ReadRouteMonitorRecoveryCustomers(ctx, db, sqlc.ReadRouteMonitorRecoveryCustomersParams{AccountID: accountID, AppID: appID})
	if err != nil {
		return routeMonitorRecoveryState{}, fmt.Errorf("read route monitor recovery customers: %w", err)
	}
	state := emptyRouteMonitorRecoveryState()
	if err := json.Unmarshal(body, &state); err != nil {
		return state, fmt.Errorf("decode route monitor recovery customers: %w", err)
	}
	if state.Routes == nil {
		state.Routes = []routeMonitorRecoveryRoute{}
	}
	for _, route := range state.Routes {
		if route.Method == "" || route.Path == "" || len(route.CustomerIDs) > api.RouteMonitorRecoveryCustomersPerRoute {
			return state, errors.New("invalid route monitor recovery customer state")
		}
		seen := map[string]bool{}
		for _, id := range route.CustomerIDs {
			if _, err := uuid.Parse(id); err != nil || seen[id] {
				return state, errors.New("invalid route monitor recovery customer identity")
			}
			seen[id] = true
		}
	}
	return state, nil
}

func routeMonitorRecoveryStateFor(prior routeMonitorRecoveryState, active *api.RouteMonitorIncident, report api.RouteMonitorReport) routeMonitorRecoveryState {
	if active == nil || active.Status != "open" || active.DeploymentID != report.DeploymentID || active.Revision != report.Revision || active.OpeningReport.CustomerGroupBy != report.CustomerGroupBy || report.CustomerGroupBy == "" {
		return emptyRouteMonitorRecoveryState()
	}
	merged := emptyRouteMonitorRecoveryState()
	merged.Incomplete = prior.Incomplete
	ids := map[string]map[string]bool{}
	for _, route := range prior.Routes {
		if ids[route.Method+"\x00"+route.Path] == nil {
			ids[route.Method+"\x00"+route.Path] = map[string]bool{}
		}
		for _, id := range route.CustomerIDs {
			ids[route.Method+"\x00"+route.Path][id] = true
		}
	}
	if opening := active.OpeningReport.Customers; opening != nil {
		merged.Incomplete = merged.Incomplete || opening.RecoveryInventoryIncomplete
		for _, route := range opening.Routes {
			key := route.Method + "\x00" + route.Path
			merged.Incomplete = merged.Incomplete || route.ViolatingCustomersTruncated
			if ids[key] == nil {
				ids[key] = map[string]bool{}
			}
			for _, id := range route.ViolatingCustomerIDs {
				ids[key][id] = true
			}
		}
	}
	for _, route := range report.Routes {
		key := route.Route.Method + "\x00" + route.Route.Path
		if ids[key] == nil {
			continue
		}
		customers := make([]string, 0, len(ids[key]))
		for id := range ids[key] {
			customers = append(customers, id)
		}
		sort.Strings(customers)
		if len(customers) > api.RouteMonitorRecoveryCustomersPerRoute {
			merged.Incomplete = true
			customers = customers[:api.RouteMonitorRecoveryCustomersPerRoute]
		}
		merged.Routes = append(merged.Routes, routeMonitorRecoveryRoute{Method: route.Route.Method, Path: route.Route.Path, CustomerIDs: customers})
	}
	return merged
}

func mergeRouteMonitorRecoveryState(prior routeMonitorRecoveryState, report api.RouteMonitorReport) (routeMonitorRecoveryState, api.RouteMonitorReport) {
	if report.Customers == nil {
		return emptyRouteMonitorRecoveryState(), report
	}
	state := routeMonitorRecoveryState{Incomplete: prior.Incomplete, Routes: []routeMonitorRecoveryRoute{}}
	byRoute := map[string]map[string]bool{}
	previousGlobal := map[string]bool{}
	for _, route := range prior.Routes {
		key := route.Method + "\x00" + route.Path
		byRoute[key] = map[string]bool{}
		for _, id := range route.CustomerIDs {
			byRoute[key][id] = true
			previousGlobal[id] = true
		}
	}
	newGlobal := map[string]bool{}
	for j := range report.Customers.Routes {
		cohortRoute := &report.Customers.Routes[j]
		key := cohortRoute.Method + "\x00" + cohortRoute.Path
		ids := byRoute[key]
		if ids == nil {
			ids = map[string]bool{}
		}
		previousCount := len(ids)
		for _, id := range cohortRoute.ViolatingCustomerIDs {
			ids[id] = true
			if !previousGlobal[id] {
				newGlobal[id] = true
			}
		}
		if cohortRoute.ViolatingCustomersTruncated {
			state.Incomplete = true
		}
		customers := make([]string, 0, len(ids))
		for id := range ids {
			customers = append(customers, id)
		}
		sort.Strings(customers)
		if len(customers) > api.RouteMonitorRecoveryCustomersPerRoute {
			state.Incomplete = true
			customers = customers[:api.RouteMonitorRecoveryCustomersPerRoute]
		}
		cohortRoute.RecoveryRemainingCustomers += int64(len(ids) - previousCount)
		if cohortRoute.RecoveryRemainingCustomers < cohortRoute.ViolatedCustomers {
			cohortRoute.RecoveryRemainingCustomers = cohortRoute.ViolatedCustomers
		}
		state.Routes = append(state.Routes, routeMonitorRecoveryRoute{Method: cohortRoute.Method, Path: cohortRoute.Path, CustomerIDs: customers})
	}
	report.Customers.RecoveryRemainingCustomers += int64(len(newGlobal))
	if report.Customers.RecoveryRemainingCustomers < report.Customers.ViolatedCustomers {
		report.Customers.RecoveryRemainingCustomers = report.Customers.ViolatedCustomers
	}
	state.Incomplete = state.Incomplete || report.Customers.RecoveryInventoryIncomplete
	report.Customers.RecoveryInventoryIncomplete = state.Incomplete
	routemonitor.Evaluate(&report, "")
	return state, report
}

func pgRouteMonitorCustomers(ctx context.Context, db sqlc.DBTX, accountID, deploymentID string, report api.RouteMonitorReport, recovery routeMonitorRecoveryState, active *api.RouteMonitorIncident) error {
	if report.Customers == nil {
		return errors.New("customer monitoring report was not initialized")
	}
	recovery = routeMonitorRecoveryStateFor(recovery, active, report)
	required := []requiredMonitorCustomer{}
	for _, route := range recovery.Routes {
		for _, id := range route.CustomerIDs {
			required = append(required, requiredMonitorCustomer{Method: route.Method, Path: route.Path, CustomerID: id})
		}
	}
	routes := make([]api.RouteMonitorRoute, 0, len(report.Routes))
	windows := []api.RouteMonitorWindow{}
	if len(report.Routes) > 0 {
		windows = report.Routes[0].Windows
	}
	for _, f := range report.Routes {
		routes = append(routes, f.Route)
	}
	routesJSON, err := json.Marshal(routes)
	if err != nil {
		return fmt.Errorf("encode monitored customer routes: %w", err)
	}
	windowsJSON, err := json.Marshal(windows)
	if err != nil {
		return fmt.Errorf("encode customer windows: %w", err)
	}
	requiredJSON, err := json.Marshal(required)
	if err != nil {
		return fmt.Errorf("encode recovery customer identities: %w", err)
	}
	body, err := sqlc.New().RouteMonitorCustomerObservations(ctx, db, sqlc.RouteMonitorCustomerObservationsParams{GroupBy: report.CustomerGroupBy, CustomerLimit: api.RouteMonitorCustomersPerRoute, Routes: routesJSON, Windows: windowsJSON, AccountID: accountID, AppID: report.AppID, DeploymentID: deploymentID, RequiredCustomers: requiredJSON, ObservationAnchor: NewPgtypeTime(*report.ObservationAnchor), RecoveryLimit: api.RouteMonitorRecoveryCustomersPerRoute, MinimumRequests: api.RouteHealthMinRequests, MinimumErrors: api.RouteHealthMinErrors, MinimumLatencyRequests: api.RouteHealthMinLatencyRequests, MaxRateBps: api.RouteMonitorMaxRateBPS, LatencyQuantile: api.RouteHealthLatencyQuantile})
	if err != nil {
		return fmt.Errorf("read production customer route budgets: %w", err)
	}
	if err := json.Unmarshal(body, report.Customers); err != nil {
		return fmt.Errorf("decode production customer route budgets: %w", err)
	}
	report.Customers.DetailsIncluded = true
	report.Customers.RecoveryInventoryIncomplete = recovery.Incomplete
	for j := range report.Customers.Routes {
		cr := &report.Customers.Routes[j]
		if cr.ViolatingCustomersTruncated {
			report.Customers.RecoveryInventoryIncomplete = true
		}
		for k := range cr.Customers {
			cohort := &cr.Customers[k]
			for w := range cohort.Windows {
				cohort.Windows[w].Observed.ErrorRate = 0
				if n := cohort.Windows[w].Observed.Requests; n > 0 {
					cohort.Windows[w].Observed.ErrorRate = float64(cohort.Windows[w].Observed.ServerErrors) / float64(n)
				}
			}
			f := api.RouteMonitorFinding{Route: report.Routes[j].Route, Windows: cohort.Windows}
			// The report's ordinary evaluator supplies reason strings as well as verdicts.
			routemonitor.EvaluateFinding(&f, report.ObservationAnchor)
			cohort.Windows, cohort.Status, cohort.Reason, cohort.ErrorStatus, cohort.LatencyStatus = f.Windows, f.Status, f.Reason, f.ErrorStatus, f.LatencyStatus
		}
		for k := range cr.Windows {
			var displayed int64
			for _, cohort := range cr.Customers {
				displayed += cohort.Windows[k].Observed.Requests
			}
			cr.Windows[k].OtherCustomerRequests = cr.Windows[k].IdentifiedRequests - displayed
		}
	}
	routemonitor.Evaluate(&report, "")
	return nil
}

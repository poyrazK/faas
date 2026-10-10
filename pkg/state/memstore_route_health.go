package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

var _ RouteHealthStore = (*MemStore)(nil)

func (m *MemStore) routeHealthGateLocked(accountID, appID string) (api.RouteHealthGate, error) {
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.RouteHealthGate{}, ErrNotFound
	}
	g, ok := m.routeHealthGates[appID]
	if !ok {
		g = defaultRouteHealthGate(appID)
	}
	g.Routes = routehealth.CloneRoutes(g.Routes)
	if g.UpdatedAt != nil {
		copy := *g.UpdatedAt
		g.UpdatedAt = &copy
	}
	return g, nil
}
func (m *MemStore) GetRouteHealthGate(_ context.Context, accountID, appID string) (api.RouteHealthGate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.routeHealthGateLocked(accountID, appID)
}
func (m *MemStore) SetRouteHealthGate(_ context.Context, accountID, appID string, req api.SetRouteHealthGateRequest) (api.RouteHealthGate, error) {
	if err := routehealth.Validate(req); err != nil {
		return api.RouteHealthGate{}, ErrInvalidArgument
	}
	req.OnRegression = routehealth.RegressionAction(req.OnRegression)
	m.mu.Lock()
	defer m.mu.Unlock()
	g, err := m.routeHealthGateLocked(accountID, appID)
	if err != nil {
		return g, err
	}
	if g.Revision != *req.ExpectedRevision {
		return g, ErrRouteHealthRevision
	}
	plan := m.accounts[accountID].Plan
	if req.Mode == "enforce" && (!plan.TrafficSplitAllowed() || !plan.DebugTelemetryEnabled()) {
		return g, ErrRouteHealthPlan
	}
	// The first explicit save records intent even when it matches the default,
	// so an empty selector list opts out of default seeding (ADR-951).
	if g.Revision > 0 && g.Mode == req.Mode && g.OnRegression == req.OnRegression && routehealth.RoutesEqual(g.Routes, req.Routes) {
		return g, nil
	}
	if g.Revision >= api.RouteRequirementsMaxRevision {
		return g, ErrRouteHealthRevision
	}
	now := time.Now().UTC()
	g.Mode, g.OnRegression, g.Routes, g.Revision, g.UpdatedAt = req.Mode, req.OnRegression, routehealth.CloneRoutes(req.Routes), g.Revision+1, &now
	if g.Routes == nil {
		g.Routes = []api.RouteHealthRoute{}
	}
	if m.routeHealthGates == nil {
		m.routeHealthGates = map[string]api.RouteHealthGate{}
	}
	m.routeHealthGates[appID] = g
	return m.routeHealthGateLocked(accountID, appID)
}
func (m *MemStore) routeHealthReportLocked(accountID, appID, deploymentID string) (api.RouteHealthReport, error) {
	g, err := m.routeHealthGateLocked(accountID, appID)
	if err != nil {
		return api.RouteHealthReport{}, err
	}
	d, ok := m.deployments[deploymentID]
	if !ok || d.AppID != appID {
		return api.RouteHealthReport{}, ErrNotFound
	}
	report, anchor := newRouteHealthReport(g, d, time.Now().UTC())
	routehealth.Evaluate(&report, anchor, "telemetry_unavailable")
	return report, nil
}
func (m *MemStore) GetRouteHealthReport(_ context.Context, accountID, appID, deploymentID string) (api.RouteHealthReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	report, err := m.routeHealthReportLocked(accountID, appID, deploymentID)
	if err == nil {
		routehealth.EvaluateClientErrors(&report, "telemetry_unavailable")
	}
	return report, err
}
func (m *MemStore) checkRouteHealthLocked(d Deployment, params CanaryAdvanceParams) (routeHealthStoredDecision, memRouteHealthNotification, error) {
	report, err := m.routeHealthReportLocked(m.apps[d.AppID].AccountID, d.AppID, d.ID)
	if err != nil {
		return routeHealthStoredDecision{}, memRouteHealthNotification{}, err
	}
	entry, record, err := buildRouteHealthHistory(report, d, params)
	if err != nil {
		return record, memRouteHealthNotification{}, err
	}
	for _, previous := range m.routeHealthHistory[d.ID] {
		if previous.Key == record.Key {
			retained, err := decodeRouteHealthHistory(previous.Body, d.AppID, d.ID)
			if err != nil {
				return record, memRouteHealthNotification{}, err
			}
			entry, report = retained, retained.Report
			break
		}
	}
	notification, err := m.prepareRouteHealthNotificationLocked(entry)
	if err != nil {
		return record, notification, err
	}
	if err := requireRouteHealth(report, params.RouteHealthDecision, entry.ID); err != nil {
		m.appendRouteHealthHistoryLocked(d.ID, record)
		m.publishRouteHealthNotificationLocked(d.ID, notification)
		return record, notification, err
	}
	return record, notification, nil
}

package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

var _ RouteMonitorStore = (*MemStore)(nil)
var _ RouteMonitorWorkerStore = (*MemStore)(nil)

func (m *MemStore) routeMonitorConfigLocked(accountID, appID string) (api.RouteMonitorConfig, error) {
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return api.RouteMonitorConfig{}, ErrNotFound
	}
	c, ok := m.routeMonitorConfigs[appID]
	if !ok {
		c = defaultRouteMonitor(appID)
	}
	c.Routes = routemonitor.CloneRoutes(c.Routes)
	if c.UpdatedAt != nil {
		at := *c.UpdatedAt
		c.UpdatedAt = &at
	}
	return c, nil
}
func (m *MemStore) GetRouteMonitor(_ context.Context, accountID, appID string) (api.RouteMonitorConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.routeMonitorConfigLocked(accountID, appID)
}
func (m *MemStore) PreviewRouteMonitor(ctx context.Context, accountID, appID string, req api.PreviewRouteMonitorRequest) (api.RouteMonitorPreview, error) {
	return m.PreviewRouteMonitorWithCustomerDetails(ctx, accountID, appID, req, false)
}
func (m *MemStore) PreviewRouteMonitorWithCustomerDetails(_ context.Context, accountID, appID string, req api.PreviewRouteMonitorRequest, details bool) (api.RouteMonitorPreview, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.routeMonitorConfigLocked(accountID, appID)
	if err != nil {
		return api.RouteMonitorPreview{}, err
	}
	if routemonitor.ValidatePreviewRequest(req, current.Revision) != nil {
		return api.RouteMonitorPreview{}, ErrInvalidArgument
	}
	configChanged := !current.Enabled || current.CustomerGroupBy != req.CustomerGroupBy || !routemonitor.RoutesEqual(current.Routes, req.Routes)
	proposed := api.RouteMonitorConfig{
		CustomerGroupBy: req.CustomerGroupBy,
		AppID:           appID,
		Enabled:         true,
		Revision:        current.Revision,
		Routes:          routemonitor.CloneRoutes(req.Routes),
	}
	if !configChanged && current.UpdatedAt != nil {
		updatedAt := current.UpdatedAt.UTC()
		proposed.UpdatedAt = &updatedAt
	}
	report := routemonitor.NewReport(proposed, time.Now())
	routemonitor.Evaluate(&report, "telemetry_unavailable")
	return api.RouteMonitorPreview{
		CurrentRevision:                     current.Revision,
		PreviewOnly:                         true,
		ConfigChangeResetsObservationAnchor: configChanged,
		Report:                              routemonitor.ProjectReport(report, details),
	}, nil
}
func (m *MemStore) SetRouteMonitor(_ context.Context, accountID, appID string, req api.SetRouteMonitorRequest) (api.RouteMonitorConfig, error) {
	if routemonitor.Validate(req) != nil {
		return api.RouteMonitorConfig{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.routeMonitorConfigLocked(accountID, appID)
	if err != nil {
		return c, err
	}
	if c.Revision != *req.ExpectedRevision {
		return c, ErrRouteHealthRevision
	}
	if req.Enabled && (!m.accounts[accountID].Plan.DebugTelemetryEnabled() || !m.accounts[accountID].MayDeploy()) {
		return c, ErrRouteInvestigationPlan
	}
	if c.Enabled == req.Enabled && c.CustomerGroupBy == req.CustomerGroupBy && routemonitor.RoutesEqual(c.Routes, req.Routes) {
		return c, nil
	}
	if c.Revision >= api.RouteRequirementsMaxRevision {
		return c, ErrRouteHealthRevision
	}
	now := time.Now().UTC()
	for j := range m.routeMonitorIncidents[appID] {
		i := &m.routeMonitorIncidents[appID][j]
		if i.Status == "open" {
			closeRouteMonitorIncident(i, "superseded", now, nil)
		}
	}
	c.Enabled, c.Routes, c.CustomerGroupBy, c.Revision, c.UpdatedAt = req.Enabled, routemonitor.CloneRoutes(req.Routes), req.CustomerGroupBy, c.Revision+1, &now
	if m.routeMonitorConfigs == nil {
		m.routeMonitorConfigs = map[string]api.RouteMonitorConfig{}
	}
	if m.routeMonitorNextCheck == nil {
		m.routeMonitorNextCheck = map[string]time.Time{}
	}
	m.routeMonitorConfigs[appID], m.routeMonitorNextCheck[appID] = c, now
	return m.routeMonitorConfigLocked(accountID, appID)
}
func (m *MemStore) GetRouteMonitorReport(ctx context.Context, accountID, appID string) (api.RouteMonitorReport, error) {
	return m.GetRouteMonitorReportWithCustomerDetails(ctx, accountID, appID, false)
}
func (m *MemStore) GetRouteMonitorReportWithCustomerDetails(_ context.Context, accountID, appID string, details bool) (api.RouteMonitorReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.routeMonitorConfigLocked(accountID, appID)
	if err != nil {
		return api.RouteMonitorReport{}, err
	}
	r := routemonitor.NewReport(c, time.Now())
	routemonitor.Evaluate(&r, "telemetry_unavailable")
	return routemonitor.ProjectReport(r, details), nil
}
func (m *MemStore) ListDueRouteMonitors(_ context.Context) ([]RouteMonitorTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []RouteMonitorTarget{}
	now := time.Now()
	for id, c := range m.routeMonitorConfigs {
		app := m.apps[id]
		if c.Enabled && app.Status != AppDeleted && !m.routeMonitorNextCheck[id].After(now) {
			out = append(out, RouteMonitorTarget{AppID: id, AccountID: app.AccountID, Revision: c.Revision, DueAt: m.routeMonitorNextCheck[id]})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := m.routeMonitorNextCheck[out[i].AppID], m.routeMonitorNextCheck[out[j].AppID]
		if !a.Equal(b) {
			return a.Before(b)
		}
		return out[i].AppID < out[j].AppID
	})
	if len(out) > api.RouteMonitorBatchSize {
		out = out[:api.RouteMonitorBatchSize]
	}
	return out, nil
}
func (m *MemStore) EvaluateRouteMonitor(_ context.Context, accountID, appID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.routeMonitorConfigLocked(accountID, appID)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	if !c.Enabled || m.routeMonitorNextCheck[appID].After(now) {
		return false, nil
	}
	m.routeMonitorNextCheck[appID] = now.Add(api.RouteMonitorEvaluationInterval)
	// MemStore has no retained request telemetry. Unknown never closes incidents.
	return true, nil
}
func cloneRouteMonitorIncident(i api.RouteMonitorIncident) (api.RouteMonitorIncident, error) {
	body, err := json.Marshal(i)
	if err != nil {
		return i, err
	}
	var out api.RouteMonitorIncident
	err = json.Unmarshal(body, &out)
	return out, err
}
func (m *MemStore) monitorIncidentAccessLocked(accountID, appID string) error {
	if _, err := m.routeMonitorConfigLocked(accountID, appID); err != nil {
		return err
	}
	if !m.accounts[accountID].Plan.DebugTelemetryEnabled() {
		return ErrRouteInvestigationPlan
	}
	return nil
}
func (m *MemStore) GetRouteMonitorIncident(ctx context.Context, accountID, appID, id string) (api.RouteMonitorIncident, error) {
	return m.GetRouteMonitorIncidentWithCustomerDetails(ctx, accountID, appID, id, false)
}
func (m *MemStore) GetRouteMonitorIncidentWithCustomerDetails(_ context.Context, accountID, appID, id string, details bool) (api.RouteMonitorIncident, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.monitorIncidentAccessLocked(accountID, appID); err != nil {
		return api.RouteMonitorIncident{}, err
	}
	for _, i := range m.routeMonitorIncidents[appID] {
		if i.ID == id {
			copy, err := cloneRouteMonitorIncident(i)
			return routemonitor.ProjectIncident(copy, details), err
		}
	}
	return api.RouteMonitorIncident{}, ErrNotFound
}
func (m *MemStore) ListRouteMonitorIncidents(_ context.Context, accountID, appID string, limit int, before string) (api.RouteMonitorIncidentPage, error) {
	out := api.RouteMonitorIncidentPage{AppID: appID, Incidents: []api.RouteMonitorIncident{}}
	if limit < 1 || limit > api.RouteMonitorMaxPage {
		return out, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.monitorIncidentAccessLocked(accountID, appID); err != nil {
		return out, err
	}
	rows := append([]api.RouteMonitorIncident{}, m.routeMonitorIncidents[appID]...)
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].OpenedAt.Equal(rows[j].OpenedAt) {
			return rows[i].OpenedAt.After(rows[j].OpenedAt)
		}
		return rows[i].ID > rows[j].ID
	})
	start := 0
	if before != "" {
		found := false
		for j, i := range rows {
			if i.ID == before {
				start = j + 1
				found = true
				break
			}
		}
		if !found {
			return out, ErrNotFound
		}
	}
	for _, i := range rows[start:] {
		if len(out.Incidents) == limit {
			out.NextBefore = out.Incidents[limit-1].ID
			break
		}
		copy, err := cloneRouteMonitorIncident(i)
		if err != nil {
			return out, err
		}
		out.Incidents = append(out.Incidents, copy)
	}
	return out, nil
}

func (m *MemStore) DeferRouteMonitor(_ context.Context, t RouteMonitorTarget) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.routeMonitorConfigLocked(t.AccountID, t.AppID)
	if err != nil {
		return err
	}
	if c.Enabled && c.Revision == t.Revision && m.routeMonitorNextCheck[t.AppID].Equal(t.DueAt) {
		m.routeMonitorNextCheck[t.AppID] = time.Now().UTC().Add(api.RouteMonitorEvaluationInterval)
	}
	return nil
}

package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
)

type scopedBudgetTestStore struct {
	*state.MemStore
	report api.RouteMonitorReport
	scope  string
}

func (s *scopedBudgetTestStore) ReadScopedRouteMonitor(_ context.Context, _, app, scope string) (api.RouteMonitorReport, error) {
	s.scope = scope
	r := s.report
	r.AppID = app
	return r, nil
}
func TestScopedRouteMonitoringHTTP(t *testing.T) {
	e := setup(t, api.PlanPro)
	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "monitor-scope"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "scoped-monitor", Type: state.AppTypeApp, RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	env, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	budget := int64(500)
	anchor := time.Now().Add(-time.Hour)
	r := routemonitor.NewReport(api.RouteMonitorConfig{AppID: app.ID, Enabled: true, Revision: 1, UpdatedAt: &anchor, Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/ready", Max5xxRateBPS: &budget}}}, time.Now())
	r.DeploymentID = uuid.NewString()
	r.ObservationAnchor = &anchor
	for i := range r.Routes[0].Windows {
		r.Routes[0].Windows[i].Observed.Requests = 100
	}
	routemonitor.Evaluate(&r, "")
	reader := &scopedBudgetTestStore{MemStore: e.store, report: r}
	e.s.store = reader
	response := e.do(t, http.MethodGet, "/v1/apps/scoped-monitor/operational-summary?environment=staging", nil, nil)
	var out api.AppOperationalSummary
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil || !out.Monitoring.Available || out.Monitoring.Status != "healthy" || out.Monitoring.RouteBudgets == nil || reader.scope != "staging" {
		t.Fatalf("scoped monitor=%d %s", response.Code, response.Body)
	}
	b := out.Monitoring.RouteBudgets
	if b.Scope != "staging" || b.EnvironmentID != env.ID || b.ProjectID != project.ID || b.BudgetSource != "app_policy" || b.Report.AppID != app.ID {
		t.Fatal("scoped route evidence lost immutable identity")
	}
	if rows, err := e.store.ListInstancesForApp(t.Context(), app.ID); err != nil || len(rows) != 0 {
		t.Fatal("read-only route monitor woke an app")
	}
}

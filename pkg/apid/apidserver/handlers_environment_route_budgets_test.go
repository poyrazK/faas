// adr: 1252
package apidserver

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentRouteBudgetsHTTP(t *testing.T) {
	e := setup(t, api.PlanPro)
	p, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "budget-http"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: p.ID, Slug: "budget-http", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	env, err := e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: p.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/budget-http/route-budgets"
	for _, query := range []string{"", "?environment=staging&scope=default", "?scope=staging", "?environment=staging&environment=staging", "?environment="} {
		if r := e.do(t, http.MethodGet, path+query, nil, nil); r.Code != 400 {
			t.Fatalf("selector %s=%d %s", query, r.Code, r.Body)
		}
	}
	response := e.do(t, http.MethodGet, path+"?environment=staging", nil, nil)
	var before api.EnvironmentRouteBudgets
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &before) != nil || before.EnvironmentID != env.ID || before.BudgetSource != "app_policy" {
		t.Fatalf("read=%d %s", response.Code, response.Body)
	}
	req := api.SetEnvironmentRouteBudgetsRequest{AppID: app.ID, ProjectID: p.ID, EnvironmentID: env.ID, RequestID: uuid.NewString(), ExpectedRevision: &before.Revision, ExpectedAppRevision: &before.AppRevision, Enabled: true, Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/checkout", MaxP95MS: 300}}}
	response = e.do(t, http.MethodPut, path+"?environment=staging", req, nil)
	var saved api.EnvironmentRouteBudgets
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &saved) != nil || saved.Revision != 1 || saved.BudgetSource != "environment_policy" {
		t.Fatalf("write=%d %s", response.Code, response.Body)
	}
	if r := e.do(t, http.MethodPut, path+"?environment=staging", req, nil); r.Code != 200 {
		t.Fatalf("replay=%d %s", r.Code, r.Body)
	}
	req.RequestID = uuid.NewString()
	if r := e.do(t, http.MethodPut, path+"?environment=staging", req, nil); r.Code != 409 {
		t.Fatalf("stale=%d %s", r.Code, r.Body)
	}
	req.EnvironmentID = uuid.NewString()
	if r := e.do(t, http.MethodPut, path+"?environment=staging", req, nil); r.Code != 409 {
		t.Fatal("changed environment accepted")
	}
	req.EnvironmentID = env.ID
	if r := e.do(t, http.MethodPut, path+"?scope=default", req, nil); r.Code != 409 {
		t.Fatal("named identities used for default")
	}
	response = e.do(t, http.MethodGet, path+"?scope=default", nil, nil)
	var neighbor api.EnvironmentRouteBudgets
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &neighbor) != nil || neighbor.Revision != 0 || neighbor.Config.Enabled {
		t.Fatal("staging changed default")
	}
	if rows, err := e.store.ListDeploymentsForApp(t.Context(), app.ID, 10, 0); err != nil || len(rows) != 0 {
		t.Fatal("budget update created a deployment")
	}
}

// adr: 20261024210000
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentBudgetReceiptHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s, a := *e.Store(), *e.Acct()
	project, err := s.CreateProject(t.Context(), state.Project{AccountID: a.ID, Slug: "receipt-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(t.Context(), state.App{AccountID: a.ID, ProjectID: project.ID, Slug: "receipt-app", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: a.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.GetEnvironmentRouteBudgets(t.Context(), a.ID, app.ID, "staging")
	if err != nil {
		t.Fatal(err)
	}
	req := api.SetEnvironmentRouteBudgetsRequest{AppID: app.ID, ProjectID: project.ID, EnvironmentID: environment.ID, RequestID: uuid.NewString(), ExpectedRevision: &before.Revision, ExpectedAppRevision: &before.AppRevision, Enabled: true, Routes: []api.RouteMonitorRoute{{Method: "GET", Path: "/private-checkout", MaxP95MS: 300}}}
	saved, err := s.SetEnvironmentRouteBudgets(t.Context(), a.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAPIKey(t.Context(), a.ID, hash, "receipt-reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/receipt-app/route-budgets/receipts/" + req.RequestID
	read := e.DoAs(t, http.MethodGet, path+"?environment=staging", nil, nil, key)
	var got api.EnvironmentRouteBudgetReceipt
	expectedHash, _ := api.EnvironmentRouteBudgetRequestHash("staging", req)
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &got) != nil || got.RequestHash != expectedHash || got.EnvironmentID != environment.ID || got.Revision != 1 {
		t.Fatalf("read-only receipt = %d %s", read.Code, read.Body)
	}
	for _, excluded := range []string{"config", "routes", "enabled", "/private-checkout"} {
		if strings.Contains(read.Body.String(), excluded) {
			t.Fatalf("receipt leaked %s", excluded)
		}
	}
	for _, query := range []string{"", "?scope=staging", "?environment=staging&scope=default", "?environment=staging&environment=staging"} {
		if r := e.DoAs(t, http.MethodGet, path+query, nil, nil, key); r.Code != http.StatusBadRequest {
			t.Fatalf("selector %s = %d", query, r.Code)
		}
	}
	for _, denied := range []string{path + "?scope=default", "/v1/apps/receipt-app/route-budgets/receipts/" + uuid.NewString() + "?environment=staging"} {
		if r := e.DoAs(t, http.MethodGet, denied, nil, nil, key); r.Code != http.StatusNotFound || strings.Contains(r.Body.String(), expectedHash) {
			t.Fatalf("foreign receipt = %d %s", r.Code, r.Body)
		}
	}
	if r := e.DoAs(t, http.MethodGet, "/v1/apps/receipt-app/route-budgets/receipts/bad?environment=staging", nil, nil, key); r.Code != http.StatusBadRequest {
		t.Fatal("invalid request UUID accepted")
	}
	// Reads remain historical after later writes, and never increment revisions.
	req.RequestID, req.ExpectedRevision, req.Enabled, req.Routes = uuid.NewString(), &saved.Revision, false, []api.RouteMonitorRoute{}
	later, err := s.SetEnvironmentRouteBudgets(t.Context(), a.ID, app.ID, "staging", req)
	if err != nil {
		t.Fatal(err)
	}
	read = e.DoAs(t, http.MethodGet, path+"?environment=staging", nil, map[string]string{"Idempotency-Key": uuid.NewString()}, key)
	var original api.EnvironmentRouteBudgetReceipt
	if read.Code != http.StatusOK || json.Unmarshal(read.Body.Bytes(), &original) != nil || !reflect.DeepEqual(got, original) {
		t.Fatalf("historical receipt changed: %d %s", read.Code, read.Body)
	}
	current, err := s.GetEnvironmentRouteBudgets(t.Context(), a.ID, app.ID, "staging")
	if err != nil || current.Revision != later.Revision {
		t.Fatal("receipt read mutated policy")
	}
	// A real key for another account cannot read this app's receipt.
	other, err := s.CreateAccount(t.Context(), "other@receipt.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, otherHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateAPIKey(t.Context(), other.ID, otherHash, "other-reader", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	if r := e.DoAs(t, http.MethodGet, path+"?environment=staging", nil, nil, otherKey); r.Code != http.StatusNotFound || strings.Contains(r.Body.String(), expectedHash) {
		t.Fatalf("cross-account receipt = %d %s", r.Code, r.Body)
	}
}

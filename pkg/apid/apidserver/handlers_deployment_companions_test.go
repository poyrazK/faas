package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	apidserver "github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentCompanionsRouteIsOwnedAndSanitized(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	appID := apidserver.MustSeedApp(t, e, "owned-companions")
	deployment, err := (*e.Store()).CreateDeployment(t.Context(), state.Deployment{AppID: appID, Scope: "default", Sidecars: json.RawMessage(`[{"name":"helper","type":"sidecar","image":"registry.example.test/helper@sha256:` + strings.Repeat("a", 64) + `","env":{"TOKEN":"must-not-return"},"cmd":["must-not-return"]}]`)})
	if err != nil {
		t.Fatal(err)
	}
	response := e.Do(t, http.MethodGet, "/v1/deployments/"+deployment.ID+"/companions", nil, nil)
	var view api.DeploymentCompanionsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &view); err != nil || response.Code != 200 || view.AccountID != e.Acct().ID || view.AppID != appID || view.DeploymentID != deployment.ID || len(view.Companions) != 1 {
		t.Fatalf("owned read=%d %+v %v", response.Code, view, err)
	}
	if strings.Contains(response.Body.String(), "must-not-return") {
		t.Fatal("credential disclosure")
	}
	other, err := (*e.Store()).CreateAccount(context.Background(), "other-companions@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	token, hash, _ := api.GenerateAPIKey()
	_, err = (*e.Store()).CreateAPIKey(t.Context(), other.ID, hash, "foreign", api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}
	*e.Key() = token
	apidserver.AssertProblem(t, e.Do(t, http.MethodGet, "/v1/deployments/"+deployment.ID+"/companions", nil, nil), 404, api.CodeNotFound)
}

func TestDeploymentCompanionsRouteRetainsScopeAndMFA(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	deployment := apidserver.MustSeedDeployment(t, e, "protected-companions")
	path := "/v1/deployments/" + deployment.ID + "/companions"
	token, hash, _ := api.GenerateAPIKey()
	_, err := (*e.Store()).CreateAPIKey(t.Context(), e.Acct().ID, hash, "write-only", []string{api.ScopeSecretsWrite})
	if err != nil {
		t.Fatal(err)
	}
	*e.Key() = token
	if response := e.Do(t, http.MethodGet, path, nil, nil); response.Code != 403 {
		t.Fatalf("missing read permission=%d", response.Code)
	}
	response := httptest.NewRecorder()
	(*e.H()).ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != 401 {
		t.Fatalf("unauthenticated=%d", response.Code)
	}
	pending := apidserver.SetupWithMFA(t, api.PlanPro, true, false)
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.AddCookie(*pending.Cookie())
	response = httptest.NewRecorder()
	(*pending.H()).ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatalf("pending MFA=%d", response.Code)
	}
}

// Existing deployment GET regressions move with its domain extraction (ADR-982).
func TestGetDeployment_HappyPath(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	dep := apidserver.MustSeedDeployment(t, e, "get-dep")
	rec := e.Do(t, "GET", "/v1/deployments/"+dep.ID, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != dep.ID || out.Status != string(state.DeployBuilding) {
		t.Errorf("got %+v, want id=%s status=building", out, dep.ID)
	}
}

func TestGetDeployment_UnknownReturns404(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, "GET", "/v1/deployments/deadbeef", nil, nil)
	apidserver.AssertProblem(t, rec, 404, api.CodeNotFound)
}

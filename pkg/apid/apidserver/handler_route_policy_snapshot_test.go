package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func deploymentRoutePolicyPath(slug, deploymentID string) string {
	return "/v1/apps/" + slug + "/deployments/" + deploymentID + "/route-policy"
}

func TestGetDeploymentRoutePolicySnapshotReturnsCapturedOwnerScopedPolicy(t *testing.T) {
	e, slug, deploymentID := apidserver.OpenAPIDocHarness(t, api.PlanHobby)
	deployment, err := (*e.Store()).DeploymentByID(context.Background(), deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (*e.Store()).CreateEdgeRule(context.Background(), state.CreateEdgeRuleParams{
		AccountID: (*e.Acct()).ID, AppID: deployment.AppID, MatchHost: "api.example.com", MatchPath: "/orders/*",
		MatchMethods: []string{"GET"}, Enabled: true, Kind: state.EdgeRuleKindRoute,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute},
	}); err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(context.Background(), deploymentID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	rec := e.Do(t, http.MethodGet, deploymentRoutePolicyPath(slug, deploymentID), nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET route policy: %d %s", rec.Code, rec.Body.String())
	}
	var response api.DeploymentRoutePolicySnapshotResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.DeploymentID != deploymentID || response.AppID != deployment.AppID || response.Scope != deployment.Scope || len(response.SHA256) != 64 || response.SchemaVersion != state.DeploymentRoutePolicySnapshotSchemaVersion || len(response.Rules) != 1 || response.Rules[0].MatchPath != "/orders/*" {
		t.Fatalf("route policy response = %+v", response)
	}
	if rec.Header().Get("Cache-Control") != "private, max-age=300" {
		t.Fatalf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
}

func TestGetDeploymentRoutePolicySnapshotDistinguishesMissingAndCrossApp(t *testing.T) {
	e, slug, deploymentID := apidserver.OpenAPIDocHarness(t, api.PlanHobby)
	rec := e.Do(t, http.MethodGet, deploymentRoutePolicyPath(slug, deploymentID), nil, nil)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "deployment_route_policy_snapshot_not_found") {
		t.Fatalf("missing snapshot response = %d %s", rec.Code, rec.Body.String())
	}
	other, err := (*e.Store()).CreateApp(context.Background(), state.App{AccountID: (*e.Acct()).ID, Slug: slug + "-other", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	rec = e.Do(t, http.MethodGet, deploymentRoutePolicyPath(other.Slug, deploymentID), nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-app snapshot response = %d %s", rec.Code, rec.Body.String())
	}
}

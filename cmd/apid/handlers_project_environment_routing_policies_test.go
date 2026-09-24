// adr: 233
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentRoutingPoliciesWriteStateAndDiff(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	if _, err := store.CreateProjectEnvironment(context.Background(), state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	const path = "/v1/projects/shop/environments/staging/workloads/shop-api/routing-policies"
	const body = `{"rules":[{"kind":"redirect","match_path":"/old/*","priority":100,"enabled":true,"action":{"status_code":308,"to":"/new/$1"}},{"kind":"rewrite","match_path":"/internal/*","priority":200,"enabled":true,"action":{"from":"/internal/*","to":"/v2/$1"}}]}`
	req, rec := projectRequest(http.MethodPut, path, "shop", []byte(body))
	req.SetPathValue("environment", "staging")
	req.SetPathValue("workload", app.Slug)
	srv.updateProjectEnvironmentRoutingPolicies(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("write status=%d body=%s", rec.Code, rec.Body.String())
	}
	var policy api.ProjectEnvironmentEdgePolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Ownership != "environment" || len(policy.Rules) != 2 || policy.Rules[0].Kind != "redirect" || policy.Rules[1].Kind != "rewrite" {
		t.Fatalf("policy response = %+v", policy)
	}
	req, rec = projectRequest(http.MethodGet, "/v1/projects/shop/environments/staging/state", "shop", nil)
	req.SetPathValue("environment", "staging")
	srv.getProjectEnvironmentState(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("state status=%d body=%s", rec.Code, rec.Body.String())
	}
	var snapshot api.ProjectEnvironmentStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Workloads) != 1 || snapshot.Workloads[0].RoutingPolicies.Ownership != "environment" || len(snapshot.Workloads[0].RoutingPolicies.Rules) != 2 || snapshot.Workloads[0].Policies.Ownership != "application" {
		t.Fatalf("state policies = %+v", snapshot.Workloads)
	}
	req, rec = projectRequest(http.MethodGet, "/v1/projects/shop/environments/staging/diff?from=production", "shop", nil)
	req.SetPathValue("environment", "staging")
	srv.diffProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("diff status=%d body=%s", rec.Code, rec.Body.String())
	}
	var diff api.ProjectEnvironmentDiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Workloads) != 1 || diff.Workloads[0].RoutingPolicies.Kind != "changed" ||
		diff.Workloads[0].RoutingPolicies.Before.Ownership != "application" || diff.Workloads[0].RoutingPolicies.After.Ownership != "environment" || diff.Workloads[0].Policies.Kind != "unchanged" {
		t.Fatalf("routing policy diff = %+v", diff.Workloads)
	}
	for _, invalid := range []string{
		`{"rules":[{"kind":"headers","match_path":"/","priority":100,"enabled":true,"action":{"response_headers":[]}}]}`,
		`{"rules":[{"kind":"redirect","match_path":"/","priority":100,"enabled":true,"action":{"status_code":200,"to":"/bad"}}]}`,
	} {
		req, rec = projectRequest(http.MethodPut, path, "shop", []byte(invalid))
		req.SetPathValue("environment", "staging")
		req.SetPathValue("workload", app.Slug)
		srv.updateProjectEnvironmentRoutingPolicies(rec, req, acct)
		if rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("invalid policy status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

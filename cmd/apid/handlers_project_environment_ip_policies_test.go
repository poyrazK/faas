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

func TestProjectEnvironmentIPPoliciesWriteStateAndDiff(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	if _, err := store.CreateProjectEnvironment(context.Background(), state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	const path = "/v1/projects/shop/environments/staging/workloads/shop-api/ip-policies"
	const body = `{"rules":[{"kind":"ip","match_path":"/","priority":100,"enabled":true,"action":{"allow":["192.0.2.0/24"]}}]}`
	req, rec := projectRequest(http.MethodPut, path, "shop", []byte(body))
	req.SetPathValue("environment", "staging")
	req.SetPathValue("workload", app.Slug)
	srv.updateProjectEnvironmentIPPolicies(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("write status=%d body=%s", rec.Code, rec.Body.String())
	}
	var policy api.ProjectEnvironmentEdgePolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Ownership != "environment" || len(policy.Rules) != 1 || policy.Rules[0].Kind != "ip" {
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
	if len(snapshot.Workloads) != 1 || snapshot.Workloads[0].IPPolicies.Ownership != "environment" || len(snapshot.Workloads[0].IPPolicies.Rules) != 1 || snapshot.Workloads[0].Policies.Ownership != "application" {
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
	if len(diff.Workloads) != 1 || diff.Workloads[0].IPPolicies.Kind != "changed" ||
		diff.Workloads[0].IPPolicies.Before.Ownership != "application" || diff.Workloads[0].IPPolicies.After.Ownership != "environment" || diff.Workloads[0].Policies.Kind != "unchanged" {
		t.Fatalf("IP policy diff = %+v", diff.Workloads)
	}
	for _, invalid := range []string{
		`{"rules":[{"kind":"headers","match_path":"/","priority":100,"enabled":true,"action":{"response_headers":[]}}]}`,
		`{"rules":[{"kind":"ip","match_path":"/","priority":100,"enabled":true,"action":{"allow":["not-a-cidr"]}}]}`,
	} {
		req, rec = projectRequest(http.MethodPut, path, "shop", []byte(invalid))
		req.SetPathValue("environment", "staging")
		req.SetPathValue("workload", app.Slug)
		srv.updateProjectEnvironmentIPPolicies(rec, req, acct)
		if rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("invalid policy status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

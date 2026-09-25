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

func TestProjectEnvironmentPoliciesWriteStateAndDiff(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	if _, err := store.CreateProjectEnvironment(context.Background(), state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	const path = "/v1/projects/shop/environments/staging/workloads/shop-api/policies"
	const body = `{"rules":[{"kind":"headers","match_path":"/","priority":100,"enabled":true,"action":{"response_headers":[{"name":"X-Environment","value":"staging","action":"set"}]}}]}`
	req, rec := projectRequest(http.MethodPut, path, "shop", []byte(body))
	req.SetPathValue("environment", "staging")
	req.SetPathValue("workload", app.Slug)
	srv.updateProjectEnvironmentPolicies(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("write status=%d body=%s", rec.Code, rec.Body.String())
	}
	var policy api.ProjectEnvironmentEdgePolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &policy); err != nil {
		t.Fatal(err)
	}
	if policy.Ownership != "environment" || len(policy.Rules) != 1 || policy.Rules[0].Kind != "headers" {
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
	if len(snapshot.Workloads) != 1 || snapshot.Workloads[0].Policies.Ownership != "environment" || len(snapshot.Workloads[0].Policies.Rules) != 1 {
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
	if len(diff.Workloads) != 1 || diff.Workloads[0].Policies.Kind != "changed" ||
		diff.Workloads[0].Policies.Before.Ownership != "application" || diff.Workloads[0].Policies.After.Ownership != "environment" {
		t.Fatalf("policy diff = %+v", diff.Workloads)
	}

	for _, invalid := range []string{
		`{"rules":[{"kind":"route","match_path":"/","priority":100,"enabled":true,"action":{"target_app_slug":"shop-api"}}]}`,
		`{"rules":[{"kind":"cors","match_path":"/","priority":100,"enabled":true,"action":{"cors_preset_id":"00000000-0000-0000-0000-000000000001"}}]}`,
	} {
		req, rec = projectRequest(http.MethodPut, path, "shop", []byte(invalid))
		req.SetPathValue("environment", "staging")
		req.SetPathValue("workload", app.Slug)
		srv.updateProjectEnvironmentPolicies(rec, req, acct)
		if rec.Code < 400 || rec.Code >= 500 {
			t.Fatalf("invalid policy status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

func TestProjectEnvironmentPolicyHostsIncludeVerifiedBoundDomains(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	environment, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomainIfUnderQuota(ctx, "shared.example.test", app.ID, "shared", 10, 10); err != nil {
		t.Fatal(err)
	}
	bound, err := store.CreateCustomDomainIfUnderQuota(ctx, "stage.example.test", app.ID, "stage", 10, 10, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	hosts, err := srv.projectEnvironmentPolicyHosts(ctx, app.ID, environment.ID, "stable.gregale.dev")
	if err != nil || len(hosts) != 1 {
		t.Fatalf("unverified domain entered convergence fence: hosts=%v err=%v", hosts, err)
	}
	if err := store.MarkDomainVerified(ctx, bound.Domain); err != nil {
		t.Fatal(err)
	}
	hosts, err = srv.projectEnvironmentPolicyHosts(ctx, app.ID, environment.ID, "stable.gregale.dev")
	if err != nil || len(hosts) != 2 || hosts[0] != "stable.gregale.dev" || hosts[1] != bound.Domain {
		t.Fatalf("bound domain missing from convergence fence: hosts=%v err=%v", hosts, err)
	}
}

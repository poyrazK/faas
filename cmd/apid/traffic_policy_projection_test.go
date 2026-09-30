package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func requireTrafficProjectionProblem(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var problem api.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnprocessableEntity || problem.Code != api.CodeTrafficPolicyTooLarge ||
		api.StatusForCode(problem.Code) != response.Code ||
		problem.LimitBytes == nil || *problem.LimitBytes != api.TrafficPolicyMaxContractBytes ||
		problem.ObservedBytes == nil || *problem.ObservedBytes <= *problem.LimitBytes ||
		problem.Limit == nil || *problem.Limit != *problem.LimitBytes ||
		problem.Observed == nil || *problem.Observed != *problem.ObservedBytes || problem.DocsURL == "" {
		t.Fatalf("incomplete limit problem: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTrafficProjectionCorsCreateAndMergedPatch(t *testing.T) {
	env := setup(t, api.PlanHobby)
	req := corsPresetReq()
	req.AllowHeaders = []string{strings.Repeat("X", api.TrafficPolicyMaxContractBytes)}
	requireTrafficProjectionProblem(t, env.do(t, http.MethodPost, "/v1/cors-presets", req, nil))
	rows, err := env.store.ListCorsPresetsForAccount(context.Background(), env.acct.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rejected create persisted: rows=%d err=%v", len(rows), err)
	}
	// Each field and PATCH body is smaller than the bound; the merged object is not.
	req.AllowHeaders = []string{strings.Repeat("X", api.TrafficPolicyMaxContractBytes/2+100)}
	response := env.do(t, http.MethodPost, "/v1/cors-presets", req, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("valid create: status=%d body=%s", response.Code, response.Body.String())
	}
	var created api.CorsPresetResponse
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	path := "/v1/cors-presets/" + created.ID
	response = env.do(t, http.MethodPatch, path, map[string]any{"expose_headers": []string{strings.Repeat("Y", api.TrafficPolicyMaxContractBytes/2+100)}}, nil)
	requireTrafficProjectionProblem(t, response)
	stored, err := env.store.GetCorsPresetByID(context.Background(), env.acct.ID, created.ID)
	if err != nil || len(stored.ExposeHeaders) != 0 || stored.AllowHeaders[0] != req.AllowHeaders[0] {
		t.Fatalf("rejected merged PATCH changed saved preset: err=%v", err)
	}
	response = env.do(t, http.MethodPatch, path, map[string]any{"allow_headers": []string{"X-Repaired"}}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("smaller PATCH: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTrafficProjectionEnvironmentWritesPreserveAndRecover(t *testing.T) {
	for _, kind := range []string{"routes", "policies"} {
		t.Run(kind, func(t *testing.T) {
			srv, store, acct, _, app := newProjectLifecycleFixture(t)
			path := "/v1/projects/shop/environments/production/workloads/shop-api/" + kind
			write := func(body any) *httptest.ResponseRecorder {
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				req, response := projectRequest(http.MethodPut, path, "shop", encoded)
				req.SetPathValue("environment", "production")
				req.SetPathValue("workload", app.Slug)
				if kind == "routes" {
					srv.updateProjectEnvironmentRoutes(response, req, acct)
				} else {
					srv.updateProjectEnvironmentPolicies(response, req, acct)
				}
				return response
			}
			var empty, large any
			if kind == "routes" {
				empty = map[string]any{"only_allow_declared_routes": false, "declared_routes": []any{}}
				large = map[string]any{"only_allow_declared_routes": true, "declared_routes": []any{map[string]any{
					"path": "/" + strings.Repeat("x", api.TrafficPolicyMaxContractBytes), "methods": []string{"GET"},
				}}}
			} else {
				empty = map[string]any{"rules": []any{}}
				large = map[string]any{"rules": []any{map[string]any{
					"kind": "cors", "match_path": "/", "enabled": true, "priority": 100,
					"action": map[string]any{"allow_origins": []string{"*"}, "allow_methods": []string{"GET"},
						"allow_headers": []string{strings.Repeat("X", api.TrafficPolicyMaxContractBytes)}},
				}}}
			}
			if response := write(empty); response.Code != http.StatusOK {
				t.Fatalf("initial write: status=%d body=%s", response.Code, response.Body.String())
			}
			requireTrafficProjectionProblem(t, write(large))
			if kind == "routes" {
				policy, err := store.GetProjectEnvironmentRoutePolicy(context.Background(), acct.ID, app.ID, "production")
				if err != nil || policy.OnlyAllowDeclaredRoutes || len(policy.DeclaredRoutes) != 0 {
					t.Fatalf("rejected contract changed saved state: err=%v", err)
				}
			} else {
				policy, err := store.GetProjectEnvironmentEdgePolicy(context.Background(), acct.ID, app.ID, "production")
				if err != nil || len(policy.Rules) != 0 {
					t.Fatalf("rejected overlay changed saved state: err=%v", err)
				}
			}
			if response := write(empty); response.Code != http.StatusOK {
				t.Fatalf("empty replacement recovery: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
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

func TestTrafficProjectionImportReplacementAtFullQuota(t *testing.T) {
	env, _ := newTestServerWithCapturingNotifier(t, api.PlanHobby)
	var target state.App
	for i := range env.acct.Plan.OpenAPIImportsPerAccount() {
		app := seedApp(t, env, fmt.Sprintf("full-import-%d", i))
		seedImport(t, env, app.ID, []byte(sampleOpenAPIDoc), 1, "3.1.0")
		if i == 0 {
			target = app
		}
	}
	if target.ID == "" {
		t.Fatal("fixture requires an enabled import tier")
	}
	path := "/v1/apps/" + target.Slug + "/openapi"
	replacement := strings.Replace(sampleOpenAPIDoc, `"title":"sample"`, `"title":"repaired"`, 1)
	response := env.do(t, http.MethodPost, path, json.RawMessage(replacement), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("replacement at full quota: status=%d body=%s", response.Code, response.Body.String())
	}
	oversized := []byte(`{"openapi":"3.1.0","info":{"title":"expansion","version":"1"},"paths":{},"x-values":[` + strings.Repeat("1e300,", 1799) + `1e300]}`)
	if len(oversized) >= state.OpenAPIImportMaxDocBytes {
		t.Fatal("fixture must fit the request body cap")
	}
	response = env.do(t, http.MethodPost, path, json.RawMessage(oversized), nil)
	requireTrafficProjectionProblem(t, response)
	doc, _, err := env.store.GetAppOpenAPIDoc(context.Background(), target.ID, env.acct.ID)
	if err != nil || string(doc) != replacement {
		t.Fatalf("rejected import changed saved document: err=%v", err)
	}
	// A new app cannot borrow the existing document's replacement slot.
	other := seedApp(t, env, "full-import-extra")
	response = env.do(t, http.MethodPost, "/v1/apps/"+other.Slug+"/openapi", json.RawMessage(sampleOpenAPIDoc), nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("new import at full quota: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTrafficProjectionCloneRejectsFallbackWithoutCreatingTarget(t *testing.T) {
	srv, store, account, project, _ := newProjectLifecycleFixture(t)
	_, err := store.CreateApp(context.Background(), state.App{AccountID: account.ID, ProjectID: project.ID,
		Slug: "oversized-fallback", Status: state.AppActive, OnlyAllowDeclaredRoutes: true,
		DeclaredRoutes: []state.DeclaredRoute{{Path: "/" + strings.Repeat("x", api.TrafficPolicyMaxContractBytes), Methods: []string{"GET"}}}})
	if err != nil {
		t.Fatal(err)
	}
	req, response := projectRequest(http.MethodPost, "/v1/projects/shop/environments", "shop",
		[]byte(`{"slug":"staging","from_environment":"production"}`))
	srv.createProjectEnvironment(response, req, account)
	requireTrafficProjectionProblem(t, response)
	if _, err := store.ProjectEnvironmentBySlug(context.Background(), account.ID, project.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refused clone created target: %v", err)
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

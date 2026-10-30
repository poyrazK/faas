package apidserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 233
func TestProjectEnvironmentRoutesWriteCloneAndDiff(t *testing.T) {
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()

	req, rec := apidserver.ProjectRequest(http.MethodPut, "/v1/projects/shop/environments/production/workloads/shop-api/routes", "shop",
		[]byte(`{"only_allow_declared_routes":true,"declared_routes":[{"path":"/health","methods":["get"]}]}`))
	req.SetPathValue("environment", "production")
	req.SetPathValue("workload", app.Slug)
	srv.UpdateProjectEnvironmentRoutes(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("put routes: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var written api.ProjectEnvironmentRoutePolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &written); err != nil || written.Ownership != "environment" || !written.OnlyAllowDeclaredRoutes || len(written.DeclaredRoutes) != 1 || written.DeclaredRoutes[0].Methods[0] != "GET" {
		t.Fatalf("written routes=%+v err=%v", written, err)
	}

	req, rec = apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environments", "shop", []byte(`{"slug":"staging","from_environment":"production"}`))
	srv.CreateProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("clone: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var cloned api.ProjectEnvironmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cloned); err != nil || cloned.Clone == nil || cloned.Clone.RoutesCopied != 1 {
		t.Fatalf("clone=%+v err=%v", cloned, err)
	}
	policy, err := store.GetProjectEnvironmentRoutePolicy(ctx, acct.ID, app.ID, "staging")
	if err != nil || !policy.OnlyAllowDeclaredRoutes || len(policy.DeclaredRoutes) != 1 || policy.DeclaredRoutes[0].Path != "/health" {
		t.Fatalf("cloned policy=%+v err=%v", policy, err)
	}

	req, rec = apidserver.ProjectRequest(http.MethodPut, "/v1/projects/shop/environments/staging/workloads/shop-api/routes", "shop",
		[]byte(`{"only_allow_declared_routes":true,"declared_routes":[{"path":"/staging","methods":["POST"]}]}`))
	req.SetPathValue("environment", "staging")
	req.SetPathValue("workload", app.Slug)
	srv.UpdateProjectEnvironmentRoutes(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("update staging routes: status=%d body=%s", rec.Code, rec.Body.String())
	}
	req, rec = apidserver.ProjectRequest(http.MethodGet, "/v1/projects/shop/environments/staging/diff?from=production", "shop", nil)
	req.SetPathValue("environment", "staging")
	srv.DiffProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusOK {
		t.Fatalf("diff: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var diff api.ProjectEnvironmentDiffResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &diff); err != nil || len(diff.Workloads) != 1 || diff.Workloads[0].Routes.Kind != "changed" || diff.Workloads[0].Routes.After.DeclaredRoutes[0].Path != "/staging" {
		t.Fatalf("route diff=%+v err=%v", diff, err)
	}
	for _, resource := range diff.SharedResources {
		if resource.Kind == "routes" {
			t.Fatalf("environment-owned routes reported as shared: %+v", diff.SharedResources)
		}
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
}

func TestProjectEnvironmentRoutesRejectInvalidAndCrossProject(t *testing.T) {
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"invalid method", `{"only_allow_declared_routes":true,"declared_routes":[{"path":"/health","methods":["BAD"]}]}`, http.StatusUnprocessableEntity},
		{"missing fields", `{"declared_routes":[]}`, http.StatusBadRequest},
		{"shared OpenAPI fallback", `{"only_allow_declared_routes":true,"declared_routes":[]}`, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, rec := apidserver.ProjectRequest(http.MethodPut, "/v1/projects/shop/environments/production/workloads/shop-api/routes", "shop", []byte(tc.body))
			req.SetPathValue("environment", "production")
			req.SetPathValue("workload", app.Slug)
			srv.UpdateProjectEnvironmentRoutes(rec, req, acct)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), tc.status)
			}
		})
	}
	if _, err := store.GetProjectEnvironmentRoutePolicy(ctx, acct.ID, app.ID, "production"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("invalid route persisted: %v", err)
	}
	other, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "other", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == project.ID {
		t.Fatal("fixture project IDs collided")
	}
	req, rec := apidserver.ProjectRequest(http.MethodPut, "/v1/projects/other/environments/production/workloads/shop-api/routes", "other",
		[]byte(`{"only_allow_declared_routes":false,"declared_routes":[]}`))
	req.SetPathValue("environment", "production")
	req.SetPathValue("workload", app.Slug)
	srv.UpdateProjectEnvironmentRoutes(rec, req, acct)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project write: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestProjectEnvironmentRoutesProtectedStageRequiresPlanOrPromotion(t *testing.T) {
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "release", Protected: true}); err != nil {
		t.Fatal(err)
	}
	put := func(environment string) int {
		t.Helper()
		req, rec := apidserver.ProjectRequest(http.MethodPut, "/v1/projects/shop/environments/"+environment+"/workloads/shop-api/routes", "shop",
			[]byte(`{"only_allow_declared_routes":true,"declared_routes":[{"path":"/health","methods":["GET"]}]}`))
		req.SetPathValue("environment", environment)
		req.SetPathValue("workload", app.Slug)
		srv.UpdateProjectEnvironmentRoutes(rec, req, acct)
		return rec.Code
	}
	if code := put("release"); code != http.StatusConflict {
		t.Fatalf("protected stage route write = %d, want 409", code)
	}
	if _, err := store.GetProjectEnvironmentRoutePolicy(ctx, acct.ID, app.ID, "release"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refused route write persisted: %v", err)
	}
	// Production is protected too, but keeps its direct route contract edit.
	production, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "production")
	if err != nil || !production.Protected {
		t.Fatalf("fixture production should be protected: %+v, %v", production, err)
	}
	if code := put("production"); code != http.StatusOK {
		t.Fatalf("production route write = %d, want 200", code)
	}
}

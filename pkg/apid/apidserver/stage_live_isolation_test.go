// adr: 590
package apidserver_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A protected stage takes its headers/CORS policy from an approved plan or
// promotion, like its routes; production keeps the direct edit (ADR-233).
func TestProjectEnvironmentPoliciesProtectedStageRequiresPlanOrPromotion(t *testing.T) {
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	if _, err := store.CreateProjectEnvironment(context.Background(), state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "release", Protected: true,
	}); err != nil {
		t.Fatal(err)
	}
	put := func(environment string) int {
		t.Helper()
		const body = `{"rules":[{"kind":"headers","match_path":"/","priority":100,"enabled":true,"action":{"response_headers":[{"name":"X-Environment","value":"x","action":"set"}]}}]}`
		req, rec := apidserver.ProjectRequest(http.MethodPut, "/v1/projects/shop/environments/"+environment+"/workloads/shop-api/policies", "shop", []byte(body))
		req.SetPathValue("environment", environment)
		req.SetPathValue("workload", app.Slug)
		srv.UpdateProjectEnvironmentPolicies(rec, req, acct)
		return rec.Code
	}
	if code := put("release"); code != http.StatusConflict {
		t.Fatalf("protected stage policy write = %d, want 409", code)
	}
	if code := put("production"); code != http.StatusOK {
		t.Fatalf("production policy write = %d, want 200", code)
	}
}

// Deleting a project cascades its environments without the cleanup that
// environment deletion runs, so a project with stages is refused until they
// are deleted.
func TestDeleteProjectRefusesWhileStagesRemain(t *testing.T) {
	srv, store, acct, project, _ := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	remove := func() (int, string) {
		t.Helper()
		req, rec := apidserver.ProjectRequest(http.MethodDelete, "/v1/projects/shop", "shop", nil)
		srv.DeleteProjectForTest(rec, req, acct)
		return rec.Code, rec.Body.String()
	}
	if code, body := remove(); code != http.StatusConflict || !strings.Contains(body, "staging") {
		t.Fatalf("delete with a stage = %d %s, want 409 naming staging", code, body)
	}
	if _, err := store.ProjectBySlug(ctx, acct.ID, "shop"); err != nil {
		t.Fatalf("refused delete removed the project: %v", err)
	}
	if err := store.DeleteProjectEnvironment(ctx, acct.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if code, body := remove(); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("delete without stages = %d %s", code, body)
	}
	if _, err := store.ProjectBySlug(ctx, acct.ID, "shop"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("project survived its delete: %v", err)
	}
}

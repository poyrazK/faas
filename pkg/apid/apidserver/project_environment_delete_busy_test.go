package apidserver_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectEnvironmentDeletionRefusesQueuedDeployment(t *testing.T) {
	srv, store, account, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := t.Context()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	request, recorder := apidserver.ProjectRequest(http.MethodDelete, "/v1/projects/shop/environments/staging", project.Slug, nil)
	request.SetPathValue("environment", "staging")
	srv.DeleteProjectEnvironment(recorder, request, account)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "in-progress deployments") {
		t.Fatalf("delete queued environment: %d %s", recorder.Code, recorder.Body.String())
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployCancelled, "cancelled before deletion"); err != nil {
		t.Fatal(err)
	}
	request, recorder = apidserver.ProjectRequest(http.MethodDelete, "/v1/projects/shop/environments/staging", project.Slug, nil)
	request.SetPathValue("environment", "staging")
	srv.DeleteProjectEnvironment(recorder, request, account)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete after cancellation: %d %s", recorder.Code, recorder.Body.String())
	}
}

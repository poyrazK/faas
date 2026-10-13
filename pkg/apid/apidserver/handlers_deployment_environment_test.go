package apidserver_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestApplyDeploymentEnvironmentResolvesRegisteredProjectEnvironment(t *testing.T) {
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	if _, err := store.CreateProjectEnvironment(context.Background(), state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}

	req := &api.CreateDeploymentRequest{Environment: "staging"}
	if problem := srv.ApplyDeploymentEnvironment(context.Background(), acct, app, req); problem != nil {
		t.Fatalf("applyDeploymentEnvironment: %v", problem)
	}
	if req.Scope != "staging" {
		t.Fatalf("resolved scope = %q, want staging", req.Scope)
	}
}

func TestApplyDeploymentEnvironmentRejectsUnknownOrStandaloneTarget(t *testing.T) {
	srv, store, acct, _, projectApp := apidserver.NewProjectLifecycleFixture(t)

	unknown := &api.CreateDeploymentRequest{Environment: "staging"}
	if problem := srv.ApplyDeploymentEnvironment(context.Background(), acct, projectApp, unknown); problem == nil || problem.Status != 404 {
		t.Fatalf("unknown environment problem = %+v, want 404", problem)
	}

	standalone, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "standalone", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	request := &api.CreateDeploymentRequest{Environment: "production"}
	if problem := srv.ApplyDeploymentEnvironment(context.Background(), acct, standalone, request); problem == nil || problem.Status != 400 {
		t.Fatalf("standalone environment problem = %+v, want 400", problem)
	}
}

func TestValidateProjectDeploymentEnvironmentUsesProjectRegistry(t *testing.T) {
	srv, store, acct, project, _ := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	if problem := srv.ValidateProjectDeploymentEnvironment(ctx, acct, project.Slug, "staging"); problem != nil {
		t.Fatalf("registered environment rejected: %v", problem)
	}
	if problem := srv.ValidateProjectDeploymentEnvironment(ctx, acct, project.Slug, "testing"); problem == nil || problem.Status != 404 {
		t.Fatalf("unknown environment problem = %+v, want 404", problem)
	}
	if problem := srv.ValidateProjectDeploymentEnvironment(ctx, acct, "new-project", "production"); problem != nil {
		t.Fatalf("seeded production environment for new project rejected: %v", problem)
	}
}

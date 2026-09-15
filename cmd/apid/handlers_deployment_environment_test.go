package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestApplyDeploymentEnvironmentResolvesRegisteredProjectEnvironment(t *testing.T) {
	srv, store, acct, project, app := newProjectLifecycleFixture(t)
	if _, err := store.CreateProjectEnvironment(context.Background(), state.ProjectEnvironment{
		AccountID: acct.ID, ProjectID: project.ID, Slug: "staging",
	}); err != nil {
		t.Fatal(err)
	}

	req := &api.CreateDeploymentRequest{Environment: "staging"}
	if problem := srv.applyDeploymentEnvironment(context.Background(), acct, app, req); problem != nil {
		t.Fatalf("applyDeploymentEnvironment: %v", problem)
	}
	if req.Scope != "staging" {
		t.Fatalf("resolved scope = %q, want staging", req.Scope)
	}
}

func TestApplyDeploymentEnvironmentRejectsUnknownOrStandaloneTarget(t *testing.T) {
	srv, store, acct, _, projectApp := newProjectLifecycleFixture(t)

	unknown := &api.CreateDeploymentRequest{Environment: "staging"}
	if problem := srv.applyDeploymentEnvironment(context.Background(), acct, projectApp, unknown); problem == nil || problem.Status != 404 {
		t.Fatalf("unknown environment problem = %+v, want 404", problem)
	}

	standalone, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "standalone", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	request := &api.CreateDeploymentRequest{Environment: "production"}
	if problem := srv.applyDeploymentEnvironment(context.Background(), acct, standalone, request); problem == nil || problem.Status != 400 {
		t.Fatalf("standalone environment problem = %+v, want 400", problem)
	}
}

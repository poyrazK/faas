package apidserver_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ChronoLake finding 25.2: after a top-level `deploy --project`, production
// is served by the default-scope deployment (named production is dark), but
// the production environment reported "not_deployed" and no bindings.
func TestProductionEnvironmentStateReportsLegacyDefaultLane(t *testing.T) {
	srv, store, acct, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := context.Background()
	live, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Scope: state.DefaultEnvScope, Status: state.DeployLive, TrafficPercent: 100,
		ImageDigest: "sha256:top-level", CommitSHA: "top-level-sha",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutManagedPostgresSecret(ctx, state.AppSecret{
		AccountID: acct.ID, AppID: app.ID, Scope: state.DefaultEnvScope, Key: "DATABASE_URL",
		Ciphertext: []byte("sealed-database"), ValueHash: "3333333333333333",
		ManagedPostgresBindingID: "binding-default", ManagedPostgresAccess: "read_write", ManagedCredentialRef: "credential-default",
		ManagedCredentialGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, problem := srv.LoadProjectEnvironmentState(ctx, acct, project.Slug, "production")
	if problem != nil || len(snapshot.Workloads) != 1 {
		t.Fatalf("state: %+v %v", snapshot, problem)
	}
	workload := snapshot.Workloads[0]
	if workload.Release.Status != "live" || workload.Release.DeploymentID != live.ID {
		t.Fatalf("production release = %+v, want the live top-level deployment %s", workload.Release, live.ID)
	}
	if len(workload.Bindings) != 1 || workload.Bindings[0].BindingID != "binding-default" {
		t.Fatalf("production bindings = %+v, want the default-scope binding", workload.Bindings)
	}

	// A staging environment is its own lane and stays not deployed.
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	staging, problem := srv.LoadProjectEnvironmentState(ctx, acct, project.Slug, "staging")
	if problem != nil || len(staging.Workloads) != 1 || staging.Workloads[0].Release.Status != "not_deployed" || len(staging.Workloads[0].Bindings) != 0 {
		t.Fatalf("staging borrowed the default lane: %+v %v", staging, problem)
	}
}

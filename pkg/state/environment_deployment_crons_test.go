package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentDeploymentCronsMemory(t *testing.T) {
	testEnvironmentDeploymentCrons(t, state.NewMemStore())
}

// ADR-20261104120000: `gregale deploy --environment staging` stages the
// manifest's HTTP crons into staging. The intent waits for staging's own
// deployment to be live with all of staging's traffic, then replaces only
// staging's manifest crons; production's crons and staging state are
// untouched, and the legacy immediate journal takes no environment.
func testEnvironmentDeploymentCrons(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "env-manifest-crons-"+uuid.NewString()[:8]+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "env-manifest-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "env-manifest-" + uuid.NewString()[:8], Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	production, err := store.CreateCron(ctx, app.ID, "* * * * *", "/prod", true)
	if err != nil {
		t.Fatal(err)
	}
	staging := store.(state.DeploymentCronStagingStore)
	environments := store.(state.EnvironmentDeploymentCronStagingStore)
	snapshot, err := environments.EnvironmentDeploymentCronStagingSnapshot(ctx, acct.ID, app.ID, "staging")
	if err != nil || snapshot.Environment != "staging" || len(snapshot.Crons) != 0 || snapshot.Revision != 0 || snapshot.LatestDeploymentID != "" {
		t.Fatalf("staging snapshot = %+v, %v", snapshot, err)
	}
	desired := []api.DeploymentCronConfig{{ID: uuid.NewString(), Schedule: "*/5 * * * *", Path: "/stage", Enabled: true, Timezone: "UTC"}}
	p := state.DeploymentCronStagingParams{AccountID: acct.ID, Limits: api.MustLimitsFor(api.PlanPro), Request: api.DeploymentCronStagingRequest{
		SessionID: strings.Repeat("b", 32), Operation: "prepare", ExpectedAppID: app.ID, ExpectedRevision: 0,
		Before: []api.DeploymentCronConfig{}, Desired: desired, Environment: "staging"}}
	legacy := p
	legacy.Request.Operation = "stage"
	if _, err = staging.ApplyDeploymentCronStaging(ctx, app.ID, legacy); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("immediate journal into an environment: err = %v", err)
	}
	result, err := staging.ApplyDeploymentCronStaging(ctx, app.ID, p)
	if err != nil || result.Receipt.Status != "prepared" || result.Receipt.Revision != 1 {
		t.Fatalf("prepare = %+v, %v", result, err)
	}

	worker := store.(state.DeploymentCronActivationStore)
	dep, err := store.CreateDeployment(state.ContextWithDeploymentSubmission(ctx, p.Request.SessionID),
		state.Deployment{AppID: app.ID, Scope: "staging", Kind: state.DeploymentKindImage, Status: state.DeployPending, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if changes, err := worker.ReconcileDeploymentCrons(ctx); err != nil || len(changes) != 0 {
		t.Fatalf("activation before staging is live = %v, %v", changes, err)
	}
	if err = store.UpdateDeploymentStatus(ctx, dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if changes, err := worker.ReconcileDeploymentCrons(ctx); err != nil || len(changes) != 1 || changes[0].AppID != app.ID {
		t.Fatalf("activation once staging is live = %v, %v", changes, err)
	}

	snapshot, err = environments.EnvironmentDeploymentCronStagingSnapshot(ctx, acct.ID, app.ID, "staging")
	if err != nil || !api.DeploymentCronsEqual(snapshot.Crons, desired) || snapshot.Revision != 2 || snapshot.LatestDeploymentID != dep.ID {
		t.Fatalf("staging after activation = %+v, %v", snapshot, err)
	}
	stageCron, err := store.CronByID(ctx, desired[0].ID)
	if err != nil || stageCron.DeploymentScope != "staging" {
		t.Fatalf("activated cron = %+v, %v; want a staging cron", stageCron, err)
	}
	prod, err := staging.DeploymentCronStagingSnapshot(ctx, acct.ID, app.ID)
	// Production's revision is the app's deploy concurrency token and moves
	// with any deployment; its crons must not.
	if err != nil || len(prod.Crons) != 1 || prod.Crons[0].ID != production.ID {
		t.Fatalf("production after staging activation = %+v, %v", prod, err)
	}
}

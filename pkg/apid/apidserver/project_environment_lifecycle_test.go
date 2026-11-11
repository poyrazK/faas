package apidserver_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectCloneIgnoresUnrelatedStageServices(t *testing.T) {
	srv, store, account, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := t.Context()
	ram := 256
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	var err error
	app, err = store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &ram, Manifest: &manifest})
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, ImageDigest: "sha256:clone-membership"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+d.ID, "apps/"+d.ID, 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 300, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: d.ID}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "qa"}); err != nil {
		t.Fatal(err)
	}
	extra, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-experiment", WorkloadName: "experiment", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(extra)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentWorkloadSpec(ctx, account.ID, project.ID, "qa", extra.ID, 0, settings); err != nil {
		t.Fatal(err)
	}
	request, recorder := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environments", project.Slug, []byte(`{"slug":"staging","from_environment":"production"}`))
	srv.CreateProjectEnvironment(recorder, request, account)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("clone production with QA-only service: %d %s", recorder.Code, recorder.Body.String())
	}
	ids, err := store.ProjectEnvironmentAppIDs(ctx, account.ID, project.ID, "staging")
	if err != nil || len(ids) != 1 || ids[0] != app.ID {
		t.Fatalf("cloned membership: %v %v", ids, err)
	}
}

func TestProjectEnvironmentRecreationWaitsForCleanup(t *testing.T) {
	srv, store, account, project, app := apidserver.NewProjectLifecycleFixture(t)
	ctx := t.Context()
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	resources := state.ProjectEnvironmentCleanupResources{Postgres: []state.ProjectEnvironmentPostgresCleanupResource{{AppID: app.ID, Scope: "staging", BindingID: uuid.NewString(), DatabaseID: uuid.NewString()}}}
	job, err := store.DeleteProjectEnvironmentWithCleanup(ctx, account.ID, project.ID, "staging", resources, "request", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	request, recorder := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environments", project.Slug, []byte(`{"slug":"staging"}`))
	srv.CreateProjectEnvironment(recorder, request, account)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "environment_cleanup_pending") {
		t.Fatalf("recreated while cleanup pending: %d %s", recorder.Code, recorder.Body.String())
	}
	if err := store.CompleteProjectEnvironmentCleanup(ctx, job.ID, job.LeaseToken); err != nil {
		t.Fatal(err)
	}
	request, recorder = apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environments", project.Slug, []byte(`{"slug":"staging"}`))
	srv.CreateProjectEnvironment(recorder, request, account)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create after cleanup: %d %s", recorder.Code, recorder.Body.String())
	}
}

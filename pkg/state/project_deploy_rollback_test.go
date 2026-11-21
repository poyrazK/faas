package state_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemProjectDeployRollbackMembership(t *testing.T) {
	testProjectDeployRollbackMembership(t, state.NewMemStore())
}

func TestPgProjectDeployRollbackMembership(t *testing.T) {
	store, _ := pgStore(t)
	testProjectDeployRollbackMembership(t, store)
}

func testProjectDeployRollbackMembership(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	account, err := store.CreateAccount(ctx, "rollback-members-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "rollback-members-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	makeApp := func(slug string) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: slug + "-" + suffix, WorkloadName: slug,
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	deploy := func(app state.App, group string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + uuid.NewString(), ProjectDeployGroupID: group, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	web, worker, other := makeApp("rb-web"), makeApp("rb-worker"), makeApp("rb-other")
	// The opens and replacements all happen inside the old 15-minute window.
	initial := uuid.NewString()
	oldWeb := deploy(web, initial)
	oldWorker := deploy(worker, initial)
	deploy(other, initial)
	current := uuid.NewString()
	newWeb, newWorker := deploy(web, current), deploy(worker, current)
	deploy(other, "")

	reader := store.(state.ProjectDeployRollbackStore)
	batch, err := reader.LatestProjectRollbackBatch(ctx, account.ID, project.ID)
	if err != nil || batch == nil || batch.ID != current || len(batch.Members) != 2 {
		t.Fatalf("batch = %+v, err = %v", batch, err)
	}
	expected := map[string]string{newWeb.ID: oldWeb.ID, newWorker.ID: oldWorker.ID}
	for _, member := range batch.Members {
		if expected[member.DeploymentID] == "" || member.PredecessorID != expected[member.DeploymentID] {
			t.Fatalf("unexpected membership or lineage %+v", member)
		}
		delete(expected, member.DeploymentID)
	}
	if batch, err := reader.LatestProjectRollbackBatch(ctx, uuid.NewString(), project.ID); err != nil || batch != nil {
		t.Fatalf("cross-account batch = %+v, err = %v", batch, err)
	}
}

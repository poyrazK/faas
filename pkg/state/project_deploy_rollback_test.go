package state_test

import (
	"errors"
	"testing"
	"time"

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
		id := uuid.NewString()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + id, ProjectDeployGroupID: group, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/layers/"+id, "layers/"+id, 4096); err != nil {
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

	preflight := store.(state.CheckedRollbackPreflightStore)
	if err := preflight.CheckRollbackPreflight(ctx, account.ID, web.ID, oldWeb.ID, newWeb.ID); err != nil {
		t.Fatalf("ready rollback pair: %v", err)
	}
	if err := preflight.CheckRollbackPreflight(ctx, account.ID, web.ID, oldWeb.ID, newWorker.ID); !errors.Is(err, state.ErrCheckedRollbackChanged) {
		t.Fatalf("wrong current deployment: %v", err)
	}

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
	targetStore := store.(state.ProjectRollbackTargetsStore)
	prior, err := targetStore.ProjectRollbackBatchByID(ctx, account.ID, project.ID, initial)
	if err != nil || prior == nil || prior.ID != initial || len(prior.Members) != 3 {
		t.Fatalf("historical batch = %+v, err = %v", prior, err)
	}
	for _, member := range prior.Members {
		if !member.Restorable {
			t.Fatalf("previously serving member is unavailable: %+v", member)
		}
	}
	neverServed := uuid.NewString()
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: other.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + uuid.NewString(),
		ProjectDeployGroupID: neverServed, TrafficPercent: 100,
	}); err != nil {
		t.Fatal(err)
	}
	targets, err := targetStore.ListProjectRollbackTargets(ctx, account.ID, project.ID)
	if err != nil || len(targets) != 2 || targets[0].DeployGroupID != current || targets[0].ServiceCount != 2 ||
		targets[1].DeployGroupID != initial || targets[1].ServiceCount != 3 {
		t.Fatalf("list only served targets in descending order: %+v, %v", targets, err)
	}
	if batch, err := targetStore.ProjectRollbackBatchByID(ctx, uuid.NewString(), project.ID, initial); err != nil || batch != nil {
		t.Fatalf("cross-account historical batch = %+v, err = %v", batch, err)
	}
	if targets, err := targetStore.ListProjectRollbackTargets(ctx, uuid.NewString(), project.ID); err != nil || len(targets) != 0 {
		t.Fatalf("cross-account rollback targets = %+v, err = %v", targets, err)
	}

	operations := store.(state.ProjectRollbackOperationStore)
	childStore := store.(state.CheckedRollbackStore)
	serving, err := store.DeploymentByID(ctx, newWeb.ID)
	if err != nil {
		t.Fatal(err)
	}
	step := api.ProjectRollbackStep{AppID: web.ID, Scope: api.DefaultEnvScope, CurrentID: newWeb.ID,
		TargetID: oldWeb.ID, CurrentServedAt: serving.RolloutCompletedAt, AttemptID: uuid.NewString(), Status: "planned"}
	record := state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
		ID: uuid.NewString(), ProjectID: project.ID, Status: "running", Steps: []api.ProjectRollbackStep{step},
	}}
	if err := operations.CreateProjectRollback(ctx, account.ID, record); err != nil {
		t.Fatal(err)
	}
	competing := record
	competing.ID = uuid.NewString()
	if err := operations.CreateProjectRollback(ctx, account.ID, competing); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("competing operation: %v", err)
	}
	if _, err := operations.GetProjectRollback(ctx, uuid.NewString(), project.ID, record.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account operation: %v", err)
	}
	expired, err := operations.ClaimProjectRollback(ctx, -time.Second)
	if err != nil {
		t.Fatal(err)
	}
	currentClaim, err := operations.ClaimProjectRollback(ctx, time.Minute)
	if err != nil || currentClaim.ID != record.ID || currentClaim.Version <= expired.Version {
		t.Fatalf("replacement claim: %+v, %v", currentClaim, err)
	}
	if err := operations.SaveProjectRollback(ctx, expired, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired parent update: %v", err)
	}
	enter := func(claim state.ProjectRollbackRecord) (api.RollbackOperation, error) {
		return childStore.CreateCheckedRollback(state.WithProjectRollbackStep(ctx, claim, step), account.ID, web.ID, oldWeb.ID, newWeb.ID, "")
	}
	if _, err := enter(expired); !errors.Is(err, state.ErrCheckedRollbackChanged) {
		t.Fatalf("expired child admission: %v", err)
	}
	child, err := enter(currentClaim)
	if err != nil || child.ID != step.AttemptID {
		t.Fatalf("child admission: %+v, %v", child, err)
	}
	recovered, err := childStore.GetCheckedRollback(ctx, account.ID, web.ID, step.AttemptID)
	if err != nil || recovered.ID != child.ID {
		t.Fatalf("recovered child: %+v, %v", recovered, err)
	}
	currentClaim.Status, currentClaim.Steps[0].ReceiptID = "blocked", child.ID
	if err := operations.SaveProjectRollback(ctx, currentClaim, 0); err != nil {
		t.Fatal(err)
	}
	paused, err := operations.GetProjectRollback(ctx, account.ID, project.ID, record.ID)
	if err != nil || paused.Status != "blocked" || paused.Steps[0].ReceiptID != child.ID {
		t.Fatalf("saved parent receipt: %+v, %v", paused, err)
	}
	paused.Status = "running"
	if err := operations.ResumeProjectRollback(ctx, account.ID, paused); err != nil {
		t.Fatal(err)
	}
}

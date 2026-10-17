package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPendingDeploymentCronsMemory(t *testing.T) {
	testPendingDeploymentCrons(t, state.NewMemStore())
}

func testPendingDeploymentCrons(t *testing.T, store state.Store) {
	app, p, _ := cronStageFixture(t, store)
	p.Request.Operation = "prepare"
	staging := store.(state.DeploymentCronStagingStore)
	reader := store.(state.PendingDeploymentCronStore)
	if _, err := staging.ApplyDeploymentCronStaging(t.Context(), app.ID, p); err != nil {
		t.Fatal(err)
	}
	pending, err := reader.PendingDeploymentCrons(t.Context(), app.AccountID, app.ID)
	if err != nil || pending != nil {
		t.Fatalf("unaccepted=%+v %v", pending, err)
	}
	ctx := state.ContextWithDeploymentSubmission(t.Context(), p.Request.SessionID)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	pending, err = reader.PendingDeploymentCrons(t.Context(), app.AccountID, app.ID)
	if err != nil || pending == nil || pending.Deployment.ID != dep.ID || pending.Intent.SessionID != p.Request.SessionID || pending.Revision != p.Request.ExpectedRevision+1 {
		t.Fatalf("accepted=%+v %v", pending, err)
	}
	if _, err := reader.PendingDeploymentCrons(t.Context(), "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", app.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ownership=%v", err)
	}
	if err := store.UpdateDeploymentStatus(t.Context(), dep.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.DeploymentCronActivationStore).ReconcileDeploymentCrons(t.Context()); err != nil {
		t.Fatal(err)
	}
	pending, err = reader.PendingDeploymentCrons(t.Context(), app.AccountID, app.ID)
	if err != nil || pending != nil {
		t.Fatalf("completed=%+v %v", pending, err)
	}
}

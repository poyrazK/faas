// adr: 20261020085706 — amendment 10: a rollback target that never served.
package conformance

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

// testDeploymentServedBefore pins that a deployment counts as served once it
// took traffic, including after it was superseded, and that a release
// superseded before it ever served does not.
func testDeploymentServedBefore(t *testing.T, fx *Fixture) {
	history, ok := fx.Store.(state.DeploymentServingHistoryStore)
	if !ok {
		t.Skip("store has no deployment serving history")
	}
	served := func(id string) bool {
		t.Helper()
		got, err := history.DeploymentServedBefore(fx.Ctx, id)
		if err != nil {
			t.Fatalf("DeploymentServedBefore(%s): %v", id, err)
		}
		return got
	}
	create := func() state.Deployment {
		t.Helper()
		d, err := fx.Store.CreateDeployment(fx.Ctx, state.Deployment{AppID: fx.App.ID, Kind: state.DeploymentKindImage,
			Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatalf("CreateDeployment: %v", err)
		}
		return d
	}

	servedOne := create()
	if served(servedOne.ID) {
		t.Fatal("a pending deployment has not served")
	}
	if err := fx.Store.MarkDeploymentLive(fx.Ctx, servedOne.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	if !served(servedOne.ID) {
		t.Fatal("a live deployment taking traffic serves")
	}
	if err := fx.Store.UpdateDeploymentStatus(fx.Ctx, servedOne.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatalf("supersede served deployment: %v", err)
	}
	if !served(servedOne.ID) {
		t.Fatal("a superseded deployment that took traffic served before")
	}

	never := create()
	if err := fx.Store.UpdateDeploymentStatus(fx.Ctx, never.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatalf("supersede unserved deployment: %v", err)
	}
	if served(never.ID) {
		t.Fatal("a deployment superseded before it went live never served")
	}

	if _, err := history.DeploymentServedBefore(fx.Ctx, uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown deployment = %v, want ErrNotFound", err)
	}
}

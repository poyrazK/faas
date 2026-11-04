package promotion_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemRollbackWorkloadRestoresRetainedTraffic(t *testing.T) {
	testRollbackWorkloadRestoresTraffic(t, state.NewMemStore(), 3600)
}

func TestMemRollbackWorkloadRestoresUnretainedRevision(t *testing.T) {
	testRollbackWorkloadRestoresTraffic(t, state.NewMemStore(), 0)
}

func TestMemRollbackWorkloadClearsDarkTarget(t *testing.T) {
	testRollbackWorkloadClearsDarkTarget(t, state.NewMemStore())
}

func testRollbackWorkloadClearsDarkTarget(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "dark-rollback@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "dark-rollback", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, Reason: "environment promotion/dark", TrafficPercent: 0, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/dark", "apps/dark", 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	p := state.ProjectEnvironmentPromotion{ID: "dark", ToEnvironment: "production"}
	w := state.ProjectEnvironmentPromotionWorkload{TargetDeploymentID: d.ID}
	for range 2 {
		status, restored, err := promotion.RollbackWorkload(ctx, store, p, w, app.ID)
		if err != nil || status != "cleared" || restored != "" {
			t.Fatalf("rollback: %s %s %v", status, restored, err)
		}
		got, err := store.DeploymentByID(ctx, d.ID)
		if err != nil || got.Status != state.DeployFailed {
			t.Fatalf("dark deployment survived rollback: %+v %v", got, err)
		}
	}
	var staged []state.Deployment
	for _, reason := range []string{"environment promotion/dark", "operator staged release"} {
		next, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, Reason: reason, TrafficPercent: 0, TrafficPercentExplicit: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, next.ID); err != nil {
			t.Fatal(err)
		}
		staged = append(staged, next)
	}
	w.TargetDeploymentID = staged[0].ID
	if _, _, err := promotion.RollbackWorkload(ctx, store, p, w, app.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("rollback could activate an unrelated staged release: %v", err)
	}
	for _, next := range staged {
		got, err := store.DeploymentByID(ctx, next.ID)
		if err != nil || got.Status != state.DeployLive || got.TrafficPercent != 0 {
			t.Fatalf("rollback changed staged deployment: %+v %v", got, err)
		}
	}
}

func testRollbackWorkloadRestoresTraffic(t *testing.T, store state.Store, retention int) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, fmt.Sprintf("rollback-%d@example.test", retention), api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "rollback-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "rollback-app", Type: state.AppTypeApp,
		RAMMB: 256, Manifest: state.AppManifest{RevisionPinTTLSeconds: retention}})
	if err != nil {
		t.Fatal(err)
	}
	makeLive := func(reason string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, Reason: reason})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+d.ID, "apps/"+d.ID, 42); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	previous, candidate := makeLive("baseline"), makeLive("environment promotion/rollback")
	p := state.ProjectEnvironmentPromotion{ID: "rollback", ToEnvironment: "production"}
	w := state.ProjectEnvironmentPromotionWorkload{PreviousTargetDeploymentID: previous.ID, TargetDeploymentID: candidate.ID}
	for range 2 {
		status, restored, err := promotion.RollbackWorkload(ctx, store, p, w, app.ID)
		if err != nil || status != "restored" || restored != previous.ID {
			t.Fatalf("rollback: %s %s %v", status, restored, err)
		}
		routed, err := state.ResolveProductionDeployment(ctx, store, app.ID)
		if err != nil || routed.ID != previous.ID || routed.TrafficPercent != 100 {
			t.Fatalf("rollback did not restore traffic: %+v %v", routed, err)
		}
	}
	if retention > 0 {
		if _, err := store.UpdateDeploymentTraffic(ctx, candidate.ID, 30); err != nil {
			t.Fatal(err)
		}
		if _, _, err := promotion.RollbackWorkload(ctx, store, p, w, app.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("rollback overwrote a traffic split: %v", err)
		}
		routed, err := store.DeploymentByID(ctx, candidate.ID)
		if err != nil || routed.TrafficPercent != 30 {
			t.Fatalf("rollback changed split traffic: %+v %v", routed, err)
		}
		if _, err := store.UpdateDeploymentTraffic(ctx, previous.ID, 100); err != nil {
			t.Fatal(err)
		}
	}
	newer := makeLive("operator replacement")
	if _, _, err := promotion.RollbackWorkload(ctx, store, p, w, app.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("rollback overwrote newer release: %v", err)
	}
	routed, err := state.ResolveProductionDeployment(ctx, store, app.ID)
	if err != nil || routed.ID != newer.ID {
		t.Fatalf("newer route changed: %+v %v", routed, err)
	}
}

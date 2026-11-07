package promotion_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestMemRollbackClearPreservesStagedRelease(t *testing.T) {
	for _, traffic := range []int{0, 100} {
		for _, concurrent := range []bool{false, true} {
			t.Run(fmt.Sprintf("traffic=%d/concurrent=%t", traffic, concurrent), func(t *testing.T) {
				store, hook := state.NewMemStore(), &rollbackReadHook{}
				testRollbackClearPreservesStagedRelease(t, store, statetest.NewMem(&rollbackMemInterleave{MemStore: store, hook: hook}), hook, traffic, concurrent)
			})
		}
	}
}

func TestMemRollbackClearsSoleWeightedRelease(t *testing.T) {
	testRollbackClearsSoleWeightedRelease(t, state.NewMemStore())
}

func testRollbackClearsSoleWeightedRelease(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "sole-clear@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "sole-clear", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	var releases []state.Deployment
	for _, scope := range []string{"production", "staging"} {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball, Reason: "environment promotion/sole"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+d.ID, "apps/"+d.ID, 42); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		releases = append(releases, d)
	}
	for range 2 {
		status, _, err := promotion.RollbackWorkload(ctx, store, state.ProjectEnvironmentPromotion{ID: "sole", ToEnvironment: "production"}, state.ProjectEnvironmentPromotionWorkload{TargetDeploymentID: releases[0].ID}, app.ID)
		if err != nil || status != "cleared" {
			t.Fatalf("clear: %s %v", status, err)
		}
	}
	cleared, err := store.DeploymentByID(ctx, releases[0].ID)
	if err != nil || cleared.Status != state.DeployFailed || cleared.TrafficPercent != 0 {
		t.Fatalf("candidate survived clear: %+v %v", cleared, err)
	}
	other, err := store.DeploymentByID(ctx, releases[1].ID)
	if err != nil || other.Status != state.DeployLive || other.TrafficPercent != 100 {
		t.Fatalf("changed another environment: %+v %v", other, err)
	}
}

func testRollbackClearPreservesStagedRelease(t *testing.T, store, observed state.Store, hook *rollbackReadHook, traffic int, concurrent bool) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "rollback-clear@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollback-clear", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	live := func(reason string, traffic int) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, Reason: reason, TrafficPercent: traffic, TrafficPercentExplicit: true})
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
	candidate := live("environment promotion/clear", traffic)
	var staged state.Deployment
	stage := func() { staged = live("operator --no-traffic", 0) }
	if concurrent {
		hook.previousID, hook.fire = candidate.ID, stage
	} else {
		stage()
	}
	p := state.ProjectEnvironmentPromotion{ID: "clear", ToEnvironment: "production"}
	w := state.ProjectEnvironmentPromotionWorkload{TargetDeploymentID: candidate.ID}
	if _, _, err := promotion.RollbackWorkload(ctx, observed, p, w, app.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("rollback must refuse clearing beside a staged release: %v", err)
	}
	if concurrent && !hook.fired {
		t.Fatal("concurrent stage was not created")
	}
	for _, want := range []struct {
		id      string
		traffic int
	}{{candidate.ID, traffic}, {staged.ID, 0}} {
		got, err := store.DeploymentByID(ctx, want.id)
		if err != nil || got.Status != state.DeployLive || got.TrafficPercent != want.traffic {
			t.Fatalf("rollback changed deployment: %+v %v", got, err)
		}
	}
}

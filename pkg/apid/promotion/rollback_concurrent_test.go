package promotion_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type rollbackReadHook struct {
	previousID string
	fire       func()
	fired      bool
}

func (h *rollbackReadHook) afterRead(d state.Deployment, err error) (state.Deployment, error) {
	if err == nil && d.ID == h.previousID && !h.fired {
		h.fired = true
		h.fire()
	}
	return d, err
}

type rollbackMemInterleave struct {
	MemStore *state.MemStore
	hook     *rollbackReadHook
}

func (f *rollbackMemInterleave) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	return f.hook.afterRead(f.MemStore.DeploymentByID(ctx, id))
}

func TestMemRollbackRejectsConcurrentRelease(t *testing.T) {
	for _, ttl := range []int{0, 3600} {
		t.Run(fmt.Sprint(ttl), func(t *testing.T) {
			store := state.NewMemStore()
			hook := &rollbackReadHook{}
			testRollbackRejectsConcurrentRelease(t, store, statetest.NewMem(&rollbackMemInterleave{MemStore: store, hook: hook}), hook, ttl)
		})
	}
}

func TestMemGuardedActivationKeepsScope(t *testing.T) {
	for _, scope := range []string{"default", "production"} {
		t.Run(scope, func(t *testing.T) { testGuardedActivationKeepsScope(t, state.NewMemStore(), scope) })
	}
}

func testGuardedActivationKeepsScope(t *testing.T, store state.Store, scope string) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, "activation-scope@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "activation-scope", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	live := func(scope string) state.Deployment {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+d.ID, "apps/"+d.ID, 42); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	previous, candidate := live(scope), live(scope)
	otherScope := "production"
	if scope == "production" {
		otherScope = "default"
	}
	other := live(otherScope)
	guarded := store.(state.DeploymentActivationGuardStore)
	for range 2 {
		if err := guarded.MarkDeploymentLiveIfServing(ctx, previous.ID, candidate.ID); err != nil {
			t.Fatalf("guard rejected its own serving lane: %v", err)
		}
		got, err := store.DeploymentByID(ctx, previous.ID)
		if err != nil || got.Status != state.DeployLive || got.TrafficPercent != 100 {
			t.Fatalf("activation: %+v %v", got, err)
		}
		untouched, err := store.DeploymentByID(ctx, other.ID)
		if err != nil || untouched.Status != state.DeployLive || untouched.TrafficPercent != 100 {
			t.Fatalf("activation changed another lane: %+v %v", untouched, err)
		}
	}
}

func testRollbackRejectsConcurrentRelease(t *testing.T, store, interleaved state.Store, hook *rollbackReadHook, ttl int) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "rollback-concurrent@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollback-concurrent", Type: state.AppTypeApp, RAMMB: 256, Manifest: state.AppManifest{RevisionPinTTLSeconds: ttl}})
	if err != nil {
		t.Fatal(err)
	}
	live := func(reason string) state.Deployment {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, Reason: reason})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+d.ID, "apps/"+d.ID, 42); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		return d
	}
	previous := live("baseline")
	candidate := live("environment promotion/concurrent")
	var newer state.Deployment
	hook.previousID = previous.ID
	hook.fire = func() { newer = live("concurrent operator release") }
	_, _, err = promotion.RollbackWorkload(ctx, interleaved, state.ProjectEnvironmentPromotion{ID: "concurrent", ToEnvironment: "production"}, state.ProjectEnvironmentPromotionWorkload{PreviousTargetDeploymentID: previous.ID, TargetDeploymentID: candidate.ID}, app.ID)
	if err == nil || !hook.fired {
		t.Fatalf("concurrent rollback accepted: %v", err)
	}
	routed, err := state.ResolveProductionDeployment(ctx, store, app.ID)
	if err != nil || routed.ID != newer.ID {
		t.Fatalf("rollback overwrote concurrent release: %+v %v", routed, err)
	}
}

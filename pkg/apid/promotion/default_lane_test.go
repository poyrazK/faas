package promotion_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/promotion"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod CLI e2e 2026-11-14: a project deployed the normal way serves
// production from the legacy default scope. Promotion looked its target up
// as an exact "production" scope, found nothing, recorded no previous
// target, and created the promoted deployment in "production", where none of
// the project's values live. The promoted service lost API_URL, and rolling
// the promotion back cleared the only serving deployment (503 until a
// redeploy).
func TestPromotionFollowsTheDefaultProductionLane(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "default-lane@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "default-lane"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "default-lane-web", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, state.DefaultEnvScope, "API_URL", "http://api.svc.gregale:10081"); err != nil {
		t.Fatal(err)
	}
	makeLive := func(scope, reason string) state.Deployment {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindTarball, Reason: reason})
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
	serving := makeLive(state.DefaultEnvScope, "deploy")

	target, err := promotion.SelectedDeployment(ctx, store, app, "production", nil)
	if err != nil || target.ID != serving.ID {
		t.Fatalf("production target = %+v, %v; want the default-scope deployment %s", target, err, serving.ID)
	}
	scope, err := state.EnvironmentDeploymentScope(ctx, store, account.ID, app.ID, "production")
	if err != nil || scope != state.DefaultEnvScope {
		t.Fatalf("production deployment scope = %q, %v; want %q, where the values live", scope, err, state.DefaultEnvScope)
	}

	candidate := makeLive(scope, "environment promotion/default-lane")
	p := state.ProjectEnvironmentPromotion{ID: "default-lane", ToEnvironment: "production"}
	w := state.ProjectEnvironmentPromotionWorkload{PreviousTargetDeploymentID: target.ID, PreviousTargetTrafficPercent: 100, TargetDeploymentID: candidate.ID}
	status, restored, err := promotion.RollbackWorkload(ctx, store, p, w, app.ID)
	if err != nil || status != "restored" || restored != serving.ID {
		t.Fatalf("rollback = %s %s %v; want the default-scope deployment restored", status, restored, err)
	}
	routed, err := state.ResolveProductionDeployment(ctx, store, app.ID)
	if err != nil || routed.ID != serving.ID || routed.TrafficPercent != 100 {
		t.Fatalf("production after rollback = %+v, %v; want %s at 100%%", routed, err, serving.ID)
	}
}

// A project already served from the named production lane keeps it.
func TestPromotionKeepsTheNamedProductionLane(t *testing.T) {
	store := state.NewMemStore()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "named-lane@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "named-lane-web", Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, Reason: "environment promotion/earlier"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, d.ID, "/rootfs/"+d.ID, "apps/"+d.ID, 42); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	scope, err := state.EnvironmentDeploymentScope(ctx, store, account.ID, app.ID, "production")
	if err != nil || scope != "production" {
		t.Fatalf("production deployment scope = %q, %v; want production", scope, err)
	}
	if target, err := promotion.SelectedDeployment(ctx, store, app, "production", nil); err != nil || target.ID != d.ID {
		t.Fatalf("production target = %+v, %v; want %s", target, err, d.ID)
	}
}

func TestEnvironmentOwnsScope(t *testing.T) {
	for _, tc := range []struct {
		environment, scope string
		want               bool
	}{
		{"production", "production", true},
		{"production", state.DefaultEnvScope, true},
		{"production", "", true},
		{"production", "staging", false},
		{"staging", "staging", true},
		{"staging", state.DefaultEnvScope, false},
		{"staging", "production", false},
	} {
		if got := state.EnvironmentOwnsScope(tc.environment, tc.scope); got != tc.want {
			t.Errorf("EnvironmentOwnsScope(%q, %q) = %v, want %v", tc.environment, tc.scope, got, tc.want)
		}
	}
}

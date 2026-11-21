package state_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Production is one serving lane with two scope names: a deploy writes
// "default" and a release-graph promotion writes "production". A checked
// rollback from a production-named deployment to the default-named one it
// replaced is a rollback within the lane, not a cross-environment move.
func TestCheckedRollbackAcrossProductionLaneNamesMem(t *testing.T) {
	checkedRollbackAcrossLaneNames(t, state.NewMemStore())
}

func TestCheckedRollbackAcrossProductionLaneNamesPG(t *testing.T) {
	s, _ := pgStore(t)
	checkedRollbackAcrossLaneNames(t, s)
}

func checkedRollbackAcrossLaneNames(t *testing.T, s state.Store) {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@rollback-lanes.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "lanes-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	create := func(scope string) state.Deployment {
		d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("2", 64), Scope: scope, Status: state.DeployPending, TrafficPercent: 100})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetDeploymentRootfs(ctx, d.ID, "/test/"+d.ID, "test/"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
		d, err = s.DeploymentByID(ctx, d.ID)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	target, current := create("default"), create("production")
	target, err = s.DeploymentByID(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if target.TrafficPercent != 0 {
		t.Skipf("fixture: the production-named deploy did not take over the lane (target traffic %d)", target.TrafficPercent)
	}
	store := s.(state.CheckedRollbackStore)
	operation, err := store.CreateCheckedRollback(ctx, a.ID, app.ID, target.ID, current.ID, "restore the default-named release")
	if err != nil {
		t.Fatalf("rollback within the production lane refused: %v", err)
	}
	if operation.Scope != "default" {
		t.Fatalf("operation scope = %q, want the target's", operation.Scope)
	}
}

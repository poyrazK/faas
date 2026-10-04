package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type rollbackRecencyStore interface {
	CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
	MarkDeploymentLive(context.Context, string) error
	LatestSupersededDeployment(context.Context, string) (state.Deployment, error)
	AutoRollbackDeploymentsTx(context.Context, string, string) (string, error)
}

// runRollbackRecencyScenario replays production-us 2026-10-04: v1 served,
// v4 replaced it, an explicit rollback put v1 back (v4 superseded), then v5
// replaced v1. Default rollback chose v4 (created after v1) instead of v1,
// the deployment that had just stopped serving. Automatic rollback shared
// the ordering.
func runRollbackRecencyScenario(t *testing.T, ctx context.Context, s rollbackRecencyStore, appID string) {
	t.Helper()
	deploy := func(name string) state.Deployment {
		t.Helper()
		d, err := s.CreateDeployment(ctx, state.Deployment{
			ID: uuid.NewString(), AppID: appID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + name, CreatedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("CreateDeployment %s: %v", name, err)
		}
		return d
	}
	live := func(d state.Deployment) {
		t.Helper()
		if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatalf("MarkDeploymentLive: %v", err)
		}
	}
	v1 := deploy("v1")
	live(v1)
	v4 := deploy("v4")
	live(v4)
	live(v1) // explicit rollback: v4 superseded
	v5 := deploy("v5")
	live(v5) // v1 superseded

	target, err := s.LatestSupersededDeployment(ctx, appID)
	if err != nil {
		t.Fatalf("LatestSupersededDeployment: %v", err)
	}
	if target.ID != v1.ID {
		t.Fatalf("default rollback target = %s, want v1 %s (v4 is %s)", target.ID, v1.ID, v4.ID)
	}
	restored, err := s.AutoRollbackDeploymentsTx(ctx, appID, v5.ID)
	if err != nil {
		t.Fatalf("AutoRollbackDeploymentsTx: %v", err)
	}
	if restored != v1.ID {
		t.Fatalf("automatic rollback restored %s, want v1 %s (v4 is %s)", restored, v1.ID, v4.ID)
	}
}

func TestMem_RollbackTargetIsMostRecentlyServing(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "recency-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "recency", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	runRollbackRecencyScenario(t, ctx, m, app.ID)
}

func TestPg_RollbackTargetIsMostRecentlyServing(t *testing.T) {
	s, ctx := pgStore(t)
	_, app := seedPgAccountAndApp(t, s, ctx)
	runRollbackRecencyScenario(t, ctx, s, app.ID)
}

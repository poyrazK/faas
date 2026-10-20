package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1446: a plain rollback of an image deployment must activate the older
// revision. PrepareDeploymentRollback marks it and promotion consumes the
// marker; the unfenced promotion imaged uses for a marked target succeeds even
// though a newer revision exists, where the fenced one supersedes it.
func TestMemStore_PreparedRollbackMarkerIsConsumedByPromotion(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	account, err := m.CreateAccount(ctx, "rollback-marker@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "rollback-marker", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, Revision: 1, Status: state.DeploySuperseded, CreatedAt: now.Add(-time.Minute)}
	current := state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindImage, Revision: 2, Status: state.DeployLive, TrafficPercent: 100, RolloutState: "complete", CreatedAt: now}
	(*m.Deployments())[target.ID] = target
	(*m.Deployments())[current.ID] = current

	if prepared, _ := m.DeploymentRollbackPrepared(ctx, target.ID); prepared {
		t.Fatal("marker set before PrepareDeploymentRollback")
	}
	if _, err := m.PrepareDeploymentRollback(ctx, app.ID, target.ID); err != nil {
		t.Fatalf("PrepareDeploymentRollback: %v", err)
	}
	if prepared, err := m.DeploymentRollbackPrepared(ctx, target.ID); err != nil || !prepared {
		t.Fatalf("DeploymentRollbackPrepared after prepare = %v, %v; want true", prepared, err)
	}
	if err := m.MarkDeploymentLiveIfLatest(ctx, target.ID); !errors.Is(err, state.ErrDeploymentSuperseded) {
		t.Fatalf("fenced promotion of the older revision = %v, want ErrDeploymentSuperseded", err)
	}
	if prepared, _ := m.DeploymentRollbackPrepared(ctx, target.ID); !prepared {
		t.Fatal("a refused promotion consumed the rollback marker")
	}
	// The fence superseded the target; a rollback re-prepares it, and imaged
	// promotes a marked target unfenced.
	if _, err := m.PrepareDeploymentRollback(ctx, app.ID, target.ID); err != nil {
		t.Fatalf("PrepareDeploymentRollback (again): %v", err)
	}
	if err := m.MarkDeploymentLive(ctx, target.ID); err != nil {
		t.Fatalf("unfenced promotion: %v", err)
	}
	if got := (*m.Deployments())[target.ID]; got.Status != state.DeployLive || got.TrafficPercent != 100 {
		t.Fatalf("rollback target after promotion = status:%s traffic:%d, want live at 100%%", got.Status, got.TrafficPercent)
	}
	if prepared, err := m.DeploymentRollbackPrepared(ctx, target.ID); err != nil || prepared {
		t.Fatalf("DeploymentRollbackPrepared after promotion = %v, %v; want false", prepared, err)
	}
}

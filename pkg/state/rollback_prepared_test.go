package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-911: a plain rollback of an image deployment must activate the older
// revision. PrepareDeploymentRollback marks it and promotion consumes the
// marker; the unfenced promotion imaged uses for a marked target succeeds even
// though a newer revision exists, where the fenced one supersedes it.
func TestMemStore_PreparedRollbackMarkerIsConsumedByPromotion(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "rollback-marker@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "rollback-marker", Type: AppTypeApp, Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: DeploymentKindImage, Revision: 1, Status: DeploySuperseded, CreatedAt: now.Add(-time.Minute)}
	current := Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: DeploymentKindImage, Revision: 2, Status: DeployLive, TrafficPercent: 100, RolloutState: "complete", CreatedAt: now}
	m.deployments[target.ID] = target
	m.deployments[current.ID] = current

	if prepared, _ := m.DeploymentRollbackPrepared(ctx, target.ID); prepared {
		t.Fatal("marker set before PrepareDeploymentRollback")
	}
	if _, err := m.PrepareDeploymentRollback(ctx, app.ID, target.ID); err != nil {
		t.Fatalf("PrepareDeploymentRollback: %v", err)
	}
	if prepared, err := m.DeploymentRollbackPrepared(ctx, target.ID); err != nil || !prepared {
		t.Fatalf("DeploymentRollbackPrepared after prepare = %v, %v; want true", prepared, err)
	}
	if err := m.MarkDeploymentLiveIfLatest(ctx, target.ID); !errors.Is(err, ErrDeploymentSuperseded) {
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
	if got := m.deployments[target.ID]; got.Status != DeployLive || got.TrafficPercent != 100 {
		t.Fatalf("rollback target after promotion = status:%s traffic:%d, want live at 100%%", got.Status, got.TrafficPercent)
	}
	if prepared, err := m.DeploymentRollbackPrepared(ctx, target.ID); err != nil || prepared {
		t.Fatalf("DeploymentRollbackPrepared after promotion = %v, %v; want false", prepared, err)
	}
}

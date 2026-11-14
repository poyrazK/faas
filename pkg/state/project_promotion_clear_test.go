package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type promotionClearTestStore interface {
	CreateDeployment(context.Context, state.Deployment) (state.Deployment, error)
	MarkDeploymentLive(context.Context, string) error
	DeploymentByID(context.Context, string) (state.Deployment, error)
	ClearProjectPromotionDeployment(context.Context, string, string, string) error
}

// runPromotionClearScenario: a rolled-back promotion's only release is
// retired with a stable code and a reason, not a bare "environment promotion
// rollback" with no code (deploy-path review P7).
func runPromotionClearScenario(t *testing.T, ctx context.Context, s promotionClearTestStore, appID string) {
	t.Helper()
	promotionID := uuid.NewString()
	d, err := s.CreateDeployment(ctx, state.Deployment{
		ID: uuid.NewString(), AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:promoted",
		CreatedAt: time.Now(), RootfsKey: "apps/x/promoted.ext4", Reason: "environment promotion/" + promotionID,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	if err := s.ClearProjectPromotionDeployment(ctx, appID, d.ID, promotionID); err != nil {
		t.Fatalf("ClearProjectPromotionDeployment: %v", err)
	}
	got, err := s.DeploymentByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.DeployFailed || got.ErrorCode != api.CodePromotionRolledBack {
		t.Fatalf("cleared promotion = status %q code %q, want failed %q", got.Status, got.ErrorCode, api.CodePromotionRolledBack)
	}
	if !strings.Contains(got.Error, promotionID) || !strings.Contains(got.Error, "no earlier release to restore") {
		t.Fatalf("cleared promotion error = %q, want the promotion and the reason", got.Error)
	}
}

func TestMem_ClearProjectPromotionDeploymentExplainsItself(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	acct, err := m.CreateAccount(ctx, "promoclear-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "promoclear", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	runPromotionClearScenario(t, ctx, m, app.ID)
}

func TestPg_ClearProjectPromotionDeploymentExplainsItself(t *testing.T) {
	s, ctx := pgStore(t)
	_, app := seedPgAccountAndApp(t, s, ctx)
	runPromotionClearScenario(t, ctx, s, app.ID)
}

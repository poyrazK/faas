package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func checkGitDrivenPromotionFence(t *testing.T, store state.Store, ctx context.Context, suffix string, kind state.DeploymentKind) {
	t.Helper()
	account, err := store.CreateAccount(ctx, "github-fence-"+suffix+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "github-fence-" + suffix,
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	stable, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:stable"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatal(err)
	}
	older, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: kind,
		CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDeploymentStatus(ctx, older.ID, state.DeployBuilding, ""); err != nil {
		t.Fatal(err)
	}
	newer, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: kind,
		CommitSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
	if err != nil {
		t.Fatal(err)
	}
	if newer.Revision <= older.Revision {
		t.Fatalf("revisions older=%d newer=%d", older.Revision, newer.Revision)
	}
	if err := store.MarkGitDrivenDeploymentLiveIfLatest(ctx, older.ID); !errors.Is(err, state.ErrDeploymentSuperseded) {
		t.Fatalf("older promotion = %v, want superseded", err)
	}
	oldRow, err := store.DeploymentByID(ctx, older.ID)
	if err != nil || oldRow.Status != state.DeploySuperseded {
		t.Fatalf("older row = (%+v, %v), want superseded", oldRow, err)
	}
	stableRow, err := store.DeploymentByID(ctx, stable.ID)
	if err != nil || stableRow.Status != state.DeployLive {
		t.Fatalf("stable row = (%+v, %v), want live until replacement", stableRow, err)
	}
	if err := store.MarkGitDrivenDeploymentLiveIfLatest(ctx, newer.ID); err != nil {
		t.Fatalf("newest promotion: %v", err)
	}
	newRow, err := store.DeploymentByID(ctx, newer.ID)
	if err != nil || newRow.Status != state.DeployLive {
		t.Fatalf("newer row = (%+v, %v), want live", newRow, err)
	}
	// An explicit operator rollback uses the existing method and can still
	// intentionally promote a prior revision after the automatic fence.
	if err := store.MarkDeploymentLive(ctx, stable.ID); err != nil {
		t.Fatalf("explicit rollback: %v", err)
	}
}

func TestMemStoreGitHubPromotionFence(t *testing.T) {
	checkGitDrivenPromotionFence(t, state.NewMemStore(), context.Background(), "mem", state.DeploymentKindGitHub)
}

func TestPgStoreGitHubPromotionFence(t *testing.T) {
	store, ctx := pgStore(t)
	checkGitDrivenPromotionFence(t, store, ctx, "pg", state.DeploymentKindGitHub)
}

func TestMemStorePreviewPromotionFence(t *testing.T) {
	checkGitDrivenPromotionFence(t, state.NewMemStore(), context.Background(), "preview-mem", state.DeploymentKindPreview)
}

func TestPgStorePreviewPromotionFence(t *testing.T) {
	store, ctx := pgStore(t)
	checkGitDrivenPromotionFence(t, store, ctx, "preview-pg", state.DeploymentKindPreview)
}

func TestMemStoreGitHubPromotionFenceIsScoped(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "github-fence-scope@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "github-fence-scope", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	production, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindGitHub, Scope: "production"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindGitHub, Scope: "staging"}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkGitDrivenDeploymentLiveIfLatest(ctx, production.ID); err != nil {
		t.Fatalf("newer staging intent blocked production: %v", err)
	}
}

package state_test

// adr: 20261028100000 — production builds are claimed before PR preview
// builds, so a busy pull request cannot hold the builder ahead of a deploy.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type previewBuildOrderStore interface {
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	CreateApp(ctx context.Context, app state.App) (state.App, error)
	CreateDeployment(ctx context.Context, dep state.Deployment) (state.Deployment, error)
	CreateBuild(ctx context.Context, deploymentID string, kind state.DeploymentKind, sourceBytes int64, logPath string) (state.Build, error)
	ClaimQueuedBuild(ctx context.Context, id string) (state.Build, error)
	ClaimNextQueuedBuildWithFairness(ctx context.Context, fairnessWindow time.Duration) (state.Build, error)
}

func previewBuildOrderContract(t *testing.T, ctx context.Context, s previewBuildOrderStore) {
	t.Helper()
	account, err := s.CreateAccount(ctx, "preview-build-order@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	queue := func(slug, parent string, kind state.DeploymentKind) state.Build {
		t.Helper()
		app := state.App{AccountID: account.ID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 60, Status: state.AppActive}
		if parent != "" {
			app.PreviewOfSlug, app.PreviewPrNumber, app.PreviewPrState = parent, 3, state.PreviewPrStateOpen
		}
		created, err := s.CreateApp(ctx, app)
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: created.ID, Kind: kind,
			SourcePath: "/tmp/source.tar.gz", SourceBytes: 100, LogPath: "/tmp/build.log"})
		if err != nil {
			t.Fatalf("deploy %s: %v", slug, err)
		}
		build, err := s.CreateBuild(ctx, dep.ID, kind, 100, dep.LogPath)
		if err != nil {
			t.Fatalf("build %s: %v", slug, err)
		}
		return build
	}
	preview := queue("pr-3-order-shop", "order-shop", state.DeploymentKindPreview)
	time.Sleep(2 * time.Millisecond)
	production := queue("order-shop", "", state.DeploymentKindTarball)

	if _, err := s.ClaimQueuedBuild(ctx, preview.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("notified preview claim while a production build waits = %v, want deferred", err)
	}
	claimed, err := s.ClaimNextQueuedBuildWithFairness(ctx, 30*time.Second)
	if err != nil || claimed.ID != production.ID {
		t.Fatalf("next claim = (%s, %v), want the later production build %s first", claimed.ID, err, production.ID)
	}
	if claimed, err := s.ClaimQueuedBuild(ctx, preview.ID); err != nil || claimed.ID != preview.ID {
		t.Fatalf("preview claim after production drained = (%s, %v)", claimed.ID, err)
	}
}

func TestMemStoreClaimsProductionBuildsBeforePreviewBuilds(t *testing.T) {
	previewBuildOrderContract(t, context.Background(), state.NewMemStore())
}

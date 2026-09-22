package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func seedMemAffinityApp(t *testing.T, store *MemStore) App {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "affinity-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, App{
		AccountID: account.ID, Slug: "affinity-" + uuid.NewString(), Type: AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return app
}

func seedMemAffinityBuild(t *testing.T, store *MemStore, appID string) Build {
	t.Helper()
	ctx := context.Background()
	dep, err := store.CreateDeployment(ctx, Deployment{AppID: appID, Kind: DeploymentKindTarball, Status: DeployPending})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	build, err := store.CreateBuild(ctx, dep.ID, DeploymentKindTarball, 100, "")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}
	return build
}

func seedMemSuccessfulAffinityBuild(t *testing.T, store *MemStore, appID, nodeID string) Build {
	t.Helper()
	ctx := context.Background()
	build := seedMemAffinityBuild(t, store, appID)
	claim, err := store.ClaimQueuedBuild(ctx, build.ID)
	if err != nil {
		t.Fatalf("ClaimQueuedBuild: %v", err)
	}
	if err := store.UpdateBuildStatus(ctx, build.ID, BuildSucceeded, "", false, true); err != nil {
		t.Fatalf("UpdateBuildStatus: %v", err)
	}
	finished, err := store.BuildByID(ctx, build.ID)
	if err != nil {
		t.Fatalf("BuildByID: %v", err)
	}
	if err := store.CreateBuildProvenance(ctx, BuildProvenance{
		BuildID: build.ID, BuilderNodeID: nodeID, StartedAt: claim.StartedAt, FinishedAt: finished.FinishedAt,
	}); err != nil {
		t.Fatalf("CreateBuildProvenance: %v", err)
	}
	return finished
}

func TestMemStoreBuildAffinityPrefersLatestSuccessfulBuilder(t *testing.T) {
	store := NewMemStore()
	app := seedMemAffinityApp(t, store)
	seedMemSuccessfulAffinityBuild(t, store, app.ID, "node-a")
	queued := seedMemAffinityBuild(t, store, app.ID)

	if _, err := store.ClaimQueuedBuildWithNodeAffinity(context.Background(), queued.ID, "node-b", time.Hour); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-node claim err = %v, want ErrNotFound", err)
	}
	claim, err := store.ClaimQueuedBuildWithNodeAffinity(context.Background(), queued.ID, "node-a", time.Hour)
	if err != nil {
		t.Fatalf("preferred-node claim: %v", err)
	}
	if claim.Status != BuildRunning {
		t.Fatalf("claim status = %s, want running", claim.Status)
	}
}

func TestMemStoreBuildAffinityFallsBackAfterGrace(t *testing.T) {
	store := NewMemStore()
	app := seedMemAffinityApp(t, store)
	seedMemSuccessfulAffinityBuild(t, store, app.ID, "node-a")
	queued := seedMemAffinityBuild(t, store, app.ID)
	store.SetBuildEnqueuedAtForTest(queued.ID, time.Now().Add(-time.Minute))

	if _, err := store.ClaimQueuedBuildWithNodeAffinity(context.Background(), queued.ID, "node-b", 5*time.Second); err != nil {
		t.Fatalf("fallback claim: %v", err)
	}
}

func TestMemStoreBuildAffinityPollingSkipsOtherNodesFreshBuild(t *testing.T) {
	store := NewMemStore()
	preferredApp := seedMemAffinityApp(t, store)
	seedMemSuccessfulAffinityBuild(t, store, preferredApp.ID, "node-a")
	blocked := seedMemAffinityBuild(t, store, preferredApp.ID)

	coldApp := seedMemAffinityApp(t, store)
	eligible := seedMemAffinityBuild(t, store, coldApp.ID)
	claim, err := store.ClaimNextQueuedBuildWithNodeAffinity(context.Background(), "node-b", time.Hour, 0)
	if err != nil {
		t.Fatalf("polling claim: %v", err)
	}
	if claim.ID != eligible.ID {
		t.Fatalf("polling claim = %s, want cold eligible build %s (blocked=%s)", claim.ID, eligible.ID, blocked.ID)
	}
	stillQueued, err := store.BuildByID(context.Background(), blocked.ID)
	if err != nil {
		t.Fatalf("BuildByID(blocked): %v", err)
	}
	if stillQueued.Status != BuildQueued {
		t.Fatalf("blocked build status = %s, want queued", stillQueued.Status)
	}
}

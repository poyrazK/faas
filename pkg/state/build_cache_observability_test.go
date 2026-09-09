package state

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreBuildCacheOutcomeAndStats(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	acct, err := store.CreateAccount(ctx, "cache-observer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: acct.ID, Slug: "cache-observer", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuild(ctx, dep.ID, DeploymentKindTarball, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetBuildCacheOutcome(ctx, build.ID, "hit", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetBuildCacheOutcome before claim = %v, want ErrNotFound", err)
	}
	if _, err := store.ClaimQueuedBuild(ctx, build.ID); err != nil {
		t.Fatal(err)
	}
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := store.SetBuildCacheOutcome(ctx, build.ID, "hit", key); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateBuildStatus(ctx, build.ID, BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	got, err := store.BuildByID(ctx, build.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CacheStatus != "hit" || got.CacheKeySHA256 != key {
		t.Fatalf("cache fields = (%q, %q), want hit + key", got.CacheStatus, got.CacheKeySHA256)
	}
	stats, err := store.BuildCacheStatsForApp(ctx, app.ID, got.FinishedAt.Add(-1))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Hits != 1 || stats.Eligible != 1 {
		t.Fatalf("stats = %+v, want one hit out of one eligible build", stats)
	}
}

func TestBuildCacheOutcomeValidation(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	acct, err := store.CreateAccount(ctx, "cache-validation@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: acct.ID, Slug: "cache-validation", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, Deployment{AppID: app.ID, Kind: DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuild(ctx, dep.ID, DeploymentKindTarball, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueuedBuild(ctx, build.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBuildCacheOutcome(ctx, build.ID, "unknown", ""); err == nil {
		t.Fatal("unknown cache status accepted")
	}
	if err := store.SetBuildCacheOutcome(ctx, build.ID, "hit", "not-a-sha256"); err == nil {
		t.Fatal("malformed cache key accepted")
	}
}

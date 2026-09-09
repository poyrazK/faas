package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploymentResponseWithBuildSurfacesCacheDecision(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "deployment-cache@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "deployment-cache", RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	buildID := "build-cache-deployment"
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, BuildID: buildID})
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuildWithID(ctx, buildID, dep.ID, state.DeploymentKindTarball, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueuedBuild(ctx, build.ID); err != nil {
		t.Fatal(err)
	}
	key := "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
	if err := store.SetBuildCacheOutcome(ctx, build.ID, "hit", key); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, testLogger(), "gregale.dev", noopNotifier{})
	resp := srv.deploymentResponseWithBuild(ctx, dep, app)
	if resp.BuildCacheStatus != "hit" || resp.CacheKeySHA256 != key {
		t.Fatalf("deployment cache fields = (%q, %q), want hit + key", resp.BuildCacheStatus, resp.CacheKeySHA256)
	}
}

func TestGetAppSurfacesBuildCacheHitRate(t *testing.T) {
	h, key, store, acct := buildTestServer(t)
	buildID := seedBuildForStatus(t, store, acct)
	if _, err := store.ClaimQueuedBuild(context.Background(), buildID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBuildCacheOutcome(context.Background(), buildID, "hit", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateBuildStatus(context.Background(), buildID, state.BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	rec := buildGet(t, h, key, "/v1/apps/build-test-app")
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp api.AppResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.BuildCacheHitRatePct != 100 {
		t.Fatalf("build_cache_hit_rate_pct = %v, want 100", resp.BuildCacheHitRatePct)
	}
}

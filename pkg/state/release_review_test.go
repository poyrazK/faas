package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type reviewedStore interface {
	state.Store
	state.ReleaseReviewStore
}

func TestMemReleaseReview(t *testing.T) { testReleaseReview(t, state.NewMemStore()) }

func testReleaseReview(t *testing.T, store reviewedStore) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "release-review@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "reviewed", Type: state.AppTypeApp, RAMMB: 256, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	base, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:base", TrafficPercent: 100, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	review, err := store.ReadReleaseReview(ctx, app.ID, api.DefaultEnvScope)
	if err != nil || len(review.ConfigurationHash) != 64 || review.LatestDeploymentID != base.ID || len(review.ServingDeploymentIDs) != 1 {
		t.Fatalf("review=%+v err=%v", review, err)
	}
	before, err := store.ListDeploymentsForApp(ctx, app.ID, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	ram := 512
	if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{RAMMB: &ram}); err != nil {
		t.Fatal(err)
	}
	candidate := state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:reviewed", TrafficPercent: 0, TrafficPercentExplicit: true, ExpectedReview: &review, Scope: api.DefaultEnvScope}
	if _, err := store.CreateDeployment(ctx, candidate); !errors.Is(err, state.ErrReleaseReviewChanged) {
		t.Fatalf("stale settings=%v", err)
	}
	after, err := store.ListDeploymentsForApp(ctx, app.ID, 100, 0)
	if err != nil || len(after) != len(before) {
		t.Fatal("stale review changed deployment rows")
	}
	latest, err := store.ReadReleaseReview(ctx, app.ID, api.DefaultEnvScope)
	if err != nil {
		t.Fatal(err)
	}
	candidate.ExpectedReview = &latest
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for range 5 {
		wg.Go(func() {
			_, err := store.CreateDeployment(context.Background(), candidate)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successes++
			} else if !errors.Is(err, state.ErrReleaseReviewChanged) {
				t.Errorf("concurrent reviewed insert=%v", err)
			}
		})
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("same review created %d candidates", successes)
	}
	after, err = store.ListDeploymentsForApp(ctx, app.ID, 100, 0)
	if err != nil || len(after) != len(before)+1 {
		t.Fatal("concurrent review inserted or superseded extra revisions")
	}
	if base, err := store.DeploymentByID(ctx, base.ID); err != nil || base.Status != state.DeployLive || base.TrafficPercent != 100 {
		t.Fatal("staging changed serving traffic")
	}
}

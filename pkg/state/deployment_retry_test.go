package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"sync"
	"testing"
)

func TestDeploymentRetryMemory(t *testing.T) { testDeploymentRetry(t, state.NewMemStore()) }
func testDeploymentRetry(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "retry@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
	if err != nil {
		t.Fatal(err)
	}
	unselected := state.ContextWithDeploymentRetry(ctx, map[string]string{"other": failed.ID})
	if _, err := store.CreateDeployment(unselected, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball}); !errors.Is(err, state.ErrDeploymentRetryChanged) {
		t.Fatalf("unselected app accepted: %v", err)
	}
	guarded := state.ContextWithDeploymentRetry(ctx, map[string]string{app.ID: failed.ID})
	if _, err := store.CreateDeployment(guarded, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball}); !errors.Is(err, state.ErrDeploymentRetryChanged) {
		t.Fatalf("nonfailure accepted: %v", err)
	}
	if err := store.UpdateDeploymentStatus(ctx, failed.ID, state.DeployFailed, ""); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := store.CreateDeployment(guarded, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, state.ErrDeploymentRetryChanged) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("accepted=%d rejected=%d", accepted, rejected)
	}
}

package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploySettingsGuardMem(t *testing.T) {
	testDeploySettingsGuard(t, state.NewMemStore())
}

func testDeploySettingsGuard(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "guarded-settings@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "guarded-settings", Type: state.AppTypeApp,
		Status: state.AppActive, RAMMB: 256, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive,
		ImageDigest: "sha256:" + strings.Repeat("a", 64), CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	makeParams := func(desired int) state.UpdateAppParams {
		return state.UpdateAppParams{DeploySettingsGuard: &state.DeploySettingsGuard{AppID: app.ID, AccountID: account.ID, DeploymentID: dep.ID,
			Prepare: func(current state.App) (state.UpdateAppParams, error) {
				if current.Manifest.RequestTimeoutS != 0 {
					return state.UpdateAppParams{}, &state.DeploySettingsConflictError{Reason: "settings_changed", Fields: []string{"request_timeout_s"}}
				}
				manifest := current.Manifest
				manifest.RequestTimeoutS = desired
				return state.UpdateAppParams{Manifest: &manifest}, nil
			}}}
	}
	start := make(chan struct{})
	errorsByWriter := make(chan error, 2)
	var wg sync.WaitGroup
	for _, desired := range []int{30, 45} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.UpdateApp(ctx, app.ID, makeParams(desired))
			errorsByWriter <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errorsByWriter)
	successes, conflicts := 0, 0
	for err := range errorsByWriter {
		var conflict *state.DeploySettingsConflictError
		if err == nil {
			successes++
		} else if errors.As(err, &conflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	current, err := store.AppByID(ctx, app.ID)
	if err != nil || (current.Manifest.RequestTimeoutS != 30 && current.Manifest.RequestTimeoutS != 45) {
		t.Fatalf("current=%+v err=%v", current.Manifest, err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending,
		ImageDigest: "sha256:" + strings.Repeat("b", 64), CreatedAt: dep.CreatedAt.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	params := makeParams(60)
	params.DeploySettingsGuard.Prepare = func(state.App) (state.UpdateAppParams, error) {
		t.Error("stale deployment reached the update builder")
		return state.UpdateAppParams{}, nil
	}
	if _, err := store.UpdateApp(ctx, app.ID, params); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("new deployment did not reject stale settings: %v", err)
	}
	activityStore := store.(state.OrgActivityAppConfigMutationStore)
	_, outboxID, err := activityStore.UpdateAppWithActivity(ctx, app.ID, params, state.OrgActivity{}, func(state.App, state.App) (json.RawMessage, bool, error) {
		t.Error("rejected update reached its activity builder")
		return nil, true, nil
	})
	if !errors.Is(err, state.ErrConflict) || outboxID != 0 {
		t.Fatalf("rejected activity update: outbox=%d err=%v", outboxID, err)
	}
	deployments, err := store.ListDeploymentsForApp(ctx, app.ID, 10, 0)
	if err != nil || len(deployments) != 2 {
		t.Fatalf("deployments=%d err=%v", len(deployments), err)
	}
}

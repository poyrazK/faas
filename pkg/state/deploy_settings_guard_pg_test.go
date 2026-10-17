//go:build !no_pg

package state_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeploySettingsGuardPG(t *testing.T) {
	store, _ := pgStore(t)
	testDeploySettingsGuard(t, store)
}

func TestDeploySettingsGuardPGBlocksNewDeploymentUntilCommit(t *testing.T) {
	store, pool, baseCtx := pgStoreWithPool(t)
	accountID, appID, deploymentID := seedLiveDeploy(t, store, baseCtx, "guarded-create", "guarded-create")
	ctx, cancel := context.WithTimeout(baseCtx, 10*time.Second)
	defer cancel()
	checked := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	updated := make(chan error, 1)
	go func() {
		_, err := store.UpdateApp(ctx, appID, state.UpdateAppParams{DeploySettingsGuard: &state.DeploySettingsGuard{
			AppID: appID, AccountID: accountID, DeploymentID: deploymentID,
			Prepare: func(current state.App) (state.UpdateAppParams, error) {
				close(checked)
				select {
				case <-release:
				case <-ctx.Done():
					return state.UpdateAppParams{}, ctx.Err()
				}
				manifest := current.Manifest
				manifest.RequestTimeoutS = 30
				return state.UpdateAppParams{Manifest: &manifest}, nil
			},
		}})
		updated <- err
	}()
	select {
	case <-checked:
	case err := <-updated:
		t.Fatalf("guarded update failed before checking: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	created := make(chan error, 1)
	go func() {
		_, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Kind: state.DeploymentKindImage, Status: state.DeployPending,
			ImageDigest: "sha256:" + strings.Repeat("b", 64)})
		created <- err
	}()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
			AND query LIKE 'select 1 from apps where id =%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-created:
			t.Fatalf("deployment was admitted before guarded update committed: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	finish()
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	app, err := store.AppByID(ctx, appID)
	if err != nil || app.Manifest.RequestTimeoutS != 30 {
		t.Fatalf("guarded update did not commit: err=%v", err)
	}
	deployments, err := store.ListDeploymentsForApp(ctx, appID, 10, 0)
	if err != nil || len(deployments) != 2 {
		t.Fatalf("deployments=%d err=%v", len(deployments), err)
	}
}

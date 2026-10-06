// adr: 597
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingSmokeDeploymentMem(t *testing.T) {
	bindingSmokeDeploymentSuite(t, state.NewMemStore())
}

func TestBindingSmokeDeploymentPG(t *testing.T) {
	s, _ := pgStore(t)
	bindingSmokeDeploymentSuite(t, s)
}

func smokeAdmissionParams(account state.Account, app state.App, deployment state.Deployment) state.CreateAppTaskParams {
	return state.CreateAppTaskParams{AccountID: account.ID, AppID: app.ID, DeploymentID: deployment.ID, Kind: state.AppTaskKindManual, RequireLiveDeployment: true,
		Command: []string{api.AppTaskServiceBindingSmokeCommand, "billing", uuid.NewString(), "/ready", "200"}, TimeoutSeconds: 90, MaxOutputBytes: 4096}
}

func bindingSmokeDeploymentSuite(t *testing.T, store state.Store) {
	t.Helper()
	ctx := context.Background()
	account, app, _, candidate := bindingPromotionFixture(t, store)
	params := smokeAdmissionParams(account, app, candidate)
	tasks := store.(state.AppTaskStore)
	task, err := tasks.CreateAppTask(ctx, params)
	if err != nil || task.DeploymentID != candidate.ID || task.DeploymentScope != candidate.Scope || task.ArtifactKey != candidate.RootfsKey || task.ImageDigest != candidate.ImageDigest || task.BindingVerification != nil {
		t.Fatalf("smoke admission=%+v err=%v candidate=%+v", task, err, candidate)
	}
	if err := store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeploySuperseded, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.CreateAppTask(ctx, params); !errors.Is(err, state.ErrAppTaskDeploymentUnavailable) {
		t.Fatalf("superseded smoke caller admitted: %v", err)
	}
	params.Command = []string{"echo", "hello"}
	if _, err := tasks.CreateAppTask(ctx, params); !errors.Is(err, state.ErrAppTaskInvalid) {
		t.Fatalf("generic explicit command admitted: %v", err)
	}
	params.RequireLiveDeployment = false
	if _, err := tasks.CreateAppTask(ctx, params); err != nil {
		t.Fatalf("ordinary historical task admission changed: %v", err)
	}
}

func TestBindingSmokeDeploymentPGWaitsForConcurrentRetirement(t *testing.T) {
	store, pool, _ := pgStoreWithPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	account, app, _, candidate := bindingPromotionFixture(t, store)
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(ctx) }()
	if _, err := writer.Exec(ctx, `UPDATE deployments SET status='superseded' WHERE id=$1`, candidate.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.CreateAppTask(ctx, smokeAdmissionParams(account, app, candidate))
		done <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%name: CreateServiceBindingSmokeTask%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("admission bypassed deployment writer: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("admission never reached deployment lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrAppTaskDeploymentUnavailable) {
		t.Fatalf("retired caller admitted after lock wait: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app_tasks WHERE app_id=$1`, app.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("task persisted after retirement: %d %v", count, err)
	}
}
